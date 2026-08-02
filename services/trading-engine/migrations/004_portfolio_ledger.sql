CREATE TABLE IF NOT EXISTS portfolio_ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    source_type VARCHAR(20) NOT NULL CHECK (source_type IN ('fill', 'cash_adjustment')),
    source_id VARCHAR(100) NOT NULL,
    leg_type VARCHAR(20) NOT NULL CHECK (leg_type IN ('cash', 'security', 'external')),
    asset VARCHAR(32) NOT NULL,
    quantity_delta NUMERIC(36, 8) NOT NULL CHECK (quantity_delta <> 0),
    value_delta NUMERIC(36, 8) NOT NULL CHECK (value_delta <> 0),
    description VARCHAR(250) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT portfolio_ledger_source_leg_unique
        UNIQUE (user_id, source_type, source_id, leg_type, asset)
);

CREATE INDEX IF NOT EXISTS idx_portfolio_ledger_user_created_at
    ON portfolio_ledger_entries (user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_portfolio_ledger_user_asset
    ON portfolio_ledger_entries (user_id, leg_type, asset);
