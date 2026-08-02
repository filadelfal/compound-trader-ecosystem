package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var (
	errOrderNotFound      = errors.New("order not found")
	errOrderNotCancelable = errors.New("order is not cancelable")
	errIdempotencyConflict = errors.New("idempotency key was already used for a different order")
	decimalPattern        = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,8})?$`)
	uuidPattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type Order struct {
	ID               string     `json:"id"`
	UserID           string     `json:"userId"`
	ClientOrderID    string     `json:"clientOrderId"`
	Symbol           string     `json:"symbol"`
	Side             string     `json:"side"`
	Type             string     `json:"type"`
	Quantity         string     `json:"quantity"`
	LimitPrice       *string    `json:"limitPrice,omitempty"`
	Status           string     `json:"status"`
	FilledQuantity   string     `json:"filledQuantity"`
	AverageFillPrice *string    `json:"averageFillPrice,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	CanceledAt       *time.Time `json:"canceledAt,omitempty"`
	RejectionReason  *string    `json:"rejectionReason,omitempty"`
	RejectedAt       *time.Time `json:"rejectedAt,omitempty"`
	IdempotentReplay bool       `json:"-"`
}

type CreateOrderInput struct {
	UserID        string
	ClientOrderID string
	Symbol        string
	Side          string
	Type          string
	Quantity      string
	LimitPrice    *string
	ReservationType   string
	ReservationAsset  string
	ReservationAmount string
}

type OrderRepository interface {
	Create(context.Context, CreateOrderInput) (Order, error)
	FindByClientOrderID(context.Context, string, string) (Order, error)
	Get(context.Context, string, string) (Order, error)
	List(context.Context, string, int) ([]Order, error)
	Cancel(context.Context, string, string) (Order, error)
	ApplyFill(context.Context, ApplyFillInput) (FillResult, error)
	ListFills(context.Context, string, string) ([]Fill, error)
	GetPortfolio(context.Context, string) (Portfolio, error)
	ListLedger(context.Context, string, int) ([]LedgerEntry, error)
	AdjustCash(context.Context, CashAdjustmentInput) (CashAdjustmentResult, error)
	ListEvents(context.Context, string, string, int) ([]OrderEvent, error)
}

type OrderService struct {
	repository OrderRepository
	risk       RiskPolicy
	marketData MarketDataClient
}

func NewOrderService(repository OrderRepository, policies ...RiskPolicy) *OrderService {
	policy := DefaultRiskPolicy()
	if len(policies) > 0 {
		policy = policies[0]
	}
	return &OrderService{repository: repository, risk: policy}
}

func (service *OrderService) EnableMarketPretrade(marketData MarketDataClient) {
	service.marketData = marketData
}

func (service *OrderService) Create(ctx context.Context, input CreateOrderInput) (Order, error) {
	input.UserID = strings.ToLower(strings.TrimSpace(input.UserID))
	input.ClientOrderID = strings.TrimSpace(input.ClientOrderID)
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Side = strings.ToLower(strings.TrimSpace(input.Side))
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Quantity = strings.TrimSpace(input.Quantity)

	if !uuidPattern.MatchString(input.UserID) {
		return Order{}, errors.New("user id must be a canonical UUID")
	}
	if input.ClientOrderID == "" || len(input.ClientOrderID) > 100 {
		return Order{}, errors.New("client order id is required and must not exceed 100 characters")
	}
	if input.Symbol == "" || len(input.Symbol) > 20 {
		return Order{}, errors.New("symbol is required and must not exceed 20 characters")
	}
	if input.Side != "buy" && input.Side != "sell" {
		return Order{}, errors.New("side must be buy or sell")
	}
	if input.Type != "market" && input.Type != "limit" {
		return Order{}, errors.New("type must be market or limit")
	}
	if !positiveDecimal(input.Quantity) {
		return Order{}, errors.New("quantity must be a positive decimal with at most 8 fractional digits")
	}
	if input.Type == "limit" {
		if input.LimitPrice == nil {
			return Order{}, errors.New("limitPrice is required for limit orders")
		}
		price := strings.TrimSpace(*input.LimitPrice)
		if !positiveDecimal(price) {
			return Order{}, errors.New("limitPrice must be a positive decimal with at most 8 fractional digits")
		}
		input.LimitPrice = &price
	} else if input.LimitPrice != nil {
		return Order{}, errors.New("limitPrice is not allowed for market orders")
	}
	if err := service.risk.Validate(input); err != nil {
		return Order{}, err
	}
	if existing, err := service.repository.FindByClientOrderID(ctx,input.UserID,input.ClientOrderID); err == nil {
		if !sameOrderEconomics(existing,input) { return Order{}, errIdempotencyConflict }
		existing.IdempotentReplay = true
		return existing,nil
	} else if !errors.Is(err,errOrderNotFound) { return Order{},err }
	if err := service.prepareReservation(ctx, &input); err != nil {
		return Order{}, err
	}

	return service.repository.Create(ctx, input)
}

func (service *OrderService) prepareReservation(ctx context.Context, input *CreateOrderInput) error {
	if input.Side == "sell" {
		input.ReservationType, input.ReservationAsset, input.ReservationAmount = "security", input.Symbol, input.Quantity
		return nil
	}
	quantity, _ := decimalRat(input.Quantity)
	var price string
	if input.Type == "limit" {
		price = *input.LimitPrice
	} else {
		if service.marketData == nil { return errors.New("market orders require configured market data") }
		quotes, missing, err := service.marketData.Latest(ctx, []string{input.Symbol})
		if err != nil { return fmt.Errorf("market quote unavailable: %w", err) }
		quote, found := quotes[input.Symbol]
		if !found || len(missing) > 0 { return errors.New("market quote is missing") }
		if quote.Stale { return errors.New("market quote is stale") }
		if quote.Currency != "USD" { return errors.New("market quote currency must be USD") }
		if !positiveDecimal(quote.Ask) { return errors.New("market quote ask is invalid") }
		price = quote.Ask
	}
	priceValue, _ := decimalRat(price)
	input.ReservationType, input.ReservationAsset = "cash", "USD"
	input.ReservationAmount = new(big.Rat).Mul(quantity, priceValue).FloatString(8)
	return nil
}

func (service *OrderService) Get(ctx context.Context, userID, orderID string) (Order, error) {
	if err := validateIdentifiers(userID, orderID); err != nil {
		return Order{}, err
	}
	return service.repository.Get(ctx, strings.ToLower(userID), strings.ToLower(orderID))
}

func (service *OrderService) List(ctx context.Context, userID string, limit int) ([]Order, error) {
	if !uuidPattern.MatchString(strings.ToLower(strings.TrimSpace(userID))) {
		return nil, errors.New("user id must be a canonical UUID")
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	return service.repository.List(ctx, strings.ToLower(userID), limit)
}

func (service *OrderService) Cancel(ctx context.Context, userID, orderID string) (Order, error) {
	if err := validateIdentifiers(userID, orderID); err != nil {
		return Order{}, err
	}
	return service.repository.Cancel(ctx, strings.ToLower(userID), strings.ToLower(orderID))
}

func validateIdentifiers(userID, orderID string) error {
	if !uuidPattern.MatchString(strings.ToLower(strings.TrimSpace(userID))) {
		return errors.New("user id must be a canonical UUID")
	}
	if !uuidPattern.MatchString(strings.ToLower(strings.TrimSpace(orderID))) {
		return errors.New("order id must be a canonical UUID")
	}
	return nil
}

func positiveDecimal(value string) bool {
	if !decimalPattern.MatchString(value) {
		return false
	}
	for _, char := range value {
		if char >= '1' && char <= '9' {
			return true
		}
	}
	return false
}

func duplicateOrderError(clientOrderID string) error {
	return fmt.Errorf("client order id %q already exists", clientOrderID)
}
