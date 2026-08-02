package main

import (
	"encoding/json"
	"net/http"
)

type updateRiskRequest struct {
	TradingEnabled   *bool   `json:"tradingEnabled"`
	MaxOpenOrders    *int    `json:"maxOpenOrders"`
	MaxGrossExposure *string `json:"maxGrossExposure"`
	MaxDailyLoss     *string `json:"maxDailyLoss"`
}

func registerRiskRoutes(mux *http.ServeMux, service *RiskProfileService) {
	mux.HandleFunc("GET /api/v1/risk", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		status, err := service.Get(r.Context(), userID)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "risk_status_failed", err.Error())
			return
		}
		jsonValueResponse(w, http.StatusOK, status)
	})
	mux.HandleFunc("PATCH /api/v1/risk", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		var request updateRiskRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_risk_profile", "request body must be valid risk profile JSON")
			return
		}
		if request.TradingEnabled == nil && request.MaxOpenOrders == nil && request.MaxGrossExposure == nil && request.MaxDailyLoss == nil {
			errorResponse(w, http.StatusBadRequest, "invalid_risk_profile", "at least one risk setting is required")
			return
		}
		status, err := service.Update(r.Context(), userID, UpdateRiskProfileInput{TradingEnabled: request.TradingEnabled, MaxOpenOrders: request.MaxOpenOrders, MaxGrossExposure: request.MaxGrossExposure, MaxDailyLoss: request.MaxDailyLoss})
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_risk_profile", err.Error())
			return
		}
		jsonValueResponse(w, http.StatusOK, status)
	})
}
