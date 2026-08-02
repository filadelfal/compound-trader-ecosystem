package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	errOrderNotFillable  = errors.New("order is not fillable")
	errOrderOverfill     = errors.New("fill quantity exceeds the remaining order quantity")
	errLimitPrice        = errors.New("fill price violates the order limit price")
	errExecutionConflict = errors.New("execution id was already used with different fill data")
)

type Fill struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"orderId"`
	ExecutionID string    `json:"executionId"`
	Quantity    string    `json:"quantity"`
	Price       string    `json:"price"`
	ExecutedAt  time.Time `json:"executedAt"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ApplyFillInput struct {
	OrderID     string
	ExecutionID string
	Quantity    string
	Price       string
	ExecutedAt  time.Time
}

type FillResult struct {
	Order   Order `json:"order"`
	Fill    Fill  `json:"fill"`
	Created bool  `json:"created"`
}

func (service *OrderService) ApplyFill(ctx context.Context, input ApplyFillInput) (FillResult, error) {
	input.OrderID = strings.ToLower(strings.TrimSpace(input.OrderID))
	input.ExecutionID = strings.TrimSpace(input.ExecutionID)
	input.Quantity = strings.TrimSpace(input.Quantity)
	input.Price = strings.TrimSpace(input.Price)

	if !uuidPattern.MatchString(input.OrderID) {
		return FillResult{}, errors.New("order id must be a canonical UUID")
	}
	if input.ExecutionID == "" || len(input.ExecutionID) > 100 {
		return FillResult{}, errors.New("execution id is required and must not exceed 100 characters")
	}
	if !positiveDecimal(input.Quantity) {
		return FillResult{}, errors.New("fill quantity must be a positive decimal with at most 8 fractional digits")
	}
	if !positiveDecimal(input.Price) {
		return FillResult{}, errors.New("fill price must be a positive decimal with at most 8 fractional digits")
	}
	if input.ExecutedAt.IsZero() {
		input.ExecutedAt = time.Now().UTC()
	}
	return service.repository.ApplyFill(ctx, input)
}

func (service *OrderService) ListFills(ctx context.Context, userID, orderID string) ([]Fill, error) {
	if err := validateIdentifiers(userID, orderID); err != nil {
		return nil, err
	}
	return service.repository.ListFills(ctx, strings.ToLower(userID), strings.ToLower(orderID))
}
