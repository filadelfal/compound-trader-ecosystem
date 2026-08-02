CREATE TABLE IF NOT EXISTS trading_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    client_order_id VARCHAR(100) NOT NULL,
    symbol VARCHAR(20) NOT NULL,
    side VARCHAR(4) NOT NULL CHECK (side IN ('buy', 'sell')),
    order_type VARCHAR(6) NOT NULL CHECK (order_type IN ('market', 'limit')),
    quantity NUMERIC(28, 8) NOT NULL CHECK (quantity > 0),
    limit_price NUMERIC(28, 8),
    status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'partially_filled', 'filled', 'canceled', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    canceled_at TIMESTAMPTZ,
    CONSTRAINT trading_orders_limit_price_check CHECK (
        (order_type = 'market' AND limit_price IS NULL) OR
        (order_type = 'limit' AND limit_price > 0)
    ),
    CONSTRAINT trading_orders_user_client_order_unique UNIQUE (user_id, client_order_id)
);

CREATE INDEX IF NOT EXISTS idx_trading_orders_user_created_at
    ON trading_orders (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_trading_orders_pending
    ON trading_orders (status, created_at)
    WHERE status IN ('pending', 'partially_filled');
