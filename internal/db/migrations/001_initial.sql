CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username         VARCHAR(32) UNIQUE NOT NULL,
    display_name     VARCHAR(128) NOT NULL DEFAULT '',
    password_hash    VARCHAR(256) NOT NULL DEFAULT '',
    office           VARCHAR(128) NOT NULL DEFAULT '',
    home_phone       VARCHAR(64)  NOT NULL DEFAULT '',
    plan             TEXT        NOT NULL DEFAULT '',
    project          VARCHAR(256) NOT NULL DEFAULT '',
    signature        TEXT        NOT NULL DEFAULT '',
    public_page      TEXT        NOT NULL DEFAULT '',
    mesg_on          BOOLEAN     NOT NULL DEFAULT true,
    vacation         BOOLEAN     NOT NULL DEFAULT false,
    vacation_message TEXT        NOT NULL DEFAULT '',
    hush_login       BOOLEAN     NOT NULL DEFAULT false,
    admin            BOOLEAN     NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login       TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS ssh_keys (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_type  VARCHAR(64) NOT NULL,
    key_data  TEXT        NOT NULL,
    comment   VARCHAR(256) NOT NULL DEFAULT '',
    added_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS login_history (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(32) NOT NULL,
    tty           VARCHAR(32) NOT NULL,
    from_addr     VARCHAR(256) NOT NULL DEFAULT '',
    logged_in_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    logged_out_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS system_messages (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    body       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS user_message_reads (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES system_messages(id) ON DELETE CASCADE,
    read_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, message_id)
);

CREATE TABLE IF NOT EXISTS fortunes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    body       TEXT NOT NULL,
    added_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS motd (
    id         INTEGER PRIMARY KEY DEFAULT 1,
    body       TEXT NOT NULL DEFAULT 'Welcome to alternate.sh',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO motd (id, body) VALUES (1, 'Welcome to alternate.sh')
    ON CONFLICT DO NOTHING;

INSERT INTO fortunes (body) VALUES
    ('The best way to predict the future is to invent it. — Alan Kay'),
    ('The network is the computer. — Sun Microsystems'),
    ('Unix is simple. It just takes a genius to understand its simplicity. — Dennis Ritchie'),
    ('Those who do not understand Unix are condemned to reinvent it, poorly. — Henry Spencer'),
    ('Real programmers use butterflies. — xkcd'),
    ('There is no cloud, only other people''s computers.'),
    ('rm -rf / is not a recovery strategy.'),
    ('Have you tried turning it off and on again?'),
    ('640K ought to be enough for anybody. (attributed, likely apocryphal)'),
    ('The internet is a series of tubes. — Ted Stevens'),
    ('Please do not attempt to start the time machine while the guard is down.'),
    ('As far as we know, our computer has never had an undetected error.')
    ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES (1) ON CONFLICT DO NOTHING;
