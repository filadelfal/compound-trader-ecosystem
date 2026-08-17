package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetenvFallback(t *testing.T) {
    t.Setenv("COMPOUND_TEST_VALUE", "")
    if got := getenv("COMPOUND_TEST_VALUE", "fallback"); got != "fallback" {
        t.Fatalf("expected fallback, got %q", got)
    }
}

func validPositionSizeRequest() positionSizeRequest {
	return positionSizeRequest{
		RequestID: "risk-001", Pair: "EURUSD", Direction: "BUY", Equity: 10000,
		RiskPercent: 0.5, Entry: 1.1000, StopLoss: 1.0950,
		PipValuePerStandardLot: 10, MinimumLot: 0.01, MaximumLot: 2, LotStep: 0.01,
		DailyLossPercent: 0.5, DrawdownPercent: 1, OpenPositions: 0,
		ExistingPairExposure: false, TradingEnabled: true, KillSwitchActive: false,
	}
}

func TestCalculatePositionSizeApprovesBoundedRisk(t *testing.T) {
	result := calculatePositionSize(validPositionSizeRequest())
	if result.Outcome != "RISK_APPROVED" || result.Lots != 0.1 || result.RiskAmount != 50 || result.StopDistancePips != 50 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCalculatePositionSizeFailsClosedAtHardLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*positionSizeRequest)
		reason string
	}{
		{"kill switch", func(v *positionSizeRequest) { v.KillSwitchActive = true }, "KILL_SWITCH_ACTIVE"},
		{"daily loss", func(v *positionSizeRequest) { v.DailyLossPercent = 2 }, "DAILY_LOSS_LIMIT_REACHED"},
		{"drawdown", func(v *positionSizeRequest) { v.DrawdownPercent = 5 }, "DRAWDOWN_LIMIT_REACHED"},
		{"position count", func(v *positionSizeRequest) { v.OpenPositions = 2 }, "POSITION_LIMIT_REACHED"},
		{"pair exposure", func(v *positionSizeRequest) { v.ExistingPairExposure = true }, "PAIR_EXPOSURE_EXISTS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validPositionSizeRequest()
			test.mutate(&input)
			result := calculatePositionSize(input)
			if result.Outcome != "NO_TRADE" || len(result.Reasons) != 1 || result.Reasons[0] != test.reason {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestCalculatePositionSizeRejectsUnsafeStructureAndTinySize(t *testing.T) {
	input := validPositionSizeRequest()
	input.StopLoss = input.Entry + 0.001
	if got := calculatePositionSize(input); got.Reasons[0] != "INVALID_STOP_STRUCTURE" {
		t.Fatalf("unexpected result: %+v", got)
	}
	input = validPositionSizeRequest()
	input.Equity = 10
	if got := calculatePositionSize(input); got.Reasons[0] != "SIZE_BELOW_BROKER_MINIMUM" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPositionSizeHandlerRejectsUnknownFields(t *testing.T) {
	body := []byte(`{"requestId":"risk-001","unexpected":true}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/risk/position-size", bytes.NewReader(body))
	(&application{}).positionSizeHandler(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["outcome"] != "NO_TRADE" {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}
