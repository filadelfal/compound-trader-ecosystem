package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

var automationNow = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func validAutomationDecision(setup automationSetup) walkForwardDecision {
	value := 2.0
	d := walkForwardDecision{
		Outcome: "READY", Warnings: historicalValidationWarnings(), Pair: setup.Pair, Timeframe: setup.Timeframe,
		Strategy: setup.Strategy, StrategyVersion: setup.StrategyVersion,
		TrainingMetrics:          &performanceMetrics{TotalTrades: 30, ExpectancyPerTrade: 2, ProfitFactor: &value},
		OutOfSampleMetrics:       &performanceMetrics{TotalTrades: 20, ExpectancyPerTrade: 1, ProfitFactor: &value},
		Folds:                    []foldEvidence{{FoldID: "fold-1", ProfitableOutOfSample: true}},
		Gates:                    []readinessGate{{Code: "MINIMUM_COMPLETED_FOLDS", Passed: true, Actual: 1, Threshold: 1}},
		ConfigurationFingerprint: fingerprint("config"), DatasetFingerprints: []string{fingerprint("dataset")},
		EvidenceFingerprints: []string{fingerprint("evidence")},
	}
	d.DatasetSetFingerprint = fingerprint(d.DatasetFingerprints)
	d.EvidenceSetFingerprint = fingerprint(d.EvidenceFingerprints)
	d.DecisionFingerprint = walkForwardDecisionFingerprint(d)
	return d
}

func automationCandleAt(at time.Time) automationCandle {
	return automationCandle{Timestamp: at.Format(time.RFC3339Nano), Open: 1.1, High: 1.2, Low: 1.0, Close: 1.15, Closed: true}
}

func validAutomationRequest(id string) paperAutomationRequest {
	setup := automationSetup{Outcome: "TRADE_SETUP", Pair: "EURUSD", Timeframe: "H1", Strategy: "EMA_PULLBACK", StrategyVersion: "1.0.0",
		Direction: "BUY", Entry: 1.1000, StopLoss: 1.0950, TakeProfit: 1.1100, MaximumRiskPercent: 0.5}
	setup.Fingerprint = setupFingerprint(setup)
	return paperAutomationRequest{
		RequestID: id, Mode: "PAPER",
		Freshness: automationFreshness{ReadinessMaxAgeSeconds: 86400, QuoteMaxAgeSeconds: 30, CandleMaxAgeSeconds: 60, MaximumSpreadPips: 3},
		StrategyManager: automationStrategyDecision{Outcome: "SELECTED_SETUP", RequestID: id, Pair: setup.Pair,
			Direction: setup.Direction, SelectedStrategy: setup.Strategy, ConfirmingStrategies: []string{},
			Evaluations: 1, Setups: []automationSetup{setup},
			Signals: []automationSignal{{Strategy: setup.Strategy, Direction: "BUY", ConditionsMet: true}}},
		Readiness: automationReadiness{DecidedAt: automationNow.Add(-time.Hour).Format(time.RFC3339Nano), Decision: validAutomationDecision(setup)},
		Quote:     automationQuote{Bid: 1.0999, Ask: 1.1001, Timestamp: automationNow.Add(-time.Second).Format(time.RFC3339Nano)},
		Candles: automationCandles{
			H1: []automationCandle{automationCandleAt(automationNow.Add(-2 * time.Hour)), automationCandleAt(automationNow.Add(-time.Hour))},
			H4: []automationCandle{automationCandleAt(automationNow.Add(-8 * time.Hour)), automationCandleAt(automationNow.Add(-4 * time.Hour))},
		},
		Risk: positionSizeRequest{Equity: 10000, RiskPercent: 0.5, PipValuePerStandardLot: 10, MinimumLot: 0.01,
			MaximumLot: 2, LotStep: 0.01, DailyLossPercent: 0.5, DrawdownPercent: 1, OpenPositions: 0,
			TradingEnabled: true},
	}
}

func automationApp() (*application, *memoryPaperAutomationStore) {
	store := newMemoryPaperAutomationStore()
	return &application{paperStore: newMemoryPaperOrderStore(), lifecycleStore: newMemoryPaperLifecycleStore(),
		automationStore: store, automationMu: &sync.Mutex{}, now: func() time.Time { return automationNow },
		automationFinalState: func(request paperAutomationRequest) positionSizeRequest { return request.Risk }}, store
}

func evaluateAutomation(t *testing.T, app *application, input paperAutomationRequest) paperAutomationResult {
	t.Helper()
	result, _ := app.evaluatePaperAutomation(context.Background(), input)
	return result
}

func requireAutomationReason(t *testing.T, got paperAutomationResult, want string) {
	t.Helper()
	if got.Outcome != "NO_TRADE" || len(got.Reasons) != 1 || got.Reasons[0] != want {
		t.Fatalf("expected NO_TRADE/%s, got %+v", want, got)
	}
}

func TestPaperAutomationAcceptsCompletePaperFlow(t *testing.T) {
	app, _ := automationApp()
	result, status := app.evaluatePaperAutomation(context.Background(), validAutomationRequest("auto-accepted"))
	if status != http.StatusAccepted || result.Outcome != "PAPER_ACCEPTED" || result.Audit == nil {
		t.Fatalf("unexpected result status=%d result=%+v", status, result)
	}
	if result.Audit.PaperOrderID == "" || result.Audit.PaperPositionID == "" || result.Audit.PositionSize <= 0 ||
		result.Audit.AutomationFingerprint != automationAuditFingerprint(*result.Audit) {
		t.Fatalf("incomplete or invalid audit: %+v", result.Audit)
	}
}

func TestPaperAutomationAcceptsCanonicalTrendContinuation(t *testing.T) {
	app, _ := automationApp()
	input := validAutomationRequest("trend-accepted")
	setup := &input.StrategyManager.Setups[0]
	setup.Strategy = "TREND_CONTINUATION"
	setup.Fingerprint = setupFingerprint(*setup)
	input.StrategyManager.Signals[0].Strategy = setup.Strategy
	input.StrategyManager.SelectedStrategy = setup.Strategy
	input.Readiness.Decision = validAutomationDecision(*setup)
	if got := evaluateAutomation(t, app, input); got.Outcome != "PAPER_ACCEPTED" {
		t.Fatalf("canonical trend continuation rejected: %+v", got)
	}
}

func TestPaperAutomationStrategyAndReadinessFailures(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*paperAutomationRequest)
	}{
		{"no setup", "NO_STRATEGY_SETUP", func(v *paperAutomationRequest) {
			v.StrategyManager.Outcome = "NO_TRADE"
			v.StrategyManager.Setups = nil
		}},
		{"conflict", "SIGNAL_CONFLICT", func(v *paperAutomationRequest) {
			v.StrategyManager.Signals = append(v.StrategyManager.Signals, automationSignal{Strategy: "EMA_CROSSOVER", Direction: "SELL", ConditionsMet: true})
		}},
		{"not ready", "STRATEGY_NOT_READY", func(v *paperAutomationRequest) { v.Readiness.Decision.Outcome = "NOT_READY" }},
		{"identity mismatch", "STRATEGY_NOT_READY", func(v *paperAutomationRequest) {
			v.Readiness.Decision.Pair = "GBPUSD"
			v.Readiness.Decision.DecisionFingerprint = walkForwardDecisionFingerprint(v.Readiness.Decision)
		}},
		{"fingerprint", "READINESS_FINGERPRINT_MISMATCH", func(v *paperAutomationRequest) { v.Readiness.Decision.DecisionFingerprint = fingerprint("wrong") }},
		{"expired", "READINESS_EXPIRED", func(v *paperAutomationRequest) {
			v.Readiness.DecidedAt = automationNow.Add(-25 * time.Hour).Format(time.RFC3339Nano)
		}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _ := automationApp()
			input := validAutomationRequest("strategy-" + string(rune('a'+i)))
			test.mutate(&input)
			requireAutomationReason(t, evaluateAutomation(t, app, input), test.reason)
		})
	}
}

func TestPaperAutomationRejectsMarketDataFailures(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*paperAutomationRequest)
	}{
		{"malformed quote", "INVALID_MARKET_DATA", func(v *paperAutomationRequest) { v.Quote.Ask = v.Quote.Bid }},
		{"stale quote", "STALE_QUOTE", func(v *paperAutomationRequest) {
			v.Quote.Timestamp = automationNow.Add(-31 * time.Second).Format(time.RFC3339Nano)
		}},
		{"spread", "EXCESSIVE_SPREAD", func(v *paperAutomationRequest) { v.Quote.Ask = v.Quote.Bid + 0.0004 }},
		{"open candle", "INVALID_MARKET_DATA", func(v *paperAutomationRequest) { v.Candles.H1[1].Closed = false }},
		{"unordered candle", "INVALID_MARKET_DATA", func(v *paperAutomationRequest) { v.Candles.H1[1], v.Candles.H1[0] = v.Candles.H1[0], v.Candles.H1[1] }},
		{"stale candle", "INVALID_MARKET_DATA", func(v *paperAutomationRequest) {
			v.Candles.H1 = []automationCandle{automationCandleAt(automationNow.Add(-4 * time.Hour)), automationCandleAt(automationNow.Add(-3 * time.Hour))}
		}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _ := automationApp()
			input := validAutomationRequest("market-" + string(rune('a'+i)))
			test.mutate(&input)
			requireAutomationReason(t, evaluateAutomation(t, app, input), test.reason)
		})
	}
}

func TestPaperAutomationPriceStructureAndRiskRewardBoundary(t *testing.T) {
	app, _ := automationApp()
	input := validAutomationRequest("bad-structure")
	input.StrategyManager.Setups[0].StopLoss = 1.101
	input.StrategyManager.Setups[0].Fingerprint = setupFingerprint(input.StrategyManager.Setups[0])
	requireAutomationReason(t, evaluateAutomation(t, app, input), "INVALID_PRICE_STRUCTURE")

	app, _ = automationApp()
	input = validAutomationRequest("entry-off-quote")
	input.StrategyManager.Setups[0].Entry = 1.2
	input.StrategyManager.Setups[0].StopLoss = 1.195
	input.StrategyManager.Setups[0].TakeProfit = 1.21
	input.StrategyManager.Setups[0].Fingerprint = setupFingerprint(input.StrategyManager.Setups[0])
	requireAutomationReason(t, evaluateAutomation(t, app, input), "INVALID_PRICE_STRUCTURE")

	app, _ = automationApp()
	input = validAutomationRequest("bad-rr")
	input.StrategyManager.Setups[0].TakeProfit = 1.1099
	input.StrategyManager.Setups[0].Fingerprint = setupFingerprint(input.StrategyManager.Setups[0])
	requireAutomationReason(t, evaluateAutomation(t, app, input), "INSUFFICIENT_RISK_REWARD")

	app, _ = automationApp()
	input = validAutomationRequest("exact-rr")
	if got := evaluateAutomation(t, app, input); got.Outcome != "PAPER_ACCEPTED" {
		t.Fatalf("exact 1:2 must pass: %+v", got)
	}
}

func TestPaperAutomationEveryRiskGate(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*positionSizeRequest)
	}{
		{"risk", "RISK_REJECTED", func(v *positionSizeRequest) { v.RiskPercent = 1.1 }},
		{"daily loss", "DAILY_LOSS_LIMIT", func(v *positionSizeRequest) { v.DailyLossPercent = 2 }},
		{"drawdown", "DRAWDOWN_LIMIT", func(v *positionSizeRequest) { v.DrawdownPercent = 5 }},
		{"position limit", "POSITION_LIMIT", func(v *positionSizeRequest) { v.OpenPositions = 2 }},
		{"pair exposure", "PAIR_EXPOSURE", func(v *positionSizeRequest) { v.ExistingPairExposure = true }},
		{"kill switch", "KILL_SWITCH_ACTIVE", func(v *positionSizeRequest) { v.KillSwitchActive = true }},
		{"trading disabled", "TRADING_DISABLED", func(v *positionSizeRequest) { v.TradingEnabled = false }},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _ := automationApp()
			input := validAutomationRequest("risk-" + string(rune('a'+i)))
			test.mutate(&input.Risk)
			requireAutomationReason(t, evaluateAutomation(t, app, input), test.reason)
		})
	}
}

func TestPaperAutomationRejectsUnsupportedIdentityAndLondonPair(t *testing.T) {
	tests := []func(*automationSetup){
		func(s *automationSetup) { s.Pair = "AUDUSD" },
		func(s *automationSetup) { s.Timeframe = "M15" },
		func(s *automationSetup) { s.Strategy = "SCALPER" },
		func(s *automationSetup) { s.Strategy = "LONDON_BREAKOUT"; s.Pair = "USDJPY" },
	}
	for i, mutate := range tests {
		app, _ := automationApp()
		input := validAutomationRequest("unsupported-" + string(rune('a'+i)))
		mutate(&input.StrategyManager.Setups[0])
		input.StrategyManager.Pair = input.StrategyManager.Setups[0].Pair
		input.StrategyManager.Direction = input.StrategyManager.Setups[0].Direction
		input.StrategyManager.SelectedStrategy = input.StrategyManager.Setups[0].Strategy
		input.StrategyManager.Setups[0].Fingerprint = setupFingerprint(input.StrategyManager.Setups[0])
		requireAutomationReason(t, evaluateAutomation(t, app, input), "INVALID_MARKET_DATA")
	}
}

func TestPaperAutomationIdempotencyAndStorageFailure(t *testing.T) {
	app, store := automationApp()
	input := validAutomationRequest("retry-001")
	first := evaluateAutomation(t, app, input)
	second := evaluateAutomation(t, app, input)
	if first.Outcome != "PAPER_ACCEPTED" || second.Outcome != "IDEMPOTENT_REPLAY" ||
		first.Audit.AutomationFingerprint != second.Audit.AutomationFingerprint {
		t.Fatalf("unexpected replay first=%+v second=%+v", first, second)
	}
	input.Quote.Bid -= 0.0001
	requireAutomationReason(t, evaluateAutomation(t, app, input), "IDEMPOTENCY_CONFLICT")

	app, store = automationApp()
	store.fail = true
	requireAutomationReason(t, evaluateAutomation(t, app, validAutomationRequest("store-fail")), "STORAGE_UNAVAILABLE")
}

func TestPaperAutomationConcurrentDuplicateAndSamePair(t *testing.T) {
	app, _ := automationApp()
	input := validAutomationRequest("concurrent-duplicate")
	results := make(chan paperAutomationResult, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); results <- evaluateAutomation(t, app, input) }()
	}
	wait.Wait()
	close(results)
	accepted, replay := 0, 0
	for result := range results {
		if result.Outcome == "PAPER_ACCEPTED" {
			accepted++
		}
		if result.Outcome == "IDEMPOTENT_REPLAY" {
			replay++
		}
	}
	if accepted != 1 || replay != 1 {
		t.Fatalf("expected one acceptance and one replay, got %d/%d", accepted, replay)
	}

	app, _ = automationApp()
	a, b := validAutomationRequest("same-pair-a"), validAutomationRequest("same-pair-b")
	results = make(chan paperAutomationResult, 2)
	for _, request := range []paperAutomationRequest{a, b} {
		wait.Add(1)
		go func(v paperAutomationRequest) { defer wait.Done(); results <- evaluateAutomation(t, app, v) }(request)
	}
	wait.Wait()
	close(results)
	accepted, rejected := 0, 0
	for result := range results {
		if result.Outcome == "PAPER_ACCEPTED" {
			accepted++
		}
		if len(result.Reasons) > 0 && result.Reasons[0] == "PAIR_EXPOSURE" {
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("expected one same-pair acceptance/rejection, got %d/%d", accepted, rejected)
	}
}

func TestPaperAutomationFinalKillSwitchAndReproducibility(t *testing.T) {
	app, _ := automationApp()
	app.automationFinalState = func(v paperAutomationRequest) positionSizeRequest {
		state := v.Risk
		state.KillSwitchActive = true
		return state
	}
	requireAutomationReason(t, evaluateAutomation(t, app, validAutomationRequest("final-kill")), "KILL_SWITCH_ACTIVE")

	app, _ = automationApp()
	app.automationFinalState = func(v paperAutomationRequest) positionSizeRequest {
		state := v.Risk
		state.DailyLossPercent = 1
		return state
	}
	requireAutomationReason(t, evaluateAutomation(t, app, validAutomationRequest("state-change")), "RISK_REJECTED")

	leftApp, _ := automationApp()
	rightApp, _ := automationApp()
	left := evaluateAutomation(t, leftApp, validAutomationRequest("deterministic"))
	right := evaluateAutomation(t, rightApp, validAutomationRequest("deterministic"))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("identical input differs:\n%+v\n%+v", left, right)
	}
}

func TestPaperAutomationHTTPStrictPaperOnlyFlow(t *testing.T) {
	app, _ := automationApp()
	body, _ := json.Marshal(validAutomationRequest("http-flow"))
	recorder := httptest.NewRecorder()
	app.paperAutomationHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper-automation/evaluate", bytes.NewReader(body)))
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("X-Trading-Mode") != "PAPER_ONLY" ||
		recorder.Header().Get("X-Live-Execution") != "PROHIBITED" {
		t.Fatalf("unexpected HTTP response: %d %v %s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown"] = true
	unknown, _ := json.Marshal(object)
	recorder = httptest.NewRecorder()
	app.paperAutomationHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper-automation/evaluate", bytes.NewReader(unknown)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d %s", recorder.Code, recorder.Body.String())
	}

	live := validAutomationRequest("live-mode")
	live.Mode = "LIVE"
	liveBody, _ := json.Marshal(live)
	recorder = httptest.NewRecorder()
	app.paperAutomationHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper-automation/evaluate", bytes.NewReader(liveBody)))
	var result paperAutomationResult
	if json.Unmarshal(recorder.Body.Bytes(), &result) != nil || result.Outcome != "NO_TRADE" {
		t.Fatalf("live mode not fail closed: %s", recorder.Body.String())
	}
}

func TestApplicationExposesNoLiveExecutionDependency(t *testing.T) {
	appType := reflect.TypeOf(application{})
	for i := 0; i < appType.NumField(); i++ {
		name := appType.Field(i).Name
		if name == "broker" || name == "liveBroker" || name == "liveOrderStore" {
			t.Fatalf("live execution dependency must not exist: %s", name)
		}
	}
}
