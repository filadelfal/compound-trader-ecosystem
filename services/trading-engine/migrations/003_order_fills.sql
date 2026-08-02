ALTER TABLE trading_orders
    ADD COLUMN IF NOT EXISTS filled_quantity NUMERIC(28, 8) NOT NULL DEFAULT 0
        CHECK (filled_quantity >= 0 AND filled_quantity <= quantity),
    ADD COLUMN IF NOT EXISTS average_fill_price NUMERIC(28, 8)
        CHECK (average_fill_price IS NULL OR average_fill_price > 0);

CREATE TABLE IF NOT EXISTS trading_order_fills (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES trading_orders(id) ON DELETE RESTRICT,
    execution_id VARCHAR(100) NOT NULL,
    quantity NUMERIC(28, 8) NOT NULL CHECK (quantity > 0),
    price NUMERIC(28, 8) NOT NULL CHECK (price > 0),
    executed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT trading_order_fills_execution_unique UNIQUE (order_id, execution_id)
);

CREATE INDEX IF NOT EXISTS idx_trading_order_fills_order_executed_at
    ON trading_order_fills (order_id, executed_at, id);
