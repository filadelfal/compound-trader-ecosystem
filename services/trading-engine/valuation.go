package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type MarketQuote struct {
	Symbol     string    `json:"symbol"`
	Bid        string    `json:"bid"`
	Ask        string    `json:"ask"`
	Last       string    `json:"last"`
	Currency   string    `json:"currency"`
	AsOf       time.Time `json:"asOf"`
	AgeSeconds int       `json:"ageSeconds"`
	Stale      bool      `json:"stale"`
}

type MarketDataClient interface {
	Latest(context.Context, []string) (map[string]MarketQuote, []string, error)
}

type PositionValuation struct {
	Position
	MarketPrice   *string    `json:"marketPrice,omitempty"`
	MarketValue   *string    `json:"marketValue,omitempty"`
	UnrealizedPnL *string    `json:"unrealizedPnl,omitempty"`
	QuoteAsOf     *time.Time `json:"quoteAsOf,omitempty"`
	QuoteStale    bool       `json:"quoteStale"`
}

type PortfolioValuation struct {
	UserID               string              `json:"userId"`
	AccountMode          string              `json:"accountMode"`
	Currency             string              `json:"currency"`
	CashBalance          string              `json:"cashBalance"`
	ReservedCash         string              `json:"reservedCash"`
	BuyingPower          string              `json:"buyingPower"`
	PositionsMarketValue string              `json:"positionsMarketValue"`
	TotalEquity          string              `json:"totalEquity"`
	UnrealizedPnL        string              `json:"unrealizedPnl"`
	RealizedPnL          string              `json:"realizedPnl"`
	Complete             bool                `json:"complete"`
	MissingSymbols       []string            `json:"missingSymbols"`
	StaleSymbols         []string            `json:"staleSymbols"`
	Positions            []PositionValuation `json:"positions"`
}

type HTTPMarketDataClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPMarketDataClient(baseURL string, timeout time.Duration) *HTTPMarketDataClient {
	return &HTTPMarketDataClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: timeout},
	}
}

func (client *HTTPMarketDataClient) Latest(
	ctx context.Context, symbols []string,
) (map[string]MarketQuote, []string, error) {
	if len(symbols) == 0 {
		return map[string]MarketQuote{}, []string{}, nil
	}
	endpoint := client.baseURL + "/api/v1/quotes?symbols=" + url.QueryEscape(strings.Join(symbols, ","))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("market-data returned status %d", response.StatusCode)
	}
	var payload struct {
		Quotes         []MarketQuote `json:"quotes"`
		MissingSymbols []string      `json:"missingSymbols"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, nil, err
	}
	quotes := make(map[string]MarketQuote, len(payload.Quotes))
	for _, quote := range payload.Quotes {
		quotes[quote.Symbol] = quote
	}
	return quotes, payload.MissingSymbols, nil
}

func (service *OrderService) ValuePortfolio(
	ctx context.Context, userID string, marketData MarketDataClient,
) (PortfolioValuation, error) {
	portfolio, err := service.GetPortfolio(ctx, userID)
	if err != nil {
		return PortfolioValuation{}, err
	}
	symbols := make([]string, 0, len(portfolio.Positions))
	for _, position := range portfolio.Positions {
		symbols = append(symbols, position.Symbol)
	}
	quotes, missing, err := marketData.Latest(ctx, symbols)
	if err != nil {
		return PortfolioValuation{}, err
	}
	cash, err := signedOrZeroDecimalRat(portfolio.CashBalance)
	if err != nil {
		return PortfolioValuation{}, err
	}
	totalMarket := new(big.Rat)
	totalUnrealized := new(big.Rat)
	stale := make([]string, 0)
	valuations := make([]PositionValuation, 0, len(portfolio.Positions))
	for _, position := range portfolio.Positions {
		valuation := PositionValuation{Position: position}
		quote, found := quotes[position.Symbol]
		if !found {
			valuations = append(valuations, valuation)
			continue
		}
		if quote.Currency != portfolio.Currency {
			return PortfolioValuation{}, errors.New("cross-currency valuation is not configured")
		}
		quantity, _ := signedOrZeroDecimalRat(position.Quantity)
		price, parseErr := decimalRat(quote.Last)
		if parseErr != nil {
			return PortfolioValuation{}, parseErr
		}
		cost, _ := signedOrZeroDecimalRat(position.CostBasis)
		marketValue := new(big.Rat).Mul(quantity, price)
		unrealized := new(big.Rat).Sub(marketValue, cost)
		marketValueText := marketValue.FloatString(8)
		unrealizedText := unrealized.FloatString(8)
		valuation.MarketPrice = &quote.Last
		valuation.MarketValue = &marketValueText
		valuation.UnrealizedPnL = &unrealizedText
		valuation.QuoteAsOf = &quote.AsOf
		valuation.QuoteStale = quote.Stale
		if quote.Stale {
			stale = append(stale, position.Symbol)
		}
		totalMarket.Add(totalMarket, marketValue)
		totalUnrealized.Add(totalUnrealized, unrealized)
		valuations = append(valuations, valuation)
	}
	totalEquity := new(big.Rat).Add(cash, totalMarket)
	return PortfolioValuation{
		UserID: portfolio.UserID, AccountMode: portfolio.AccountMode, Currency: portfolio.Currency,
		CashBalance: portfolio.CashBalance, ReservedCash: portfolio.ReservedCash,
		BuyingPower: portfolio.BuyingPower, PositionsMarketValue: totalMarket.FloatString(8),
		TotalEquity: totalEquity.FloatString(8), UnrealizedPnL: totalUnrealized.FloatString(8),
		RealizedPnL: portfolio.RealizedPnL, Complete: len(missing) == 0 && len(stale) == 0,
		MissingSymbols: missing, StaleSymbols: stale, Positions: valuations,
	}, nil
}
