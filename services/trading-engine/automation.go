package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const automationWarning = "HISTORICAL_READINESS_DOES_NOT_GUARANTEE_FUTURE_PERFORMANCE"

var canonicalAutomationStrategies = map[string]bool{
	"EMA_PULLBACK": true, "EMA_CROSSOVER": true, "TREND_CONTINUATION": true, "LONDON_BREAKOUT": true,
}

type automationFreshness struct {
	ReadinessMaxAgeSeconds int     `json:"readinessMaxAgeSeconds"`
	QuoteMaxAgeSeconds     int     `json:"quoteMaxAgeSeconds"`
	CandleMaxAgeSeconds    int     `json:"candleMaxAgeSeconds"`
	MaximumSpreadPips      float64 `json:"maximumSpreadPips"`
}
type automationSetup struct {
	Outcome            string  `json:"outcome"`
	Pair               string  `json:"pair"`
	Timeframe          string  `json:"timeframe"`
	Strategy           string  `json:"strategy"`
	StrategyVersion    string  `json:"strategyVersion"`
	Direction          string  `json:"direction"`
	Entry              float64 `json:"entry"`
	StopLoss           float64 `json:"stopLoss"`
	TakeProfit         float64 `json:"takeProfit"`
	MaximumRiskPercent float64 `json:"maximumRiskPercent"`
	Fingerprint        string  `json:"fingerprint"`
}
type automationSignal struct {
	Strategy      string `json:"strategy"`
	Direction     string `json:"direction,omitempty"`
	ConditionsMet bool   `json:"conditionsMet"`
}
type automationStrategyDecision struct {
	Outcome              string             `json:"outcome"`
	Reason               string             `json:"reason,omitempty"`
	RequestID            string             `json:"requestId,omitempty"`
	Pair                 string             `json:"pair,omitempty"`
	Direction            string             `json:"direction,omitempty"`
	SelectedStrategy     string             `json:"selectedStrategy,omitempty"`
	ConfirmingStrategies []string           `json:"confirmingStrategies"`
	Evaluations          int                `json:"evaluations"`
	Setups               []automationSetup  `json:"setups"`
	Signals              []automationSignal `json:"signals"`
}
type automationReadiness struct {
	DecidedAt string              `json:"decidedAt"`
	Decision  walkForwardDecision `json:"decision"`
}
type automationQuote struct {
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`
	Timestamp string  `json:"timestamp"`
}
type automationCandle struct {
	Timestamp string  `json:"timestamp"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Closed    bool    `json:"closed"`
}
type automationCandles struct {
	H1 []automationCandle `json:"h1"`
	H4 []automationCandle `json:"h4"`
}
type paperAutomationRequest struct {
	RequestID       string                     `json:"requestId"`
	Mode            string                     `json:"mode"`
	Freshness       automationFreshness        `json:"freshness"`
	StrategyManager automationStrategyDecision `json:"strategyManager"`
	Readiness       automationReadiness        `json:"readiness"`
	Quote           automationQuote            `json:"quote"`
	Candles         automationCandles          `json:"candles"`
	Risk            positionSizeRequest        `json:"risk"`
}
type automationGate struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
}
type paperAutomationAudit struct {
	RequestID                    string             `json:"requestId"`
	RequestFingerprint           string             `json:"requestFingerprint"`
	SetupFingerprint             string             `json:"setupFingerprint,omitempty"`
	ReadinessDecisionFingerprint string             `json:"readinessDecisionFingerprint,omitempty"`
	Strategy                     string             `json:"strategy,omitempty"`
	StrategyVersion              string             `json:"strategyVersion,omitempty"`
	Pair                         string             `json:"pair,omitempty"`
	Timeframe                    string             `json:"timeframe,omitempty"`
	RiskDecision                 positionSizeResult `json:"riskDecision"`
	PositionSize                 float64            `json:"positionSize,omitempty"`
	PaperOrderID                 string             `json:"paperOrderId,omitempty"`
	PaperPositionID              string             `json:"paperPositionId,omitempty"`
	Gates                        []automationGate   `json:"gates"`
	Outcome                      string             `json:"outcome"`
	Reasons                      []string           `json:"reasons,omitempty"`
	Timestamp                    time.Time          `json:"timestamp"`
	AutomationFingerprint        string             `json:"automationFingerprint"`
}
type paperAutomationResult struct {
	Outcome  string                `json:"outcome"`
	Reasons  []string              `json:"reasons,omitempty"`
	Warnings []string              `json:"warnings"`
	Audit    *paperAutomationAudit `json:"audit,omitempty"`
}

type paperAutomationStore interface {
	Get(context.Context, string) (paperAutomationAudit, error)
	Save(context.Context, paperAutomationAudit) (paperAutomationAudit, bool, error)
	HasPairExposure(context.Context, string) (bool, error)
	Acquire(context.Context) (func(), error)
}
type memoryPaperAutomationStore struct {
	mu     sync.Mutex
	audits map[string]paperAutomationAudit
	pairs  map[string]bool
	fail   bool
}

func newMemoryPaperAutomationStore() *memoryPaperAutomationStore {
	return &memoryPaperAutomationStore{audits: map[string]paperAutomationAudit{}, pairs: map[string]bool{}}
}
func (s *memoryPaperAutomationStore) Get(_ context.Context, id string) (paperAutomationAudit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return paperAutomationAudit{}, errors.New("storage unavailable")
	}
	a, ok := s.audits[id]
	if !ok {
		return paperAutomationAudit{}, redis.Nil
	}
	return a, nil
}
func (s *memoryPaperAutomationStore) Save(_ context.Context, a paperAutomationAudit) (paperAutomationAudit, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return paperAutomationAudit{}, false, errors.New("storage unavailable")
	}
	if old, ok := s.audits[a.RequestID]; ok {
		return old, false, nil
	}
	s.audits[a.RequestID] = a
	if a.Outcome == "PAPER_ACCEPTED" {
		s.pairs[a.Pair] = true
	}
	return a, true, nil
}
func (s *memoryPaperAutomationStore) HasPairExposure(_ context.Context, pair string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return false, errors.New("storage unavailable")
	}
	return s.pairs[pair], nil
}
func (s *memoryPaperAutomationStore) Acquire(context.Context) (func(), error) {
	return func() {}, nil
}

type redisPaperAutomationStore struct {
	client    *redis.Client
	retention time.Duration
}

func (s redisPaperAutomationStore) key(id string) string {
	return "trading-engine:paper-automation:" + id
}
func (s redisPaperAutomationStore) pairKey(pair string) string {
	return "trading-engine:paper-automation-open-pair:" + pair
}
func (s redisPaperAutomationStore) Get(ctx context.Context, id string) (paperAutomationAudit, error) {
	value, err := s.client.Get(ctx, s.key(id)).Bytes()
	if err != nil {
		return paperAutomationAudit{}, err
	}
	var a paperAutomationAudit
	err = json.Unmarshal(value, &a)
	return a, err
}
func (s redisPaperAutomationStore) Save(ctx context.Context, a paperAutomationAudit) (result paperAutomationAudit, created bool, err error) {
	key := s.key(a.RequestID)
	err = s.client.Watch(ctx, func(tx *redis.Tx) error {
		value, getErr := tx.Get(ctx, key).Bytes()
		if getErr == nil {
			return json.Unmarshal(value, &result)
		}
		if !errors.Is(getErr, redis.Nil) {
			return getErr
		}
		payload, marshalErr := json.Marshal(a)
		if marshalErr != nil {
			return marshalErr
		}
		_, pipeErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, s.retention)
			if a.Outcome == "PAPER_ACCEPTED" {
				pipe.Set(ctx, s.pairKey(a.Pair), a.PaperPositionID, s.retention)
			}
			return nil
		})
		if pipeErr == nil {
			result, created = a, true
		}
		return pipeErr
	}, key)
	return
}
func (s redisPaperAutomationStore) HasPairExposure(ctx context.Context, pair string) (bool, error) {
	count, err := s.client.Exists(ctx, s.pairKey(pair)).Result()
	return count > 0, err
}
func (s redisPaperAutomationStore) Acquire(ctx context.Context) (func(), error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := fingerprint(tokenBytes)
	key := "trading-engine:paper-automation:global-lock"
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		locked, err := s.client.SetNX(ctx, key, token, 30*time.Second).Result()
		if err != nil {
			return nil, err
		}
		if locked {
			return func() {
				const release = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
				_ = s.client.Eval(context.Background(), release, []string{key}, token).Err()
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("automation lock unavailable")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func setupFingerprint(setup automationSetup) string {
	setup.Fingerprint = ""
	return fingerprint(setup)
}
func walkForwardDecisionFingerprint(d walkForwardDecision) string {
	identity := walkForwardIdentity{d.Pair, d.Timeframe, d.Strategy, d.StrategyVersion}
	return fingerprint(struct {
		Outcome                         string
		Identity                        walkForwardIdentity
		Config, DatasetSet, EvidenceSet string
		Datasets, Evidence              []string
		Folds                           []foldEvidence
		Gates                           []readinessGate
	}{d.Outcome, identity, d.ConfigurationFingerprint, d.DatasetSetFingerprint, d.EvidenceSetFingerprint,
		d.DatasetFingerprints, d.EvidenceFingerprints, d.Folds, d.Gates})
}
func validActiveReadinessDecision(d walkForwardDecision) bool {
	if d.Outcome != "READY" || len(d.Reasons) != 0 || d.TrainingMetrics == nil || d.OutOfSampleMetrics == nil ||
		len(d.Folds) == 0 || len(d.Gates) == 0 || len(d.DatasetFingerprints) == 0 || len(d.EvidenceFingerprints) == 0 ||
		!sha256FingerprintPattern.MatchString(d.ConfigurationFingerprint) ||
		d.DatasetSetFingerprint != fingerprint(d.DatasetFingerprints) ||
		d.EvidenceSetFingerprint != fingerprint(d.EvidenceFingerprints) {
		return false
	}
	for _, value := range append(append([]string{}, d.DatasetFingerprints...), d.EvidenceFingerprints...) {
		if !sha256FingerprintPattern.MatchString(value) {
			return false
		}
	}
	for _, gate := range d.Gates {
		if !gate.Passed || !finiteNumber(gate.Actual) || !finiteNumber(gate.Threshold) {
			return false
		}
	}
	return sha256FingerprintPattern.MatchString(d.DecisionFingerprint) &&
		d.DecisionFingerprint == walkForwardDecisionFingerprint(d)
}
func validateAutomationCandles(candles []automationCandle, interval time.Duration, now time.Time, maxAge time.Duration) bool {
	if len(candles) < 2 {
		return false
	}
	var previous time.Time
	for i, candle := range candles {
		at, err := time.Parse(time.RFC3339Nano, candle.Timestamp)
		base := backtestCandle{candle.Timestamp, candle.Open, candle.High, candle.Low, candle.Close}
		if err != nil || !candle.Closed || !validOHLC(base) || at.Add(interval).After(now.Add(paperFutureSkew)) {
			return false
		}
		if i > 0 && at.Sub(previous) != interval {
			return false
		}
		previous = at
	}
	age := now.Sub(previous.Add(interval))
	return age >= -paperFutureSkew && age <= maxAge
}

func (app *application) paperAutomationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAutomationResult(w, http.StatusMethodNotAllowed, paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_MARKET_DATA"}, Warnings: []string{automationWarning}})
		return
	}
	var input paperAutomationRequest
	if decodeStrictJSON(w, r, &input) != nil {
		writeAutomationResult(w, http.StatusBadRequest, paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_MARKET_DATA"}, Warnings: []string{automationWarning}})
		return
	}
	result, status := app.evaluatePaperAutomation(r.Context(), input)
	writeAutomationResult(w, status, result)
}

func (app *application) evaluatePaperAutomation(ctx context.Context, input paperAutomationRequest) (paperAutomationResult, int) {
	warnings := []string{automationWarning, "PAPER_ONLY_NO_LIVE_EXECUTION"}
	requestFP := fingerprint(input)
	if app.automationStore == nil || app.paperStore == nil || app.lifecycleStore == nil {
		return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"STORAGE_UNAVAILABLE"}, Warnings: warnings}, http.StatusServiceUnavailable
	}
	mutex := app.automationMu
	if mutex == nil {
		mutex = &sync.Mutex{}
		app.automationMu = mutex
	}
	mutex.Lock()
	defer mutex.Unlock()
	unlock, err := app.automationStore.Acquire(ctx)
	if err != nil {
		return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"STORAGE_UNAVAILABLE"}, Warnings: warnings}, http.StatusServiceUnavailable
	}
	defer unlock()
	if old, err := app.automationStore.Get(ctx, strings.TrimSpace(input.RequestID)); err == nil {
		if old.RequestFingerprint != requestFP {
			return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"IDEMPOTENCY_CONFLICT"}, Warnings: warnings, Audit: &old}, http.StatusConflict
		}
		return paperAutomationResult{Outcome: "IDEMPOTENT_REPLAY", Warnings: warnings, Audit: &old}, http.StatusOK
	} else if !errors.Is(err, redis.Nil) {
		return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"STORAGE_UNAVAILABLE"}, Warnings: warnings}, http.StatusServiceUnavailable
	}
	now, gates := app.currentTime(), []automationGate{}
	pass := func(code string) { gates = append(gates, automationGate{code, true}) }
	fail := func(reason string, setup *automationSetup, risk positionSizeResult) (paperAutomationResult, int) {
		gates = append(gates, automationGate{reason, false})
		a := newAutomationAudit(input, requestFP, setup, risk, gates, "NO_TRADE", []string{reason}, now)
		stored, created, err := app.automationStore.Save(ctx, a)
		if err != nil {
			return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"STORAGE_UNAVAILABLE"}, Warnings: warnings}, http.StatusServiceUnavailable
		}
		if !created && stored.RequestFingerprint != requestFP {
			return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"IDEMPOTENCY_CONFLICT"}, Warnings: warnings, Audit: &stored}, http.StatusConflict
		}
		return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{reason}, Warnings: warnings, Audit: &stored}, http.StatusOK
	}
	if !validPaperIdentifier(strings.TrimSpace(input.RequestID)) || strings.ToUpper(strings.TrimSpace(input.Mode)) != "PAPER" ||
		input.Freshness.ReadinessMaxAgeSeconds <= 0 || input.Freshness.QuoteMaxAgeSeconds <= 0 || input.Freshness.CandleMaxAgeSeconds <= 0 ||
		!finitePositive(input.Freshness.MaximumSpreadPips) || input.Freshness.MaximumSpreadPips > 5 {
		return fail("INVALID_MARKET_DATA", nil, positionSizeResult{})
	}
	pass("PAPER_MODE")
	if input.StrategyManager.Outcome != "SELECTED_SETUP" || len(input.StrategyManager.Setups) != 1 ||
		input.StrategyManager.Setups[0].Outcome != "TRADE_SETUP" {
		return fail("NO_STRATEGY_SETUP", nil, positionSizeResult{})
	}
	setup := input.StrategyManager.Setups[0]
	if input.StrategyManager.RequestID != input.RequestID || input.StrategyManager.Pair != setup.Pair ||
		input.StrategyManager.Direction != setup.Direction || input.StrategyManager.SelectedStrategy != setup.Strategy {
		return fail("NO_STRATEGY_SETUP", &setup, positionSizeResult{})
	}
	for _, signal := range input.StrategyManager.Signals {
		if signal.ConditionsMet && signal.Direction != setup.Direction {
			return fail("SIGNAL_CONFLICT", &setup, positionSizeResult{})
		}
	}
	pass("STRATEGY_SETUP")
	if !paperPairs[setup.Pair] || backtestTimeframes[setup.Timeframe] == 0 || !canonicalAutomationStrategies[setup.Strategy] ||
		!validPaperIdentifier(setup.StrategyVersion) || (setup.Strategy == "LONDON_BREAKOUT" && setup.Pair == "USDJPY") ||
		(setup.Direction != "BUY" && setup.Direction != "SELL") || setup.Fingerprint != setupFingerprint(setup) {
		return fail("INVALID_MARKET_DATA", &setup, positionSizeResult{})
	}
	d := input.Readiness.Decision
	if d.Outcome != "READY" || len(d.Reasons) != 0 {
		return fail("STRATEGY_NOT_READY", &setup, positionSizeResult{})
	}
	if d.Pair != setup.Pair || d.Timeframe != setup.Timeframe || d.Strategy != setup.Strategy || d.StrategyVersion != setup.StrategyVersion {
		return fail("STRATEGY_NOT_READY", &setup, positionSizeResult{})
	}
	if !validActiveReadinessDecision(d) {
		return fail("READINESS_FINGERPRINT_MISMATCH", &setup, positionSizeResult{})
	}
	decidedAt, err := time.Parse(time.RFC3339Nano, input.Readiness.DecidedAt)
	if err != nil || decidedAt.After(now.Add(paperFutureSkew)) || now.Sub(decidedAt) > time.Duration(input.Freshness.ReadinessMaxAgeSeconds)*time.Second {
		return fail("READINESS_EXPIRED", &setup, positionSizeResult{})
	}
	pass("READINESS_READY")
	quoteAt, err := time.Parse(time.RFC3339Nano, input.Quote.Timestamp)
	if err != nil || !finitePositive(input.Quote.Bid) || !finitePositive(input.Quote.Ask) || input.Quote.Bid >= input.Quote.Ask {
		return fail("INVALID_MARKET_DATA", &setup, positionSizeResult{})
	}
	if quoteAt.After(now.Add(paperFutureSkew)) || now.Sub(quoteAt) > time.Duration(input.Freshness.QuoteMaxAgeSeconds)*time.Second {
		return fail("STALE_QUOTE", &setup, positionSizeResult{})
	}
	pip, _ := pipSize(setup.Pair)
	if (input.Quote.Ask-input.Quote.Bid)/pip > input.Freshness.MaximumSpreadPips+1e-9 {
		return fail("EXCESSIVE_SPREAD", &setup, positionSizeResult{})
	}
	pass("QUOTE_VALID")
	candleAge := time.Duration(input.Freshness.CandleMaxAgeSeconds) * time.Second
	if !validateAutomationCandles(input.Candles.H1, time.Hour, now, candleAge) ||
		!validateAutomationCandles(input.Candles.H4, 4*time.Hour, now, candleAge) {
		return fail("INVALID_MARKET_DATA", &setup, positionSizeResult{})
	}
	pass("CANDLES_VALID")
	validStructure := setup.Direction == "BUY" && setup.StopLoss < setup.Entry && setup.TakeProfit > setup.Entry ||
		setup.Direction == "SELL" && setup.StopLoss > setup.Entry && setup.TakeProfit < setup.Entry
	if !validStructure || !finitePositive(setup.Entry) || !finitePositive(setup.StopLoss) || !finitePositive(setup.TakeProfit) {
		return fail("INVALID_PRICE_STRUCTURE", &setup, positionSizeResult{})
	}
	if setup.Entry < input.Quote.Bid-1e-9 || setup.Entry > input.Quote.Ask+1e-9 {
		return fail("INVALID_PRICE_STRUCTURE", &setup, positionSizeResult{})
	}
	if math.Abs(setup.TakeProfit-setup.Entry)/math.Abs(setup.Entry-setup.StopLoss)+1e-9 < 2 {
		return fail("INSUFFICIENT_RISK_REWARD", &setup, positionSizeResult{})
	}
	pass("PRICE_STRUCTURE")
	riskInput := input.Risk
	riskInput.RequestID, riskInput.Pair, riskInput.Direction = input.RequestID, setup.Pair, setup.Direction
	riskInput.Entry, riskInput.StopLoss = setup.Entry, setup.StopLoss
	if setup.MaximumRiskPercent <= 0 || setup.MaximumRiskPercent > 1 || math.Abs(riskInput.RiskPercent-setup.MaximumRiskPercent) > 1e-9 {
		return fail("RISK_REJECTED", &setup, positionSizeResult{})
	}
	risk := calculatePositionSize(riskInput)
	if risk.Outcome != "RISK_APPROVED" {
		return fail(mapRiskReason(risk.Reasons), &setup, risk)
	}
	pass("RISK_APPROVED")
	exposed, err := app.automationStore.HasPairExposure(ctx, setup.Pair)
	if err != nil {
		return fail("STORAGE_UNAVAILABLE", &setup, risk)
	}
	if exposed {
		return fail("PAIR_EXPOSURE", &setup, risk)
	}
	if app.automationFinalState == nil {
		return fail("STORAGE_UNAVAILABLE", &setup, risk)
	}
	finalInput := app.automationFinalState(input)
	finalInput.RequestID, finalInput.Pair, finalInput.Direction, finalInput.Entry, finalInput.StopLoss =
		input.RequestID, setup.Pair, setup.Direction, setup.Entry, setup.StopLoss
	finalRisk := calculatePositionSize(finalInput)
	if finalRisk.Outcome != "RISK_APPROVED" {
		return fail(mapRiskReason(finalRisk.Reasons), &setup, finalRisk)
	}
	if fingerprint(finalInput) != fingerprint(riskInput) {
		return fail("RISK_REJECTED", &setup, positionSizeResult{Outcome: "NO_TRADE", Reasons: []string{"STATE_CHANGED"}})
	}
	pass("FINAL_STATE_UNCHANGED")
	if app.automationAcceptanceGate != nil {
		if reason := app.automationAcceptanceGate(ctx, input); reason != "" {
			return fail(reason, &setup, finalRisk)
		}
		pass("RUNTIME_FENCE_VERIFIED")
	}
	if app.operationsAcceptanceGate != nil {
		if reason := app.operationsAcceptanceGate(ctx, input); reason != "" {
			return fail(reason, &setup, finalRisk)
		}
		pass("OPERATIONS_RECONCILIATION_VERIFIED")
	}
	orderResult := evaluatePaperOrder(paperOrderRequest{RequestID: input.RequestID, Mode: "PAPER", Pair: setup.Pair,
		Strategy: setup.Strategy, Direction: setup.Direction, Lots: risk.Lots, Entry: setup.Entry, StopLoss: setup.StopLoss,
		TakeProfit: setup.TakeProfit, QuoteTimestamp: quoteAt.Format(time.RFC3339Nano), RiskApproved: true}, now)
	if orderResult.Order == nil {
		return fail("RISK_REJECTED", &setup, risk)
	}
	order, created, err := app.paperStore.Create(ctx, *orderResult.Order)
	if err != nil || (!created && !samePaperOrder(order, *orderResult.Order)) {
		return fail("STORAGE_UNAVAILABLE", &setup, risk)
	}
	position := paperPosition{OrderID: order.OrderID, RequestID: order.RequestID, Pair: order.Pair, Strategy: order.Strategy,
		Direction: order.Direction, Lots: order.Lots, Entry: order.Entry, StopLoss: order.StopLoss, TakeProfit: order.TakeProfit,
		PipValuePerStandardLot: riskInput.PipValuePerStandardLot, Status: "OPEN", CurrentPrice: order.Entry, OpenedAt: now}
	eventID := "automation-open-" + input.RequestID
	event := paperJournalEvent{EventID: eventID, Type: "POSITION_OPENED", RecordedAt: now, Position: position}
	storedPosition, _, err := app.lifecycleStore.Open(ctx, eventID, position, event)
	if err != nil {
		return fail("STORAGE_UNAVAILABLE", &setup, risk)
	}
	if app.operationsRecordAccepted != nil {
		if err := app.operationsRecordAccepted(ctx, input, setup, order, storedPosition); err != nil {
			return fail("STORAGE_UNAVAILABLE", &setup, risk)
		}
	}
	pass("PAPER_POSITION_OPENED")
	audit := newAutomationAudit(input, requestFP, &setup, risk, gates, "PAPER_ACCEPTED", nil, now)
	audit.PaperOrderID, audit.PaperPositionID = order.OrderID, storedPosition.OrderID
	audit.AutomationFingerprint = automationAuditFingerprint(audit)
	stored, created, err := app.automationStore.Save(ctx, audit)
	if err != nil {
		return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"STORAGE_UNAVAILABLE"}, Warnings: warnings}, http.StatusServiceUnavailable
	}
	if !created && stored.RequestFingerprint != requestFP {
		return paperAutomationResult{Outcome: "NO_TRADE", Reasons: []string{"IDEMPOTENCY_CONFLICT"}, Warnings: warnings, Audit: &stored}, http.StatusConflict
	}
	return paperAutomationResult{Outcome: "PAPER_ACCEPTED", Warnings: warnings, Audit: &stored}, http.StatusAccepted
}

func mapRiskReason(reasons []string) string {
	if len(reasons) == 0 {
		return "RISK_REJECTED"
	}
	switch reasons[0] {
	case "DAILY_LOSS_LIMIT_REACHED":
		return "DAILY_LOSS_LIMIT"
	case "DRAWDOWN_LIMIT_REACHED":
		return "DRAWDOWN_LIMIT"
	case "POSITION_LIMIT_REACHED":
		return "POSITION_LIMIT"
	case "PAIR_EXPOSURE_EXISTS":
		return "PAIR_EXPOSURE"
	case "KILL_SWITCH_ACTIVE":
		return "KILL_SWITCH_ACTIVE"
	case "TRADING_DISABLED":
		return "TRADING_DISABLED"
	default:
		return "RISK_REJECTED"
	}
}
func newAutomationAudit(input paperAutomationRequest, requestFP string, setup *automationSetup, risk positionSizeResult, gates []automationGate, outcome string, reasons []string, now time.Time) paperAutomationAudit {
	a := paperAutomationAudit{RequestID: strings.TrimSpace(input.RequestID), RequestFingerprint: requestFP,
		ReadinessDecisionFingerprint: input.Readiness.Decision.DecisionFingerprint, RiskDecision: risk, PositionSize: risk.Lots,
		Gates: append([]automationGate(nil), gates...), Outcome: outcome, Reasons: reasons, Timestamp: now.UTC()}
	if setup != nil {
		a.SetupFingerprint, a.Strategy, a.StrategyVersion, a.Pair, a.Timeframe = setup.Fingerprint, setup.Strategy, setup.StrategyVersion, setup.Pair, setup.Timeframe
	}
	a.AutomationFingerprint = automationAuditFingerprint(a)
	return a
}
func automationAuditFingerprint(a paperAutomationAudit) string {
	a.AutomationFingerprint = ""
	return fingerprint(a)
}
func writeAutomationResult(w http.ResponseWriter, status int, result paperAutomationResult) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Trading-Mode", "PAPER_ONLY")
	w.Header().Set("X-Live-Execution", "PROHIBITED")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
