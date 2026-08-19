package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"
)

const (
	performanceInSample  = "IN_SAMPLE"
	performanceOutSample = "OUT_OF_SAMPLE"
	performanceEpsilon   = 1e-8
)

var sha256FingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type performanceRun struct {
	Sample string         `json:"sample"`
	Result backtestResult `json:"result"`
}

type performanceRequest struct {
	MinimumSampleSize int              `json:"minimumSampleSize"`
	Runs              []performanceRun `json:"runs"`
}

type performanceMetrics struct {
	TotalTrades        int      `json:"totalTrades"`
	Wins               int      `json:"wins"`
	Losses             int      `json:"losses"`
	BreakevenTrades    int      `json:"breakevenTrades"`
	WinRate            float64  `json:"winRate"`
	GrossProfit        float64  `json:"grossProfit"`
	GrossLoss          float64  `json:"grossLoss"`
	PreCostPnL         float64  `json:"preCostPnl"`
	Costs              float64  `json:"costs"`
	NetPnL             float64  `json:"netPnl"`
	ProfitFactor       *float64 `json:"profitFactor,omitempty"`
	AverageWin         float64  `json:"averageWin"`
	AverageLoss        float64  `json:"averageLoss"`
	ExpectancyPerTrade float64  `json:"expectancyPerTrade"`
	MaximumDrawdown    float64  `json:"maximumDrawdown"`
	ConsecutiveWins    int      `json:"consecutiveWins"`
	ConsecutiveLosses  int      `json:"consecutiveLosses"`
	ReturnToDrawdown   *float64 `json:"returnToDrawdownRatio,omitempty"`
	StartDate          string   `json:"startDate,omitempty"`
	EndDate            string   `json:"endDate,omitempty"`
}

type performanceGroup struct {
	Pair            string             `json:"pair,omitempty"`
	Timeframe       string             `json:"timeframe,omitempty"`
	Strategy        string             `json:"strategy,omitempty"`
	StrategyVersion string             `json:"strategyVersion,omitempty"`
	Month           string             `json:"month,omitempty"`
	Metrics         performanceMetrics `json:"metrics"`
}

type performanceReport struct {
	Outcome                   string              `json:"outcome"`
	Reasons                   []string            `json:"reasons,omitempty"`
	Warnings                  []string            `json:"warnings,omitempty"`
	MinimumSampleSize         int                 `json:"minimumSampleSize,omitempty"`
	RejectedRuns              int                 `json:"rejectedRuns"`
	RejectionReasons          map[string]int      `json:"rejectionReasons,omitempty"`
	ConfigurationFingerprints []string            `json:"configurationFingerprints,omitempty"`
	DatasetFingerprints       []string            `json:"datasetFingerprints,omitempty"`
	RunFingerprints           []string            `json:"runFingerprints,omitempty"`
	Overall                   *performanceMetrics `json:"overall,omitempty"`
	ByPair                    []performanceGroup  `json:"byPair,omitempty"`
	ByStrategy                []performanceGroup  `json:"byStrategy,omitempty"`
	ByMonth                   []performanceGroup  `json:"byMonth,omitempty"`
	InSample                  *performanceMetrics `json:"inSample,omitempty"`
	OutOfSample               *performanceMetrics `json:"outOfSample,omitempty"`
}

type evidenceTrade struct {
	pair, timeframe, strategy, strategyVersion, sample string
	result                                             backtestTradeResult
	entryAt, exitAt                                    time.Time
}

func buildPerformanceReport(input performanceRequest) performanceReport {
	fail := func(reason string) performanceReport {
		return performanceReport{Outcome: "NO_TRADE", Reasons: []string{reason}}
	}
	if input.MinimumSampleSize <= 0 || len(input.Runs) == 0 {
		return fail("INVALID_EVIDENCE_REQUEST")
	}
	trades := make([]evidenceTrade, 0)
	rejections := map[string]int{}
	configurationHashes, datasetHashes, runHashes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	rejectedRuns := 0
	for _, run := range input.Runs {
		if run.Sample != performanceInSample && run.Sample != performanceOutSample {
			return fail("INVALID_SAMPLE_CLASSIFICATION")
		}
		result := run.Result
		if result.Outcome == "NO_TRADE" {
			if len(result.Reasons) == 0 || len(result.Trades) != 0 {
				return fail("INCONSISTENT_REJECTED_RUN")
			}
			rejectedRuns++
			for _, reason := range result.Reasons {
				if reason == "" {
					return fail("INVALID_REJECTION_REASON")
				}
				rejections[reason]++
			}
			continue
		}
		if err := validateCompletedEvidenceRun(result); err != nil {
			return fail(err.Error())
		}
		configurationHashes[result.ConfigurationHash] = true
		datasetHashes[result.DatasetHash] = true
		runHashes[result.RunHash] = true
		for _, trade := range result.Trades {
			entryAt, _ := time.Parse(time.RFC3339Nano, trade.EntryAt)
			exitAt, _ := time.Parse(time.RFC3339Nano, trade.ExitAt)
			trades = append(trades, evidenceTrade{pair: result.Pair, timeframe: result.Timeframe,
				strategy: result.Strategy, strategyVersion: result.StrategyVersion, sample: run.Sample,
				result: trade, entryAt: entryAt.UTC(), exitAt: exitAt.UTC()})
		}
	}
	if len(trades) == 0 {
		report := fail("NO_COMPLETED_TRADES")
		report.RejectedRuns, report.RejectionReasons = rejectedRuns, rejections
		return report
	}
	sortEvidenceTrades(trades)
	overall, warnings := calculatePerformanceMetrics(trades)
	report := performanceReport{
		Outcome: "PERFORMANCE_EVIDENCE", Warnings: warnings, MinimumSampleSize: input.MinimumSampleSize,
		RejectedRuns: rejectedRuns, RejectionReasons: rejections, Overall: &overall,
		ConfigurationFingerprints: sortedKeys(configurationHashes), DatasetFingerprints: sortedKeys(datasetHashes),
		RunFingerprints: sortedKeys(runHashes),
	}
	report.ByPair = groupPerformance(trades, func(t evidenceTrade) string { return t.pair + "\x00" + t.timeframe }, "pair")
	report.ByStrategy = groupPerformance(trades, func(t evidenceTrade) string {
		return t.strategy + "\x00" + t.strategyVersion + "\x00" + t.timeframe
	}, "strategy")
	report.ByMonth = groupPerformance(trades, func(t evidenceTrade) string { return t.exitAt.Format("2006-01") }, "month")
	inSample := filterEvidenceTrades(trades, performanceInSample)
	outSample := filterEvidenceTrades(trades, performanceOutSample)
	if len(inSample) > 0 {
		metrics, extra := calculatePerformanceMetrics(inSample)
		report.InSample = &metrics
		report.Warnings = append(report.Warnings, prefixWarnings("IN_SAMPLE", extra)...)
	}
	if len(outSample) > 0 {
		metrics, extra := calculatePerformanceMetrics(outSample)
		report.OutOfSample = &metrics
		report.Warnings = append(report.Warnings, prefixWarnings("OUT_OF_SAMPLE", extra)...)
	}
	if len(inSample) == 0 {
		report.Warnings = append(report.Warnings, "IN_SAMPLE_EMPTY")
	}
	if len(outSample) == 0 {
		report.Warnings = append(report.Warnings, "OUT_OF_SAMPLE_EMPTY")
	}
	if len(trades) < input.MinimumSampleSize {
		report.Outcome = "INSUFFICIENT_EVIDENCE"
		report.Warnings = append(report.Warnings, "SAMPLE_SIZE_BELOW_MINIMUM")
	}
	report.Warnings = sortedUnique(report.Warnings)
	return report
}

func validateCompletedEvidenceRun(result backtestResult) error {
	if result.Outcome != "BACKTEST_COMPLETE" || len(result.Reasons) != 0 || !paperPairs[result.Pair] ||
		backtestTimeframes[result.Timeframe] == 0 || !paperStrategies[result.Strategy] || !validPaperIdentifier(result.StrategyVersion) ||
		!sha256FingerprintPattern.MatchString(result.ConfigurationHash) || !sha256FingerprintPattern.MatchString(result.DatasetHash) ||
		!sha256FingerprintPattern.MatchString(result.RunHash) {
		return errors.New("INVALID_COMPLETED_RUN")
	}
	seen := map[string]bool{}
	for _, trade := range result.Trades {
		entryAt, entryErr := time.Parse(time.RFC3339Nano, trade.EntryAt)
		exitAt, exitErr := time.Parse(time.RFC3339Nano, trade.ExitAt)
		if entryErr != nil || exitErr != nil || exitAt.Before(entryAt) || !validPaperIdentifier(trade.TradeID) || seen[trade.TradeID] ||
			(trade.Outcome != "STOP_LOSS" && trade.Outcome != "TAKE_PROFIT") || !finitePositive(trade.Lots) ||
			!finiteNumber(trade.GrossPnL) || !finiteNonNegative(trade.Costs) || !finiteNumber(trade.NetPnL) ||
			math.Abs(round(trade.GrossPnL-trade.Costs, 8)-trade.NetPnL) > performanceEpsilon {
			return errors.New("INVALID_TRADE_EVIDENCE")
		}
		seen[trade.TradeID] = true
	}
	expectedRunHash := fingerprint(struct {
		Config, Dataset string
		Trades          []backtestTradeResult
	}{result.ConfigurationHash, result.DatasetHash, result.Trades})
	if expectedRunHash != result.RunHash {
		return errors.New("RUN_FINGERPRINT_MISMATCH")
	}
	return nil
}

func calculatePerformanceMetrics(trades []evidenceTrade) (performanceMetrics, []string) {
	metrics := performanceMetrics{TotalTrades: len(trades)}
	if len(trades) == 0 {
		return metrics, []string{"EMPTY_DATASET"}
	}
	peak, equity, maxDrawdown := 0.0, 0.0, 0.0
	currentWins, currentLosses := 0, 0
	var winTotal, lossTotal float64
	metrics.StartDate, metrics.EndDate = trades[0].entryAt.Format(time.RFC3339Nano), trades[0].exitAt.Format(time.RFC3339Nano)
	for _, trade := range trades {
		pnl := trade.result.NetPnL
		metrics.PreCostPnL += trade.result.GrossPnL
		metrics.Costs += trade.result.Costs
		metrics.NetPnL += pnl
		if trade.entryAt.Format(time.RFC3339Nano) < metrics.StartDate {
			metrics.StartDate = trade.entryAt.Format(time.RFC3339Nano)
		}
		if trade.exitAt.Format(time.RFC3339Nano) > metrics.EndDate {
			metrics.EndDate = trade.exitAt.Format(time.RFC3339Nano)
		}
		switch {
		case pnl > performanceEpsilon:
			metrics.Wins++
			metrics.GrossProfit += pnl
			winTotal += pnl
			currentWins++
			currentLosses = 0
			if currentWins > metrics.ConsecutiveWins {
				metrics.ConsecutiveWins = currentWins
			}
		case pnl < -performanceEpsilon:
			metrics.Losses++
			metrics.GrossLoss += -pnl
			lossTotal += pnl
			currentLosses++
			currentWins = 0
			if currentLosses > metrics.ConsecutiveLosses {
				metrics.ConsecutiveLosses = currentLosses
			}
		default:
			metrics.BreakevenTrades++
			currentWins, currentLosses = 0, 0
		}
		equity += pnl
		if equity > peak {
			peak = equity
		}
		if drawdown := peak - equity; drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}
	metrics.WinRate = float64(metrics.Wins) / float64(metrics.TotalTrades) * 100
	if metrics.Wins > 0 {
		metrics.AverageWin = winTotal / float64(metrics.Wins)
	}
	if metrics.Losses > 0 {
		metrics.AverageLoss = lossTotal / float64(metrics.Losses)
	}
	metrics.ExpectancyPerTrade = metrics.NetPnL / float64(metrics.TotalTrades)
	metrics.MaximumDrawdown = maxDrawdown
	warnings := []string{}
	if metrics.GrossLoss > performanceEpsilon {
		value := metrics.GrossProfit / metrics.GrossLoss
		metrics.ProfitFactor = floatPointer(value)
	} else {
		warnings = append(warnings, "PROFIT_FACTOR_UNDEFINED")
	}
	if maxDrawdown > performanceEpsilon {
		metrics.ReturnToDrawdown = floatPointer(metrics.NetPnL / maxDrawdown)
	} else {
		warnings = append(warnings, "RETURN_TO_DRAWDOWN_UNDEFINED")
	}
	roundPerformanceMetrics(&metrics)
	return metrics, warnings
}

func groupPerformance(trades []evidenceTrade, keyFn func(evidenceTrade) string, kind string) []performanceGroup {
	groups := map[string][]evidenceTrade{}
	for _, trade := range trades {
		key := keyFn(trade)
		groups[key] = append(groups[key], trade)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]performanceGroup, 0, len(keys))
	for _, key := range keys {
		metrics, _ := calculatePerformanceMetrics(groups[key])
		group := performanceGroup{Metrics: metrics}
		switch kind {
		case "pair":
			group.Pair, group.Timeframe = groups[key][0].pair, groups[key][0].timeframe
		case "month":
			group.Month = key
		case "strategy":
			group.Strategy, group.StrategyVersion, group.Timeframe = groups[key][0].strategy, groups[key][0].strategyVersion, groups[key][0].timeframe
		}
		result = append(result, group)
	}
	return result
}

func sortEvidenceTrades(trades []evidenceTrade) {
	sort.SliceStable(trades, func(i, j int) bool {
		if trades[i].exitAt.Equal(trades[j].exitAt) {
			return trades[i].result.TradeID < trades[j].result.TradeID
		}
		return trades[i].exitAt.Before(trades[j].exitAt)
	})
}
func filterEvidenceTrades(trades []evidenceTrade, sample string) []evidenceTrade {
	result := []evidenceTrade{}
	for _, trade := range trades {
		if trade.sample == sample {
			result = append(result, trade)
		}
	}
	return result
}
func finiteNumber(v float64) bool     { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func floatPointer(v float64) *float64 { rounded := round(v, 8); return &rounded }
func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func prefixWarnings(prefix string, warnings []string) []string {
	result := make([]string, len(warnings))
	for i, warning := range warnings {
		result[i] = prefix + "_" + warning
	}
	return result
}
func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	return sortedKeys(seen)
}
func roundPerformanceMetrics(m *performanceMetrics) {
	m.WinRate = round(m.WinRate, 8)
	m.GrossProfit = round(m.GrossProfit, 8)
	m.GrossLoss = round(m.GrossLoss, 8)
	m.PreCostPnL = round(m.PreCostPnL, 8)
	m.Costs = round(m.Costs, 8)
	m.NetPnL = round(m.NetPnL, 8)
	m.AverageWin = round(m.AverageWin, 8)
	m.AverageLoss = round(m.AverageLoss, 8)
	m.ExpectancyPerTrade = round(m.ExpectancyPerTrade, 8)
	m.MaximumDrawdown = round(m.MaximumDrawdown, 8)
}

func (app *application) performanceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	var input performanceRequest
	if decodeStrictJSON(w, r, &input) != nil {
		writePerformanceReport(w, http.StatusBadRequest, performanceReport{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}})
		return
	}
	report := buildPerformanceReport(input)
	status := http.StatusOK
	if report.Outcome == "NO_TRADE" {
		status = http.StatusUnprocessableEntity
	}
	writePerformanceReport(w, status, report)
}

func writePerformanceReport(w http.ResponseWriter, status int, report performanceReport) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Trading-Mode", "HISTORICAL_EVIDENCE_ONLY")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(report)
}
