package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

var backtestTimeframes = map[string]time.Duration{"H1": time.Hour, "H4": 4 * time.Hour}

type backtestCandle struct {
	Timestamp string  `json:"timestamp"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
}

type backtestTrade struct {
	TradeID    string  `json:"tradeId"`
	SignalAt   string  `json:"signalAt"`
	EntryAt    string  `json:"entryAt"`
	Direction  string  `json:"direction"`
	Entry      float64 `json:"entry"`
	StopLoss   float64 `json:"stopLoss"`
	TakeProfit float64 `json:"takeProfit"`
}

type backtestConfig struct {
	Pair                   string  `json:"pair"`
	Timeframe              string  `json:"timeframe"`
	Strategy               string  `json:"strategy"`
	StrategyVersion        string  `json:"strategyVersion"`
	Equity                 float64 `json:"equity"`
	RiskPercent            float64 `json:"riskPercent"`
	PipValuePerStandardLot float64 `json:"pipValuePerStandardLot"`
	MinimumLot             float64 `json:"minimumLot"`
	MaximumLot             float64 `json:"maximumLot"`
	LotStep                float64 `json:"lotStep"`
	SpreadPips             float64 `json:"spreadPips"`
	SlippagePips           float64 `json:"slippagePips"`
	CommissionPerLot       float64 `json:"commissionPerLot"`
}

type backtestRequest struct {
	AsOf    string           `json:"asOf"`
	Config  backtestConfig   `json:"config"`
	Candles []backtestCandle `json:"candles"`
	Trades  []backtestTrade  `json:"trades"`
}

type backtestTradeResult struct {
	TradeID   string  `json:"tradeId"`
	Outcome   string  `json:"outcome"`
	EntryAt   string  `json:"entryAt"`
	ExitAt    string  `json:"exitAt"`
	Lots      float64 `json:"lots"`
	GrossPnL  float64 `json:"grossPnl"`
	Costs     float64 `json:"costs"`
	NetPnL    float64 `json:"netPnl"`
	Ambiguous bool    `json:"ambiguous"`
}

type backtestResult struct {
	Outcome           string                `json:"outcome"`
	Reasons           []string              `json:"reasons,omitempty"`
	Pair              string                `json:"pair,omitempty"`
	Timeframe         string                `json:"timeframe,omitempty"`
	Strategy          string                `json:"strategy,omitempty"`
	StrategyVersion   string                `json:"strategyVersion,omitempty"`
	ConfigurationHash string                `json:"configurationFingerprint,omitempty"`
	DatasetHash       string                `json:"datasetFingerprint,omitempty"`
	RunHash           string                `json:"runFingerprint,omitempty"`
	Trades            []backtestTradeResult `json:"trades,omitempty"`
}

type parsedBacktestCandle struct {
	backtestCandle
	at time.Time
}

func runBacktest(input backtestRequest) backtestResult {
	fail := func(reason string) backtestResult {
		return backtestResult{Outcome: "NO_TRADE", Reasons: []string{reason}}
	}
	cfg := input.Config
	interval, ok := backtestTimeframes[cfg.Timeframe]
	if !ok || !paperPairs[cfg.Pair] || !paperStrategies[cfg.Strategy] ||
		!validPaperIdentifier(cfg.StrategyVersion) || !finitePositive(cfg.Equity) ||
		!finitePositive(cfg.RiskPercent) || cfg.RiskPercent > 1 || !finitePositive(cfg.PipValuePerStandardLot) ||
		!finitePositive(cfg.MinimumLot) || cfg.MaximumLot < cfg.MinimumLot || !finitePositive(cfg.LotStep) ||
		!finiteNonNegative(cfg.SpreadPips) || !finiteNonNegative(cfg.SlippagePips) || !finiteNonNegative(cfg.CommissionPerLot) ||
		(cfg.Strategy == "LONDON_BREAKOUT" && cfg.Pair == "USDJPY") {
		return fail("INVALID_CONFIG")
	}
	asOf, err := time.Parse(time.RFC3339Nano, input.AsOf)
	if err != nil || len(input.Candles) < 2 {
		return fail("INVALID_DATASET")
	}
	parsed := make([]parsedBacktestCandle, len(input.Candles))
	for i, candle := range input.Candles {
		at, parseErr := time.Parse(time.RFC3339Nano, candle.Timestamp)
		if parseErr != nil || !validOHLC(candle) || at.Add(interval).After(asOf) {
			return fail("INVALID_CANDLE")
		}
		if i > 0 {
			delta := at.Sub(parsed[i-1].at)
			if delta == 0 {
				return fail("DUPLICATE_CANDLE")
			}
			if delta < 0 {
				return fail("UNORDERED_CANDLES")
			}
			if delta != interval {
				return fail("CANDLE_GAP")
			}
		}
		parsed[i] = parsedBacktestCandle{backtestCandle: candle, at: at.UTC()}
	}
	configHash := fingerprint(cfg)
	datasetHash := fingerprint(input.Candles)
	result := backtestResult{Outcome: "BACKTEST_COMPLETE", Pair: cfg.Pair, Timeframe: cfg.Timeframe,
		Strategy: cfg.Strategy, StrategyVersion: cfg.StrategyVersion, ConfigurationHash: configHash,
		DatasetHash: datasetHash, Trades: make([]backtestTradeResult, 0, len(input.Trades))}
	seen := map[string]bool{}
	for _, trade := range input.Trades {
		if seen[trade.TradeID] {
			return fail("DUPLICATE_TRADE")
		}
		seen[trade.TradeID] = true
		tradeResult, tradeErr := simulateBacktestTrade(cfg, parsed, trade)
		if tradeErr != nil {
			return fail(tradeErr.Error())
		}
		result.Trades = append(result.Trades, tradeResult)
	}
	result.RunHash = fingerprint(struct {
		Config, Dataset string
		Trades          []backtestTradeResult
	}{configHash, datasetHash, result.Trades})
	return result
}

func simulateBacktestTrade(cfg backtestConfig, candles []parsedBacktestCandle, trade backtestTrade) (backtestTradeResult, error) {
	if !validPaperIdentifier(trade.TradeID) || (trade.Direction != "BUY" && trade.Direction != "SELL") ||
		!finitePositive(trade.Entry) || !finitePositive(trade.StopLoss) || !finitePositive(trade.TakeProfit) {
		return backtestTradeResult{}, errors.New("INVALID_TRADE")
	}
	signalAt, e1 := time.Parse(time.RFC3339Nano, trade.SignalAt)
	entryAt, e2 := time.Parse(time.RFC3339Nano, trade.EntryAt)
	if e1 != nil || e2 != nil || !signalAt.Before(entryAt) {
		return backtestTradeResult{}, errors.New("LOOK_AHEAD_ATTEMPT")
	}
	index := sort.Search(len(candles), func(i int) bool { return !candles[i].at.Before(entryAt) })
	if index == len(candles) || !candles[index].at.Equal(entryAt) || index == 0 || signalAt.After(candles[index-1].at.Add(backtestTimeframes[cfg.Timeframe])) {
		return backtestTradeResult{}, errors.New("LOOK_AHEAD_ATTEMPT")
	}
	validStructure := trade.Direction == "BUY" && trade.StopLoss < trade.Entry && trade.TakeProfit > trade.Entry ||
		trade.Direction == "SELL" && trade.StopLoss > trade.Entry && trade.TakeProfit < trade.Entry
	riskDistance := math.Abs(trade.Entry - trade.StopLoss)
	rewardDistance := math.Abs(trade.TakeProfit - trade.Entry)
	if !validStructure || rewardDistance+1e-12 < 2*riskDistance {
		return backtestTradeResult{}, errors.New("INVALID_TRADE_STRUCTURE")
	}
	risk := calculatePositionSize(positionSizeRequest{RequestID: trade.TradeID, Pair: cfg.Pair, Direction: trade.Direction,
		Equity: cfg.Equity, RiskPercent: cfg.RiskPercent, Entry: trade.Entry, StopLoss: trade.StopLoss,
		PipValuePerStandardLot: cfg.PipValuePerStandardLot, MinimumLot: cfg.MinimumLot, MaximumLot: cfg.MaximumLot,
		LotStep: cfg.LotStep, TradingEnabled: true})
	if risk.Outcome != "RISK_APPROVED" {
		return backtestTradeResult{}, errors.New("RISK_REJECTED")
	}
	pip, _ := pipSize(cfg.Pair)
	halfSpread := cfg.SpreadPips * pip / 2
	for i := index; i < len(candles); i++ {
		c := candles[i]
		hitStop, hitTarget := candleTouches(trade, c.backtestCandle, halfSpread)
		if !hitStop && !hitTarget {
			continue
		}
		ambiguous := hitStop && hitTarget
		exit := trade.TakeProfit
		outcome := "TAKE_PROFIT"
		if hitStop {
			exit, outcome = trade.StopLoss, "STOP_LOSS"
		}
		pips := (exit - trade.Entry) / pip
		if trade.Direction == "SELL" {
			pips = -pips
		}
		gross := round(pips*cfg.PipValuePerStandardLot*risk.Lots, 8)
		marketCosts := (cfg.SpreadPips + 2*cfg.SlippagePips) * cfg.PipValuePerStandardLot * risk.Lots
		costs := round(marketCosts+cfg.CommissionPerLot*risk.Lots, 8)
		return backtestTradeResult{TradeID: trade.TradeID, Outcome: outcome, EntryAt: entryAt.UTC().Format(time.RFC3339Nano),
			ExitAt: c.at.Format(time.RFC3339Nano), Lots: risk.Lots, GrossPnL: gross, Costs: costs,
			NetPnL: round(gross-costs, 8), Ambiguous: ambiguous}, nil
	}
	return backtestTradeResult{}, errors.New("TRADE_NOT_CLOSED")
}

func candleTouches(trade backtestTrade, candle backtestCandle, halfSpread float64) (bool, bool) {
	if trade.Direction == "BUY" {
		return candle.Low-halfSpread <= trade.StopLoss, candle.High-halfSpread >= trade.TakeProfit
	}
	return candle.High+halfSpread >= trade.StopLoss, candle.Low+halfSpread <= trade.TakeProfit
}

func validOHLC(c backtestCandle) bool {
	values := []float64{c.Open, c.High, c.Low, c.Close}
	for _, v := range values {
		if !finitePositive(v) {
			return false
		}
	}
	return c.High >= c.Open && c.High >= c.Close && c.High >= c.Low && c.Low <= c.Open && c.Low <= c.Close
}

func finiteNonNegative(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func fingerprint(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("canonical backtest fingerprint: %v", err))
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (app *application) backtestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	var input backtestRequest
	if decodeStrictJSON(w, r, &input) != nil {
		writeBacktestResult(w, http.StatusBadRequest, backtestResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}})
		return
	}
	result := runBacktest(input)
	status := http.StatusOK
	if result.Outcome == "NO_TRADE" {
		status = http.StatusUnprocessableEntity
	}
	writeBacktestResult(w, status, result)
}

func writeBacktestResult(w http.ResponseWriter, status int, result backtestResult) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Trading-Mode", "BACKTEST_ONLY")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
