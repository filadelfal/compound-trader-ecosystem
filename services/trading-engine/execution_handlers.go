package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type applyFillRequest struct {
	ExecutionID string     `json:"executionId"`
	Quantity    string     `json:"quantity"`
	Price       string     `json:"price"`
	ExecutedAt  *time.Time `json:"executedAt"`
}

func registerExecutionRoutes(mux *http.ServeMux, service *OrderService, executionAPIKey string) {
	mux.HandleFunc("POST /internal/v1/orders/{orderID}/fills", func(w http.ResponseWriter, r *http.Request) {
		if !validExecutionCredential(r.Header.Get("X-Execution-Key"), executionAPIKey) {
			errorResponse(w, http.StatusUnauthorized, "unauthorized", "valid execution service credentials are required")
			return
		}
		var request applyFillRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_request", "request body must be valid fill JSON")
			return
		}
		executedAt := time.Time{}
		if request.ExecutedAt != nil {
			executedAt = request.ExecutedAt.UTC()
		}
		result, err := service.ApplyFill(r.Context(), ApplyFillInput{
			OrderID: r.PathValue("orderID"), ExecutionID: request.ExecutionID,
			Quantity: request.Quantity, Price: request.Price, ExecutedAt: executedAt,
		})
		if err != nil {
			handleExecutionError(w, err)
			return
		}
		status := http.StatusCreated
		if !result.Created {
			status = http.StatusOK
		}
		jsonValueResponse(w, status, result)
	})
}

func validExecutionCredential(provided, configured string) bool {
	provided = strings.TrimSpace(provided)
	configured = strings.TrimSpace(configured)
	if provided == "" || configured == "" || len(provided) != len(configured) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(configured)) == 1
}

func handleExecutionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOrderNotFound):
		errorResponse(w, http.StatusNotFound, "order_not_found", err.Error())
	case errors.Is(err, errOrderNotFillable), errors.Is(err, errOrderOverfill),
		errors.Is(err, errLimitPrice), errors.Is(err, errExecutionConflict),
		errors.Is(err, errInsufficientFunds), errors.Is(err, errInsufficientPosition):
		errorResponse(w, http.StatusConflict, "fill_rejected", err.Error())
	default:
		errorResponse(w, http.StatusBadRequest, "invalid_fill", err.Error())
	}
}
