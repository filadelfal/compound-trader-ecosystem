package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type createOrderRequest struct {
	ClientOrderID string  `json:"clientOrderId"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Type          string  `json:"type"`
	Quantity      string  `json:"quantity"`
	LimitPrice    *string `json:"limitPrice"`
}

func registerOrderRoutes(mux *http.ServeMux, service *OrderService) {
	mux.HandleFunc("POST /api/v1/orders", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}

		var request createOrderRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_request", "request body must be valid order JSON")
			return
		}
		if decoder.Decode(&struct{}{}) == nil {
			errorResponse(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
			return
		}

		clientOrderID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if clientOrderID == "" {
			clientOrderID = request.ClientOrderID
		}
		order, err := service.Create(r.Context(), CreateOrderInput{
			UserID:        userID,
			ClientOrderID: clientOrderID,
			Symbol:        request.Symbol,
			Side:          request.Side,
			Type:          request.Type,
			Quantity:      request.Quantity,
			LimitPrice:    request.LimitPrice,
		})
		if err != nil {
			status := http.StatusBadRequest
			code := "invalid_order"
			if errors.Is(err, errIdempotencyConflict) {
				status = http.StatusConflict
				code = "idempotency_conflict"
			} else if errors.Is(err, errTradingDisabled) {
				status = http.StatusConflict
				code = "trading_disabled"
			} else if errors.Is(err, errMaxOpenOrders) {
				status = http.StatusConflict
				code = "max_open_orders_reached"
			} else if errors.Is(err, errMaxGrossExposure) {
				status = http.StatusConflict
				code = "max_gross_exposure_exceeded"
			} else if errors.Is(err, errMaxDailyLoss) {
				status = http.StatusConflict
				code = "daily_loss_limit_reached"
			} else if errors.Is(err, errInsufficientBuyingPower) {
				status = http.StatusConflict
				code = "insufficient_buying_power"
			} else if errors.Is(err, errInsufficientAvailablePosition) {
				status = http.StatusConflict
				code = "insufficient_available_position"
			} else if strings.Contains(err.Error(), "already exists") {
				status = http.StatusConflict
				code = "duplicate_order"
			}
			errorResponse(w, status, code, err.Error())
			return
		}
		w.Header().Set("Location", "/api/v1/orders/"+order.ID)
		status := http.StatusCreated
		if order.IdempotentReplay {
			status = http.StatusOK
			w.Header().Set("Idempotent-Replayed", "true")
		}
		jsonValueResponse(w, status, order)
	})

	mux.HandleFunc("GET /api/v1/orders", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		limit := 50
		if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil {
				errorResponse(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer")
				return
			}
			limit = parsed
		}
		orders, err := service.List(r.Context(), userID, limit)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		jsonValueResponse(w, http.StatusOK, map[string]any{"orders": orders})
	})

	mux.HandleFunc("GET /api/v1/orders/{orderID}", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		order, err := service.Get(r.Context(), userID, r.PathValue("orderID"))
		if err != nil {
			handleOrderError(w, err)
			return
		}
		jsonValueResponse(w, http.StatusOK, order)
	})

	mux.HandleFunc("GET /api/v1/orders/{orderID}/events", func(w http.ResponseWriter, r *http.Request) {
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
		events, err := service.ListEvents(r.Context(), userID, r.PathValue("orderID"), limit)
		if err != nil {
			handleOrderError(w, err)
			return
		}
		jsonValueResponse(w, http.StatusOK, map[string]any{"events": events})
	})

	mux.HandleFunc("POST /api/v1/orders/{orderID}/cancel", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		order, err := service.Cancel(r.Context(), userID, r.PathValue("orderID"))
		if err != nil {
			handleOrderError(w, err)
			return
		}
		jsonValueResponse(w, http.StatusOK, order)
	})

	mux.HandleFunc("GET /api/v1/orders/{orderID}/fills", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}
		fills, err := service.ListFills(r.Context(), userID, r.PathValue("orderID"))
		if err != nil {
			handleOrderError(w, err)
			return
		}
		jsonValueResponse(w, http.StatusOK, map[string]any{"fills": fills})
	})
}

func authenticatedUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.ToLower(strings.TrimSpace(r.Header.Get("X-User-ID")))
	if !uuidPattern.MatchString(userID) {
		errorResponse(w, http.StatusUnauthorized, "unauthorized", "a valid authenticated X-User-ID header is required")
		return "", false
	}
	return userID, true
}

func handleOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOrderNotFound):
		errorResponse(w, http.StatusNotFound, "order_not_found", err.Error())
	case errors.Is(err, errOrderNotCancelable):
		errorResponse(w, http.StatusConflict, "order_not_cancelable", err.Error())
	default:
		errorResponse(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

func errorResponse(w http.ResponseWriter, status int, code, message string) {
	jsonValueResponse(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func jsonValueResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
