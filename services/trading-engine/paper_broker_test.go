package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeBrokerStore struct {
	dispatch *BrokerDispatch
	completed bool
	rejected string
	retried string
	retryAt time.Time
}

func (store *fakeBrokerStore) Claim(context.Context) (*BrokerDispatch, error) { dispatch := store.dispatch; store.dispatch = nil; return dispatch, nil }
func (store *fakeBrokerStore) Complete(context.Context, string) error { store.completed = true; return nil }
func (store *fakeBrokerStore) Retry(_ context.Context, _ string, reason string, at time.Time) error { store.retried, store.retryAt = reason, at; return nil }
func (store *fakeBrokerStore) Reject(_ context.Context, _ string, reason string) error { store.rejected = reason; return nil }

type fakeFillExecutor struct { input ApplyFillInput; err error }
func (executor *fakeFillExecutor) ApplyFill(_ context.Context, input ApplyFillInput) (FillResult, error) { executor.input = input; return FillResult{}, executor.err }

func paperOrder(side, orderType string, limit *string) Order {
	return Order{ID: testOrderID, UserID: testUserID, Symbol: "AAPL", Side: side, Type: orderType, Quantity: "5.00000000", FilledQuantity: "2.00000000", LimitPrice: limit, Status: "partially_filled"}
}

func TestPaperBrokerExecutesRemainingQuantityAtExecutableSide(t *testing.T) {
	limit := "105"
	store := &fakeBrokerStore{dispatch: &BrokerDispatch{Order: paperOrder("buy", "limit", &limit), Attempt: 1}}
	executor := &fakeFillExecutor{}
	broker := NewPaperBroker(store, fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "104.10", Ask: "104.20", Last: "104.15"},
	}}, executor, time.Second)
	broker.now = func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) }

	processed, err := broker.ProcessNext(context.Background())
	if err != nil || !processed || !store.completed { t.Fatalf("expected completion, processed=%v err=%v store=%#v", processed, err, store) }
	if executor.input.Price != "104.20" || executor.input.Quantity != "3.00000000" || executor.input.ExecutionID != "paper-"+testOrderID {
		t.Fatalf("unexpected fill: %#v", executor.input)
	}
}

func TestPaperBrokerWaitsForFreshMarketableQuote(t *testing.T) {
	limit := "100"
	store := &fakeBrokerStore{dispatch: &BrokerDispatch{Order: paperOrder("buy", "limit", &limit), Attempt: 2}}
	broker := NewPaperBroker(store, fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "104", Ask: "105", Last: "104.5"},
	}}, &fakeFillExecutor{}, time.Second)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	broker.now = func() time.Time { return now }
	_, err := broker.ProcessNext(context.Background())
	if err != nil || store.retried != "limit_not_marketable" || !store.retryAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("unexpected retry: %#v err=%v", store, err)
	}

	staleStore := &fakeBrokerStore{dispatch: &BrokerDispatch{Order: paperOrder("buy", "market", nil), Attempt: 1}}
	stale := NewPaperBroker(staleStore, fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "99", Ask: "100", Last: "99.5", Stale: true},
	}}, &fakeFillExecutor{}, time.Second)
	_, _ = stale.ProcessNext(context.Background())
	if staleStore.retried != "quote_stale" { t.Fatalf("expected stale retry, got %#v", staleStore) }
}

func TestPaperBrokerPermanentlyRejectsUnfundedOrder(t *testing.T) {
	store := &fakeBrokerStore{dispatch: &BrokerDispatch{Order: paperOrder("buy", "market", nil), Attempt: 1}}
	executor := &fakeFillExecutor{err: errInsufficientFunds}
	broker := NewPaperBroker(store, fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "99", Ask: "100", Last: "99.5"},
	}}, executor, time.Second)
	_, err := broker.ProcessNext(context.Background())
	if err != nil || !errors.Is(executor.err, errInsufficientFunds) || store.rejected != errInsufficientFunds.Error() {
		t.Fatalf("expected permanent rejection: %#v err=%v", store, err)
	}
}
