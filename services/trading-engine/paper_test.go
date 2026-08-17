package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var paperTestNow = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func validPaperOrderRequest() paperOrderRequest {
	return paperOrderRequest{
		RequestID: "order-001", Mode: "PAPER", Pair: "EURUSD", Strategy: "EMA_PULLBACK",
		Direction: "BUY", Lots: 0.1, Entry: 1.1000, StopLoss: 1.0950, TakeProfit: 1.1100,
		QuoteTimestamp: paperTestNow.Add(-5 * time.Second).Format(time.RFC3339Nano), RiskApproved: true,
	}
}

func TestEvaluatePaperOrderAcceptsDisciplinedSetup(t *testing.T) {
	result := evaluatePaperOrder(validPaperOrderRequest(), paperTestNow)
	if result.Outcome != "PAPER_ACCEPTED" || result.Order == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Order.OrderID != "paper-order-001" || result.Order.Status != "PAPER_ACCEPTED" {
		t.Fatalf("unexpected order: %+v", result.Order)
	}
}

func TestEvaluatePaperOrderFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paperOrderRequest)
		reason string
	}{
		{"live mode", func(v *paperOrderRequest) { v.Mode = "LIVE" }, "INVALID_INPUT"},
		{"London USDJPY", func(v *paperOrderRequest) { v.Strategy = "LONDON_BREAKOUT"; v.Pair = "USDJPY" }, "INVALID_INPUT"},
		{"risk missing", func(v *paperOrderRequest) { v.RiskApproved = false }, "RISK_NOT_APPROVED"},
		{"kill switch", func(v *paperOrderRequest) { v.KillSwitchActive = true }, "KILL_SWITCH_ACTIVE"},
		{"stale quote", func(v *paperOrderRequest) { v.QuoteTimestamp = paperTestNow.Add(-31 * time.Second).Format(time.RFC3339) }, "STALE_QUOTE"},
		{"future quote", func(v *paperOrderRequest) { v.QuoteTimestamp = paperTestNow.Add(3 * time.Second).Format(time.RFC3339) }, "STALE_QUOTE"},
		{"wrong stop side", func(v *paperOrderRequest) { v.StopLoss = v.Entry + 0.0010 }, "INVALID_PRICE_STRUCTURE"},
		{"poor reward", func(v *paperOrderRequest) { v.TakeProfit = v.Entry + 0.0050 }, "INSUFFICIENT_RISK_REWARD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validPaperOrderRequest()
			test.mutate(&input)
			result := evaluatePaperOrder(input, paperTestNow)
			if result.Outcome != "NO_TRADE" || len(result.Reasons) != 1 || result.Reasons[0] != test.reason || result.Order != nil {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestPaperOrderHandlerRejectsIdempotencyConflict(t *testing.T) {
	app := &application{paperStore: newMemoryPaperOrderStore(), now: func() time.Time { return paperTestNow }}
	input := validPaperOrderRequest()
	firstBody, _ := json.Marshal(input)
	app.paperOrderHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/paper/orders", bytes.NewReader(firstBody)))

	input.Lots = 0.2
	secondBody, _ := json.Marshal(input)
	second := httptest.NewRecorder()
	app.paperOrderHandler(second, httptest.NewRequest(http.MethodPost, "/api/v1/paper/orders", bytes.NewReader(secondBody)))
	if second.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", second.Code, second.Body.String())
	}
	var result paperOrderResult
	if err := json.Unmarshal(second.Body.Bytes(), &result); err != nil || len(result.Reasons) != 1 || result.Reasons[0] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("unexpected result: %s", second.Body.String())
	}
}

func TestPaperOrderHandlerIsIdempotent(t *testing.T) {
	app := &application{paperStore: newMemoryPaperOrderStore(), now: func() time.Time { return paperTestNow }}
	input := validPaperOrderRequest()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	app.paperOrderHandler(first, httptest.NewRequest(http.MethodPost, "/api/v1/paper/orders", bytes.NewReader(body)))
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", first.Code, first.Body.String())
	}
	var firstResult paperOrderResult
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}

	second := httptest.NewRecorder()
	app.paperOrderHandler(second, httptest.NewRequest(http.MethodPost, "/api/v1/paper/orders", bytes.NewReader(body)))
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 replay, got %d: %s", second.Code, second.Body.String())
	}
	var secondResult paperOrderResult
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if !secondResult.Replay || firstResult.Order.OrderID != secondResult.Order.OrderID || !firstResult.Order.CreatedAt.Equal(secondResult.Order.CreatedAt) {
		t.Fatalf("expected immutable replay, first=%+v second=%+v", firstResult, secondResult)
	}
}

func TestPaperOrderHandlerRejectsUnknownFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/paper/orders", bytes.NewBufferString(`{"requestId":"order-001","unexpected":true}`))
	(&application{}).paperOrderHandler(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
	var result paperOrderResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Outcome != "NO_TRADE" {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestPaperOrderHandlerFailsWhenStoreUnavailable(t *testing.T) {
	input := validPaperOrderRequest()
	body, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	app := &application{now: func() time.Time { return paperTestNow }}
	app.paperOrderHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper/orders", bytes.NewReader(body)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
