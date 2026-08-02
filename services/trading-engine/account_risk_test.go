package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRiskRepository struct {
	status  RiskStatus
	updates int
}

func (repository *fakeRiskRepository) GetRiskStatus(context.Context, string) (RiskStatus, error) {
	return repository.status, nil
}
func (repository *fakeRiskRepository) UpdateRiskProfile(_ context.Context, userID string, input UpdateRiskProfileInput) (RiskStatus, error) {
	repository.updates++
	repository.status.Profile.UserID = userID
	if input.TradingEnabled != nil {
		repository.status.Profile.TradingEnabled = *input.TradingEnabled
	}
	if input.MaxOpenOrders != nil {
		repository.status.Profile.MaxOpenOrders = *input.MaxOpenOrders
	}
	if input.MaxGrossExposure != nil {
		repository.status.Profile.MaxGrossExposure = *input.MaxGrossExposure
	}
	if input.MaxDailyLoss != nil {
		repository.status.Profile.MaxDailyLoss = *input.MaxDailyLoss
	}
	return repository.status, nil
}

func TestRiskProfileValidationAndUpdate(t *testing.T) {
	repository := &fakeRiskRepository{status: RiskStatus{Profile: RiskProfile{UserID: testUserID, TradingEnabled: true, MaxOpenOrders: 25, MaxGrossExposure: "250000", MaxDailyLoss: "10000", UpdatedAt: time.Now()}}}
	service := NewRiskProfileService(repository)
	disabled := false
	open := 5
	exposure := "50000"
	loss := "1000"
	status, err := service.Update(context.Background(), testUserID, UpdateRiskProfileInput{TradingEnabled: &disabled, MaxOpenOrders: &open, MaxGrossExposure: &exposure, MaxDailyLoss: &loss})
	if err != nil || status.Profile.TradingEnabled || status.Profile.MaxOpenOrders != 5 {
		t.Fatalf("unexpected update: %#v %v", status, err)
	}
	tooMany := 101
	if _, err = service.Update(context.Background(), testUserID, UpdateRiskProfileInput{MaxOpenOrders: &tooMany}); err == nil {
		t.Fatal("expected maximum validation")
	}
	tooHigh := "10000001"
	if _, err = service.Update(context.Background(), testUserID, UpdateRiskProfileInput{MaxGrossExposure: &tooHigh}); err == nil {
		t.Fatal("expected exposure ceiling validation")
	}
}

func TestRiskRoutesRequireIdentityAndRejectEmptyPatch(t *testing.T) {
	repository := &fakeRiskRepository{status: RiskStatus{Profile: RiskProfile{UserID: testUserID, TradingEnabled: true}}}
	mux := http.NewServeMux()
	registerRiskRoutes(mux, NewRiskProfileService(repository))
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/risk", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", unauthorized.Code)
	}
	emptyRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/risk", strings.NewReader(`{}`))
	emptyRequest.Header.Set("X-User-ID", testUserID)
	empty := httptest.NewRecorder()
	mux.ServeHTTP(empty, emptyRequest)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", empty.Code)
	}
	disabled := httptest.NewRequest(http.MethodPatch, "/api/v1/risk", strings.NewReader(`{"tradingEnabled":false}`))
	disabled.Header.Set("X-User-ID", testUserID)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, disabled)
	if response.Code != http.StatusOK || repository.updates != 1 {
		t.Fatalf("unexpected patch: %d %s", response.Code, response.Body.String())
	}
}

func TestDailyLossComparisonUsesExactDecimals(t *testing.T) {
	if !negatedAtLeast("-100.00000000", "100") {
		t.Fatal("expected limit reached")
	}
	if negatedAtLeast("-99.99999999", "100") {
		t.Fatal("limit should not be reached")
	}
}
