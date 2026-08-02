CREATE TABLE IF NOT EXISTS trading_risk_profiles (
    user_id UUID PRIMARY KEY,
    trading_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    max_open_orders INTEGER NOT NULL DEFAULT 25
        CHECK (max_open_orders BETWEEN 1 AND 100),
    max_gross_exposure NUMERIC(28,8) NOT NULL DEFAULT 250000
        CHECK (max_gross_exposure > 0 AND max_gross_exposure <= 10000000),
    max_daily_loss NUMERIC(28,8) NOT NULL DEFAULT 10000
        CHECK (max_daily_loss > 0 AND max_daily_loss <= 1000000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO trading_risk_profiles(user_id)
SELECT user_id FROM trading_paper_accounts
ON CONFLICT(user_id) DO NOTHING;
