ALTER TABLE trading_orders
    ADD COLUMN IF NOT EXISTS rejection_reason TEXT,
    ADD COLUMN IF NOT EXISTS rejected_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS trading_order_dispatches (
    order_id UUID PRIMARY KEY REFERENCES trading_orders(id) ON DELETE CASCADE,
    venue VARCHAR(30) NOT NULL DEFAULT 'paper',
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trading_order_dispatches_ready
    ON trading_order_dispatches (available_at, created_at)
    WHERE status IN ('pending', 'retry', 'processing');

CREATE OR REPLACE FUNCTION enqueue_paper_order_dispatch()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO trading_order_dispatches (order_id) VALUES (NEW.id)
    ON CONFLICT (order_id) DO NOTHING;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_enqueue_paper_order_dispatch ON trading_orders;
CREATE TRIGGER trg_enqueue_paper_order_dispatch
AFTER INSERT ON trading_orders
FOR EACH ROW EXECUTE FUNCTION enqueue_paper_order_dispatch();

CREATE OR REPLACE FUNCTION close_terminal_order_dispatch()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    UPDATE trading_order_dispatches
    SET status = 'completed', completed_at = COALESCE(completed_at, NOW()),
        locked_at = NULL, updated_at = NOW()
    WHERE order_id = NEW.id AND status <> 'failed';
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_close_terminal_order_dispatch ON trading_orders;
CREATE TRIGGER trg_close_terminal_order_dispatch
AFTER UPDATE OF status ON trading_orders
FOR EACH ROW
WHEN (NEW.status IN ('filled', 'canceled', 'rejected'))
EXECUTE FUNCTION close_terminal_order_dispatch();

INSERT INTO trading_order_dispatches (order_id)
SELECT id FROM trading_orders WHERE status IN ('pending', 'partially_filled')
ON CONFLICT (order_id) DO NOTHING;

UPDATE trading_order_dispatches dispatch
SET status = 'completed', completed_at = COALESCE(completed_at, NOW()),
    locked_at = NULL, updated_at = NOW()
FROM trading_orders orders
WHERE orders.id = dispatch.order_id
  AND orders.status IN ('filled', 'canceled', 'rejected')
  AND dispatch.status <> 'failed';
