package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"
)

type readinessThresholds struct {
	MinimumTotalTrades             int     `json:"minimumTotalTrades"`
	MinimumOutOfSampleTrades       int     `json:"minimumOutOfSampleTrades"`
	MinimumCompletedFolds          int     `json:"minimumCompletedFolds"`
	MinimumProfitableFoldPercent   float64 `json:"minimumProfitableFoldPercent"`
	MinimumOutOfSampleExpectancy   float64 `json:"minimumOutOfSampleExpectancy"`
	MinimumOutOfSampleProfitFactor float64 `json:"minimumOutOfSampleProfitFactor"`
	MaximumOutOfSampleDrawdown     float64 `json:"maximumOutOfSampleDrawdown"`
	MaximumDegradationPercent      float64 `json:"maximumDegradationPercent"`
	MaximumConsecutiveLosses       *int    `json:"maximumConsecutiveLosses,omitempty"`
}

type validationPeriod struct {
	Start  string         `json:"start"`
	End    string         `json:"end"`
	Result backtestResult `json:"result"`
}

type walkForwardFold struct {
	FoldID   string           `json:"foldId"`
	Training validationPeriod `json:"training"`
	Testing  validationPeriod `json:"testing"`
}

type walkForwardRequest struct {
	Thresholds readinessThresholds `json:"thresholds"`
	Folds      []walkForwardFold   `json:"folds"`
}

type readinessGate struct {
	Code      string  `json:"code"`
	Passed    bool    `json:"passed"`
	Actual    float64 `json:"actual"`
	Threshold float64 `json:"threshold"`
}

type foldEvidence struct {
	FoldID                   string             `json:"foldId"`
	TrainingStart            string             `json:"trainingStart"`
	TrainingEnd              string             `json:"trainingEnd"`
	TestingStart             string             `json:"testingStart"`
	TestingEnd               string             `json:"testingEnd"`
	TrainingDatasetHash      string             `json:"trainingDatasetFingerprint"`
	TestingDatasetHash       string             `json:"testingDatasetFingerprint"`
	TrainingRunHash          string             `json:"trainingRunFingerprint"`
	TestingRunHash           string             `json:"testingRunFingerprint"`
	TrainingMetrics          performanceMetrics `json:"trainingMetrics"`
	TestingMetrics           performanceMetrics `json:"testingMetrics"`
	ProfitableOutOfSample    bool               `json:"profitableOutOfSample"`
	ExpectancyDegradationPct float64            `json:"expectancyDegradationPercent"`
}

type walkForwardDecision struct {
	Outcome                  string              `json:"outcome"`
	Reasons                  []string            `json:"reasons,omitempty"`
	Warnings                 []string            `json:"warnings"`
	Pair                     string              `json:"pair,omitempty"`
	Timeframe                string              `json:"timeframe,omitempty"`
	Strategy                 string              `json:"strategy,omitempty"`
	StrategyVersion          string              `json:"strategyVersion,omitempty"`
	TrainingMetrics          *performanceMetrics `json:"trainingMetrics,omitempty"`
	OutOfSampleMetrics       *performanceMetrics `json:"outOfSampleMetrics,omitempty"`
	Folds                    []foldEvidence      `json:"folds,omitempty"`
	Gates                    []readinessGate     `json:"gates,omitempty"`
	ConfigurationFingerprint string              `json:"configurationFingerprint,omitempty"`
	DatasetFingerprints      []string            `json:"datasetFingerprints,omitempty"`
	EvidenceFingerprints     []string            `json:"evidenceFingerprints,omitempty"`
	DatasetSetFingerprint    string              `json:"datasetSetFingerprint,omitempty"`
	EvidenceSetFingerprint   string              `json:"evidenceSetFingerprint,omitempty"`
	DecisionFingerprint      string              `json:"decisionFingerprint,omitempty"`
}

type parsedValidationPeriod struct {
	start, end time.Time
	result     backtestResult
}

type walkForwardIdentity struct {
	Pair, Timeframe, Strategy, Version string
}

func evaluateWalkForward(input walkForwardRequest) walkForwardDecision {
	fail := func(reason string) walkForwardDecision {
		return walkForwardDecision{Outcome: "NOT_READY", Reasons: []string{reason}, Warnings: historicalValidationWarnings()}
	}
	if !validReadinessThresholds(input.Thresholds) || len(input.Folds) == 0 {
		return fail("INVALID_READINESS_CONFIG")
	}
	var expected walkForwardIdentity
	seenFolds, seenPeriods, seenTrades, seenDatasets, seenRuns := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	allTraining, allTesting := []evidenceTrade{}, []evidenceTrade{}
	datasetHashes, evidenceHashes := map[string]bool{}, map[string]bool{}
	foldOutput := make([]foldEvidence, 0, len(input.Folds))
	var previousTestEnd time.Time
	profitableFolds := 0
	undefinedFoldProfitFactor, undefinedFoldDegradation := false, false
	for index, fold := range input.Folds {
		if !validPaperIdentifier(fold.FoldID) || seenFolds[fold.FoldID] {
			return fail("DUPLICATE_OR_INVALID_FOLD")
		}
		seenFolds[fold.FoldID] = true
		training, err := parseValidationPeriod(fold.Training)
		if err != nil {
			return fail(err.Error())
		}
		testing, err := parseValidationPeriod(fold.Testing)
		if err != nil {
			return fail(err.Error())
		}
		if !training.end.Equal(testing.start) {
			if training.end.After(testing.start) {
				return fail("TRAIN_TEST_OVERLAP")
			}
			return fail("MISSING_PERIOD")
		}
		periodKeys := []string{training.start.Format(time.RFC3339Nano) + "/" + training.end.Format(time.RFC3339Nano), testing.start.Format(time.RFC3339Nano) + "/" + testing.end.Format(time.RFC3339Nano)}
		for _, key := range periodKeys {
			if seenPeriods[key] {
				return fail("DUPLICATE_PERIOD")
			}
			seenPeriods[key] = true
		}
		if index > 0 {
			if previousTestEnd.After(training.start) {
				return fail("FOLD_OVERLAP")
			}
			if !previousTestEnd.Equal(training.start) {
				return fail("MISSING_PERIOD")
			}
		}
		previousTestEnd = testing.end
		current := walkForwardIdentity{training.result.Pair, training.result.Timeframe, training.result.Strategy, training.result.StrategyVersion}
		testIdentity := walkForwardIdentity{testing.result.Pair, testing.result.Timeframe, testing.result.Strategy, testing.result.StrategyVersion}
		if current != testIdentity {
			return fail("IDENTITY_MISMATCH")
		}
		if index == 0 {
			expected = current
		} else if current != expected {
			return fail("IDENTITY_ISOLATION_VIOLATION")
		}
		if training.result.DatasetHash == testing.result.DatasetHash || training.result.RunHash == testing.result.RunHash {
			return fail("TRAIN_TEST_LEAKAGE")
		}
		for _, hash := range []string{training.result.DatasetHash, testing.result.DatasetHash} {
			if seenDatasets[hash] {
				return fail("EVIDENCE_REUSE")
			}
			seenDatasets[hash] = true
			datasetHashes[hash] = true
		}
		for _, hash := range []string{training.result.RunHash, testing.result.RunHash} {
			if seenRuns[hash] {
				return fail("EVIDENCE_REUSE")
			}
			seenRuns[hash] = true
			evidenceHashes[hash] = true
		}
		trainingTrades, err := periodEvidenceTrades(training, performanceInSample, seenTrades)
		if err != nil {
			return fail(err.Error())
		}
		testingTrades, err := periodEvidenceTrades(testing, performanceOutSample, seenTrades)
		if err != nil {
			return fail(err.Error())
		}
		foldReport := buildPerformanceReport(performanceRequest{MinimumSampleSize: 1, Runs: []performanceRun{{Sample: performanceInSample, Result: training.result}, {Sample: performanceOutSample, Result: testing.result}}})
		if foldReport.Outcome != "PERFORMANCE_EVIDENCE" || foldReport.InSample == nil || foldReport.OutOfSample == nil {
			return fail("INVALID_PERFORMANCE_EVIDENCE")
		}
		if foldReport.OutOfSample.NetPnL > performanceEpsilon {
			profitableFolds++
		}
		if foldReport.OutOfSample.ProfitFactor == nil {
			undefinedFoldProfitFactor = true
		}
		degradation, ok := expectancyDegradation(foldReport.InSample.ExpectancyPerTrade, foldReport.OutOfSample.ExpectancyPerTrade)
		if !ok {
			degradation = 0
			undefinedFoldDegradation = true
		}
		foldOutput = append(foldOutput, foldEvidence{FoldID: fold.FoldID,
			TrainingStart: training.start.Format(time.RFC3339Nano), TrainingEnd: training.end.Format(time.RFC3339Nano),
			TestingStart: testing.start.Format(time.RFC3339Nano), TestingEnd: testing.end.Format(time.RFC3339Nano),
			TrainingDatasetHash: training.result.DatasetHash, TestingDatasetHash: testing.result.DatasetHash,
			TrainingRunHash: training.result.RunHash, TestingRunHash: testing.result.RunHash,
			TrainingMetrics: *foldReport.InSample, TestingMetrics: *foldReport.OutOfSample,
			ProfitableOutOfSample: foldReport.OutOfSample.NetPnL > performanceEpsilon, ExpectancyDegradationPct: round(degradation, 8)})
		allTraining = append(allTraining, trainingTrades...)
		allTesting = append(allTesting, testingTrades...)
	}
	sortEvidenceTrades(allTraining)
	sortEvidenceTrades(allTesting)
	trainingMetrics, trainingWarnings := calculatePerformanceMetrics(allTraining)
	testingMetrics, testingWarnings := calculatePerformanceMetrics(allTesting)
	reasons := []string{}
	if undefinedFoldProfitFactor {
		reasons = append(reasons, "FOLD_OUT_OF_SAMPLE_PROFIT_FACTOR_UNDEFINED")
	}
	if undefinedFoldDegradation {
		reasons = append(reasons, "FOLD_DEGRADATION_UNDEFINED")
	}
	gates := []readinessGate{}
	addGate := func(code string, actual, threshold float64, passed bool) {
		gates = append(gates, readinessGate{code, passed, round(actual, 8), round(threshold, 8)})
		if !passed {
			reasons = append(reasons, code)
		}
	}
	addGate("MINIMUM_TOTAL_TRADES", float64(len(allTraining)+len(allTesting)), float64(input.Thresholds.MinimumTotalTrades), len(allTraining)+len(allTesting) >= input.Thresholds.MinimumTotalTrades)
	addGate("MINIMUM_OUT_OF_SAMPLE_TRADES", float64(len(allTesting)), float64(input.Thresholds.MinimumOutOfSampleTrades), len(allTesting) >= input.Thresholds.MinimumOutOfSampleTrades)
	addGate("MINIMUM_COMPLETED_FOLDS", float64(len(foldOutput)), float64(input.Thresholds.MinimumCompletedFolds), len(foldOutput) >= input.Thresholds.MinimumCompletedFolds)
	profitablePercent := float64(profitableFolds) / float64(len(foldOutput)) * 100
	addGate("MINIMUM_PROFITABLE_FOLD_PERCENT", profitablePercent, input.Thresholds.MinimumProfitableFoldPercent, profitablePercent+performanceEpsilon >= input.Thresholds.MinimumProfitableFoldPercent)
	addGate("MINIMUM_OUT_OF_SAMPLE_EXPECTANCY", testingMetrics.ExpectancyPerTrade, input.Thresholds.MinimumOutOfSampleExpectancy, testingMetrics.ExpectancyPerTrade+performanceEpsilon >= input.Thresholds.MinimumOutOfSampleExpectancy)
	if testingMetrics.ProfitFactor == nil {
		reasons = append(reasons, "OUT_OF_SAMPLE_PROFIT_FACTOR_UNDEFINED")
		gates = append(gates, readinessGate{Code: "MINIMUM_OUT_OF_SAMPLE_PROFIT_FACTOR", Passed: false, Threshold: input.Thresholds.MinimumOutOfSampleProfitFactor})
	} else {
		addGate("MINIMUM_OUT_OF_SAMPLE_PROFIT_FACTOR", *testingMetrics.ProfitFactor, input.Thresholds.MinimumOutOfSampleProfitFactor, *testingMetrics.ProfitFactor+performanceEpsilon >= input.Thresholds.MinimumOutOfSampleProfitFactor)
	}
	addGate("MAXIMUM_OUT_OF_SAMPLE_DRAWDOWN", testingMetrics.MaximumDrawdown, input.Thresholds.MaximumOutOfSampleDrawdown, testingMetrics.MaximumDrawdown <= input.Thresholds.MaximumOutOfSampleDrawdown+performanceEpsilon)
	degradation, degradationDefined := expectancyDegradation(trainingMetrics.ExpectancyPerTrade, testingMetrics.ExpectancyPerTrade)
	if !degradationDefined {
		reasons = append(reasons, "DEGRADATION_UNDEFINED")
		gates = append(gates, readinessGate{Code: "MAXIMUM_EXPECTANCY_DEGRADATION", Passed: false, Threshold: input.Thresholds.MaximumDegradationPercent})
	} else {
		addGate("MAXIMUM_EXPECTANCY_DEGRADATION", degradation, input.Thresholds.MaximumDegradationPercent, degradation <= input.Thresholds.MaximumDegradationPercent+performanceEpsilon)
	}
	if input.Thresholds.MaximumConsecutiveLosses != nil {
		addGate("MAXIMUM_CONSECUTIVE_LOSSES", float64(testingMetrics.ConsecutiveLosses), float64(*input.Thresholds.MaximumConsecutiveLosses), testingMetrics.ConsecutiveLosses <= *input.Thresholds.MaximumConsecutiveLosses)
	}
	warnings := append(historicalValidationWarnings(), prefixWarnings("TRAINING", trainingWarnings)...)
	warnings = append(warnings, prefixWarnings("OUT_OF_SAMPLE", testingWarnings)...)
	if len(allTraining) == 0 || len(allTesting) == 0 {
		reasons = append(reasons, "STATISTICALLY_INADEQUATE")
	}
	decision := walkForwardDecision{Outcome: "NOT_READY", Reasons: sortedUnique(reasons), Warnings: sortedUnique(warnings), Pair: expected.Pair, Timeframe: expected.Timeframe, Strategy: expected.Strategy, StrategyVersion: expected.Version,
		TrainingMetrics: &trainingMetrics, OutOfSampleMetrics: &testingMetrics, Folds: foldOutput, Gates: gates, ConfigurationFingerprint: fingerprint(input.Thresholds), DatasetFingerprints: sortedKeys(datasetHashes), EvidenceFingerprints: sortedKeys(evidenceHashes)}
	decision.DatasetSetFingerprint = fingerprint(decision.DatasetFingerprints)
	decision.EvidenceSetFingerprint = fingerprint(decision.EvidenceFingerprints)
	if len(decision.Reasons) == 0 {
		decision.Outcome = "READY"
	}
	decision.DecisionFingerprint = fingerprint(struct {
		Outcome            string
		Identity           walkForwardIdentity
		Config             string
		DatasetSet         string
		EvidenceSet        string
		Datasets, Evidence []string
		Folds              []foldEvidence
		Gates              []readinessGate
	}{decision.Outcome, expected, decision.ConfigurationFingerprint, decision.DatasetSetFingerprint, decision.EvidenceSetFingerprint, decision.DatasetFingerprints, decision.EvidenceFingerprints, decision.Folds, decision.Gates})
	return decision
}

func validReadinessThresholds(t readinessThresholds) bool {
	if t.MinimumTotalTrades <= 0 || t.MinimumOutOfSampleTrades <= 0 || t.MinimumCompletedFolds <= 0 || !finiteNonNegative(t.MinimumProfitableFoldPercent) || t.MinimumProfitableFoldPercent > 100 || !finiteNumber(t.MinimumOutOfSampleExpectancy) || !finiteNonNegative(t.MinimumOutOfSampleProfitFactor) || !finiteNonNegative(t.MaximumOutOfSampleDrawdown) || !finiteNonNegative(t.MaximumDegradationPercent) {
		return false
	}
	return t.MaximumConsecutiveLosses == nil || *t.MaximumConsecutiveLosses >= 0
}

func parseValidationPeriod(period validationPeriod) (parsedValidationPeriod, error) {
	start, e1 := time.Parse(time.RFC3339Nano, period.Start)
	end, e2 := time.Parse(time.RFC3339Nano, period.End)
	if e1 != nil || e2 != nil || !start.Before(end) {
		return parsedValidationPeriod{}, errors.New("INVALID_OR_REVERSED_PERIOD")
	}
	if err := validateCompletedEvidenceRun(period.Result); err != nil {
		return parsedValidationPeriod{}, err
	}
	return parsedValidationPeriod{start.UTC(), end.UTC(), period.Result}, nil
}

func periodEvidenceTrades(period parsedValidationPeriod, sample string, seen map[string]bool) ([]evidenceTrade, error) {
	trades := make([]evidenceTrade, 0, len(period.result.Trades))
	if len(period.result.Trades) == 0 {
		return nil, errors.New("EMPTY_PERIOD")
	}
	for _, trade := range period.result.Trades {
		entry, _ := time.Parse(time.RFC3339Nano, trade.EntryAt)
		exit, _ := time.Parse(time.RFC3339Nano, trade.ExitAt)
		if entry.Before(period.start) || exit.After(period.end) {
			return nil, errors.New("TRADE_OUTSIDE_PERIOD")
		}
		if seen[trade.TradeID] {
			return nil, errors.New("TRAIN_TEST_LEAKAGE")
		}
		seen[trade.TradeID] = true
		trades = append(trades, evidenceTrade{period.result.Pair, period.result.Timeframe, period.result.Strategy, period.result.StrategyVersion, sample, trade, entry.UTC(), exit.UTC()})
	}
	return trades, nil
}

func expectancyDegradation(training, testing float64) (float64, bool) {
	if !finiteNumber(training) || !finiteNumber(testing) || training <= performanceEpsilon {
		return 0, false
	}
	value := (training - testing) / math.Abs(training) * 100
	if value < 0 {
		value = 0
	}
	return round(value, 8), finiteNumber(value)
}
func historicalValidationWarnings() []string {
	return []string{"HISTORICAL_VALIDATION_ONLY", "HISTORICAL_RESULTS_DO_NOT_GUARANTEE_FUTURE_PERFORMANCE", "LIVE_TRADING_NOT_AUTHORIZED"}
}

func (app *application) walkForwardHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	var input walkForwardRequest
	if decodeStrictJSON(w, r, &input) != nil {
		writeWalkForwardDecision(w, http.StatusBadRequest, walkForwardDecision{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}, Warnings: historicalValidationWarnings()})
		return
	}
	writeWalkForwardDecision(w, http.StatusOK, evaluateWalkForward(input))
}
func writeWalkForwardDecision(w http.ResponseWriter, status int, decision walkForwardDecision) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Trading-Mode", "HISTORICAL_VALIDATION_ONLY")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(decision)
}
