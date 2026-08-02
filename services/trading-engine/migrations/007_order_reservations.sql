CREATE TABLE IF NOT EXISTS trading_order_reservations (
    order_id UUID PRIMARY KEY REFERENCES trading_orders(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    reservation_type VARCHAR(10) NOT NULL CHECK (reservation_type IN ('cash', 'security')),
    asset VARCHAR(20) NOT NULL,
    original_amount NUMERIC(28, 8) NOT NULL CHECK (original_amount > 0),
    remaining_amount NUMERIC(28, 8) NOT NULL CHECK (remaining_amount >= 0),
    status VARCHAR(12) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'consumed', 'released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    released_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_order_reservations_available
    ON trading_order_reservations (user_id, reservation_type, asset)
    WHERE status = 'active';

CREATE OR REPLACE FUNCTION settle_terminal_order_reservation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'filled' THEN
        UPDATE trading_order_reservations
        SET status = 'consumed', remaining_amount = 0, updated_at = NOW()
        WHERE order_id = NEW.id AND status = 'active';
    ELSIF NEW.status IN ('canceled', 'rejected') THEN
        UPDATE trading_order_reservations
        SET status = 'released', remaining_amount = 0,
            released_at = NOW(), updated_at = NOW()
        WHERE order_id = NEW.id AND status = 'active';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_settle_terminal_order_reservation ON trading_orders;
CREATE TRIGGER trg_settle_terminal_order_reservation
AFTER UPDATE OF status ON trading_orders
FOR EACH ROW
WHEN (NEW.status IN ('filled', 'canceled', 'rejected'))
EXECUTE FUNCTION settle_terminal_order_reservation();

-- Open orders created by an older binary have no reservation and cannot be
-- allowed to compete with reservation-aware orders after this migration.
UPDATE trading_orders orders
SET status = 'rejected',
    rejection_reason = 'order requires resubmission after reservation upgrade',
    rejected_at = NOW(), updated_at = NOW()
WHERE orders.status IN ('pending', 'partially_filled')
  AND NOT EXISTS (
      SELECT 1 FROM trading_order_reservations reservations
      WHERE reservations.order_id = orders.id
  );
