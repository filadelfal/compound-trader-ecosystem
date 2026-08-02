package main

import (
	"context"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOrderRepository struct {
	db                *pgxpool.Pool
	allowShortSelling bool
}

func NewPostgresOrderRepository(db *pgxpool.Pool, allowShortSelling ...bool) *PostgresOrderRepository {
	allowShort := false
	if len(allowShortSelling) > 0 {
		allowShort = allowShortSelling[0]
	}
	return &PostgresOrderRepository{db: db, allowShortSelling: allowShort}
}

const orderColumns = `id::text, user_id::text, client_order_id, symbol, side, order_type,
	quantity::text, limit_price::text, status, filled_quantity::text,
	average_fill_price::text, created_at, updated_at, canceled_at, rejection_reason, rejected_at`

const qualifiedOrderColumns = `trading_orders.id::text, trading_orders.user_id::text,
	trading_orders.client_order_id, trading_orders.symbol, trading_orders.side,
	trading_orders.order_type, trading_orders.quantity::text, trading_orders.limit_price::text,
	trading_orders.status, trading_orders.filled_quantity::text,
	trading_orders.average_fill_price::text, trading_orders.created_at,
	trading_orders.updated_at, trading_orders.canceled_at,
	trading_orders.rejection_reason, trading_orders.rejected_at`

func (repository *PostgresOrderRepository) Create(ctx context.Context, input CreateOrderInput) (Order, error) {
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.UserID); err != nil {
		return Order{}, err
	}
	existing, existingErr := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM trading_orders WHERE user_id=$1::uuid AND client_order_id=$2`, input.UserID, input.ClientOrderID))
	if existingErr == nil {
		if !sameOrderEconomics(existing, input) {
			return Order{}, errIdempotencyConflict
		}
		existing.IdempotentReplay = true
		if err = tx.Commit(ctx); err != nil {
			return Order{}, err
		}
		return existing, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return Order{}, existingErr
	}
	if err = repository.enforceAccountRisk(ctx, tx, input); err != nil {
		return Order{}, err
	}

	var ledgerAmount, reservedAmount string
	if input.ReservationType == "cash" {
		err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_delta), 0)::text FROM portfolio_ledger_entries WHERE user_id=$1::uuid AND leg_type='cash' AND asset=$2`, input.UserID, input.ReservationAsset).Scan(&ledgerAmount)
	} else {
		err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_delta), 0)::text FROM portfolio_ledger_entries WHERE user_id=$1::uuid AND leg_type='security' AND asset=$2`, input.UserID, input.ReservationAsset).Scan(&ledgerAmount)
	}
	if err != nil {
		return Order{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(remaining_amount), 0)::text FROM trading_order_reservations WHERE user_id=$1::uuid AND reservation_type=$2 AND asset=$3 AND status='active'`, input.UserID, input.ReservationType, input.ReservationAsset).Scan(&reservedAmount); err != nil {
		return Order{}, err
	}
	ledger, _ := signedOrZeroDecimalRat(ledgerAmount)
	reserved, _ := signedOrZeroDecimalRat(reservedAmount)
	requested, _ := decimalRat(input.ReservationAmount)
	available := new(big.Rat).Sub(ledger, reserved)
	if available.Cmp(requested) < 0 {
		if input.ReservationType == "cash" {
			return Order{}, errInsufficientBuyingPower
		}
		return Order{}, errInsufficientAvailablePosition
	}

	order, err := scanOrder(tx.QueryRow(ctx, `
		INSERT INTO trading_orders
			(user_id, client_order_id, symbol, side, order_type, quantity, limit_price)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::numeric, $7::numeric)
		RETURNING `+orderColumns,
		input.UserID, input.ClientOrderID, input.Symbol, input.Side, input.Type,
		input.Quantity, input.LimitPrice,
	))
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return Order{}, duplicateOrderError(input.ClientOrderID)
	}
	if err != nil {
		return Order{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trading_order_reservations (order_id,user_id,reservation_type,asset,original_amount,remaining_amount) VALUES ($1::uuid,$2::uuid,$3,$4,$5::numeric,$5::numeric)`, order.ID, input.UserID, input.ReservationType, input.ReservationAsset, input.ReservationAmount); err != nil {
		return Order{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Order{}, err
	}
	return order, nil
}

func sameOrderEconomics(order Order, input CreateOrderInput) bool {
	if order.Symbol != input.Symbol || order.Side != input.Side || order.Type != input.Type {
		return false
	}
	orderQuantity, err := decimalRat(order.Quantity)
	if err != nil {
		return false
	}
	inputQuantity, err := decimalRat(input.Quantity)
	if err != nil || orderQuantity.Cmp(inputQuantity) != 0 {
		return false
	}
	if (order.LimitPrice == nil) != (input.LimitPrice == nil) {
		return false
	}
	if order.LimitPrice != nil {
		orderLimit, err := decimalRat(*order.LimitPrice)
		if err != nil {
			return false
		}
		inputLimit, err := decimalRat(*input.LimitPrice)
		if err != nil || orderLimit.Cmp(inputLimit) != 0 {
			return false
		}
	}
	return true
}

func (repository *PostgresOrderRepository) Get(ctx context.Context, userID, orderID string) (Order, error) {
	order, err := scanOrder(repository.db.QueryRow(ctx, `
		SELECT `+orderColumns+`
		FROM trading_orders
		WHERE id = $1::uuid AND user_id = $2::uuid
	`, orderID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, errOrderNotFound
	}
	return order, err
}

func (repository *PostgresOrderRepository) FindByClientOrderID(ctx context.Context, userID, clientOrderID string) (Order, error) {
	order, err := scanOrder(repository.db.QueryRow(ctx, `SELECT `+orderColumns+` FROM trading_orders WHERE user_id=$1::uuid AND client_order_id=$2`, userID, clientOrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, errOrderNotFound
	}
	return order, err
}

func (repository *PostgresOrderRepository) List(ctx context.Context, userID string, limit int) ([]Order, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT `+orderColumns+`
		FROM trading_orders
		WHERE user_id = $1::uuid
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make([]Order, 0)
	for rows.Next() {
		order, scanErr := scanOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (repository *PostgresOrderRepository) Cancel(ctx context.Context, userID, orderID string) (Order, error) {
	order, err := scanOrder(repository.db.QueryRow(ctx, `
		UPDATE trading_orders
		SET status = 'canceled', canceled_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid AND user_id = $2::uuid
		  AND status IN ('pending', 'partially_filled')
		RETURNING `+orderColumns,
		orderID, userID,
	))
	if err == nil {
		return order, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, err
	}

	var exists bool
	if lookupErr := repository.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM trading_orders WHERE id = $1::uuid AND user_id = $2::uuid
		)
	`, orderID, userID).Scan(&exists); lookupErr != nil {
		return Order{}, lookupErr
	}
	if !exists {
		return Order{}, errOrderNotFound
	}
	return Order{}, errOrderNotCancelable
}

func (repository *PostgresOrderRepository) ApplyFill(ctx context.Context, input ApplyFillInput) (FillResult, error) {
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FillResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	order, err := scanOrder(tx.QueryRow(ctx, `
		SELECT `+orderColumns+`
		FROM trading_orders
		WHERE id = $1::uuid
		FOR UPDATE
	`, input.OrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return FillResult{}, errOrderNotFound
	}
	if err != nil {
		return FillResult{}, err
	}

	existing, existingErr := scanFill(tx.QueryRow(ctx, `
		SELECT id::text, order_id::text, execution_id, quantity::text, price::text,
		       executed_at, created_at
		FROM trading_order_fills
		WHERE order_id = $1::uuid AND execution_id = $2
	`, input.OrderID, input.ExecutionID))
	if existingErr == nil {
		existingQuantity, _ := decimalRat(existing.Quantity)
		inputQuantity, _ := decimalRat(input.Quantity)
		existingPrice, _ := decimalRat(existing.Price)
		inputPrice, _ := decimalRat(input.Price)
		if existingQuantity.Cmp(inputQuantity) != 0 || existingPrice.Cmp(inputPrice) != 0 {
			return FillResult{}, errExecutionConflict
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return FillResult{}, commitErr
		}
		return FillResult{Order: order, Fill: existing, Created: false}, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return FillResult{}, existingErr
	}

	if order.Status != "pending" && order.Status != "partially_filled" {
		return FillResult{}, errOrderNotFillable
	}
	fillQuantity, _ := decimalRat(input.Quantity)
	orderQuantity, _ := decimalRat(order.Quantity)
	filledQuantity, _ := decimalRatAllowZero(order.FilledQuantity)
	remaining := new(big.Rat).Sub(orderQuantity, filledQuantity)
	if fillQuantity.Cmp(remaining) > 0 {
		return FillResult{}, errOrderOverfill
	}
	if order.LimitPrice != nil {
		fillPrice, _ := decimalRat(input.Price)
		limitPrice, _ := decimalRat(*order.LimitPrice)
		if (order.Side == "buy" && fillPrice.Cmp(limitPrice) > 0) ||
			(order.Side == "sell" && fillPrice.Cmp(limitPrice) < 0) {
			return FillResult{}, errLimitPrice
		}
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, order.UserID); err != nil {
		return FillResult{}, err
	}
	var cashBalanceRaw, positionRaw, positionCostRaw, otherCashReservedRaw, otherPositionReservedRaw string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity_delta), 0)::text
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid AND leg_type = 'cash' AND asset = 'USD'
	`, order.UserID).Scan(&cashBalanceRaw); err != nil {
		return FillResult{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity_delta), 0)::text,
		       COALESCE(SUM(cost_delta), 0)::text
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid AND leg_type = 'security' AND asset = $2
	`, order.UserID, order.Symbol).Scan(&positionRaw, &positionCostRaw); err != nil {
		return FillResult{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(remaining_amount),0)::text FROM trading_order_reservations WHERE user_id=$1::uuid AND reservation_type='cash' AND asset='USD' AND status='active' AND order_id<>$2::uuid`, order.UserID, order.ID).Scan(&otherCashReservedRaw); err != nil {
		return FillResult{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(remaining_amount),0)::text FROM trading_order_reservations WHERE user_id=$1::uuid AND reservation_type='security' AND asset=$2 AND status='active' AND order_id<>$3::uuid`, order.UserID, order.Symbol, order.ID).Scan(&otherPositionReservedRaw); err != nil {
		return FillResult{}, err
	}
	fillPrice, _ := decimalRat(input.Price)
	notional := new(big.Rat).Mul(fillQuantity, fillPrice)
	if order.Side == "buy" {
		cashBalance, _ := decimalRatAllowZero(cashBalanceRaw)
		otherReserved, _ := decimalRatAllowZero(otherCashReservedRaw)
		if new(big.Rat).Sub(cashBalance, otherReserved).Cmp(notional) < 0 {
			return FillResult{}, errInsufficientFunds
		}
	} else if !repository.allowShortSelling {
		position, _ := signedOrZeroDecimalRat(positionRaw)
		otherReserved, _ := decimalRatAllowZero(otherPositionReservedRaw)
		if new(big.Rat).Sub(position, otherReserved).Cmp(fillQuantity) < 0 {
			return FillResult{}, errInsufficientPosition
		}
	}

	fill, err := scanFill(tx.QueryRow(ctx, `
		INSERT INTO trading_order_fills
			(order_id, execution_id, quantity, price, executed_at)
		VALUES ($1::uuid, $2, $3::numeric, $4::numeric, $5)
		RETURNING id::text, order_id::text, execution_id, quantity::text, price::text,
		          executed_at, created_at
	`, input.OrderID, input.ExecutionID, input.Quantity, input.Price, input.ExecutedAt))
	if err != nil {
		return FillResult{}, err
	}

	quantityDelta := fillQuantity.FloatString(8)
	valueDelta := notional.FloatString(8)
	cashDelta := new(big.Rat).Neg(notional).FloatString(8)
	costDelta := notional.FloatString(8)
	realizedPnL := "0.00000000"
	if order.Side == "sell" {
		quantityDelta = new(big.Rat).Neg(fillQuantity).FloatString(8)
		valueDelta = new(big.Rat).Neg(notional).FloatString(8)
		cashDelta = notional.FloatString(8)
		position, _ := signedOrZeroDecimalRat(positionRaw)
		positionCost, _ := signedOrZeroDecimalRat(positionCostRaw)
		averageCost := new(big.Rat).Quo(positionCost, position)
		allocatedCost := new(big.Rat).Mul(averageCost, fillQuantity)
		costDelta = new(big.Rat).Neg(allocatedCost).FloatString(8)
		realizedPnL = new(big.Rat).Sub(notional, allocatedCost).FloatString(8)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO portfolio_ledger_entries
			(user_id, source_type, source_id, leg_type, asset,
			 quantity_delta, value_delta, cost_delta, realized_pnl, description)
		VALUES
			($1::uuid, 'fill', $2, 'security', $3, $4::numeric, $5::numeric,
			 $6::numeric, $7::numeric, $8),
			($1::uuid, 'fill', $2, 'cash', 'USD', $9::numeric, $10::numeric,
			 0, 0, $8)
	`, order.UserID, fill.ID, order.Symbol, quantityDelta, valueDelta,
		costDelta, realizedPnL, "Execution "+fill.ExecutionID, cashDelta, cashDelta); err != nil {
		return FillResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE trading_order_reservations
		SET remaining_amount = GREATEST(0, remaining_amount - original_amount * ($2::numeric / $3::numeric)),
		    updated_at = NOW()
		WHERE order_id = $1::uuid AND status = 'active'
	`, order.ID, input.Quantity, order.Quantity); err != nil {
		return FillResult{}, err
	}

	order, err = scanOrder(tx.QueryRow(ctx, `
		UPDATE trading_orders
		SET filled_quantity = aggregate.filled_quantity,
		    average_fill_price = aggregate.average_fill_price,
		    status = CASE
		        WHEN aggregate.filled_quantity = quantity THEN 'filled'
		        ELSE 'partially_filled'
		    END,
		    updated_at = NOW()
		FROM (
			SELECT COALESCE(SUM(quantity), 0) AS filled_quantity,
			       SUM(quantity * price) / SUM(quantity) AS average_fill_price
			FROM trading_order_fills
			WHERE order_id = $1::uuid
		) AS aggregate
		WHERE trading_orders.id = $1::uuid
		RETURNING `+qualifiedOrderColumns,
		input.OrderID))
	if err != nil {
		return FillResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FillResult{}, err
	}
	return FillResult{Order: order, Fill: fill, Created: true}, nil
}

func (repository *PostgresOrderRepository) GetPortfolio(ctx context.Context, userID string) (Portfolio, error) {
	portfolio := Portfolio{UserID: userID, AccountMode: "unconfigured", Currency: "USD", Positions: make([]Position, 0)}
	var paperAccount bool
	if err := repository.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trading_paper_accounts WHERE user_id=$1::uuid AND status='active')`, userID).Scan(&paperAccount); err != nil {
		return Portfolio{}, err
	}
	if paperAccount {
		portfolio.AccountMode = "paper"
	}
	if err := repository.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity_delta) FILTER (
			WHERE leg_type = 'cash' AND asset = 'USD'
		), 0)::text,
		COALESCE(SUM(realized_pnl) FILTER (WHERE leg_type = 'security'), 0)::text
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid
	`, userID).Scan(&portfolio.CashBalance, &portfolio.RealizedPnL); err != nil {
		return Portfolio{}, err
	}
	if err := repository.db.QueryRow(ctx, `SELECT COALESCE(SUM(remaining_amount),0)::text FROM trading_order_reservations WHERE user_id=$1::uuid AND reservation_type='cash' AND asset='USD' AND status='active'`, userID).Scan(&portfolio.ReservedCash); err != nil {
		return Portfolio{}, err
	}
	cash, _ := signedOrZeroDecimalRat(portfolio.CashBalance)
	reservedCash, _ := signedOrZeroDecimalRat(portfolio.ReservedCash)
	portfolio.BuyingPower = new(big.Rat).Sub(cash, reservedCash).FloatString(8)
	rows, err := repository.db.Query(ctx, `
		SELECT asset, SUM(quantity_delta)::text, SUM(cost_delta)::text,
		       (SUM(cost_delta) / SUM(quantity_delta))::text,
		       SUM(realized_pnl)::text,
		       COALESCE((SELECT SUM(res.remaining_amount) FROM trading_order_reservations res WHERE res.user_id=$1::uuid AND res.reservation_type='security' AND res.asset=portfolio_ledger_entries.asset AND res.status='active'),0)::text,
		       (SUM(quantity_delta) - COALESCE((SELECT SUM(res.remaining_amount) FROM trading_order_reservations res WHERE res.user_id=$1::uuid AND res.reservation_type='security' AND res.asset=portfolio_ledger_entries.asset AND res.status='active'),0))::text
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid AND leg_type = 'security'
		GROUP BY asset
		HAVING SUM(quantity_delta) <> 0
		ORDER BY asset
	`, userID)
	if err != nil {
		return Portfolio{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var position Position
		if err := rows.Scan(&position.Symbol, &position.Quantity, &position.CostBasis,
			&position.AverageCost, &position.RealizedPnL, &position.ReservedQuantity,
			&position.AvailableQuantity); err != nil {
			return Portfolio{}, err
		}
		portfolio.Positions = append(portfolio.Positions, position)
	}
	return portfolio, rows.Err()
}

func (repository *PostgresOrderRepository) ListLedger(ctx context.Context, userID string, limit int) ([]LedgerEntry, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT id::text, user_id::text, source_type, source_id, leg_type, asset,
		       quantity_delta::text, value_delta::text, cost_delta::text,
		       realized_pnl::text, description, created_at
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]LedgerEntry, 0)
	for rows.Next() {
		entry, scanErr := scanLedgerEntry(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (repository *PostgresOrderRepository) AdjustCash(ctx context.Context, input CashAdjustmentInput) (CashAdjustmentResult, error) {
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CashAdjustmentResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.UserID); err != nil {
		return CashAdjustmentResult{}, err
	}

	var existingAmount, existingReason string
	existingErr := tx.QueryRow(ctx, `
		SELECT quantity_delta::text, description
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid AND source_type = 'cash_adjustment'
		  AND source_id = $2 AND leg_type = 'cash' AND asset = 'USD'
	`, input.UserID, input.ExternalID).Scan(&existingAmount, &existingReason)
	if existingErr == nil {
		existingRat, _ := signedDecimalRat(existingAmount)
		inputRat, _ := signedDecimalRat(input.Amount)
		if existingRat.Cmp(inputRat) != 0 || existingReason != input.Reason {
			return CashAdjustmentResult{}, errAdjustmentConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return CashAdjustmentResult{}, err
		}
		return repository.cashAdjustmentResult(ctx, input, false)
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return CashAdjustmentResult{}, existingErr
	}

	var cashRaw, reservedCashRaw string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity_delta), 0)::text
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid AND leg_type = 'cash' AND asset = 'USD'
	`, input.UserID).Scan(&cashRaw); err != nil {
		return CashAdjustmentResult{}, err
	}
	cash, _ := signedOrZeroDecimalRat(cashRaw)
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(remaining_amount),0)::text FROM trading_order_reservations WHERE user_id=$1::uuid AND reservation_type='cash' AND asset='USD' AND status='active'`, input.UserID).Scan(&reservedCashRaw); err != nil {
		return CashAdjustmentResult{}, err
	}
	reservedCash, _ := signedOrZeroDecimalRat(reservedCashRaw)
	amount, _ := signedDecimalRat(input.Amount)
	if new(big.Rat).Add(cash, amount).Cmp(reservedCash) < 0 {
		return CashAdjustmentResult{}, errInsufficientFunds
	}
	amountText := amount.FloatString(8)
	oppositeText := new(big.Rat).Neg(amount).FloatString(8)
	if _, err := tx.Exec(ctx, `
		INSERT INTO portfolio_ledger_entries
			(user_id, source_type, source_id, leg_type, asset,
			 quantity_delta, value_delta, description)
		VALUES
			($1::uuid, 'cash_adjustment', $2, 'cash', 'USD', $3::numeric, $3::numeric, $4),
			($1::uuid, 'cash_adjustment', $2, 'external', 'USD', $5::numeric, $5::numeric, $4)
	`, input.UserID, input.ExternalID, amountText, input.Reason, oppositeText); err != nil {
		return CashAdjustmentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CashAdjustmentResult{}, err
	}
	return repository.cashAdjustmentResult(ctx, input, true)
}

func (repository *PostgresOrderRepository) cashAdjustmentResult(
	ctx context.Context, input CashAdjustmentInput, created bool,
) (CashAdjustmentResult, error) {
	portfolio, err := repository.GetPortfolio(ctx, input.UserID)
	if err != nil {
		return CashAdjustmentResult{}, err
	}
	rows, err := repository.db.Query(ctx, `
		SELECT id::text, user_id::text, source_type, source_id, leg_type, asset,
		       quantity_delta::text, value_delta::text, cost_delta::text,
		       realized_pnl::text, description, created_at
		FROM portfolio_ledger_entries
		WHERE user_id = $1::uuid AND source_type = 'cash_adjustment' AND source_id = $2
		ORDER BY leg_type
	`, input.UserID, input.ExternalID)
	if err != nil {
		return CashAdjustmentResult{}, err
	}
	defer rows.Close()
	entries := make([]LedgerEntry, 0, 2)
	for rows.Next() {
		entry, scanErr := scanLedgerEntry(rows)
		if scanErr != nil {
			return CashAdjustmentResult{}, scanErr
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return CashAdjustmentResult{}, err
	}
	return CashAdjustmentResult{Portfolio: portfolio, Entries: entries, Created: created}, nil
}

func (repository *PostgresOrderRepository) ListFills(ctx context.Context, userID, orderID string) ([]Fill, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT fills.id::text, fills.order_id::text, fills.execution_id,
		       fills.quantity::text, fills.price::text, fills.executed_at, fills.created_at
		FROM trading_order_fills AS fills
		JOIN trading_orders AS orders ON orders.id = fills.order_id
		WHERE fills.order_id = $1::uuid AND orders.user_id = $2::uuid
		ORDER BY fills.executed_at, fills.id
	`, orderID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fills := make([]Fill, 0)
	for rows.Next() {
		fill, scanErr := scanFill(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		fills = append(fills, fill)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(fills) == 0 {
		if _, err := repository.Get(ctx, userID, orderID); err != nil {
			return nil, err
		}
	}
	return fills, nil
}

func (repository *PostgresOrderRepository) ListEvents(ctx context.Context, userID, orderID string, limit int) ([]OrderEvent, error) {
	var owns bool
	if err := repository.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trading_orders WHERE id=$1::uuid AND user_id=$2::uuid)`, orderID, userID).Scan(&owns); err != nil {
		return nil, err
	}
	if !owns {
		return nil, errOrderNotFound
	}
	rows, err := repository.db.Query(ctx, `SELECT id,order_id::text,event_type,payload,created_at FROM trading_order_events WHERE order_id=$1::uuid ORDER BY created_at,id LIMIT $2`, orderID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]OrderEvent, 0)
	for rows.Next() {
		var event OrderEvent
		if err := rows.Scan(&event.ID, &event.OrderID, &event.EventType, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type orderScanner interface {
	Scan(...any) error
}

func scanOrder(scanner orderScanner) (Order, error) {
	var order Order
	err := scanner.Scan(
		&order.ID,
		&order.UserID,
		&order.ClientOrderID,
		&order.Symbol,
		&order.Side,
		&order.Type,
		&order.Quantity,
		&order.LimitPrice,
		&order.Status,
		&order.FilledQuantity,
		&order.AverageFillPrice,
		&order.CreatedAt,
		&order.UpdatedAt,
		&order.CanceledAt,
		&order.RejectionReason,
		&order.RejectedAt,
	)
	return order, err
}

func scanFill(scanner orderScanner) (Fill, error) {
	var fill Fill
	err := scanner.Scan(&fill.ID, &fill.OrderID, &fill.ExecutionID, &fill.Quantity,
		&fill.Price, &fill.ExecutedAt, &fill.CreatedAt)
	return fill, err
}

func scanLedgerEntry(scanner orderScanner) (LedgerEntry, error) {
	var entry LedgerEntry
	err := scanner.Scan(&entry.ID, &entry.UserID, &entry.SourceType, &entry.SourceID,
		&entry.LegType, &entry.Asset, &entry.QuantityDelta, &entry.ValueDelta,
		&entry.CostDelta, &entry.RealizedPnL, &entry.Description, &entry.CreatedAt)
	return entry, err
}
