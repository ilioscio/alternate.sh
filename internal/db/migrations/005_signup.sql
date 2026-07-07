-- Public self-signup: confirmed users gain an email; unconfirmed signups live
-- in pending_accounts until they confirm via link or code.

ALTER TABLE users ADD COLUMN IF NOT EXISTS email VARCHAR(254) NOT NULL DEFAULT '';

-- Unique email among confirmed accounts (blank allowed for legacy/admin users).
CREATE UNIQUE INDEX IF NOT EXISTS users_email_idx
    ON users (lower(email)) WHERE email <> '';

CREATE TABLE IF NOT EXISTS pending_accounts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(32) NOT NULL,
    email         VARCHAR(254) NOT NULL,
    password_hash TEXT NOT NULL,
    token         UUID NOT NULL DEFAULT gen_random_uuid(),
    code          VARCHAR(12) NOT NULL,
    attempts      INT NOT NULL DEFAULT 0,
    from_ip       TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at    TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

-- One pending signup per username (case-insensitive).
CREATE UNIQUE INDEX IF NOT EXISTS pending_username_idx ON pending_accounts (lower(username));
CREATE INDEX IF NOT EXISTS pending_token_idx   ON pending_accounts (token);
CREATE INDEX IF NOT EXISTS pending_expires_idx ON pending_accounts (expires_at);

INSERT INTO schema_migrations (version) VALUES (5) ON CONFLICT DO NOTHING;
