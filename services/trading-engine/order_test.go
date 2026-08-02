package main

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

const (
	testUserID  = "11111111-1111-4111-8111-111111111111"
	testOrderID = "22222222-2222-4222-8222-222222222222"
)

type fakeOrderRepository struct {
	orders      map[string]Order
	fills       map[string][]Fill
	createInput CreateOrderInput
	portfolio   *Portfolio
}

func newFakeOrderRepository() *fakeOrderRepository {
	return &fakeOrderRepository{orders: make(map[string]Order), fills: make(map[string][]Fill)}
}

func (repository *fakeOrderRepository) Create(_ context.Context, input CreateOrderInput) (Order, error) {
	repository.createInput = input
	for _, existing := range repository.orders {
		if existing.UserID == input.UserID && existing.ClientOrderID == input.ClientOrderID {
			if !sameOrderEconomics(existing, input) { return Order{}, errIdempotencyConflict }
			existing.IdempotentReplay = true
			return existing, nil
		}
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	order := Order{
		ID: testOrderID, UserID: input.UserID, ClientOrderID: input.ClientOrderID,
		Symbol: input.Symbol, Side: input.Side, Type: input.Type,
		Quantity: input.Quantity, LimitPrice: input.LimitPrice, Status: "pending",
		FilledQuantity: "0.00000000",
		CreatedAt:      now, UpdatedAt: now,
	}
	repository.orders[order.ID] = order
	return order, nil
}

func (repository *fakeOrderRepository) ApplyFill(_ context.Context, input ApplyFillInput) (FillResult, error) {
	order, exists := repository.orders[input.OrderID]
	if !exists {
		return FillResult{}, errOrderNotFound
	}
	for _, fill := range repository.fills[input.OrderID] {
		if fill.ExecutionID == input.ExecutionID {
			existingQuantity, _ := decimalRat(fill.Quantity)
			inputQuantity, _ := decimalRat(input.Quantity)
			existingPrice, _ := decimalRat(fill.Price)
			inputPrice, _ := decimalRat(input.Price)
			if existingQuantity.Cmp(inputQuantity) != 0 || existingPrice.Cmp(inputPrice) != 0 {
				return FillResult{}, errExecutionConflict
			}
			return FillResult{Order: order, Fill: fill, Created: false}, nil
		}
	}
	if order.Status != "pending" && order.Status != "partially_filled" {
		return FillResult{}, errOrderNotFillable
	}
	fillQuantity, _ := decimalRat(input.Quantity)
	orderQuantity, _ := decimalRat(order.Quantity)
	filledRaw := order.FilledQuantity
	if filledRaw == "" {
		filledRaw = "0"
	}
	filledQuantity, _ := decimalRatAllowZero(filledRaw)
	total := new(big.Rat).Add(filledQuantity, fillQuantity)
	if total.Cmp(orderQuantity) > 0 {
		return FillResult{}, errOrderOverfill
	}
	if order.LimitPrice != nil {
		fillPrice, _ := decimalRat(input.Price)
		limitPrice, _ := decimalRat(*order.LimitPrice)
		if (order.Side == "buy" && fillPrice.Cmp(limitPrice) > 0) ||
			(order.Side == "sell" && fillPrice.Cmp(limitPrice) < 0) {
			return FillResult{}, errLimitPrice
		}
	}
	fill := Fill{
		ID: "44444444-4444-4444-8444-444444444444", OrderID: input.OrderID,
		ExecutionID: input.ExecutionID, Quantity: input.Quantity, Price: input.Price,
		ExecutedAt: input.ExecutedAt, CreatedAt: input.ExecutedAt,
	}
	repository.fills[input.OrderID] = append(repository.fills[input.OrderID], fill)
	order.FilledQuantity = total.FloatString(8)
	if total.Cmp(orderQuantity) == 0 {
		order.Status = "filled"
	} else {
		order.Status = "partially_filled"
	}
	order.AverageFillPrice = &input.Price
	repository.orders[input.OrderID] = order
	return FillResult{Order: order, Fill: fill, Created: true}, nil
}

func (repository *fakeOrderRepository) ListFills(_ context.Context, userID, orderID string) ([]Fill, error) {
	order, exists := repository.orders[orderID]
	if !exists || order.UserID != userID {
		return nil, errOrderNotFound
	}
	return repository.fills[orderID], nil
}

func (repository *fakeOrderRepository) ListEvents(_ context.Context, userID, orderID string, _ int) ([]OrderEvent, error) {
	order, exists := repository.orders[orderID]
	if !exists || order.UserID != userID { return nil, errOrderNotFound }
	return []OrderEvent{{ID: 1, OrderID: orderID, EventType: "created", Payload: []byte(`{"status":"pending"}`), CreatedAt: order.CreatedAt}}, nil
}

func (repository *fakeOrderRepository) GetPortfolio(_ context.Context, userID string) (Portfolio, error) {
	if repository.portfolio != nil {
		return *repository.portfolio, nil
	}
	return Portfolio{UserID: userID, Currency: "USD", CashBalance: "0.00000000", Positions: []Position{}}, nil
}

func (repository *fakeOrderRepository) ListLedger(_ context.Context, _ string, _ int) ([]LedgerEntry, error) {
	return []LedgerEntry{}, nil
}

func (repository *fakeOrderRepository) AdjustCash(_ context.Context, input CashAdjustmentInput) (CashAdjustmentResult, error) {
	return CashAdjustmentResult{
		Portfolio: Portfolio{UserID: input.UserID, Currency: "USD", CashBalance: input.Amount, Positions: []Position{}},
		Created:   true,
	}, nil
}

func (repository *fakeOrderRepository) Get(_ context.Context, userID, orderID string) (Order, error) {
	order, exists := repository.orders[orderID]
	if !exists || order.UserID != userID {
		return Order{}, errOrderNotFound
	}
	return order, nil
}

func (repository *fakeOrderRepository) FindByClientOrderID(_ context.Context,userID,clientOrderID string) (Order,error) {
	for _,order := range repository.orders { if order.UserID==userID && order.ClientOrderID==clientOrderID { return order,nil } }
	return Order{},errOrderNotFound
}

func (repository *fakeOrderRepository) List(_ context.Context, userID string, limit int) ([]Order, error) {
	orders := make([]Order, 0)
	for _, order := range repository.orders {
		if order.UserID == userID {
			orders = append(orders, order)
			if len(orders) == limit {
				break
			}
		}
	}
	return orders, nil
}

func (repository *fakeOrderRepository) Cancel(_ context.Context, userID, orderID string) (Order, error) {
	order, err := repository.Get(context.Background(), userID, orderID)
	if err != nil {
		return Order{}, err
	}
	if order.Status != "pending" {
		return Order{}, errOrderNotCancelable
	}
	now := time.Date(2026, 8, 1, 12, 1, 0, 0, time.UTC)
	order.Status = "canceled"
	order.CanceledAt = &now
	order.UpdatedAt = now
	repository.orders[orderID] = order
	return order, nil
}

func TestCreateLimitOrderNormalizesAndPersists(t *testing.T) {
	repository := newFakeOrderRepository()
	service := NewOrderService(repository)
	price := "182.5000"

	order, err := service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "client-1", Symbol: " aapl ",
		Side: "BUY", Type: "LIMIT", Quantity: "10.25", LimitPrice: &price,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if order.Symbol != "AAPL" || order.Side != "buy" || order.Type != "limit" {
		t.Fatalf("order was not normalized: %#v", order)
	}
	if repository.createInput.Symbol != "AAPL" {
		t.Fatalf("normalized input was not persisted: %#v", repository.createInput)
	}
}

func TestCreateOrderRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name  string
		input CreateOrderInput
	}{
		{
			name: "limit without price",
			input: CreateOrderInput{UserID: testUserID, ClientOrderID: "1", Symbol: "AAPL",
				Side: "buy", Type: "limit", Quantity: "1"},
		},
		{
			name: "market with price",
			input: func() CreateOrderInput {
				price := "100"
				return CreateOrderInput{UserID: testUserID, ClientOrderID: "2", Symbol: "AAPL",
					Side: "buy", Type: "market", Quantity: "1", LimitPrice: &price}
			}(),
		},
		{
			name: "zero quantity",
			input: CreateOrderInput{UserID: testUserID, ClientOrderID: "3", Symbol: "AAPL",
				Side: "buy", Type: "market", Quantity: "0.000"},
		},
		{
			name: "invalid side",
			input: CreateOrderInput{UserID: testUserID, ClientOrderID: "4", Symbol: "AAPL",
				Side: "hold", Type: "market", Quantity: "1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewOrderService(newFakeOrderRepository()).Create(context.Background(), test.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCreateOrderCalculatesConservativeReservation(t *testing.T) {
	repository := newFakeOrderRepository()
	service := NewOrderService(repository)
	service.EnableMarketPretrade(fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "99.90", Ask: "100.25", Last: "100", Currency: "USD"},
	}})
	_, err := service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "reserve-buy", Symbol: "AAPL",
		Side: "buy", Type: "market", Quantity: "2.5",
	})
	if err != nil { t.Fatalf("create market buy: %v", err) }
	if repository.createInput.ReservationType != "cash" || repository.createInput.ReservationAsset != "USD" || repository.createInput.ReservationAmount != "250.62500000" {
		t.Fatalf("unexpected buy reservation: %#v", repository.createInput)
	}

	_, err = service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "reserve-sell", Symbol: "MSFT",
		Side: "sell", Type: "market", Quantity: "3.25",
	})
	if err != nil { t.Fatalf("create market sell: %v", err) }
	if repository.createInput.ReservationType != "security" || repository.createInput.ReservationAsset != "MSFT" || repository.createInput.ReservationAmount != "3.25" {
		t.Fatalf("unexpected sell reservation: %#v", repository.createInput)
	}
}

func TestMarketBuyRequiresFreshQuote(t *testing.T) {
	service := NewOrderService(newFakeOrderRepository())
	service.EnableMarketPretrade(fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "99", Ask: "100", Last: "99.5", Currency: "USD", Stale: true},
	}})
	_, err := service.Create(context.Background(), CreateOrderInput{UserID: testUserID, ClientOrderID: "stale", Symbol: "AAPL", Side: "buy", Type: "market", Quantity: "1"})
	if err == nil || !strings.Contains(err.Error(), "stale") { t.Fatalf("expected stale quote rejection, got %v", err) }
}

func TestMarketOrderReplayDoesNotDependOnLaterQuote(t *testing.T) {
	repository := newFakeOrderRepository()
	quotes := map[string]MarketQuote{"AAPL": {Symbol:"AAPL",Bid:"99",Ask:"100",Last:"99.5",Currency:"USD"}}
	service := NewOrderService(repository); service.EnableMarketPretrade(fakeMarketDataClient{quotes:quotes})
	input := CreateOrderInput{UserID:testUserID,ClientOrderID:"market-replay",Symbol:"AAPL",Side:"buy",Type:"market",Quantity:"1"}
	first, err := service.Create(context.Background(),input); if err != nil { t.Fatalf("first create: %v",err) }
	quotes["AAPL"] = MarketQuote{Symbol:"AAPL",Bid:"99",Ask:"100",Last:"99.5",Currency:"USD",Stale:true}
	replay, err := service.Create(context.Background(),input)
	if err != nil || !replay.IdempotentReplay || replay.ID != first.ID { t.Fatalf("expected quote-independent replay: %#v %v",replay,err) }
}

func TestCancelOrderEnforcesLifecycle(t *testing.T) {
	repository := newFakeOrderRepository()
	service := NewOrderService(repository)
	_, err := service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "cancel-me", Symbol: "MSFT",
		Side: "sell", Type: "market", Quantity: "2",
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	order, err := service.Cancel(context.Background(), testUserID, testOrderID)
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if order.Status != "canceled" || order.CanceledAt == nil {
		t.Fatalf("expected canceled order, got %#v", order)
	}
	if _, err = service.Cancel(context.Background(), testUserID, testOrderID); !errors.Is(err, errOrderNotCancelable) {
		t.Fatalf("expected lifecycle conflict, got %v", err)
	}
}

func TestGetOrderDoesNotCrossOwnerBoundary(t *testing.T) {
	repository := newFakeOrderRepository()
	repository.orders[testOrderID] = Order{ID: testOrderID, UserID: testUserID}
	otherUser := "33333333-3333-4333-8333-333333333333"

	_, err := NewOrderService(repository).Get(context.Background(), otherUser, testOrderID)
	if !errors.Is(err, errOrderNotFound) {
		t.Fatalf("expected not found across owner boundary, got %v", err)
	}
}

func TestRiskPolicyRejectsQuantityAndNotionalBreaches(t *testing.T) {
	service := NewOrderService(newFakeOrderRepository(), RiskPolicy{
		MaxOrderQuantity: "100",
		MaxOrderNotional: "1000",
	})
	if _, err := service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "risk-quantity", Symbol: "AAPL",
		Side: "buy", Type: "market", Quantity: "100.00000001",
	}); err == nil {
		t.Fatal("expected quantity risk rejection")
	}
	price := "10.00000001"
	if _, err := service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "risk-notional", Symbol: "AAPL",
		Side: "buy", Type: "limit", Quantity: "100", LimitPrice: &price,
	}); err == nil {
		t.Fatal("expected notional risk rejection")
	}
}

func TestApplyFillTransitionsAndIsIdempotent(t *testing.T) {
	repository := newFakeOrderRepository()
	service := NewOrderService(repository)
	price := "100"
	_, err := service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "fills", Symbol: "AAPL",
		Side: "buy", Type: "limit", Quantity: "10", LimitPrice: &price,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	input := ApplyFillInput{
		OrderID: testOrderID, ExecutionID: "execution-1", Quantity: "4",
		Price: "99", ExecutedAt: time.Now().UTC(),
	}
	result, err := service.ApplyFill(context.Background(), input)
	if err != nil {
		t.Fatalf("apply fill: %v", err)
	}
	if result.Order.Status != "partially_filled" || result.Order.FilledQuantity != "4.00000000" {
		t.Fatalf("unexpected partial fill: %#v", result.Order)
	}
	retry, err := service.ApplyFill(context.Background(), input)
	if err != nil || retry.Created {
		t.Fatalf("expected idempotent retry, got %#v, %v", retry, err)
	}
	_, err = service.ApplyFill(context.Background(), ApplyFillInput{
		OrderID: testOrderID, ExecutionID: "execution-1", Quantity: "5",
		Price: "99", ExecutedAt: time.Now().UTC(),
	})
	if !errors.Is(err, errExecutionConflict) {
		t.Fatalf("expected conflicting retry rejection, got %v", err)
	}
	final, err := service.ApplyFill(context.Background(), ApplyFillInput{
		OrderID: testOrderID, ExecutionID: "execution-2", Quantity: "6",
		Price: "98", ExecutedAt: time.Now().UTC(),
	})
	if err != nil || final.Order.Status != "filled" {
		t.Fatalf("expected filled order, got %#v, %v", final, err)
	}
}

func TestApplyFillRejectsOverfillAndLimitViolation(t *testing.T) {
	repository := newFakeOrderRepository()
	service := NewOrderService(repository)
	price := "100"
	_, _ = service.Create(context.Background(), CreateOrderInput{
		UserID: testUserID, ClientOrderID: "protected", Symbol: "AAPL",
		Side: "buy", Type: "limit", Quantity: "5", LimitPrice: &price,
	})
	_, err := service.ApplyFill(context.Background(), ApplyFillInput{
		OrderID: testOrderID, ExecutionID: "bad-price", Quantity: "1",
		Price: "101", ExecutedAt: time.Now().UTC(),
	})
	if !errors.Is(err, errLimitPrice) {
		t.Fatalf("expected limit rejection, got %v", err)
	}
	_, err = service.ApplyFill(context.Background(), ApplyFillInput{
		OrderID: testOrderID, ExecutionID: "overfill", Quantity: "6",
		Price: "99", ExecutedAt: time.Now().UTC(),
	})
	if !errors.Is(err, errOrderOverfill) {
		t.Fatalf("expected overfill rejection, got %v", err)
	}
}
