package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func lifecycleTestPosition(direction string) paperPosition {
	position := paperPosition{
		OrderID: "paper-order-001", RequestID: "order-001", Pair: "EURUSD", Strategy: "EMA_PULLBACK",
		Direction: direction, Lots: 0.1, PipValuePerStandardLot: 10, Status: "OPEN",
		OpenedAt: paperTestNow, CurrentPrice: 1.1000,
	}
	if direction == "BUY" {
		position.Entry, position.StopLoss, position.TakeProfit = 1.1000, 1.0950, 1.1100
	} else {
		position.Entry, position.StopLoss, position.TakeProfit = 1.1000, 1.1050, 1.0900
	}
	return position
}

func TestApplyPaperQuoteMarksAndClosesBuy(t *testing.T) {
	position := lifecycleTestPosition("BUY")
	marked, eventType, err := applyPaperQuote(position, 1.1050, 1.1052, paperTestNow.Add(time.Second))
	if err != nil || eventType != "POSITION_MARKED" || marked.Status != "OPEN" || marked.UnrealizedPnL != 50 {
		t.Fatalf("unexpected mark: position=%+v type=%s err=%v", marked, eventType, err)
	}
	closed, eventType, err := applyPaperQuote(marked, 1.1101, 1.1103, paperTestNow.Add(2*time.Second))
	if err != nil || eventType != "POSITION_CLOSED_TAKE_PROFIT" || closed.Status != "CLOSED" ||
		closed.CloseReason != "TAKE_PROFIT" || closed.ExitPrice != 1.1100 || closed.RealizedPnL != 100 {
		t.Fatalf("unexpected close: position=%+v type=%s err=%v", closed, eventType, err)
	}
}

func TestApplyPaperQuoteUsesAskToStopSell(t *testing.T) {
	position := lifecycleTestPosition("SELL")
	closed, eventType, err := applyPaperQuote(position, 1.1049, 1.1051, paperTestNow.Add(time.Second))
	if err != nil || eventType != "POSITION_CLOSED_STOP_LOSS" || closed.ExitPrice != 1.1050 || closed.RealizedPnL != -50 {
		t.Fatalf("unexpected close: position=%+v type=%s err=%v", closed, eventType, err)
	}
}

func TestApplyPaperQuoteRejectsOutOfOrderQuote(t *testing.T) {
	position := lifecycleTestPosition("BUY")
	if _, _, err := applyPaperQuote(position, 1.1010, 1.1012, paperTestNow); err != errOutOfOrderQuote {
		t.Fatalf("expected out-of-order error, got %v", err)
	}
}

func TestApplyPaperQuoteRejectsWideSpread(t *testing.T) {
	position := lifecycleTestPosition("BUY")
	if _, _, err := applyPaperQuote(position, 1.1000, 1.1010, paperTestNow.Add(time.Second)); err != errPaperSpreadTooWide {
		t.Fatalf("expected spread error, got %v", err)
	}
}

func TestMemoryLifecycleStoreIsIdempotentAndJournaled(t *testing.T) {
	store := newMemoryPaperLifecycleStore()
	position := lifecycleTestPosition("BUY")
	event := paperJournalEvent{EventID: "open-001", Type: "POSITION_OPENED", RecordedAt: paperTestNow, Position: position}
	if _, replay, err := store.Open(context.Background(), "open-001", position, event); err != nil || replay {
		t.Fatalf("unexpected first open replay=%v err=%v", replay, err)
	}
	if _, replay, err := store.Open(context.Background(), "open-001", position, event); err != nil || !replay {
		t.Fatalf("expected exact replay, replay=%v err=%v", replay, err)
	}
	conflicting := position
	conflicting.OrderID = "paper-order-002"
	if _, _, err := store.Open(context.Background(), "open-001", conflicting, event); err != errLifecycleConflict {
		t.Fatalf("expected conflict, got %v", err)
	}
	closed, replay, err := store.ApplyQuote(
		context.Background(), "quote-001", position.OrderID, 1.0949, 1.0951,
		paperTestNow.Add(time.Second), paperTestNow.Add(time.Second),
	)
	if err != nil || replay || closed.Status != "CLOSED" || closed.CloseReason != "STOP_LOSS" {
		t.Fatalf("unexpected quote result: position=%+v replay=%v err=%v", closed, replay, err)
	}
	originalOpen, replay, err := store.Open(context.Background(), "open-001", position, event)
	if err != nil || !replay || originalOpen.Status != "OPEN" {
		t.Fatalf("expected immutable original open replay, position=%+v replay=%v err=%v", originalOpen, replay, err)
	}
	events, err := store.Journal(context.Background(), position.OrderID)
	if err != nil || len(events) != 2 || events[0].Type != "POSITION_OPENED" || events[1].Type != "POSITION_CLOSED_STOP_LOSS" {
		t.Fatalf("unexpected journal: events=%+v err=%v", events, err)
	}
}

func TestPaperLifecycleHandlersOpenCloseAndExposeJournal(t *testing.T) {
	orderStore := newMemoryPaperOrderStore()
	orderResult := evaluatePaperOrder(validPaperOrderRequest(), paperTestNow)
	if _, _, err := orderStore.Create(context.Background(), *orderResult.Order); err != nil {
		t.Fatal(err)
	}
	app := &application{
		paperStore: orderStore, lifecycleStore: newMemoryPaperLifecycleStore(),
		now: func() time.Time { return paperTestNow },
	}
	openBody, _ := json.Marshal(openPaperPositionRequest{
		EventID: "open-001", RequestID: "order-001", PipValuePerStandardLot: 10,
	})
	openRecorder := httptest.NewRecorder()
	app.openPaperPositionHandler(openRecorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper/positions", bytes.NewReader(openBody)))
	if openRecorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", openRecorder.Code, openRecorder.Body.String())
	}

	quoteBody, _ := json.Marshal(paperQuoteRequest{
		EventID: "quote-001", OrderID: "paper-order-001", Bid: 1.1101, Ask: 1.1103,
		QuoteTimestamp: paperTestNow.Add(time.Second).Format(time.RFC3339Nano),
	})
	quoteRecorder := httptest.NewRecorder()
	app.paperQuoteHandler(quoteRecorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper/positions/quotes", bytes.NewReader(quoteBody)))
	if quoteRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", quoteRecorder.Code, quoteRecorder.Body.String())
	}
	var quoteResult paperLifecycleResult
	if err := json.Unmarshal(quoteRecorder.Body.Bytes(), &quoteResult); err != nil || quoteResult.Outcome != "POSITION_CLOSED" || quoteResult.Position.RealizedPnL != 100 {
		t.Fatalf("unexpected close response: %s", quoteRecorder.Body.String())
	}

	journalRecorder := httptest.NewRecorder()
	app.paperJournalHandler(journalRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/paper/journal?orderId=paper-order-001", nil))
	if journalRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", journalRecorder.Code, journalRecorder.Body.String())
	}
	var journalResponse struct {
		Events []paperJournalEvent `json:"events"`
	}
	if err := json.Unmarshal(journalRecorder.Body.Bytes(), &journalResponse); err != nil || len(journalResponse.Events) != 2 {
		t.Fatalf("unexpected journal response: %s", journalRecorder.Body.String())
	}
}

func TestPaperQuoteHandlerRejectsStaleAndMalformedQuotes(t *testing.T) {
	app := &application{lifecycleStore: newMemoryPaperLifecycleStore(), now: func() time.Time { return paperTestNow }}
	tests := []string{
		`{"eventId":"q1","orderId":"p1","bid":1.1,"ask":1.2,"quoteTimestamp":"2026-08-17T09:59:00Z"}`,
		`{"eventId":"q1","orderId":"p1","bid":1.2,"ask":1.1,"quoteTimestamp":"2026-08-17T10:00:00Z"}`,
		`{"eventId":"q1","orderId":"p1","bid":1.1,"ask":1.2,"quoteTimestamp":"2026-08-17T10:00:00Z","extra":true}`,
	}
	for index, body := range tests {
		recorder := httptest.NewRecorder()
		app.paperQuoteHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/paper/positions/quotes", bytes.NewBufferString(body)))
		if index == 0 && recorder.Code != http.StatusOK || index > 0 && recorder.Code != http.StatusBadRequest {
			t.Fatalf("case %d unexpected status %d: %s", index, recorder.Code, recorder.Body.String())
		}
	}
}
