package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func executionTestMux(repository OrderRepository, key string) *http.ServeMux {
	mux := http.NewServeMux()
	registerExecutionRoutes(mux, NewOrderService(repository), key)
	return mux
}

func TestExecutionEndpointRequiresServiceCredential(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/orders/"+testOrderID+"/fills",
		strings.NewReader(`{"executionId":"one","quantity":"1","price":"10"}`))
	response := httptest.NewRecorder()

	executionTestMux(newFakeOrderRepository(), "secret").ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestExecutionEndpointAppliesFillAndSupportsRetry(t *testing.T) {
	repository := newFakeOrderRepository()
	repository.orders[testOrderID] = Order{
		ID: testOrderID, UserID: testUserID, Symbol: "AAPL", Side: "buy",
		Type: "market", Quantity: "2", FilledQuantity: "0.00000000", Status: "pending",
	}
	body := `{"executionId":"venue-123","quantity":"2","price":"99.25"}`
	mux := executionTestMux(repository, "secret")

	first := httptest.NewRequest(http.MethodPost, "/internal/v1/orders/"+testOrderID+"/fills", strings.NewReader(body))
	first.Header.Set("X-Execution-Key", "secret")
	firstResponse := httptest.NewRecorder()
	mux.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", firstResponse.Code, firstResponse.Body.String())
	}

	retry := httptest.NewRequest(http.MethodPost, "/internal/v1/orders/"+testOrderID+"/fills", strings.NewReader(body))
	retry.Header.Set("X-Execution-Key", "secret")
	retryResponse := httptest.NewRecorder()
	mux.ServeHTTP(retryResponse, retry)
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("expected idempotent 200, got %d: %s", retryResponse.Code, retryResponse.Body.String())
	}
}
