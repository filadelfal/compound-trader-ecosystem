package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

const operatorAssertionHeader = "X-Operator-Assertion"
const operatorAssertionVersion = "operator-assertion.v1"
const operatorBodyLimit = 16 << 10

const (
	permissionPause       = "PAPER_AUTOMATION_PAUSE"
	permissionResume      = "PAPER_AUTOMATION_RESUME"
	permissionKill        = "PAPER_KILL_SWITCH_ACTIVATE"
	permissionRelease     = "PAPER_KILL_SWITCH_RELEASE_REQUEST"
	permissionReconcile   = "PAPER_RECONCILIATION_RUN"
	permissionRepairView  = "PAPER_REPAIR_PREVIEW"
	permissionRepairApply = "PAPER_REPAIR_APPLY"
	permissionRead        = "PAPER_OPERATIONS_READ"
)

var operatorPermissions = map[string]bool{permissionPause: true, permissionResume: true, permissionKill: true, permissionRelease: true, permissionReconcile: true, permissionRepairView: true, permissionRepairApply: true, permissionRead: true}
var operatorSecurityEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "paper_operator_security_events_total", Help: "Bounded PAPER operator security events."}, []string{"event", "reason"})
var operatorSecurityReady = prometheus.NewGauge(prometheus.GaugeOpts{Name: "paper_operator_security_ready", Help: "One when operator authentication and replay protection are available."})

func init() { prometheus.MustRegister(operatorSecurityEvents, operatorSecurityReady) }

type operatorSecurityConfig struct {
	Enabled     bool
	Algorithm   string
	Secret      []byte
	Issuer      string
	Audience    string
	MaxLifetime time.Duration
	ClockSkew   time.Duration
	ReplayTTL   time.Duration
	RateLimit   int
	RateWindow  time.Duration
}

func (c operatorSecurityConfig) validate() error {
	if !c.Enabled {
		return errors.New("operator authentication must be enabled")
	}
	if c.Algorithm != "HS256" {
		return errors.New("unsupported operator assertion algorithm")
	}
	if len(c.Secret) < 64 {
		return errors.New("operator assertion secret must contain at least 64 bytes")
	}
	if strings.TrimSpace(c.Issuer) == "" || strings.TrimSpace(c.Audience) == "" {
		return errors.New("operator assertion issuer and audience are required")
	}
	if c.MaxLifetime < 5*time.Second || c.MaxLifetime > 5*time.Minute {
		return errors.New("unsafe operator assertion maximum lifetime")
	}
	if c.ClockSkew < 0 || c.ClockSkew > 30*time.Second {
		return errors.New("unsafe operator assertion clock skew")
	}
	if c.ReplayTTL < c.MaxLifetime+c.ClockSkew || c.ReplayTTL > 10*time.Minute {
		return errors.New("unsafe operator assertion replay retention")
	}
	if c.RateLimit < 1 || c.RateLimit > 100 || c.RateWindow < time.Second || c.RateWindow > time.Minute {
		return errors.New("unsafe operator administrative rate limit")
	}
	return nil
}
func envOperatorSecurityConfig() (operatorSecurityConfig, error) {
	seconds := func(name string, fallback int) (time.Duration, error) {
		n, e := strconv.Atoi(getenv(name, strconv.Itoa(fallback)))
		return time.Duration(n) * time.Second, e
	}
	max, e := seconds("OPERATOR_ASSERTION_MAX_LIFETIME_SECONDS", 60)
	if e != nil {
		return operatorSecurityConfig{}, e
	}
	skew, e := seconds("OPERATOR_ASSERTION_CLOCK_SKEW_SECONDS", 5)
	if e != nil {
		return operatorSecurityConfig{}, e
	}
	replay, e := seconds("OPERATOR_ASSERTION_REPLAY_TTL_SECONDS", 90)
	if e != nil {
		return operatorSecurityConfig{}, e
	}
	window, e := seconds("OPERATOR_ADMIN_RATE_WINDOW_SECONDS", 60)
	if e != nil {
		return operatorSecurityConfig{}, e
	}
	limit, e := strconv.Atoi(getenv("OPERATOR_ADMIN_RATE_LIMIT", "20"))
	if e != nil {
		return operatorSecurityConfig{}, e
	}
	c := operatorSecurityConfig{Enabled: strings.EqualFold(getenv("OPERATOR_AUTH_ENABLED", "true"), "true"), Algorithm: getenv("OPERATOR_ASSERTION_ALGORITHM", "HS256"), Secret: []byte(strings.TrimSpace(getenv("OPERATOR_ASSERTION_SECRET", ""))), Issuer: getenv("OPERATOR_ASSERTION_ISSUER", "compound-api-gateway"), Audience: getenv("OPERATOR_ASSERTION_AUDIENCE", "compound-trading-engine"), MaxLifetime: max, ClockSkew: skew, ReplayTTL: replay, RateLimit: limit, RateWindow: window}
	return c, c.validate()
}

type operatorClaims struct {
	Version     string   `json:"ver"`
	Issuer      string   `json:"iss"`
	Audience    string   `json:"aud"`
	Subject     string   `json:"sub"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	IssuedAt    int64    `json:"iat"`
	ExpiresAt   int64    `json:"exp"`
	NotBefore   int64    `json:"nbf"`
	TokenID     string   `json:"jti"`
}
type replayStore interface {
	Consume(context.Context, string, time.Duration) (bool, error)
	Healthy(context.Context) error
}
type redisOperatorReplayStore struct{ client *redis.Client }

func (s redisOperatorReplayStore) Consume(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, "trading-engine:operator-replay:"+id, "used", ttl).Result()
}
func (s redisOperatorReplayStore) Healthy(ctx context.Context) error { return s.client.Ping(ctx).Err() }

type memoryOperatorReplayStore struct {
	mu          sync.Mutex
	used        map[string]bool
	unavailable bool
}

func (s *memoryOperatorReplayStore) Consume(_ context.Context, id string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return false, errors.New("replay unavailable")
	}
	if s.used[id] {
		return false, nil
	}
	s.used[id] = true
	return true, nil
}
func (s *memoryOperatorReplayStore) Healthy(context.Context) error {
	if s.unavailable {
		return errors.New("replay unavailable")
	}
	return nil
}

type operatorAuthorizer struct {
	cfg    operatorSecurityConfig
	replay replayStore
	now    func() time.Time
}

func (a *operatorAuthorizer) authenticate(ctx context.Context, raw, required string) (operatorClaims, string, error) {
	if a == nil || a.replay == nil {
		return operatorClaims{}, "AUTH_DEPENDENCY_UNAVAILABLE", errors.New("authentication unavailable")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > 8192 {
		return operatorClaims{}, "MALFORMED_ASSERTION", errors.New("invalid assertion")
	}
	decode := func(v string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(v) }
	hb, e := decode(parts[0])
	if e != nil {
		return operatorClaims{}, "MALFORMED_ASSERTION", e
	}
	var h struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(hb, &h) != nil {
		return operatorClaims{}, "MALFORMED_ASSERTION", errors.New("invalid header")
	}
	if h.Algorithm != "HS256" || h.Algorithm != a.cfg.Algorithm || h.Type != "JWT" {
		return operatorClaims{}, "UNSUPPORTED_ALGORITHM", errors.New("unsupported algorithm")
	}
	sig, e := decode(parts[2])
	if e != nil {
		return operatorClaims{}, "INVALID_SIGNATURE", e
	}
	mac := hmac.New(sha256.New, a.cfg.Secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return operatorClaims{}, "INVALID_SIGNATURE", errors.New("invalid signature")
	}
	pb, e := decode(parts[1])
	if e != nil {
		return operatorClaims{}, "MALFORMED_ASSERTION", e
	}
	var c operatorClaims
	if json.Unmarshal(pb, &c) != nil {
		return c, "MALFORMED_ASSERTION", errors.New("invalid claims")
	}
	now := a.now().UTC().Unix()
	if c.Version != operatorAssertionVersion || c.Issuer != a.cfg.Issuer || c.Audience != a.cfg.Audience || !validPaperIdentifier(c.Subject) || !validPaperIdentifier(c.TokenID) {
		return c, "INVALID_CLAIMS", errors.New("invalid claims")
	}
	if c.Role != "operator" && c.Role != "security_operator" {
		return c, "UNKNOWN_ROLE", errors.New("unknown role")
	}
	if c.IssuedAt <= 0 || c.ExpiresAt <= 0 || c.NotBefore <= 0 || c.ExpiresAt <= c.IssuedAt || time.Duration(c.ExpiresAt-c.IssuedAt)*time.Second > a.cfg.MaxLifetime {
		return c, "INVALID_LIFETIME", errors.New("invalid lifetime")
	}
	skew := int64(a.cfg.ClockSkew.Seconds())
	if c.IssuedAt > now+skew {
		return c, "FUTURE_ASSERTION", errors.New("future assertion")
	}
	if c.ExpiresAt < now-skew {
		return c, "EXPIRED_ASSERTION", errors.New("expired assertion")
	}
	if c.NotBefore > now+skew {
		return c, "ASSERTION_NOT_ACTIVE", errors.New("assertion not active")
	}
	seen := map[string]bool{}
	allowed := false
	for _, p := range c.Permissions {
		if !operatorPermissions[p] || seen[p] {
			return c, "UNKNOWN_PERMISSION", errors.New("unknown permission")
		}
		seen[p] = true
		if p == required {
			allowed = true
		}
	}
	if !operatorPermissions[required] || !allowed {
		return c, "PERMISSION_DENIED", errors.New("permission denied")
	}
	ok, e := a.replay.Consume(ctx, c.TokenID, a.cfg.ReplayTTL)
	if e != nil {
		return c, "REPLAY_STORAGE_UNAVAILABLE", e
	}
	if !ok {
		return c, "REPLAY_ATTEMPT", errors.New("replay attempt")
	}
	return c, "AUTHENTICATED", nil
}
func (a *operatorAuthorizer) readiness(ctx context.Context) (bool, string) {
	if a == nil || a.cfg.validate() != nil {
		return false, "AUTH_CONFIGURATION_INVALID"
	}
	if a.replay == nil || a.replay.Healthy(ctx) != nil {
		return false, "REPLAY_STORAGE_UNAVAILABLE"
	}
	return true, "READY"
}

type rateLimiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}
type redisOperatorRateLimiter struct{ client *redis.Client }

func (s redisOperatorRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	k := "trading-engine:operator-rate:" + key
	n, e := s.client.Incr(ctx, k).Result()
	if e != nil {
		return false, e
	}
	if n == 1 {
		_ = s.client.Expire(ctx, k, window).Err()
	}
	return n <= int64(limit), nil
}

type memoryOperatorRateLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (s *memoryOperatorRateLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[key]++
	return s.counts[key] <= limit, nil
}

type operatorCommandRecord struct {
	Fingerprint string         `json:"fingerprint"`
	Result      operatorResult `json:"result"`
}

var errOperatorCommandNotFound = errors.New("operator command not found")

type operatorCommandStore interface {
	Load(context.Context, string) (operatorCommandRecord, error)
	Save(context.Context, string, operatorCommandRecord) (bool, error)
}
type redisOperatorCommandStore struct {
	client    *redis.Client
	retention time.Duration
}

func (s redisOperatorCommandStore) Load(ctx context.Context, key string) (operatorCommandRecord, error) {
	var v operatorCommandRecord
	b, e := s.client.Get(ctx, "trading-engine:operator-command:"+key).Bytes()
	if errors.Is(e, redis.Nil) {
		return v, errOperatorCommandNotFound
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	return v, e
}
func (s redisOperatorCommandStore) Save(ctx context.Context, key string, v operatorCommandRecord) (bool, error) {
	b, _ := json.Marshal(v)
	return s.client.SetNX(ctx, "trading-engine:operator-command:"+key, b, s.retention).Result()
}

type memoryOperatorCommandStore struct {
	mu          sync.Mutex
	values      map[string]operatorCommandRecord
	unavailable bool
}

func (s *memoryOperatorCommandStore) Load(_ context.Context, key string) (operatorCommandRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return operatorCommandRecord{}, errors.New("command store unavailable")
	}
	v, ok := s.values[key]
	if !ok {
		return v, errOperatorCommandNotFound
	}
	return v, nil
}
func (s *memoryOperatorCommandStore) Save(_ context.Context, key string, v operatorCommandRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return false, errors.New("command store unavailable")
	}
	if _, ok := s.values[key]; ok {
		return false, nil
	}
	s.values[key] = v
	return true, nil
}

type operatorAPI struct {
	auth       *operatorAuthorizer
	operations *paperOperations
	limiter    rateLimiter
	commands   operatorCommandStore
}
type operatorRequest struct {
	RequestID          string    `json:"requestId"`
	IdempotencyKey     string    `json:"idempotencyKey"`
	Reason             string    `json:"reason"`
	Timestamp          time.Time `json:"timestamp"`
	PreviewFingerprint string    `json:"previewFingerprint,omitempty"`
}
type operatorResult struct {
	Outcome             string `json:"outcome"`
	Action              string `json:"action"`
	RequestID           string `json:"requestId"`
	IdempotentReplay    bool   `json:"idempotentReplay,omitempty"`
	EvidenceFingerprint string `json:"evidenceFingerprint,omitempty"`
	Warning             string `json:"warning"`
}

func decodeOperatorRequest(w http.ResponseWriter, r *http.Request) (operatorRequest, error) {
	var in operatorRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, operatorBodyLimit))
	d.DisallowUnknownFields()
	if e := d.Decode(&in); e != nil {
		return in, e
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		return in, errors.New("multiple json values")
	}
	if !validPaperIdentifier(in.RequestID) || !validPaperIdentifier(in.IdempotencyKey) || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 || in.Timestamp.IsZero() {
		return in, errors.New("invalid request")
	}
	return in, nil
}
func (api *operatorAPI) handler(permission, action string, mutation bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mutation && r.Method != http.MethodPost || !mutation && r.Method != http.MethodGet {
			jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		claims, reason, e := api.auth.authenticate(r.Context(), strings.TrimSpace(r.Header.Get(operatorAssertionHeader)), permission)
		if e != nil {
			if reason == "PERMISSION_DENIED" || reason == "UNKNOWN_PERMISSION" || reason == "UNKNOWN_ROLE" {
				api.operations.auditRejected(r.Context(), claims.Subject, "AUTHORIZATION_DENIED", action)
				operatorSecurityEvents.WithLabelValues("authorization_denied", reason).Inc()
				jsonResponse(w, http.StatusForbidden, map[string]any{"error": "operator_authorization_failed"})
				return
			}
			operatorSecurityEvents.WithLabelValues("authentication_rejected", reason).Inc()
			jsonResponse(w, http.StatusUnauthorized, map[string]any{"error": "operator_authentication_failed"})
			return
		}
		operatorSecurityEvents.WithLabelValues("authentication_success", "NONE").Inc()
		if !mutation {
			api.read(w, r, action)
			return
		}
		in, e := decodeOperatorRequest(w, r)
		if e != nil {
			api.operations.auditRejected(r.Context(), claims.Subject, "INVALID_REQUEST", action)
			jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		delta := api.auth.now().UTC().Sub(in.Timestamp.UTC())
		if delta > api.auth.cfg.ClockSkew || delta < -api.auth.cfg.ClockSkew {
			api.operations.auditRejected(r.Context(), claims.Subject, "INVALID_TIMESTAMP", action)
			jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid_timestamp"})
			return
		}
		allowed, e := api.limiter.Allow(r.Context(), claims.Subject, api.auth.cfg.RateLimit, api.auth.cfg.RateWindow)
		if e != nil || !allowed {
			operatorSecurityEvents.WithLabelValues("rate_limited", "LIMIT_EXCEEDED").Inc()
			api.operations.auditRejected(r.Context(), claims.Subject, "RATE_LIMITED", action)
			jsonResponse(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited"})
			return
		}
		fp := safeCommandFingerprint(in, action, claims.Subject)
		if old, e := api.commands.Load(r.Context(), in.IdempotencyKey); e == nil {
			if old.Fingerprint != fp {
				api.operations.auditRejected(r.Context(), claims.Subject, "IDEMPOTENCY_CONFLICT", action)
				operatorSecurityEvents.WithLabelValues("idempotency_conflict", "CHANGED_REUSE").Inc()
				jsonResponse(w, http.StatusConflict, map[string]any{"error": "idempotency_conflict"})
				return
			}
			old.Result.IdempotentReplay = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(old.Result)
			return
		} else if !errors.Is(e, errOperatorCommandNotFound) {
			api.operations.auditRejected(r.Context(), claims.Subject, "IDEMPOTENCY_STORAGE_UNAVAILABLE", action)
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "operator_command_unavailable"})
			return
		}
		result, status, e := api.operations.executeSecureCommand(r.Context(), claims, in, action)
		if e != nil {
			jsonResponse(w, status, map[string]any{"error": "operator_command_rejected"})
			return
		}
		created, e := api.commands.Save(r.Context(), in.IdempotencyKey, operatorCommandRecord{Fingerprint: fp, Result: result})
		if e != nil || !created {
			api.operations.auditRejected(r.Context(), claims.Subject, "IDEMPOTENCY_STORAGE_UNAVAILABLE", action)
			jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "operator_command_unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	}
}
func (api *operatorAPI) read(w http.ResponseWriter, r *http.Request, action string) {
	switch action {
	case "STATUS":
		jsonResponse(w, http.StatusOK, map[string]any{"operations": api.operations.status()})
	case "FINDINGS":
		api.operations.findingsHandler(w, r)
	case "LEDGER":
		api.operations.ledgerHandler(w, r)
	case "ROLLUP":
		api.operations.rollupHandler(w, r)
	default:
		jsonResponse(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
}
func (api *operatorAPI) protect(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, reason, e := api.auth.authenticate(r.Context(), strings.TrimSpace(r.Header.Get(operatorAssertionHeader)), permission)
		if e != nil {
			operatorSecurityEvents.WithLabelValues("authentication_rejected", reason).Inc()
			jsonResponse(w, http.StatusUnauthorized, map[string]any{"error": "operator_authentication_failed"})
			return
		}
		next(w, r)
	}
}
func signOperatorAssertion(c operatorClaims, secret []byte) string {
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	p, _ := json.Marshal(c)
	v := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write([]byte(v))
	return v + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func safeCommandFingerprint(in operatorRequest, action, actor string) string {
	return fingerprint(struct {
		RequestID, IdempotencyKey, Reason, Action, Actor, Preview string
		Timestamp                                                 time.Time
	}{in.RequestID, in.IdempotencyKey, strings.TrimSpace(in.Reason), action, actor, in.PreviewFingerprint, in.Timestamp.UTC()})
}
func operatorEventID(prefix, key string) string {
	return prefix + "-" + strings.TrimPrefix(fingerprint(key), "sha256:")
}
