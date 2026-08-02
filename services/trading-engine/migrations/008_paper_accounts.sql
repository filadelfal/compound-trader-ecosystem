ALTER TABLE portfolio_ledger_entries
    DROP CONSTRAINT IF EXISTS portfolio_ledger_entries_source_type_check;
ALTER TABLE portfolio_ledger_entries
    ADD CONSTRAINT portfolio_ledger_entries_source_type_check
    CHECK (source_type IN ('fill', 'cash_adjustment', 'paper_account'));

CREATE TABLE IF NOT EXISTS trading_paper_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    starting_cash NUMERIC(28, 8) NOT NULL CHECK (starting_cash > 0),
    status VARCHAR(12) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
    activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_paper_accounts_user
    ON trading_paper_accounts (user_id);
