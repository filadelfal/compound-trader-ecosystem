ALTER TABLE portfolio_ledger_entries
    ADD COLUMN IF NOT EXISTS cost_delta NUMERIC(36, 8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS realized_pnl NUMERIC(36, 8) NOT NULL DEFAULT 0;

COMMENT ON COLUMN portfolio_ledger_entries.cost_delta IS
    'Signed book-cost change for security legs; zero for cash/external legs.';
COMMENT ON COLUMN portfolio_ledger_entries.realized_pnl IS
    'Realized profit/loss recognized by a sell fill; zero for other legs.';
