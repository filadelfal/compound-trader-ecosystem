package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakePaperAccountRepository struct { account *PaperAccount; activations int }
func (repository *fakePaperAccountRepository) ActivatePaperAccount(_ context.Context, userID, startingCash string) (PaperAccount, bool, error) {
	if repository.account != nil { return *repository.account, false, nil }
	repository.activations++
	account := PaperAccount{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", UserID: userID, Currency: "USD", StartingCash: startingCash, Status: "active", ActivatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	repository.account = &account
	return account, true, nil
}
func (repository *fakePaperAccountRepository) GetPaperAccount(_ context.Context, _ string) (PaperAccount, error) {
	if repository.account == nil { return PaperAccount{}, errPaperAccountNotFound }
	return *repository.account, nil
}

func TestPaperAccountActivationIsReplaySafe(t *testing.T) {
	accounts := &fakePaperAccountRepository{}
	portfolios := newFakeOrderRepository()
	portfolios.portfolio = &Portfolio{UserID: testUserID, AccountMode: "paper", Currency: "USD", CashBalance: "100000.00000000", ReservedCash: "0", BuyingPower: "100000.00000000", Positions: []Position{}}
	service, err := NewPaperAccountService(accounts, portfolios, "100000")
	if err != nil { t.Fatalf("new service: %v", err) }
	first, err := service.Activate(context.Background(), testUserID)
	if err != nil || !first.Created { t.Fatalf("first activation: %#v %v", first, err) }
	second, err := service.Activate(context.Background(), testUserID)
	if err != nil || second.Created || accounts.activations != 1 { t.Fatalf("replayed activation: %#v %v count=%d", second, err, accounts.activations) }
}

func TestPaperAccountRoutesRequireIdentityAndRespectFeatureFlag(t *testing.T) {
	accounts := &fakePaperAccountRepository{}
	portfolios := newFakeOrderRepository()
	portfolios.portfolio = &Portfolio{UserID: testUserID, AccountMode: "paper", Currency: "USD", CashBalance: "100000", Positions: []Position{}}
	service, _ := NewPaperAccountService(accounts, portfolios, "100000")

	disabledMux := http.NewServeMux(); registerPaperAccountRoutes(disabledMux, service, false)
	disabled := httptest.NewRecorder(); disabledMux.ServeHTTP(disabled, httptest.NewRequest(http.MethodPost, "/api/v1/paper-account", nil))
	if disabled.Code != http.StatusNotFound { t.Fatalf("expected disabled 404, got %d", disabled.Code) }

	mux := http.NewServeMux(); registerPaperAccountRoutes(mux, service, true)
	unauthorized := httptest.NewRecorder(); mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/paper-account", nil))
	if unauthorized.Code != http.StatusUnauthorized { t.Fatalf("expected 401, got %d", unauthorized.Code) }
	request := httptest.NewRequest(http.MethodPost, "/api/v1/paper-account", nil); request.Header.Set("X-User-ID", testUserID)
	created := httptest.NewRecorder(); mux.ServeHTTP(created, request)
	if created.Code != http.StatusCreated { t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String()) }
}
