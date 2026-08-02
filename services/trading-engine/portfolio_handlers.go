package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type cashAdjustmentRequest struct {
	ExternalID string `json:"externalId"`
	Amount     string `json:"amount"`
	Reason     string `json:"reason"`
}

func registerPortfolioRoutes(
	mux *http.ServeMux, service *OrderService, executionAPIKey string,
	marketDataClients ...MarketDataClient,
) {
	mux.HandleFunc("GET /api/v1/portfolio", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		portfolio, err := service.GetPortfolio(r.Context(), userID)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "portfolio_unavailable", err.Error())
			return
		}
		jsonValueResponse(w, http.StatusOK, portfolio)
	})

	mux.HandleFunc("GET /api/v1/portfolio/ledger", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				errorResponse(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer")
				return
			}
			limit = parsed
		}
		entries, err := service.ListLedger(r.Context(), userID, limit)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		jsonValueResponse(w, http.StatusOK, map[string]any{"entries": entries})
	})

	if len(marketDataClients) > 0 {
		mux.HandleFunc("GET /api/v1/portfolio/valuation", func(w http.ResponseWriter, r *http.Request) {
			userID, ok := authenticatedUserID(w, r)
			if !ok {
				return
			}
			valuation, err := service.ValuePortfolio(r.Context(), userID, marketDataClients[0])
			if err != nil {
				errorResponse(w, http.StatusServiceUnavailable, "valuation_unavailable", err.Error())
				return
			}
			jsonValueResponse(w, http.StatusOK, valuation)
		})
	}

	mux.HandleFunc("POST /internal/v1/accounts/{userID}/cash-adjustments", func(w http.ResponseWriter, r *http.Request) {
		if !validExecutionCredential(r.Header.Get("X-Execution-Key"), executionAPIKey) {
			errorResponse(w, http.StatusUnauthorized, "unauthorized", "valid settlement service credentials are required")
			return
		}
		var request cashAdjustmentRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_request", "request body must be valid adjustment JSON")
			return
		}
		result, err := service.AdjustCash(r.Context(), CashAdjustmentInput{
			UserID: r.PathValue("userID"), ExternalID: request.ExternalID,
			Amount: request.Amount, Reason: request.Reason,
		})
		if err != nil {
			switch {
			case errors.Is(err, errInsufficientFunds), errors.Is(err, errAdjustmentConflict):
				errorResponse(w, http.StatusConflict, "adjustment_rejected", err.Error())
			default:
				errorResponse(w, http.StatusBadRequest, "invalid_adjustment", err.Error())
			}
			return
		}
		status := http.StatusCreated
		if !result.Created {
			status = http.StatusOK
		}
		jsonValueResponse(w, status, result)
	})
}
