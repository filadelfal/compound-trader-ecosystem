CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE SCHEMA IF NOT EXISTS compound;

CREATE OR REPLACE FUNCTION compound.set_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

CREATE TABLE IF NOT EXISTS compound.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    email VARCHAR(320) NOT NULL,
    password_hash TEXT NOT NULL,
    display_name VARCHAR(150),

    status VARCHAR(30) NOT NULL DEFAULT 'pending_verification'
        CHECK (
            status IN (
                'pending_verification',
                'active',
                'suspended',
                'locked',
                'disabled'
            )
        ),

    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    email_verified_at TIMESTAMPTZ,

    failed_login_attempts INTEGER NOT NULL DEFAULT 0
        CHECK (failed_login_attempts >= 0),

    locked_until TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ,
    password_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email_lower
    ON compound.users (LOWER(email))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_users_status
    ON compound.users (status);

CREATE INDEX IF NOT EXISTS idx_users_created_at
    ON compound.users (created_at DESC);

DROP TRIGGER IF EXISTS trg_users_updated_at
    ON compound.users;

CREATE TRIGGER trg_users_updated_at
BEFORE UPDATE ON compound.users
FOR EACH ROW
EXECUTE FUNCTION compound.set_updated_at();


CREATE TABLE IF NOT EXISTS compound.user_profiles (
    user_id UUID PRIMARY KEY
        REFERENCES compound.users(id)
        ON DELETE CASCADE,

    first_name VARCHAR(100),
    last_name VARCHAR(100),
    phone_number VARCHAR(40),
    country_code VARCHAR(2),
    timezone VARCHAR(100) NOT NULL DEFAULT 'UTC',
    locale VARCHAR(20) NOT NULL DEFAULT 'en',
    avatar_url TEXT,
    bio TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS trg_user_profiles_updated_at
    ON compound.user_profiles;

CREATE TRIGGER trg_user_profiles_updated_at
BEFORE UPDATE ON compound.user_profiles
FOR EACH ROW
EXECUTE FUNCTION compound.set_updated_at();


CREATE TABLE IF NOT EXISTS compound.roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO compound.roles (name, description)
VALUES
    ('user', 'Standard Compound Trader user'),
    ('trader', 'Verified trading platform user'),
    ('analyst', 'Market analyst'),
    ('support', 'Customer support operator'),
    ('admin', 'Platform administrator'),
    ('super_admin', 'Full platform administrator')
ON CONFLICT (name) DO NOTHING;


CREATE TABLE IF NOT EXISTS compound.user_roles (
    user_id UUID NOT NULL
        REFERENCES compound.users(id)
        ON DELETE CASCADE,

    role_id UUID NOT NULL
        REFERENCES compound.roles(id)
        ON DELETE RESTRICT,

    assigned_by UUID
        REFERENCES compound.users(id)
        ON DELETE SET NULL,

    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_role_id
    ON compound.user_roles (role_id);


CREATE TABLE IF NOT EXISTS compound.refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES compound.users(id)
        ON DELETE CASCADE,

    token_hash TEXT NOT NULL UNIQUE,
    token_family UUID NOT NULL DEFAULT gen_random_uuid(),

    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    replaced_by_token_id UUID
        REFERENCES compound.refresh_tokens(id)
        ON DELETE SET NULL,

    ip_address INET,
    user_agent TEXT,

    CHECK (expires_at > issued_at)
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id
    ON compound.refresh_tokens (user_id);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family
    ON compound.refresh_tokens (token_family);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at
    ON compound.refresh_tokens (expires_at);


CREATE TABLE IF NOT EXISTS compound.email_verification_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES compound.users(id)
        ON DELETE CASCADE,

    token_hash TEXT NOT NULL UNIQUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,

    CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS idx_email_verification_user_id
    ON compound.email_verification_tokens (user_id);


CREATE TABLE IF NOT EXISTS compound.password_reset_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES compound.users(id)
        ON DELETE CASCADE,

    token_hash TEXT NOT NULL UNIQUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,

    requested_ip INET,

    CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS idx_password_reset_user_id
    ON compound.password_reset_tokens (user_id);


CREATE TABLE IF NOT EXISTS compound.login_attempts (
    id BIGSERIAL PRIMARY KEY,

    user_id UUID
        REFERENCES compound.users(id)
        ON DELETE SET NULL,

    attempted_email VARCHAR(320),
    successful BOOLEAN NOT NULL DEFAULT FALSE,
    failure_reason VARCHAR(100),

    ip_address INET,
    user_agent TEXT,

    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_login_attempts_user_id
    ON compound.login_attempts (user_id);

CREATE INDEX IF NOT EXISTS idx_login_attempts_email_time
    ON compound.login_attempts (LOWER(attempted_email), attempted_at DESC);

CREATE INDEX IF NOT EXISTS idx_login_attempts_ip_time
    ON compound.login_attempts (ip_address, attempted_at DESC);


CREATE TABLE IF NOT EXISTS compound.audit_logs (
    id BIGSERIAL PRIMARY KEY,

    user_id UUID
        REFERENCES compound.users(id)
        ON DELETE SET NULL,

    actor_user_id UUID
        REFERENCES compound.users(id)
        ON DELETE SET NULL,

    event_type VARCHAR(150) NOT NULL,
    entity_type VARCHAR(100),
    entity_id UUID,

    ip_address INET,
    user_agent TEXT,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id
    ON compound.audit_logs (user_id);

CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_user_id
    ON compound.audit_logs (actor_user_id);

CREATE INDEX IF NOT EXISTS idx_audit_logs_event_type
    ON compound.audit_logs (event_type);

CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at
    ON compound.audit_logs (created_at DESC);


CREATE TABLE IF NOT EXISTS compound.user_preferences (
    user_id UUID PRIMARY KEY
        REFERENCES compound.users(id)
        ON DELETE CASCADE,

    theme VARCHAR(20) NOT NULL DEFAULT 'system'
        CHECK (theme IN ('light', 'dark', 'system')),

    notifications_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    email_notifications_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    trading_alerts_enabled BOOLEAN NOT NULL DEFAULT TRUE,

    preferences JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS trg_user_preferences_updated_at
    ON compound.user_preferences;

CREATE TRIGGER trg_user_preferences_updated_at
BEFORE UPDATE ON compound.user_preferences
FOR EACH ROW
EXECUTE FUNCTION compound.set_updated_at();
