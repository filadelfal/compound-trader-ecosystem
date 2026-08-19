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

func evidenceResult(pair, timeframe, strategy, version string, trades []backtestTradeResult) backtestResult {
	result := backtestResult{Outcome: "BACKTEST_COMPLETE", Pair: pair, Timeframe: timeframe, Strategy: strategy,
		StrategyVersion: version, ConfigurationHash: fingerprint(pair + timeframe + strategy + version),
		DatasetHash: fingerprint(pair + timeframe + "dataset"), Trades: trades}
	result.RunHash = fingerprint(struct {
		Config, Dataset string
		Trades          []backtestTradeResult
	}{
		result.ConfigurationHash, result.DatasetHash, result.Trades,
	})
	return result
}

func evidenceTradeResult(id string, entry time.Time, gross, costs float64) backtestTradeResult {
	outcome := "TAKE_PROFIT"
	if gross < 0 {
		outcome = "STOP_LOSS"
	}
	return backtestTradeResult{TradeID: id, Outcome: outcome, EntryAt: entry.Format(time.RFC3339),
		ExitAt: entry.Add(time.Hour).Format(time.RFC3339), Lots: 0.1, GrossPnL: gross, Costs: costs, NetPnL: round(gross-costs, 8)}
}

func TestPerformanceRejectsZeroTradesAndInvalidEvidence(t *testing.T) {
	got := buildPerformanceReport(performanceRequest{MinimumSampleSize: 1, Runs: []performanceRun{{Sample: performanceInSample, Result: backtestResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_CANDLE"}}}}})
	if got.Outcome != "NO_TRADE" || got.Reasons[0] != "NO_COMPLETED_TRADES" || got.RejectionReasons["INVALID_CANDLE"] != 1 {
		t.Fatalf("got %+v", got)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	result := evidenceResult("EURUSD", "H1", "EMA_PULLBACK", "1.0.0", []backtestTradeResult{evidenceTradeResult("t1", now, 10, 1)})
	result.Trades[0].NetPnL = math.NaN()
	got = buildPerformanceReport(performanceRequest{MinimumSampleSize: 1, Runs: []performanceRun{{Sample: performanceInSample, Result: result}}})
	if got.Outcome != "NO_TRADE" || got.Reasons[0] != "INVALID_TRADE_EVIDENCE" {
		t.Fatalf("got %+v", got)
	}
}

func TestPerformanceAllWinsAllLossesAndBreakeven(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winning := evidenceResult("EURUSD", "H1", "EMA_PULLBACK", "1.0.0", []backtestTradeResult{
		evidenceTradeResult("w1", now, 20, 2), evidenceTradeResult("w2", now.Add(2*time.Hour), 30, 3),
	})
	wins := buildPerformanceReport(performanceRequest{MinimumSampleSize: 2, Runs: []performanceRun{{Sample: performanceInSample, Result: winning}}})
	if wins.Overall.Wins != 2 || wins.Overall.Losses != 0 || wins.Overall.WinRate != 100 || wins.Overall.ProfitFactor != nil {
		t.Fatalf("wins %+v", wins)
	}
	losing := evidenceResult("GBPUSD", "H1", "EMA_CROSSOVER", "2.0.0", []backtestTradeResult{
		evidenceTradeResult("l1", now, -10, 1), evidenceTradeResult("l2", now.Add(2*time.Hour), -20, 2),
	})
	losses := buildPerformanceReport(performanceRequest{MinimumSampleSize: 2, Runs: []performanceRun{{Sample: performanceOutSample, Result: losing}}})
	if losses.Overall.Losses != 2 || losses.Overall.ProfitFactor == nil || *losses.Overall.ProfitFactor != 0 || losses.Overall.ConsecutiveLosses != 2 {
		t.Fatalf("losses %+v", losses)
	}
	breakeven := evidenceResult("USDJPY", "H4", "ADX_TREND_CONTINUATION", "1.0.0", []backtestTradeResult{evidenceTradeResult("b1", now, 2, 2)})
	even := buildPerformanceReport(performanceRequest{MinimumSampleSize: 1, Runs: []performanceRun{{Sample: performanceInSample, Result: breakeven}}})
	if even.Overall.BreakevenTrades != 1 || even.Overall.ExpectancyPerTrade != 0 {
		t.Fatalf("breakeven %+v", even)
	}
}

func TestPerformanceCostsCanTurnGrossProfitIntoNetLossAndDrawdown(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	result := evidenceResult("EURUSD", "H1", "EMA_PULLBACK", "1.0.0", []backtestTradeResult{
		evidenceTradeResult("t1", now, 100, 10), evidenceTradeResult("t2", now.Add(2*time.Hour), 10, 30), evidenceTradeResult("t3", now.Add(4*time.Hour), -40, 5),
	})
	report := buildPerformanceReport(performanceRequest{MinimumSampleSize: 3, Runs: []performanceRun{{Sample: performanceOutSample, Result: result}}})
	if report.Overall.PreCostPnL != 70 || report.Overall.Costs != 45 || report.Overall.NetPnL != 25 || report.Overall.MaximumDrawdown != 65 || report.Overall.Losses != 2 {
		t.Fatalf("report %+v", report)
	}
}

func TestPerformanceGroupingSamplesRejectionsAndInsufficientWarning(t *testing.T) {
	jan := time.Date(2026, 1, 31, 22, 0, 0, 0, time.UTC)
	feb := jan.Add(4 * time.Hour)
	first := evidenceResult("EURUSD", "H1", "EMA_PULLBACK", "1.0.0", []backtestTradeResult{evidenceTradeResult("a", jan, 20, 2)})
	second := evidenceResult("GBPUSD", "H4", "EMA_CROSSOVER", "2.0.0", []backtestTradeResult{evidenceTradeResult("b", feb, -10, 1)})
	rejected := backtestResult{Outcome: "NO_TRADE", Reasons: []string{"CANDLE_GAP"}}
	input := performanceRequest{MinimumSampleSize: 10, Runs: []performanceRun{{performanceInSample, first}, {performanceOutSample, second}, {performanceOutSample, rejected}}}
	report := buildPerformanceReport(input)
	if report.Outcome != "INSUFFICIENT_EVIDENCE" || len(report.ByPair) != 2 || len(report.ByStrategy) != 2 || len(report.ByMonth) != 2 || report.InSample.TotalTrades != 1 || report.OutOfSample.TotalTrades != 1 || report.RejectedRuns != 1 || report.RejectionReasons["CANDLE_GAP"] != 1 {
		t.Fatalf("report %+v", report)
	}
	if !containsString(report.Warnings, "SAMPLE_SIZE_BELOW_MINIMUM") {
		t.Fatalf("warnings %+v", report.Warnings)
	}
	secondRun := buildPerformanceReport(input)
	if !reflect.DeepEqual(report, secondRun) {
		t.Fatal("report is not deterministic")
	}
}

func TestPerformanceHandlerRejectsUnknownFields(t *testing.T) {
	payload := []byte(`{"minimumSampleSize":1,"runs":[],"unknown":true}`)
	recorder := httptest.NewRecorder()
	(&application{}).performanceHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/backtests/performance", bytes.NewReader(payload)))
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("X-Trading-Mode") != "HISTORICAL_EVIDENCE_ONLY" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var report performanceReport
	if json.Unmarshal(recorder.Body.Bytes(), &report) != nil || report.Outcome != "NO_TRADE" {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
