package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func walkForwardResult(tag, pair, timeframe, strategy, version string, start time.Time, values []float64) backtestResult {
	trades := make([]backtestTradeResult, 0, len(values))
	for index, value := range values {
		cost := 1.0
		gross := value + cost
		trades = append(trades, evidenceTradeResult(tag+"-t"+string(rune('a'+index)), start.Add(time.Duration(index*3+1)*time.Hour), gross, cost))
	}
	result := evidenceResult(pair, timeframe, strategy, version, trades)
	result.DatasetHash = fingerprint(tag + "-dataset")
	result.RunHash = fingerprint(struct {
		Config, Dataset string
		Trades          []backtestTradeResult
	}{result.ConfigurationHash, result.DatasetHash, result.Trades})
	return result
}

func validWalkForwardRequest() walkForwardRequest {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	period := 24 * time.Hour
	fold := func(id string, start time.Time) walkForwardFold {
		return walkForwardFold{FoldID: id,
			Training: validationPeriod{start.Format(time.RFC3339), start.Add(period).Format(time.RFC3339), walkForwardResult(id+"-train", "EURUSD", "H1", "EMA_PULLBACK", "1.0.0", start, []float64{20, 20, -10})},
			Testing:  validationPeriod{start.Add(period).Format(time.RFC3339), start.Add(2 * period).Format(time.RFC3339), walkForwardResult(id+"-test", "EURUSD", "H1", "EMA_PULLBACK", "1.0.0", start.Add(period), []float64{15, 15, -10})}}
	}
	maxLoss := 2
	return walkForwardRequest{Thresholds: readinessThresholds{MinimumTotalTrades: 12, MinimumOutOfSampleTrades: 6, MinimumCompletedFolds: 2, MinimumProfitableFoldPercent: 100, MinimumOutOfSampleExpectancy: 5, MinimumOutOfSampleProfitFactor: 2, MaximumOutOfSampleDrawdown: 30, MaximumDegradationPercent: 40, MaximumConsecutiveLosses: &maxLoss}, Folds: []walkForwardFold{fold("fold-1", base), fold("fold-2", base.Add(48*time.Hour))}}
}

func resignWalkForwardResult(result *backtestResult) {
	result.RunHash = fingerprint(struct {
		Config, Dataset string
		Trades          []backtestTradeResult
	}{result.ConfigurationHash, result.DatasetHash, result.Trades})
}

func TestWalkForwardReadyOnlyWhenEveryGatePasses(t *testing.T) {
	decision := evaluateWalkForward(validWalkForwardRequest())
	if decision.Outcome != "READY" || len(decision.Reasons) != 0 || len(decision.Folds) != 2 || decision.DecisionFingerprint == "" {
		t.Fatalf("decision %+v", decision)
	}
	for _, gate := range decision.Gates {
		if !gate.Passed {
			t.Fatalf("failed gate %+v", gate)
		}
	}
}

func TestWalkForwardRejectsChronologyProblems(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*walkForwardRequest)
	}{
		{"overlap", "TRAIN_TEST_OVERLAP", func(v *walkForwardRequest) { v.Folds[0].Training.End = "2026-01-02T01:00:00Z" }},
		{"reversed", "INVALID_OR_REVERSED_PERIOD", func(v *walkForwardRequest) { v.Folds[0].Training.End = v.Folds[0].Training.Start }},
		{"missing train test", "MISSING_PERIOD", func(v *walkForwardRequest) { v.Folds[0].Training.End = "2026-01-01T23:00:00Z" }},
		{"missing folds", "MISSING_PERIOD", func(v *walkForwardRequest) { v.Folds[1].Training.Start = "2026-01-03T01:00:00Z" }},
		{"duplicate fold", "DUPLICATE_OR_INVALID_FOLD", func(v *walkForwardRequest) { v.Folds[1].FoldID = v.Folds[0].FoldID }},
		{"duplicate period", "DUPLICATE_PERIOD", func(v *walkForwardRequest) {
			v.Folds[1].Training.Start = v.Folds[0].Training.Start
			v.Folds[1].Training.End = v.Folds[0].Training.End
			v.Folds[1].Testing.Start = v.Folds[0].Testing.Start
			v.Folds[1].Testing.End = v.Folds[0].Testing.End
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validWalkForwardRequest()
			tc.mutate(&input)
			got := evaluateWalkForward(input)
			if got.Outcome != "NOT_READY" || got.Reasons[0] != tc.reason {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestWalkForwardRejectsLeakageAndFingerprintMismatch(t *testing.T) {
	input := validWalkForwardRequest()
	input.Folds[0].Testing.Result.DatasetHash = input.Folds[0].Training.Result.DatasetHash
	resignWalkForwardResult(&input.Folds[0].Testing.Result)
	if got := evaluateWalkForward(input); got.Reasons[0] != "TRAIN_TEST_LEAKAGE" {
		t.Fatalf("got %+v", got)
	}
	input = validWalkForwardRequest()
	input.Folds[0].Testing.Result.Trades[0].TradeID = input.Folds[0].Training.Result.Trades[0].TradeID
	resignWalkForwardResult(&input.Folds[0].Testing.Result)
	if got := evaluateWalkForward(input); got.Reasons[0] != "TRAIN_TEST_LEAKAGE" {
		t.Fatalf("got %+v", got)
	}
	input = validWalkForwardRequest()
	input.Folds[0].Testing.Result.RunHash = fingerprint("tampered")
	if got := evaluateWalkForward(input); got.Reasons[0] != "RUN_FINGERPRINT_MISMATCH" {
		t.Fatalf("got %+v", got)
	}
}

func TestWalkForwardFailsSampleFoldAndProfitabilityGates(t *testing.T) {
	input := validWalkForwardRequest()
	input.Thresholds.MinimumCompletedFolds = 3
	input.Thresholds.MinimumTotalTrades = 20
	input.Thresholds.MinimumOutOfSampleTrades = 10
	got := evaluateWalkForward(input)
	if got.Outcome != "NOT_READY" || !containsString(got.Reasons, "MINIMUM_COMPLETED_FOLDS") || !containsString(got.Reasons, "MINIMUM_TOTAL_TRADES") || !containsString(got.Reasons, "MINIMUM_OUT_OF_SAMPLE_TRADES") {
		t.Fatalf("got %+v", got)
	}
	input = validWalkForwardRequest()
	for i := range input.Folds[1].Testing.Result.Trades {
		input.Folds[1].Testing.Result.Trades[i].GrossPnL = -9
		input.Folds[1].Testing.Result.Trades[i].NetPnL = -10
	}
	resignWalkForwardResult(&input.Folds[1].Testing.Result)
	got = evaluateWalkForward(input)
	if !containsString(got.Reasons, "MINIMUM_PROFITABLE_FOLD_PERCENT") {
		t.Fatalf("got %+v", got)
	}
}

func TestWalkForwardFailsMetricAndDegradationGates(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*walkForwardRequest)
	}{
		{"expectancy", "MINIMUM_OUT_OF_SAMPLE_EXPECTANCY", func(v *walkForwardRequest) { v.Thresholds.MinimumOutOfSampleExpectancy = 10 }},
		{"profit factor", "MINIMUM_OUT_OF_SAMPLE_PROFIT_FACTOR", func(v *walkForwardRequest) { v.Thresholds.MinimumOutOfSampleProfitFactor = 4 }},
		{"drawdown", "MAXIMUM_OUT_OF_SAMPLE_DRAWDOWN", func(v *walkForwardRequest) { v.Thresholds.MaximumOutOfSampleDrawdown = 5 }},
		{"degradation", "MAXIMUM_EXPECTANCY_DEGRADATION", func(v *walkForwardRequest) { v.Thresholds.MaximumDegradationPercent = 20 }},
		{"loss streak", "MAXIMUM_CONSECUTIVE_LOSSES", func(v *walkForwardRequest) { limit := 0; v.Thresholds.MaximumConsecutiveLosses = &limit }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validWalkForwardRequest()
			tc.mutate(&input)
			got := evaluateWalkForward(input)
			if got.Outcome != "NOT_READY" || !containsString(got.Reasons, tc.reason) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestWalkForwardFailsUndefinedAndNonFiniteMetrics(t *testing.T) {
	input := validWalkForwardRequest()
	for f := range input.Folds {
		for i := range input.Folds[f].Testing.Result.Trades {
			input.Folds[f].Testing.Result.Trades[i].GrossPnL = 11
			input.Folds[f].Testing.Result.Trades[i].NetPnL = 10
		}
		resignWalkForwardResult(&input.Folds[f].Testing.Result)
	}
	got := evaluateWalkForward(input)
	if !containsString(got.Reasons, "OUT_OF_SAMPLE_PROFIT_FACTOR_UNDEFINED") {
		t.Fatalf("got %+v", got)
	}
	input = validWalkForwardRequest()
	input.Folds[0].Testing.Result.Trades[0].NetPnL = math.NaN()
	got = evaluateWalkForward(input)
	if got.Reasons[0] != "INVALID_TRADE_EVIDENCE" {
		t.Fatalf("got %+v", got)
	}
}

func TestWalkForwardEnforcesIdentityIsolation(t *testing.T) {
	input := validWalkForwardRequest()
	input.Folds[0].Testing.Result.StrategyVersion = "2.0.0"
	input.Folds[0].Testing.Result.ConfigurationHash = fingerprint("v2")
	resignWalkForwardResult(&input.Folds[0].Testing.Result)
	if got := evaluateWalkForward(input); got.Reasons[0] != "IDENTITY_MISMATCH" {
		t.Fatalf("got %+v", got)
	}
	for _, mutation := range []func(*backtestResult){func(r *backtestResult) { r.Pair = "GBPUSD" }, func(r *backtestResult) { r.Timeframe = "H4" }} {
		input = validWalkForwardRequest()
		mutation(&input.Folds[1].Training.Result)
		mutation(&input.Folds[1].Testing.Result)
		for _, r := range []*backtestResult{&input.Folds[1].Training.Result, &input.Folds[1].Testing.Result} {
			r.ConfigurationHash = fingerprint(r.Pair + r.Timeframe)
			resignWalkForwardResult(r)
		}
		if got := evaluateWalkForward(input); got.Reasons[0] != "IDENTITY_ISOLATION_VIOLATION" {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestWalkForwardIsReproducibleAndRejectsUnknownJSON(t *testing.T) {
	input := validWalkForwardRequest()
	first, second := evaluateWalkForward(input), evaluateWalkForward(input)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("not reproducible")
	}
	payload, _ := json.Marshal(input)
	payload = bytes.Replace(payload, []byte(`"folds":`), []byte(`"unknown":true,"folds":`), 1)
	recorder := httptest.NewRecorder()
	(&application{}).walkForwardHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/backtests/walk-forward", bytes.NewReader(payload)))
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("X-Trading-Mode") != "HISTORICAL_VALIDATION_ONLY" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
