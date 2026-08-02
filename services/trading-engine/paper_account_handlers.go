package main

import (
	"errors"
	"net/http"
)

func registerPaperAccountRoutes(mux *http.ServeMux, service *PaperAccountService, enabled bool) {
	handleDisabled := func(w http.ResponseWriter) bool {
		if enabled { return false }
		errorResponse(w, http.StatusNotFound, "paper_trading_disabled", "paper trading is not enabled")
		return true
	}
	mux.HandleFunc("POST /api/v1/paper-account", func(w http.ResponseWriter, r *http.Request) {
		if handleDisabled(w) { return }
		userID, ok := authenticatedUserID(w, r); if !ok { return }
		result, err := service.Activate(r.Context(), userID)
		if err != nil { errorResponse(w, http.StatusBadRequest, "paper_account_activation_failed", err.Error()); return }
		status := http.StatusOK
		if result.Created { status = http.StatusCreated }
		jsonValueResponse(w, status, result)
	})
	mux.HandleFunc("GET /api/v1/paper-account", func(w http.ResponseWriter, r *http.Request) {
		if handleDisabled(w) { return }
		userID, ok := authenticatedUserID(w, r); if !ok { return }
		result, err := service.Get(r.Context(), userID)
		if errors.Is(err, errPaperAccountNotFound) { errorResponse(w, http.StatusNotFound, "paper_account_not_activated", err.Error()); return }
		if err != nil { errorResponse(w, http.StatusBadRequest, "paper_account_lookup_failed", err.Error()); return }
		jsonValueResponse(w, http.StatusOK, result)
	})
}
