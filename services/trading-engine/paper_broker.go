package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BrokerDispatch struct {
	Order   Order
	Attempt int
}

type PaperBrokerStore interface {
	Claim(context.Context) (*BrokerDispatch, error)
	Complete(context.Context, string) error
	Retry(context.Context, string, string, time.Time) error
	Reject(context.Context, string, string) error
}

type FillExecutor interface {
	ApplyFill(context.Context, ApplyFillInput) (FillResult, error)
}

type PostgresPaperBrokerStore struct{ db *pgxpool.Pool }

func NewPostgresPaperBrokerStore(db *pgxpool.Pool) *PostgresPaperBrokerStore {
	return &PostgresPaperBrokerStore{db: db}
}

func (store *PostgresPaperBrokerStore) Claim(ctx context.Context) (*BrokerDispatch, error) {
	tx, err := store.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil { return nil, err }
	defer func() { _ = tx.Rollback(ctx) }()
	var orderID string
	var attempt int
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT dispatch.order_id
			FROM trading_order_dispatches dispatch
			JOIN trading_orders orders ON orders.id = dispatch.order_id
			WHERE orders.status IN ('pending', 'partially_filled')
			  AND dispatch.available_at <= NOW()
			  AND (dispatch.status IN ('pending', 'retry')
			       OR (dispatch.status = 'processing' AND dispatch.locked_at < NOW() - INTERVAL '1 minute'))
			ORDER BY dispatch.available_at, dispatch.created_at
			FOR UPDATE OF dispatch SKIP LOCKED LIMIT 1
		)
		UPDATE trading_order_dispatches dispatch
		SET status = 'processing', locked_at = NOW(), attempt_count = attempt_count + 1,
		    updated_at = NOW(), last_error = NULL
		FROM candidate WHERE dispatch.order_id = candidate.order_id
		RETURNING dispatch.order_id::text, dispatch.attempt_count
	`).Scan(&orderID, &attempt)
	if errors.Is(err, pgx.ErrNoRows) { _ = tx.Commit(ctx); return nil, nil }
	if err != nil { return nil, err }
	order, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM trading_orders WHERE id = $1`, orderID))
	if err != nil { return nil, err }
	if err := tx.Commit(ctx); err != nil { return nil, err }
	return &BrokerDispatch{Order: order, Attempt: attempt}, nil
}

func (store *PostgresPaperBrokerStore) Complete(ctx context.Context, orderID string) error {
	_, err := store.db.Exec(ctx, `UPDATE trading_order_dispatches SET status='completed', completed_at=NOW(), locked_at=NULL, updated_at=NOW(), last_error=NULL WHERE order_id=$1::uuid`, orderID)
	return err
}

func (store *PostgresPaperBrokerStore) Retry(ctx context.Context, orderID, reason string, availableAt time.Time) error {
	_, err := store.db.Exec(ctx, `UPDATE trading_order_dispatches SET status='retry', available_at=$2, locked_at=NULL, updated_at=NOW(), last_error=$3 WHERE order_id=$1::uuid`, orderID, availableAt, reason)
	return err
}

func (store *PostgresPaperBrokerStore) Reject(ctx context.Context, orderID, reason string) error {
	tx, err := store.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE trading_orders SET status='rejected', rejection_reason=$2, rejected_at=NOW(), updated_at=NOW() WHERE id=$1::uuid AND status IN ('pending', 'partially_filled')`, orderID, reason); err != nil { return err }
	if _, err = tx.Exec(ctx, `UPDATE trading_order_dispatches SET status='failed', completed_at=NOW(), locked_at=NULL, updated_at=NOW(), last_error=$2 WHERE order_id=$1::uuid`, orderID, reason); err != nil { return err }
	return tx.Commit(ctx)
}

type PaperBroker struct {
	store PaperBrokerStore
	market MarketDataClient
	executor FillExecutor
	now func() time.Time
	retryBase time.Duration
}

func NewPaperBroker(store PaperBrokerStore, market MarketDataClient, executor FillExecutor, retryBase time.Duration) *PaperBroker {
	return &PaperBroker{store: store, market: market, executor: executor, now: func() time.Time { return time.Now().UTC() }, retryBase: retryBase}
}

func (broker *PaperBroker) Run(ctx context.Context, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		processed, _ := broker.ProcessNext(ctx)
		if processed { continue }
		select { case <-ctx.Done(): return; case <-ticker.C: }
	}
}

func (broker *PaperBroker) ProcessNext(ctx context.Context) (bool, error) {
	dispatch, err := broker.store.Claim(ctx)
	if err != nil || dispatch == nil { return false, err }
	order := dispatch.Order
	quotes, missing, err := broker.market.Latest(ctx, []string{order.Symbol})
	if err != nil { return true, broker.scheduleRetry(ctx, dispatch, "market_data_unavailable") }
	quote, found := quotes[order.Symbol]
	if !found || len(missing) > 0 { return true, broker.scheduleRetry(ctx, dispatch, "quote_missing") }
	if quote.Stale { return true, broker.scheduleRetry(ctx, dispatch, "quote_stale") }
	price, marketable, err := executablePrice(order, quote)
	if err != nil { return true, broker.store.Reject(ctx, order.ID, err.Error()) }
	if !marketable { return true, broker.scheduleRetry(ctx, dispatch, "limit_not_marketable") }
	quantity, err := remainingQuantity(order)
	if err != nil { return true, broker.store.Reject(ctx, order.ID, err.Error()) }
	_, err = broker.executor.ApplyFill(ctx, ApplyFillInput{OrderID: order.ID, ExecutionID: "paper-" + order.ID, Quantity: quantity, Price: price, ExecutedAt: broker.now()})
	if err == nil { return true, broker.store.Complete(ctx, order.ID) }
	if errors.Is(err, errInsufficientFunds) || errors.Is(err, errInsufficientPosition) {
		return true, broker.store.Reject(ctx, order.ID, err.Error())
	}
	if errors.Is(err, errOrderNotFillable) { return true, broker.store.Complete(ctx, order.ID) }
	return true, broker.scheduleRetry(ctx, dispatch, "execution_failed")
}

func (broker *PaperBroker) scheduleRetry(ctx context.Context, dispatch *BrokerDispatch, reason string) error {
	delay := broker.retryBase * time.Duration(1<<min(dispatch.Attempt-1, 6))
	return broker.store.Retry(ctx, dispatch.Order.ID, reason, broker.now().Add(delay))
}

func executablePrice(order Order, quote MarketQuote) (string, bool, error) {
	price := quote.Ask
	if order.Side == "sell" { price = quote.Bid }
	if !positiveDecimal(price) { return "", false, fmt.Errorf("quote has invalid executable price") }
	if order.Type == "market" { return price, true, nil }
	executable, _ := decimalRat(price)
	limit, _ := decimalRat(*order.LimitPrice)
	marketable := (order.Side == "buy" && executable.Cmp(limit) <= 0) || (order.Side == "sell" && executable.Cmp(limit) >= 0)
	return price, marketable, nil
}

func remainingQuantity(order Order) (string, error) {
	total, err := decimalRat(order.Quantity); if err != nil { return "", err }
	filled, err := decimalRatAllowZero(order.FilledQuantity); if err != nil { return "", err }
	remaining := new(big.Rat).Sub(total, filled)
	if remaining.Sign() <= 0 { return "", errors.New("order has no remaining quantity") }
	return remaining.FloatString(8), nil
}
