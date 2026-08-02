package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type OrderEvent struct {
	ID        int64           `json:"id"`
	OrderID   string          `json:"orderId"`
	EventType string          `json:"eventType"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

func (service *OrderService) ListEvents(ctx context.Context, userID, orderID string, limit int) ([]OrderEvent, error) {
	if err := validateIdentifiers(userID, orderID); err != nil { return nil, err }
	if limit < 1 || limit > 500 { return nil, errors.New("limit must be between 1 and 500") }
	return service.repository.ListEvents(ctx, strings.ToLower(userID), strings.ToLower(orderID), limit)
}
