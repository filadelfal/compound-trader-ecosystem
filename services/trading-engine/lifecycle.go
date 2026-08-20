package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const paperLifecycleRetention = 30 * 24 * time.Hour

var (
	errPaperPositionNotFound = errors.New("paper position not found")
	errLifecycleConflict     = errors.New("lifecycle idempotency conflict")
	errOutOfOrderQuote       = errors.New("out-of-order quote")
	errPaperSpreadTooWide    = errors.New("paper quote spread too wide")
)

type paperPosition struct {
	OrderID               string     `json:"orderId"`
	RequestID             string     `json:"requestId"`
	Pair                  string     `json:"pair"`
	Strategy              string     `json:"strategy"`
	Direction             string     `json:"direction"`
	Lots                  float64    `json:"lots"`
	Entry                  float64    `json:"entry"`
	StopLoss               float64    `json:"stopLoss"`
	TakeProfit             float64    `json:"takeProfit"`
	PipValuePerStandardLot float64    `json:"pipValuePerStandardLot"`
	Status                 string     `json:"status"`
	CloseReason            string     `json:"closeReason,omitempty"`
	CurrentPrice           float64    `json:"currentPrice"`
	ExitPrice              float64    `json:"exitPrice,omitempty"`
	UnrealizedPnL          float64    `json:"unrealizedPnl"`
	RealizedPnL            float64    `json:"realizedPnl"`
	OpenedAt               time.Time  `json:"openedAt"`
	LastQuoteAt            *time.Time `json:"lastQuoteAt,omitempty"`
	ClosedAt               *time.Time `json:"closedAt,omitempty"`
}

type paperJournalEvent struct {
	EventID    string        `json:"eventId"`
	Type       string        `json:"type"`
	RecordedAt time.Time     `json:"recordedAt"`
	Position   paperPosition `json:"position"`
}

type openPaperPositionRequest struct {
	EventID               string  `json:"eventId"`
	RequestID             string  `json:"requestId"`
	PipValuePerStandardLot float64 `json:"pipValuePerStandardLot"`
}

type paperQuoteRequest struct {
	EventID       string  `json:"eventId"`
	OrderID       string  `json:"orderId"`
	Bid           float64 `json:"bid"`
	Ask           float64 `json:"ask"`
	QuoteTimestamp string `json:"quoteTimestamp"`
}

type paperLifecycleResult struct {
	Outcome  string         `json:"outcome"`
	Reasons  []string       `json:"reasons,omitempty"`
	Replay   bool           `json:"replay"`
	Position *paperPosition `json:"position,omitempty"`
}

type paperLifecycleStore interface {
	Open(context.Context, string, paperPosition, paperJournalEvent) (paperPosition, bool, error)
	ApplyQuote(context.Context, string, string, float64, float64, time.Time, time.Time) (paperPosition, bool, error)
	Journal(context.Context, string) ([]paperJournalEvent, error)
}

type lifecycleEventRecord struct {
	Fingerprint string        `json:"fingerprint"`
	Position    paperPosition `json:"position"`
}

type memoryPaperLifecycleStore struct {
	mu        sync.Mutex
	positions map[string]paperPosition
	events    map[string]lifecycleEventRecord
	journal   map[string][]paperJournalEvent
}

func newMemoryPaperLifecycleStore() *memoryPaperLifecycleStore {
	return &memoryPaperLifecycleStore{
		positions: make(map[string]paperPosition), events: make(map[string]lifecycleEventRecord),
		journal: make(map[string][]paperJournalEvent),
	}
}

func (store *memoryPaperLifecycleStore) Open(_ context.Context, eventID string, position paperPosition, event paperJournalEvent) (paperPosition, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	fingerprint := lifecycleOpenFingerprint(position)
	if existing, ok := store.events[eventID]; ok {
		if existing.Fingerprint != fingerprint {
			return paperPosition{}, false, errLifecycleConflict
		}
		return existing.Position, true, nil
	}
	if existing, ok := store.positions[position.OrderID]; ok {
		return existing, false, errLifecycleConflict
	}
	store.positions[position.OrderID] = position
	store.events[eventID] = lifecycleEventRecord{Fingerprint: fingerprint, Position: position}
	store.journal[position.OrderID] = append(store.journal[position.OrderID], event)
	return position, false, nil
}

func (store *memoryPaperLifecycleStore) ApplyQuote(_ context.Context, eventID, orderID string, bid, ask float64, quoteAt, recordedAt time.Time) (paperPosition, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	fingerprint := lifecycleQuoteFingerprint(orderID, bid, ask, quoteAt)
	if existing, ok := store.events[eventID]; ok {
		if existing.Fingerprint != fingerprint {
			return paperPosition{}, false, errLifecycleConflict
		}
		return existing.Position, true, nil
	}
	position, ok := store.positions[orderID]
	if !ok {
		return paperPosition{}, false, errPaperPositionNotFound
	}
	updated, eventType, err := applyPaperQuote(position, bid, ask, quoteAt)
	if err != nil {
		return paperPosition{}, false, err
	}
	store.positions[orderID] = updated
	store.events[eventID] = lifecycleEventRecord{Fingerprint: fingerprint, Position: updated}
	store.journal[orderID] = append(store.journal[orderID], paperJournalEvent{
		EventID: eventID, Type: eventType, RecordedAt: recordedAt.UTC(), Position: updated,
	})
	return updated, false, nil
}

func (store *memoryPaperLifecycleStore) Journal(_ context.Context, orderID string) ([]paperJournalEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	events, ok := store.journal[orderID]
	if !ok {
		return nil, errPaperPositionNotFound
	}
	return append([]paperJournalEvent(nil), events...), nil
}

type redisPaperLifecycleStore struct {
	client    *redis.Client
	retention time.Duration
}

func (store redisPaperLifecycleStore) positionKey(orderID string) string {
	return "trading-engine:paper-position:" + orderID
}

func (store redisPaperLifecycleStore) eventKey(eventID string) string {
	return "trading-engine:paper-lifecycle-event:" + eventID
}

func (store redisPaperLifecycleStore) journalKey(orderID string) string {
	return "trading-engine:paper-journal:" + orderID
}

func (store redisPaperLifecycleStore) Open(ctx context.Context, eventID string, position paperPosition, event paperJournalEvent) (result paperPosition, replay bool, err error) {
	positionKey, eventKey := store.positionKey(position.OrderID), store.eventKey(eventID)
	fingerprint := lifecycleOpenFingerprint(position)
	err = store.client.Watch(ctx, func(tx *redis.Tx) error {
		existingRecord, eventErr := decodeLifecycleEventRecord(ctx, tx, eventKey)
		if eventErr == nil {
			if existingRecord.Fingerprint != fingerprint {
				return errLifecycleConflict
			}
			replay = true
			result = existingRecord.Position
			return nil
		}
		if !errors.Is(eventErr, redis.Nil) {
			return eventErr
		}
		if err := decodeRedisPosition(ctx, tx, positionKey, &result); err == nil {
			return errLifecycleConflict
		} else if !errors.Is(err, redis.Nil) {
			return err
		}
		positionJSON, _ := json.Marshal(position)
		eventJSON, _ := json.Marshal(event)
		eventRecordJSON, _ := json.Marshal(lifecycleEventRecord{Fingerprint: fingerprint, Position: position})
		_, pipeErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, positionKey, positionJSON, store.retention)
			pipe.Set(ctx, eventKey, eventRecordJSON, store.retention)
			pipe.RPush(ctx, store.journalKey(position.OrderID), eventJSON)
			pipe.Expire(ctx, store.journalKey(position.OrderID), store.retention)
			pipe.ZAdd(ctx, "trading-engine:paper-position-index", redis.Z{Score: float64(position.OpenedAt.Unix()), Member: position.OrderID})
			return nil
		})
		result = position
		return pipeErr
	}, positionKey, eventKey)
	if err == nil {
		err = store.client.ZAdd(ctx, "trading-engine:paper-position-index", redis.Z{Score: float64(result.OpenedAt.Unix()), Member: result.OrderID}).Err()
	}
	return
}

func (store redisPaperLifecycleStore) ApplyQuote(ctx context.Context, eventID, orderID string, bid, ask float64, quoteAt, recordedAt time.Time) (result paperPosition, replay bool, err error) {
	positionKey, eventKey := store.positionKey(orderID), store.eventKey(eventID)
	fingerprint := lifecycleQuoteFingerprint(orderID, bid, ask, quoteAt)
	err = store.client.Watch(ctx, func(tx *redis.Tx) error {
		existingRecord, eventErr := decodeLifecycleEventRecord(ctx, tx, eventKey)
		if eventErr == nil {
			if existingRecord.Fingerprint != fingerprint {
				return errLifecycleConflict
			}
			replay = true
			result = existingRecord.Position
			return nil
		}
		if !errors.Is(eventErr, redis.Nil) {
			return eventErr
		}
		var current paperPosition
		if err := decodeRedisPosition(ctx, tx, positionKey, &current); err != nil {
			if errors.Is(err, redis.Nil) {
				return errPaperPositionNotFound
			}
			return err
		}
		updated, eventType, err := applyPaperQuote(current, bid, ask, quoteAt)
		if err != nil {
			return err
		}
		event := paperJournalEvent{EventID: eventID, Type: eventType, RecordedAt: recordedAt.UTC(), Position: updated}
		positionJSON, _ := json.Marshal(updated)
		eventJSON, _ := json.Marshal(event)
		eventRecordJSON, _ := json.Marshal(lifecycleEventRecord{Fingerprint: fingerprint, Position: updated})
		_, pipeErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, positionKey, positionJSON, store.retention)
			pipe.Set(ctx, eventKey, eventRecordJSON, store.retention)
			pipe.RPush(ctx, store.journalKey(orderID), eventJSON)
			pipe.Expire(ctx, store.journalKey(orderID), store.retention)
			return nil
		})
		result = updated
		return pipeErr
	}, positionKey, eventKey)
	return
}

func (store redisPaperLifecycleStore) Journal(ctx context.Context, orderID string) ([]paperJournalEvent, error) {
	items, err := store.client.LRange(ctx, store.journalKey(orderID), 0, 1000).Result()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errPaperPositionNotFound
	}
	if len(items) > 1000 {
		return nil, errors.New("paper journal reconciliation bound exceeded")
	}
	events := make([]paperJournalEvent, 0, len(items))
	for _, item := range items {
		var event paperJournalEvent
		if err := json.Unmarshal([]byte(item), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func decodeRedisPosition(ctx context.Context, client redis.Cmdable, key string, position *paperPosition) error {
	payload, err := client.Get(ctx, key).Result()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(payload), position)
}

func decodeLifecycleEventRecord(ctx context.Context, client redis.Cmdable, key string) (lifecycleEventRecord, error) {
	payload, err := client.Get(ctx, key).Result()
	if err != nil {
		return lifecycleEventRecord{}, err
	}
	var record lifecycleEventRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return lifecycleEventRecord{}, err
	}
	return record, nil
}

func applyPaperQuote(position paperPosition, bid, ask float64, quoteAt time.Time) (paperPosition, string, error) {
	if position.Status != "OPEN" {
		return position, "POSITION_ALREADY_CLOSED", nil
	}
	if (ask-bid)/paperPipSize(position.Pair) > 5 {
		return paperPosition{}, "", errPaperSpreadTooWide
	}
	if !quoteAt.After(position.OpenedAt) || position.LastQuoteAt != nil && !quoteAt.After(*position.LastQuoteAt) {
		return paperPosition{}, "", errOutOfOrderQuote
	}
	price := bid
	if position.Direction == "SELL" {
		price = ask
	}
	position.CurrentPrice = price
	quoteUTC := quoteAt.UTC()
	position.LastQuoteAt = &quoteUTC
	eventType := "POSITION_MARKED"
	if position.Direction == "BUY" && price <= position.StopLoss || position.Direction == "SELL" && price >= position.StopLoss {
		position.ExitPrice = position.StopLoss
		position.CloseReason = "STOP_LOSS"
		eventType = "POSITION_CLOSED_STOP_LOSS"
	} else if position.Direction == "BUY" && price >= position.TakeProfit || position.Direction == "SELL" && price <= position.TakeProfit {
		position.ExitPrice = position.TakeProfit
		position.CloseReason = "TAKE_PROFIT"
		eventType = "POSITION_CLOSED_TAKE_PROFIT"
	}
	if position.CloseReason != "" {
		position.Status = "CLOSED"
		position.CurrentPrice = position.ExitPrice
		position.RealizedPnL = calculatePaperPnL(position, position.ExitPrice)
		position.UnrealizedPnL = 0
		position.ClosedAt = &quoteUTC
	} else {
		position.UnrealizedPnL = calculatePaperPnL(position, price)
	}
	return position, eventType, nil
}

func calculatePaperPnL(position paperPosition, price float64) float64 {
	pipSize := paperPipSize(position.Pair)
	pips := (price - position.Entry) / pipSize
	if position.Direction == "SELL" {
		pips = -pips
	}
	return math.Round(pips*position.PipValuePerStandardLot*position.Lots*1e8) / 1e8
}

func paperPipSize(pair string) float64 {
	if pair == "USDJPY" {
		return 0.01
	}
	return 0.0001
}

func lifecycleOpenFingerprint(position paperPosition) string {
	return position.OrderID + ":OPEN:" + strconv.FormatFloat(position.PipValuePerStandardLot, 'g', -1, 64)
}

func lifecycleQuoteFingerprint(orderID string, bid, ask float64, quoteAt time.Time) string {
	return orderID + ":QUOTE:" + quoteAt.UTC().Format(time.RFC3339Nano) + ":" +
		strconv.FormatFloat(bid, 'g', -1, 64) + ":" + strconv.FormatFloat(ask, 'g', -1, 64)
}

func (app *application) openPaperPositionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	var input openPaperPositionRequest
	if decodeStrictJSON(w, r, &input) != nil || !validPaperIdentifier(strings.TrimSpace(input.EventID)) ||
		!validPaperIdentifier(strings.TrimSpace(input.RequestID)) || !finitePositive(input.PipValuePerStandardLot) {
		writeLifecycleResult(w, http.StatusBadRequest, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}})
		return
	}
	if app.paperStore == nil || app.lifecycleStore == nil {
		writeLifecycleResult(w, http.StatusServiceUnavailable, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{"PAPER_STORE_UNAVAILABLE"}})
		return
	}
	if app.operationsPaperGate != nil {
		if reason := app.operationsPaperGate(r.Context()); reason != "" {
			writeLifecycleResult(w, http.StatusServiceUnavailable, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{reason}})
			return
		}
	}
	order, err := app.paperStore.Get(r.Context(), strings.TrimSpace(input.RequestID))
	if err != nil || order.Status != "PAPER_ACCEPTED" {
		writeLifecycleResult(w, http.StatusNotFound, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{"PAPER_ORDER_NOT_FOUND"}})
		return
	}
	now := app.currentTime()
	position := paperPosition{
		OrderID: order.OrderID, RequestID: order.RequestID, Pair: order.Pair, Strategy: order.Strategy,
		Direction: order.Direction, Lots: order.Lots, Entry: order.Entry, StopLoss: order.StopLoss,
		TakeProfit: order.TakeProfit, PipValuePerStandardLot: input.PipValuePerStandardLot,
		Status: "OPEN", CurrentPrice: order.Entry, OpenedAt: now,
	}
	eventID := strings.TrimSpace(input.EventID)
	event := paperJournalEvent{EventID: eventID, Type: "POSITION_OPENED", RecordedAt: now, Position: position}
	stored, replay, err := app.lifecycleStore.Open(r.Context(), eventID, position, event)
	writeLifecycleStoreResult(w, stored, replay, err, http.StatusCreated, "POSITION_OPENED")
}

func (app *application) paperQuoteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	var input paperQuoteRequest
	if decodeStrictJSON(w, r, &input) != nil || !validPaperIdentifier(strings.TrimSpace(input.EventID)) ||
		!validPaperIdentifier(strings.TrimSpace(input.OrderID)) || !finitePositive(input.Bid) || !finitePositive(input.Ask) || input.Bid >= input.Ask {
		writeLifecycleResult(w, http.StatusBadRequest, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{"INVALID_INPUT"}})
		return
	}
	now := app.currentTime()
	quoteAt, err := time.Parse(time.RFC3339Nano, input.QuoteTimestamp)
	if err != nil || quoteAt.Before(now.Add(-paperQuoteMaxAge)) || quoteAt.After(now.Add(paperFutureSkew)) {
		writeLifecycleResult(w, http.StatusOK, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{"STALE_QUOTE"}})
		return
	}
	if app.lifecycleStore == nil {
		writeLifecycleResult(w, http.StatusServiceUnavailable, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{"PAPER_STORE_UNAVAILABLE"}})
		return
	}
	position, replay, err := app.lifecycleStore.ApplyQuote(
		r.Context(), strings.TrimSpace(input.EventID), strings.TrimSpace(input.OrderID), input.Bid, input.Ask, quoteAt.UTC(), now,
	)
	outcome := "POSITION_UPDATED"
	if position.Status == "CLOSED" {
		outcome = "POSITION_CLOSED"
	}
	writeLifecycleStoreResult(w, position, replay, err, http.StatusOK, outcome)
}

func (app *application) paperJournalHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	orderID := strings.TrimSpace(r.URL.Query().Get("orderId"))
	if !validPaperIdentifier(orderID) {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid_input"})
		return
	}
	if app.lifecycleStore == nil {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "journal_unavailable"})
		return
	}
	events, err := app.lifecycleStore.Journal(r.Context(), orderID)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, errPaperPositionNotFound) {
			status = http.StatusNotFound
		}
		jsonResponse(w, status, map[string]any{"error": "journal_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"orderId": orderID, "events": events})
}

func (app *application) currentTime() time.Time {
	if app.now != nil {
		return app.now().UTC()
	}
	return time.Now().UTC()
}

func writeLifecycleStoreResult(w http.ResponseWriter, position paperPosition, replay bool, err error, createdStatus int, outcome string) {
	if err != nil {
		status, reason := http.StatusServiceUnavailable, "PAPER_STORE_UNAVAILABLE"
		if errors.Is(err, errLifecycleConflict) {
			status, reason = http.StatusConflict, "IDEMPOTENCY_CONFLICT"
		} else if errors.Is(err, errPaperPositionNotFound) {
			status, reason = http.StatusNotFound, "PAPER_POSITION_NOT_FOUND"
		} else if errors.Is(err, errOutOfOrderQuote) {
			status, reason = http.StatusConflict, "OUT_OF_ORDER_QUOTE"
		} else if errors.Is(err, errPaperSpreadTooWide) {
			status, reason = http.StatusUnprocessableEntity, "SPREAD_TOO_WIDE"
		}
		writeLifecycleResult(w, status, paperLifecycleResult{Outcome: "NO_TRADE", Reasons: []string{reason}})
		return
	}
	status := createdStatus
	if replay {
		status = http.StatusOK
	}
	writeLifecycleResult(w, status, paperLifecycleResult{Outcome: outcome, Replay: replay, Position: &position})
}

func writeLifecycleResult(w http.ResponseWriter, status int, result paperLifecycleResult) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
