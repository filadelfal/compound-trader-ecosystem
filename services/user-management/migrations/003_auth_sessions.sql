BEGIN;

CREATE TABLE IF NOT EXISTS user_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    status VARCHAR(24) NOT NULL
        CHECK (
            status IN (
                'active',
                'revoked',
                'expired',
                'compromised'
            )
        ),
    device_id VARCHAR(255),
    device_name VARCHAR(255),
    browser VARCHAR(255),
    operating_system VARCHAR(255),
    ip_address INET,
    user_agent TEXT,
    country_code CHAR(2),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(255)
);

CREATE INDEX IF NOT EXISTS
    idx_user_sessions_user_status
ON user_sessions (user_id, status);

CREATE INDEX IF NOT EXISTS
    idx_user_sessions_expires_at
ON user_sessions (expires_at);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL
        REFERENCES user_sessions(id)
        ON DELETE CASCADE,
    user_id UUID NOT NULL,
    token_hash CHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL
        CHECK (
            status IN (
                'active',
                'rotated',
                'revoked',
                'expired'
            )
        ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    rotated_at TIMESTAMPTZ,
    replaced_by_token_id UUID,
    revoked_at TIMESTAMPTZ,

    CONSTRAINT refresh_tokens_replacement_fk
        FOREIGN KEY (replaced_by_token_id)
        REFERENCES refresh_tokens(id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX IF NOT EXISTS
    uq_refresh_tokens_hash
ON refresh_tokens (token_hash);

CREATE INDEX IF NOT EXISTS
    idx_refresh_tokens_session_status
ON refresh_tokens (session_id, status);

CREATE INDEX IF NOT EXISTS
    idx_refresh_tokens_user_status
ON refresh_tokens (user_id, status);

CREATE INDEX IF NOT EXISTS
    idx_refresh_tokens_expires_at
ON refresh_tokens (expires_at);

CREATE TABLE IF NOT EXISTS authentication_security_events (
    id UUID PRIMARY KEY,
    user_id UUID,
    session_id UUID,
    event_type VARCHAR(100) NOT NULL,
    severity VARCHAR(20) NOT NULL
        CHECK (
            severity IN (
                'info',
                'warning',
                'critical'
            )
        ),
    ip_address INET,
    user_agent TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS
    idx_auth_security_events_user_created
ON authentication_security_events (
    user_id,
    created_at DESC
);

CREATE INDEX IF NOT EXISTS
    idx_auth_security_events_type_created
ON authentication_security_events (
    event_type,
    created_at DESC
);

COMMIT;

