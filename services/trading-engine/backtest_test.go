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

func validBacktestRequest() backtestRequest {
	start := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	candles := []backtestCandle{
		{start.Format(time.RFC3339), 1.1000, 1.1010, 1.0990, 1.1005},
		{start.Add(time.Hour).Format(time.RFC3339), 1.1005, 1.1015, 1.0995, 1.1000},
		{start.Add(2 * time.Hour).Format(time.RFC3339), 1.1000, 1.1110, 1.0940, 1.1050},
		{start.Add(3 * time.Hour).Format(time.RFC3339), 1.1050, 1.1060, 1.1040, 1.1055},
	}
	return backtestRequest{
		AsOf: start.Add(4 * time.Hour).Format(time.RFC3339),
		Config: backtestConfig{Pair: "EURUSD", Timeframe: "H1", Strategy: "EMA_PULLBACK", StrategyVersion: "1.0.0",
			Equity: 10000, RiskPercent: 1, PipValuePerStandardLot: 10, MinimumLot: 0.01, MaximumLot: 2, LotStep: 0.01,
			SpreadPips: 1, SlippagePips: 0.5, CommissionPerLot: 7},
		Candles: candles,
		Trades: []backtestTrade{{TradeID: "trade-001", SignalAt: start.Add(time.Hour).Format(time.RFC3339),
			EntryAt: start.Add(2 * time.Hour).Format(time.RFC3339), Direction: "BUY", Entry: 1.1000, StopLoss: 1.0950, TakeProfit: 1.1100}},
	}
}

func TestBacktestRejectsInvalidDuplicateGapAndUnorderedCandles(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*backtestRequest)
	}{
		{"invalid OHLC", "INVALID_CANDLE", func(v *backtestRequest) { v.Candles[1].High = 1.0 }},
		{"non finite", "INVALID_CANDLE", func(v *backtestRequest) { v.Candles[1].Close = math.NaN() }},
		{"duplicate", "DUPLICATE_CANDLE", func(v *backtestRequest) { v.Candles[2].Timestamp = v.Candles[1].Timestamp }},
		{"gap", "CANDLE_GAP", func(v *backtestRequest) { v.Candles = v.Candles[:3]; v.Candles[2].Timestamp = "2026-01-02T11:00:00Z" }},
		{"unordered", "UNORDERED_CANDLES", func(v *backtestRequest) { v.Candles[2].Timestamp = "2026-01-02T07:00:00Z" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validBacktestRequest()
			tc.mutate(&input)
			got := runBacktest(input)
			if got.Outcome != "NO_TRADE" || got.Reasons[0] != tc.reason {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestBacktestRejectsOpenCandleAndLookAhead(t *testing.T) {
	input := validBacktestRequest()
	input.AsOf = "2026-01-02T11:30:00Z"
	if got := runBacktest(input); got.Reasons[0] != "INVALID_CANDLE" {
		t.Fatalf("got %+v", got)
	}
	input = validBacktestRequest()
	input.Trades[0].SignalAt = input.Trades[0].EntryAt
	if got := runBacktest(input); got.Reasons[0] != "LOOK_AHEAD_ATTEMPT" {
		t.Fatalf("got %+v", got)
	}
}

func TestBacktestChargesSpreadSlippageCommissionAndUsesAdverseAmbiguity(t *testing.T) {
	input := validBacktestRequest()
	got := runBacktest(input)
	if got.Outcome != "BACKTEST_COMPLETE" || len(got.Trades) != 1 {
		t.Fatalf("got %+v", got)
	}
	trade := got.Trades[0]
	if trade.Outcome != "STOP_LOSS" || !trade.Ambiguous || trade.Costs != 5.4 || trade.NetPnL >= trade.GrossPnL {
		t.Fatalf("trade %+v", trade)
	}
	withoutCosts := validBacktestRequest()
	withoutCosts.Config.SpreadPips = 0
	withoutCosts.Config.SlippagePips = 0
	withoutCosts.Config.CommissionPerLot = 0
	baseline := runBacktest(withoutCosts).Trades[0]
	if trade.NetPnL >= baseline.NetPnL {
		t.Fatalf("costs not applied: cost=%+v baseline=%+v", trade, baseline)
	}
}

func TestBacktestIsReproducibleAndSeparatesIdentity(t *testing.T) {
	input := validBacktestRequest()
	first := runBacktest(input)
	second := runBacktest(input)
	if !reflect.DeepEqual(first, second) || first.RunHash == "" || first.DatasetHash == "" || first.ConfigurationHash == "" {
		t.Fatalf("not reproducible: %+v %+v", first, second)
	}
	changed := validBacktestRequest()
	changed.Config.StrategyVersion = "1.0.1"
	other := runBacktest(changed)
	if first.ConfigurationHash == other.ConfigurationHash || first.RunHash == other.RunHash {
		t.Fatal("strategy version did not isolate run")
	}
}

func TestBacktestHandlerRejectsUnknownFields(t *testing.T) {
	input := validBacktestRequest()
	payload, _ := json.Marshal(input)
	payload = bytes.Replace(payload, []byte(`"asOf":`), []byte(`"unknown":true,"asOf":`), 1)
	recorder := httptest.NewRecorder()
	(&application{}).backtestHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/backtests", bytes.NewReader(payload)))
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("X-Trading-Mode") != "BACKTEST_ONLY" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
