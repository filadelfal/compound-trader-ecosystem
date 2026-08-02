package main

import (
	"context"
	"testing"
	"time"
)

type fakeMarketDataClient struct {
	quotes  map[string]MarketQuote
	missing []string
	err     error
}

func (client fakeMarketDataClient) Latest(
	_ context.Context, _ []string,
) (map[string]MarketQuote, []string, error) {
	return client.quotes, client.missing, client.err
}

func TestPortfolioValuationUsesExactDecimals(t *testing.T) {
	repository := newFakeOrderRepository()
	repository.portfolio = &Portfolio{
		UserID: testUserID, Currency: "USD", CashBalance: "505.00000000",
		RealizedPnL: "0.00000000",
		Positions: []Position{{
			Symbol: "AAPL", Quantity: "5.00000000", CostBasis: "495.00000000",
			AverageCost: "99.00000000", RealizedPnL: "0.00000000",
		}},
	}
	asOf := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	valuation, err := NewOrderService(repository).ValuePortfolio(
		context.Background(),
		testUserID,
		fakeMarketDataClient{quotes: map[string]MarketQuote{
			"AAPL": {
				Symbol: "AAPL", Last: "104.15", Currency: "USD", AsOf: asOf,
			},
		}},
	)
	if err != nil {
		t.Fatalf("value portfolio: %v", err)
	}
	if valuation.PositionsMarketValue != "520.75000000" ||
		valuation.UnrealizedPnL != "25.75000000" ||
		valuation.TotalEquity != "1025.75000000" {
		t.Fatalf("unexpected valuation: %#v", valuation)
	}
	if !valuation.Complete {
		t.Fatal("expected complete valuation")
	}
}

func TestPortfolioValuationDisclosesStaleAndMissingPrices(t *testing.T) {
	repository := newFakeOrderRepository()
	repository.portfolio = &Portfolio{
		UserID: testUserID, Currency: "USD", CashBalance: "100.00000000",
		RealizedPnL: "0.00000000",
		Positions: []Position{
			{Symbol: "AAPL", Quantity: "1", CostBasis: "90", AverageCost: "90"},
			{Symbol: "MSFT", Quantity: "2", CostBasis: "400", AverageCost: "200"},
		},
	}
	valuation, err := NewOrderService(repository).ValuePortfolio(
		context.Background(),
		testUserID,
		fakeMarketDataClient{
			quotes: map[string]MarketQuote{
				"AAPL": {Symbol: "AAPL", Last: "100", Currency: "USD", Stale: true},
			},
			missing: []string{"MSFT"},
		},
	)
	if err != nil {
		t.Fatalf("value portfolio: %v", err)
	}
	if valuation.Complete || len(valuation.StaleSymbols) != 1 || len(valuation.MissingSymbols) != 1 {
		t.Fatalf("expected incomplete disclosed valuation: %#v", valuation)
	}
	if valuation.Positions[1].MarketValue != nil {
		t.Fatal("missing quote must not produce a market value")
	}
}
