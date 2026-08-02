package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	errInsufficientFunds    = errors.New("insufficient settled cash for fill")
	errInsufficientPosition = errors.New("insufficient position and short selling is disabled")
	errInsufficientBuyingPower = errors.New("insufficient available buying power")
	errInsufficientAvailablePosition = errors.New("insufficient available position")
	errAdjustmentConflict   = errors.New("external adjustment id was already used with different data")
)

type Position struct {
	Symbol            string `json:"symbol"`
	Quantity          string `json:"quantity"`
	ReservedQuantity  string `json:"reservedQuantity"`
	AvailableQuantity string `json:"availableQuantity"`
	CostBasis         string `json:"costBasis"`
	AverageCost       string `json:"averageCost"`
	RealizedPnL       string `json:"realizedPnl"`
}

type Portfolio struct {
	UserID      string     `json:"userId"`
	AccountMode string     `json:"accountMode"`
	Currency    string     `json:"currency"`
	CashBalance string     `json:"cashBalance"`
	ReservedCash string    `json:"reservedCash"`
	BuyingPower  string    `json:"buyingPower"`
	RealizedPnL string     `json:"realizedPnl"`
	Positions   []Position `json:"positions"`
}

type LedgerEntry struct {
	ID            string    `json:"id"`
	UserID        string    `json:"userId"`
	SourceType    string    `json:"sourceType"`
	SourceID      string    `json:"sourceId"`
	LegType       string    `json:"legType"`
	Asset         string    `json:"asset"`
	QuantityDelta string    `json:"quantityDelta"`
	ValueDelta    string    `json:"valueDelta"`
	CostDelta     string    `json:"costDelta"`
	RealizedPnL   string    `json:"realizedPnl"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"createdAt"`
}

type CashAdjustmentInput struct {
	UserID     string
	ExternalID string
	Amount     string
	Reason     string
}

type CashAdjustmentResult struct {
	Portfolio Portfolio     `json:"portfolio"`
	Entries   []LedgerEntry `json:"entries"`
	Created   bool          `json:"created"`
}

func (service *OrderService) GetPortfolio(ctx context.Context, userID string) (Portfolio, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if !uuidPattern.MatchString(userID) {
		return Portfolio{}, errors.New("user id must be a canonical UUID")
	}
	return service.repository.GetPortfolio(ctx, userID)
}

func (service *OrderService) ListLedger(ctx context.Context, userID string, limit int) ([]LedgerEntry, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if !uuidPattern.MatchString(userID) {
		return nil, errors.New("user id must be a canonical UUID")
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("limit must be between 1 and 500")
	}
	return service.repository.ListLedger(ctx, userID, limit)
}

func (service *OrderService) AdjustCash(ctx context.Context, input CashAdjustmentInput) (CashAdjustmentResult, error) {
	input.UserID = strings.ToLower(strings.TrimSpace(input.UserID))
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.Amount = strings.TrimSpace(input.Amount)
	input.Reason = strings.TrimSpace(input.Reason)
	if !uuidPattern.MatchString(input.UserID) {
		return CashAdjustmentResult{}, errors.New("user id must be a canonical UUID")
	}
	if input.ExternalID == "" || len(input.ExternalID) > 100 {
		return CashAdjustmentResult{}, errors.New("external id is required and must not exceed 100 characters")
	}
	if input.Reason == "" || len(input.Reason) > 250 {
		return CashAdjustmentResult{}, errors.New("reason is required and must not exceed 250 characters")
	}
	if _, err := signedDecimalRat(input.Amount); err != nil {
		return CashAdjustmentResult{}, errors.New("amount must be a non-zero decimal with at most 8 fractional digits")
	}
	return service.repository.AdjustCash(ctx, input)
}
