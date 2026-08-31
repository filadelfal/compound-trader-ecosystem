package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var operatorTestNow = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
var operatorTestSecret = []byte("operator-test-secret-0123456789abcdef-operator-test-secret-0123456789abcdef")

func testSecurityConfig() operatorSecurityConfig {
	return operatorSecurityConfig{Enabled: true, Algorithm: "HS256", Secret: operatorTestSecret, Issuer: "gateway", Audience: "trading", MaxLifetime: time.Minute, ClockSkew: 5 * time.Second, ReplayTTL: 90 * time.Second, RateLimit: 20, RateWindow: time.Minute}
}
func validOperatorClaims(id string, permission string) operatorClaims {
	n := operatorTestNow.Unix()
	return operatorClaims{Version: operatorAssertionVersion, Issuer: "gateway", Audience: "trading", Subject: "operator-1", Role: "operator", Permissions: []string{permission}, IssuedAt: n, NotBefore: n - 1, ExpiresAt: n + 30, TokenID: id}
}
func newTestAuthorizer(store *memoryOperatorReplayStore) *operatorAuthorizer {
	return &operatorAuthorizer{cfg: testSecurityConfig(), replay: store, now: func() time.Time { return operatorTestNow }}
}

func TestOperatorAssertionValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*operatorClaims)
		want   string
	}{{"wrong issuer", func(c *operatorClaims) { c.Issuer = "wrong" }, "INVALID_CLAIMS"}, {"wrong audience", func(c *operatorClaims) { c.Audience = "wrong" }, "INVALID_CLAIMS"}, {"missing subject", func(c *operatorClaims) { c.Subject = "" }, "INVALID_CLAIMS"}, {"expired", func(c *operatorClaims) {
		c.IssuedAt = operatorTestNow.Unix() - 40
		c.NotBefore = c.IssuedAt
		c.ExpiresAt = operatorTestNow.Unix() - 10
	}, "EXPIRED_ASSERTION"}, {"future issued at", func(c *operatorClaims) { c.IssuedAt = operatorTestNow.Unix() + 10 }, "FUTURE_ASSERTION"}, {"future not before", func(c *operatorClaims) { c.NotBefore = operatorTestNow.Unix() + 10 }, "ASSERTION_NOT_ACTIVE"}, {"excessive lifetime", func(c *operatorClaims) { c.ExpiresAt = c.IssuedAt + 61 }, "INVALID_LIFETIME"}, {"missing permission", func(c *operatorClaims) { c.Permissions = nil }, "PERMISSION_DENIED"}, {"unknown permission", func(c *operatorClaims) { c.Permissions = []string{"LIVE_TRADE"} }, "UNKNOWN_PERMISSION"}, {"unknown role", func(c *operatorClaims) { c.Role = "admin" }, "UNKNOWN_ROLE"}}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validOperatorClaims("token-"+string(rune('a'+i)), permissionPause)
			tc.mutate(&c)
			_, reason, err := newTestAuthorizer(&memoryOperatorReplayStore{used: map[string]bool{}}).authenticate(context.Background(), signOperatorAssertion(c, operatorTestSecret), permissionPause)
			if err == nil || reason != tc.want {
				t.Fatalf("want %s, got %s/%v", tc.want, reason, err)
			}
		})
	}
}

func TestValidOperatorAssertionAndReplay(t *testing.T) {
	store := &memoryOperatorReplayStore{used: map[string]bool{}}
	auth := newTestAuthorizer(store)
	token := signOperatorAssertion(validOperatorClaims("unique-token", permissionPause), operatorTestSecret)
	if _, reason, err := auth.authenticate(context.Background(), token, permissionPause); err != nil || reason != "AUTHENTICATED" {
		t.Fatalf("valid assertion rejected: %s %v", reason, err)
	}
	if _, reason, err := auth.authenticate(context.Background(), token, permissionPause); err == nil || reason != "REPLAY_ATTEMPT" {
		t.Fatalf("replay accepted: %s", reason)
	}
}
func TestMalformedSignatureAlgorithmAndOutage(t *testing.T) {
	auth := newTestAuthorizer(&memoryOperatorReplayStore{used: map[string]bool{}})
	if _, r, e := auth.authenticate(context.Background(), "bad", permissionPause); e == nil || r != "MALFORMED_ASSERTION" {
		t.Fatal("malformed accepted")
	}
	token := signOperatorAssertion(validOperatorClaims("bad-signature", permissionPause), []byte(strings.Repeat("x", 64)))
	if _, r, e := auth.authenticate(context.Background(), token, permissionPause); e == nil || r != "INVALID_SIGNATURE" {
		t.Fatal("signature accepted")
	}
	parts := strings.Split(signOperatorAssertion(validOperatorClaims("none", permissionPause), operatorTestSecret), ".")
	h, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	parts[0] = base64.RawURLEncoding.EncodeToString(h)
	if _, r, e := auth.authenticate(context.Background(), strings.Join(parts, "."), permissionPause); e == nil || r != "UNSUPPORTED_ALGORITHM" {
		t.Fatal("none accepted")
	}
	out := &memoryOperatorReplayStore{used: map[string]bool{}, unavailable: true}
	if _, r, e := newTestAuthorizer(out).authenticate(context.Background(), signOperatorAssertion(validOperatorClaims("outage", permissionPause), operatorTestSecret), permissionPause); e == nil || r != "REPLAY_STORAGE_UNAVAILABLE" {
		t.Fatal("outage accepted")
	}
}

func testOperatorAPI() (*operatorAPI, *paperOperations) {
	ops := newPaperOperations(newMemoryPaperOperationsStore(), func() time.Time { return operatorTestNow }, func(context.Context) (operationsEvidence, error) {
		return operationsEvidence{Journals: map[string][]paperJournalEvent{}, Risk: operationsRiskState{StartingEquity: 10000, CurrentEquity: 10000, PeakEquity: 10000, PairExposure: map[string]bool{}}}, nil
	})
	ops.ready = true
	return &operatorAPI{auth: newTestAuthorizer(&memoryOperatorReplayStore{used: map[string]bool{}}), operations: ops, limiter: &memoryOperatorRateLimiter{counts: map[string]int{}}, commands: &memoryOperatorCommandStore{values: map[string]operatorCommandRecord{}}}, ops
}
func operatorCall(api *operatorAPI, permission, action, id string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set(operatorAssertionHeader, signOperatorAssertion(validOperatorClaims(id, permission), operatorTestSecret))
	w := httptest.NewRecorder()
	api.handler(permission, action, true)(w, req)
	return w
}
func validOperatorBody(key string) string {
	return `{"requestId":"request-1","idempotencyKey":"` + key + `","reason":"maintenance","timestamp":"2026-08-28T12:00:00Z"}`
}

func TestAuthorizedPauseResumeKillAndUnsafeRelease(t *testing.T) {
	api, ops := testOperatorAPI()
	if w := operatorCall(api, permissionPause, "PAUSE", "pause-token", validOperatorBody("pause-key")); w.Code != 200 {
		t.Fatalf("pause: %d %s", w.Code, w.Body.String())
	}
	if !ops.paused {
		t.Fatal("not paused")
	}
	ops.ready = true
	if w := operatorCall(api, permissionResume, "RESUME", "resume-token", validOperatorBody("resume-key")); w.Code != 200 {
		t.Fatalf("resume: %d", w.Code)
	}
	if w := operatorCall(api, permissionKill, "KILL_SWITCH_ACTIVATE", "kill-token", validOperatorBody("kill-key")); w.Code != 200 {
		t.Fatalf("kill: %d", w.Code)
	}
	ops.blocking = true
	if w := operatorCall(api, permissionRelease, "KILL_SWITCH_RELEASE_REQUEST", "release-token", validOperatorBody("release-key")); w.Code != http.StatusConflict {
		t.Fatalf("unsafe release: %d", w.Code)
	}
}
func TestAuthorizationStrictJSONBodyTimestampAndLimit(t *testing.T) {
	api, _ := testOperatorAPI()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(validOperatorBody("denied-key")))
	req.Header.Set(operatorAssertionHeader, signOperatorAssertion(validOperatorClaims("denied-token", permissionRead), operatorTestSecret))
	w := httptest.NewRecorder()
	api.handler(permissionPause, "PAUSE", true)(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("permission denial: %d", w.Code)
	}
	w = operatorCall(api, permissionPause, "PAUSE", "unknown-token", `{"requestId":"request-1","idempotencyKey":"unknown-key","reason":"x","timestamp":"2026-08-28T12:00:00Z","extra":true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", w.Code)
	}
	w = operatorCall(api, permissionPause, "PAUSE", "time-token", `{"requestId":"request-1","idempotencyKey":"time-key","reason":"x","timestamp":"2026-08-28T12:01:00Z"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("timestamp: %d", w.Code)
	}
	large := strings.Repeat("x", operatorBodyLimit+1)
	w = operatorCall(api, permissionPause, "PAUSE", "large-token", large)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("body limit: %d", w.Code)
	}
}
func TestAdministrativeIdempotencyAndConflict(t *testing.T) {
	api, _ := testOperatorAPI()
	body := validOperatorBody("same-key")
	if w := operatorCall(api, permissionPause, "PAUSE", "idempotent-one", body); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := operatorCall(api, permissionPause, "PAUSE", "idempotent-two", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "idempotentReplay") {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	changed := strings.Replace(body, "maintenance", "changed", 1)
	if w = operatorCall(api, permissionPause, "PAUSE", "idempotent-three", changed); w.Code != http.StatusConflict {
		t.Fatalf("changed reuse: %d", w.Code)
	}
}
func TestRaceSensitiveReplay(t *testing.T) {
	auth := newTestAuthorizer(&memoryOperatorReplayStore{used: map[string]bool{}})
	token := signOperatorAssertion(validOperatorClaims("race-token", permissionPause), operatorTestSecret)
	var accepted int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, e := auth.authenticate(context.Background(), token, permissionPause); e == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d replays", accepted)
	}
}
func TestInvalidConfigurationAndNoLiveSurface(t *testing.T) {
	c := testSecurityConfig()
	c.Algorithm = "RS256"
	if c.validate() == nil {
		t.Fatal("unsupported algorithm accepted")
	}
	c = testSecurityConfig()
	c.Secret = []byte("weak")
	if c.validate() == nil {
		t.Fatal("weak secret accepted")
	}
	raw, _ := json.Marshal(operatorPermissions)
	if strings.Contains(string(raw), "LIVE") || strings.Contains(string(raw), "BROKER") {
		t.Fatal("live permission exists")
	}
}

func TestAuthenticationReadinessFailsClosed(t *testing.T) {
	auth := newTestAuthorizer(&memoryOperatorReplayStore{used: map[string]bool{}, unavailable: true})
	if ready, reason := auth.readiness(context.Background()); ready || reason != "REPLAY_STORAGE_UNAVAILABLE" {
		t.Fatalf("unexpected readiness: %v %s", ready, reason)
	}
	if ready, reason := (*operatorAuthorizer)(nil).readiness(context.Background()); ready || reason != "AUTH_CONFIGURATION_INVALID" {
		t.Fatalf("nil authentication appeared ready: %v %s", ready, reason)
	}
}

func TestRequiredAdministrativeFields(t *testing.T) {
	api, _ := testOperatorAPI()
	bodies := []string{
		`{"idempotencyKey":"key","reason":"maintenance","timestamp":"2026-08-28T12:00:00Z"}`,
		`{"requestId":"request","reason":"maintenance","timestamp":"2026-08-28T12:00:00Z"}`,
		`{"requestId":"request","idempotencyKey":"key","reason":"","timestamp":"2026-08-28T12:00:00Z"}`,
	}
	for i, body := range bodies {
		if w := operatorCall(api, permissionPause, "PAUSE", "required-token-"+string(rune('a'+i)), body); w.Code != http.StatusBadRequest {
			t.Fatalf("case %d accepted: %d", i, w.Code)
		}
	}
}

func TestReconciliationAndRepairAuthorizationFlow(t *testing.T) {
	api, _ := testOperatorAPI()
	if w := operatorCall(api, permissionReconcile, "RECONCILE", "reconcile-token", validOperatorBody("reconcile-key")); w.Code != http.StatusOK {
		t.Fatalf("reconcile failed: %d %s", w.Code, w.Body.String())
	}
	preview := operatorCall(api, permissionRepairView, "REPAIR_PREVIEW", "preview-token", validOperatorBody("preview-key"))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview failed: %d %s", preview.Code, preview.Body.String())
	}
	var result operatorResult
	if json.Unmarshal(preview.Body.Bytes(), &result) != nil || result.EvidenceFingerprint == "" {
		t.Fatalf("missing preview evidence: %s", preview.Body.String())
	}
	body := `{"requestId":"request-apply","idempotencyKey":"apply-key","reason":"derived state repair","timestamp":"2026-08-28T12:00:00Z","previewFingerprint":"` + result.EvidenceFingerprint + `"}`
	if w := operatorCall(api, permissionRepairApply, "REPAIR_APPLY", "apply-token", body); w.Code != http.StatusOK {
		t.Fatalf("apply failed: %d %s", w.Code, w.Body.String())
	}
}
