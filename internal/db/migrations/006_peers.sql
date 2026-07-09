-- Federation peer registry. Each row is another alternate.sh node we federate
-- with: its ASSP identity (node name), where to dial it, and the shared secret
-- used for the mutual HMAC handshake. Peering is bilateral — both admins add
-- each other with the same secret.

CREATE TABLE IF NOT EXISTS peers (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node     VARCHAR(255) UNIQUE NOT NULL,  -- ASSP node identity (usually the hostname)
    address  TEXT NOT NULL DEFAULT '',      -- host:port to dial; empty = node + default port
    secret   TEXT NOT NULL,                 -- shared peering secret (HMAC)
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES (6) ON CONFLICT DO NOTHING;
