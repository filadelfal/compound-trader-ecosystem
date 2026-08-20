package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	paperQuoteMaxAge    = 30 * time.Second
	paperFutureSkew     = 2 * time.Second
	paperOrderRetention = 7 * 24 * time.Hour
)

var paperPairs = map[string]bool{"EURUSD": true, "GBPUSD": true, "USDJPY": true}
var paperStrategies = map[string]bool{
	"EMA_PULLBACK":           true,
	"EMA_CROSSOVER":          true,
	"ADX_TREND_CONTINUATION": true,
	"TREND_CONTINUATION":     true,
	"LONDON_BREAKOUT":        true,
}

type paperOrderRequest struct {
	RequestID        string  `json:"requestId"`
	Mode             string  `json:"mode"`
	Pair             string  `json:"pair"`
	Strategy         string  `json:"strategy"`
	Direction        string  `json:"direction"`
	Lots             float64 `json:"lots"`
	Entry            float64 `json:"entry"`
	StopLoss         float64 `json:"stopLoss"`
	TakeProfit       float64 `json:"takeProfit"`
	QuoteTimestamp   string  `json:"quoteTimestamp"`
	RiskApproved     bool    `json:"riskApproved"`
	KillSwitchActive bool    `json:"killSwitchActive"`
}

type paperOrder struct {
	OrderID        string    `json:"orderId"`
	RequestID      string    `json:"requestId"`
	Mode           string    `json:"mode"`
	Pair           string    `json:"pair"`
	Strategy       string    `json:"strategy"`
	Direction      string    `json:"direction"`
	Lots           float64   `json:"lots"`
	Entry          float64   `json:"entry"`
	StopLoss       float64   `json:"stopLoss"`
	TakeProfit     float64   `json:"takeProfit"`
	QuoteTimestamp time.Time `json:"quoteTimestamp"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
}

type paperOrderResult struct {
	Outcome string      `json:"outcome"`
	Reasons []string    `json:"reasons,omitempty"`
	Replay  bool        `json:"replay"`
	Order   *paperOrder `json:"order,omitempty"`
}

type paperOrderStore interface {
	Create(context.Context, paperOrder) (paperOrder, bool, error)
	Get(context.Context, string) (paperOrder, error)
}

type redisPaperOrderStore struct {
	client    *redis.Client
	retention time.Duration
}

func (store redisPaperOrderStore) Create(ctx context.Context, order paperOrder) (paperOrder, bool, error) {
	payload, err := json.Marshal(order)
	if err != nil {
		return paperOrder{}, false, err
	}
	key := "trading-engine:paper-order:" + order.RequestID
	created, err := store.client.SetNX(ctx, key, payload, store.retention).Result()
	if err != nil {
		return paperOrder{}, false, err
	}
	if created {
		if err := store.client.ZAdd(ctx, "trading-engine:paper-order-index", redis.Z{Score: float64(order.CreatedAt.Unix()), Member: order.RequestID}).Err(); err != nil {
			return paperOrder{}, false, err
		}
		return order, true, nil
	}
	existingJSON, err := store.client.Get(ctx, key).Result()
	if err != nil {
		return paperOrder{}, false, err
	}
	var existing paperOrder
	if err := json.Unmarshal([]byte(existingJSON), &existing); err != nil {
		return paperOrder{}, false, err
	}
	if err := store.client.ZAdd(ctx, "trading-engine:paper-order-index", redis.Z{Score: float64(existing.CreatedAt.Unix()), Member: existing.RequestID}).Err(); err != nil {
		return paperOrder{}, false, err
	}
	return existing, false, nil
}

func (store redisPaperOrderStore) Get(ctx context.Context, requestID string) (paperOrder, error) {
	payload, err := store.client.Get(ctx, "trading-engine:paper-order:"+requestID).Result()
	if err != nil {
		return paperOrder{}, err
	}
	var order paperOrder
	if err := json.Unmarshal([]byte(payload), &order); err != nil {
		return paperOrder{}, err
	}
	return order, nil
}

type memoryPaperOrderStore struct {
	mu     sync.Mutex
	orders map[string]paperOrder
}

func newMemoryPaperOrderStore() *memoryPaperOrderStore {
	return &memoryPaperOrderStore{orders: make(map[string]paperOrder)}
}

func (store *memoryPaperOrderStore) Create(_ context.Context, order paperOrder) (paperOrder, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.orders[order.RequestID]; ok {
		return existing, false, nil
	}
	store.orders[order.RequestID] = order
	return order, true, nil
}

func (store *memoryPaperOrderStore) Get(_ context.Context, requestID string) (paperOrder, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	order, ok := store.orders[requestID]
	if !ok {
		return paperOrder{}, errors.New("paper order not found")
	}
	return order, nil
}

func evaluatePaperOrder(input paperOrderRequest, now time.Time) paperOrderResult {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Mode = strings.ToUpper(strings.TrimSpace(input.Mode))
	input.Pair = strings.ToUpper(strings.TrimSpace(input.Pair))
	input.Strategy = strings.ToUpper(strings.TrimSpace(input.Strategy))
	input.Direction = strings.ToUpper(strings.TrimSpace(input.Direction))
	if !validPaperIdentifier(input.RequestID) || input.Mode != "PAPER" || !paperPairs[input.Pair] ||
		!paperStrategies[input.Strategy] || (input.Direction != "BUY" && input.Direction != "SELL") ||
		!finitePositive(input.Lots) || !finitePositive(input.Entry) || !finitePositive(input.StopLoss) ||
		!finitePositive(input.TakeProfit) || (input.Strategy == "LONDON_BREAKOUT" && input.Pair == "USDJPY") {
		return paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}}
	}
	if !input.RiskApproved {
		return paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"RISK_NOT_APPROVED"}}
	}
	if input.KillSwitchActive {
		return paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"KILL_SWITCH_ACTIVE"}}
	}
	quoteTime, err := time.Parse(time.RFC3339Nano, input.QuoteTimestamp)
	if err != nil || quoteTime.Before(now.Add(-paperQuoteMaxAge)) || quoteTime.After(now.Add(paperFutureSkew)) {
		return paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"STALE_QUOTE"}}
	}
	risk := math.Abs(input.Entry - input.StopLoss)
	reward := math.Abs(input.TakeProfit - input.Entry)
	validStructure := input.Direction == "BUY" && input.StopLoss < input.Entry && input.TakeProfit > input.Entry ||
		input.Direction == "SELL" && input.StopLoss > input.Entry && input.TakeProfit < input.Entry
	if !validStructure {
		return paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_PRICE_STRUCTURE"}}
	}
	if reward/risk+1e-9 < 2 {
		return paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"INSUFFICIENT_RISK_REWARD"}}
	}
	order := paperOrder{
		OrderID: "paper-" + input.RequestID, RequestID: input.RequestID, Mode: input.Mode,
		Pair: input.Pair, Strategy: input.Strategy, Direction: input.Direction, Lots: input.Lots,
		Entry: input.Entry, StopLoss: input.StopLoss, TakeProfit: input.TakeProfit,
		QuoteTimestamp: quoteTime.UTC(), Status: "PAPER_ACCEPTED", CreatedAt: now.UTC(),
	}
	return paperOrderResult{Outcome: "PAPER_ACCEPTED", Order: &order}
}

func validPaperIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 100 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (app *application) paperOrderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	var input paperOrderRequest
	if err := decodeStrictJSON(w, r, &input); err != nil {
		writePaperResult(w, http.StatusBadRequest, paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}})
		return
	}
	now := time.Now().UTC()
	if app.now != nil {
		now = app.now().UTC()
	}
	result := evaluatePaperOrder(input, now)
	if result.Order == nil {
		writePaperResult(w, http.StatusOK, result)
		return
	}
	if app.paperStore == nil {
		writePaperResult(w, http.StatusServiceUnavailable, paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"PAPER_STORE_UNAVAILABLE"}})
		return
	}
	if app.operationsPaperGate != nil {
		if reason := app.operationsPaperGate(r.Context()); reason != "" {
			writePaperResult(w, http.StatusServiceUnavailable, paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{reason}})
			return
		}
	}
	proposed := *result.Order
	stored, created, err := app.paperStore.Create(r.Context(), proposed)
	if err != nil {
		writePaperResult(w, http.StatusServiceUnavailable, paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"PAPER_STORE_UNAVAILABLE"}})
		return
	}
	if !created {
		if !samePaperOrder(stored, proposed) {
			writePaperResult(w, http.StatusConflict, paperOrderResult{Outcome: "NO_TRADE", Reasons: []string{"IDEMPOTENCY_CONFLICT"}})
			return
		}
	}
	result.Order = &stored
	result.Replay = !created
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writePaperResult(w, status, result)
}

func samePaperOrder(left, right paperOrder) bool {
	return left.RequestID == right.RequestID && left.Mode == right.Mode && left.Pair == right.Pair &&
		left.Strategy == right.Strategy && left.Direction == right.Direction && left.Lots == right.Lots &&
		left.Entry == right.Entry && left.StopLoss == right.StopLoss && left.TakeProfit == right.TakeProfit &&
		left.QuoteTimestamp.Equal(right.QuoteTimestamp)
}

func writePaperResult(w http.ResponseWriter, status int, result paperOrderResult) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
