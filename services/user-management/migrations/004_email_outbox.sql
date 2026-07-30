CREATE TABLE IF NOT EXISTS compound.email_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    recipient VARCHAR(320) NOT NULL,
    subject TEXT NOT NULL,
    text_body TEXT NOT NULL,
    html_body TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_email_outbox_delivery
    ON compound.email_outbox (available_at, created_at)
    WHERE status IN ('pending', 'failed', 'processing');

DROP TRIGGER IF EXISTS trg_email_outbox_updated_at
    ON compound.email_outbox;

CREATE TRIGGER trg_email_outbox_updated_at
BEFORE UPDATE ON compound.email_outbox
FOR EACH ROW
EXECUTE FUNCTION compound.set_updated_at();
