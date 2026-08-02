CREATE TABLE IF NOT EXISTS market_quotes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider VARCHAR(50) NOT NULL,
    external_id VARCHAR(100) NOT NULL,
    symbol VARCHAR(20) NOT NULL,
    bid NUMERIC(28, 8) NOT NULL CHECK (bid >= 0),
    ask NUMERIC(28, 8) NOT NULL CHECK (ask >= 0 AND ask >= bid),
    last NUMERIC(28, 8) NOT NULL CHECK (last >= 0),
    currency CHAR(3) NOT NULL,
    as_of TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT market_quotes_provider_external_unique UNIQUE (provider, external_id)
);

CREATE INDEX IF NOT EXISTS idx_market_quotes_symbol_as_of
    ON market_quotes (symbol, as_of DESC, received_at DESC);
