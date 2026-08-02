package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func portfolioTestMux(repository OrderRepository, key string) *http.ServeMux {
	mux := http.NewServeMux()
	registerPortfolioRoutes(mux, NewOrderService(repository), key)
	return mux
}

func TestPortfolioEndpointIsOwnerAuthenticated(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio", nil)
	response := httptest.NewRecorder()
	portfolioTestMux(newFakeOrderRepository(), "secret").ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/portfolio", nil)
	request.Header.Set("X-User-ID", testUserID)
	response = httptest.NewRecorder()
	portfolioTestMux(newFakeOrderRepository(), "secret").ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestCashAdjustmentEndpointRequiresSettlementCredential(t *testing.T) {
	body := `{"externalId":"wire-1","amount":"1000","reason":"Initial funding"}`
	request := httptest.NewRequest(http.MethodPost,
		"/internal/v1/accounts/"+testUserID+"/cash-adjustments", strings.NewReader(body))
	response := httptest.NewRecorder()
	portfolioTestMux(newFakeOrderRepository(), "secret").ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost,
		"/internal/v1/accounts/"+testUserID+"/cash-adjustments", strings.NewReader(body))
	request.Header.Set("X-Execution-Key", "secret")
	response = httptest.NewRecorder()
	portfolioTestMux(newFakeOrderRepository(), "secret").ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
}

func TestCashAdjustmentValidationRejectsZero(t *testing.T) {
	_, err := NewOrderService(newFakeOrderRepository()).AdjustCash(
		context.Background(),
		CashAdjustmentInput{
			UserID: testUserID, ExternalID: "zero", Amount: "0", Reason: "invalid",
		},
	)
	if err == nil {
		t.Fatal("expected zero adjustment to be rejected")
	}
}
