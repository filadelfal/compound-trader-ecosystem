package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func orderTestMux(repository OrderRepository) *http.ServeMux {
	mux := http.NewServeMux()
	service := NewOrderService(repository)
	service.EnableMarketPretrade(fakeMarketDataClient{quotes: map[string]MarketQuote{
		"AAPL": {Symbol: "AAPL", Bid: "99.90", Ask: "100.10", Last: "100", Currency: "USD"},
	}})
	registerOrderRoutes(mux, service)
	return mux
}

func TestCreateOrderEndpoint(t *testing.T) {
	repository := newFakeOrderRepository()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(`{
		"symbol":"AAPL",
		"side":"buy",
		"type":"market",
		"quantity":"3"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", testUserID)
	request.Header.Set("Idempotency-Key", "http-create-1")
	response := httptest.NewRecorder()

	orderTestMux(repository).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Location") != "/api/v1/orders/"+testOrderID {
		t.Fatalf("unexpected Location header: %q", response.Header().Get("Location"))
	}
	var order Order
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if order.ClientOrderID != "http-create-1" || order.Status != "pending" {
		t.Fatalf("unexpected order: %#v", order)
	}
}

func TestCreateOrderIdempotentReplayAndConflict(t *testing.T) {
	repository := newFakeOrderRepository()
	mux := orderTestMux(repository)
	create := func(quantity string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost,"/api/v1/orders",strings.NewReader(`{"symbol":"AAPL","side":"buy","type":"limit","quantity":"`+quantity+`","limitPrice":"100"}`))
		request.Header.Set("X-User-ID",testUserID); request.Header.Set("Idempotency-Key","same-key")
		response := httptest.NewRecorder(); mux.ServeHTTP(response,request); return response
	}
	if response := create("1"); response.Code != http.StatusCreated { t.Fatalf("first create: %d %s",response.Code,response.Body.String()) }
	if response := create("1.00000000"); response.Code != http.StatusOK || response.Header().Get("Idempotent-Replayed") != "true" { t.Fatalf("replay: %d headers=%v body=%s",response.Code,response.Header(),response.Body.String()) }
	if response := create("2"); response.Code != http.StatusConflict { t.Fatalf("changed replay: %d %s",response.Code,response.Body.String()) }
}

func TestOrderEventsEndpoint(t *testing.T) {
	repository := newFakeOrderRepository()
	_, _ = NewOrderService(repository).Create(context.Background(),CreateOrderInput{UserID:testUserID,ClientOrderID:"events",Symbol:"AAPL",Side:"sell",Type:"market",Quantity:"1"})
	request := httptest.NewRequest(http.MethodGet,"/api/v1/orders/"+testOrderID+"/events",nil); request.Header.Set("X-User-ID",testUserID)
	response := httptest.NewRecorder(); orderTestMux(repository).ServeHTTP(response,request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(),`"eventType":"created"`) { t.Fatalf("events: %d %s",response.Code,response.Body.String()) }
}

func TestOrderEndpointsRequireAuthenticatedUser(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
	response := httptest.NewRecorder()

	orderTestMux(newFakeOrderRepository()).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestCancelOrderEndpointReturnsLifecycleConflict(t *testing.T) {
	repository := newFakeOrderRepository()
	repository.orders[testOrderID] = Order{
		ID: testOrderID, UserID: testUserID, Status: "filled",
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+testOrderID+"/cancel", nil)
	request.Header.Set("X-User-ID", testUserID)
	response := httptest.NewRecorder()

	orderTestMux(repository).ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", response.Code, response.Body.String())
	}
}

func TestGetOrderHidesOtherUsersOrders(t *testing.T) {
	repository := newFakeOrderRepository()
	repository.orders[testOrderID] = Order{ID: testOrderID, UserID: testUserID, Status: "pending"}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/orders/"+testOrderID, nil)
	request.Header.Set("X-User-ID", "33333333-3333-4333-8333-333333333333")
	response := httptest.NewRecorder()

	orderTestMux(repository).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
}
