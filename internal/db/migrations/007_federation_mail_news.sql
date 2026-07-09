-- Federated mail & news (DESIGN.md §8.4).
--
-- Remote parties don't exist in the local users table, so both mail senders
-- and article authors gain a nullable local reference plus a qualified
-- remote address ("user@host"); exactly one of the two must be present.
-- MAILER-DAEMON bounces also use the remote form (sender_id NULL).

-- ── Mail: remote senders ─────────────────────────────────────────────────────
ALTER TABLE mail ALTER COLUMN sender_id DROP NOT NULL;
ALTER TABLE mail ADD COLUMN IF NOT EXISTS remote_sender TEXT;
ALTER TABLE mail ADD CONSTRAINT mail_sender_present
    CHECK (sender_id IS NOT NULL OR remote_sender IS NOT NULL);

-- ── Mail: store-and-forward outbox for cross-node delivery ──────────────────
CREATE TABLE IF NOT EXISTS mail_outbox (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id    UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    peer_node    TEXT         NOT NULL,           -- destination node name
    remote_user  TEXT         NOT NULL,           -- recipient username on that node
    subject      VARCHAR(512) NOT NULL,
    body         TEXT         NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    next_attempt TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    attempts     INT          NOT NULL DEFAULT 0,
    last_error   TEXT         NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS mail_outbox_due_idx ON mail_outbox (next_attempt);

-- ── Mail: vacation auto-reply dedupe for remote senders ─────────────────────
CREATE TABLE IF NOT EXISTS vacation_replies_remote (
    vacationer_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_addr   TEXT        NOT NULL,            -- "user@host"
    sent_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (vacationer_id, sender_addr)
);

-- ── News: remote authors + federation identity ──────────────────────────────
ALTER TABLE articles ALTER COLUMN author_id DROP NOT NULL;
ALTER TABLE articles ADD COLUMN IF NOT EXISTS remote_author TEXT;
ALTER TABLE articles ADD CONSTRAINT articles_author_present
    CHECK (author_id IS NOT NULL OR remote_author IS NOT NULL);

-- Origin identity: which node authored the article and its id there.
-- NULL origin_node = authored locally. Deduplicates push-vs-sync overlap and
-- scopes remote cancels to the origin node.
ALTER TABLE articles ADD COLUMN IF NOT EXISTS origin_node TEXT;
ALTER TABLE articles ADD COLUMN IF NOT EXISTS origin_id UUID;
CREATE UNIQUE INDEX IF NOT EXISTS articles_origin_idx
    ON articles (origin_node, origin_id) WHERE origin_node IS NOT NULL;

-- updated_at drives catch-up sync (NEWS_SINCE): it bumps on cancel too, so a
-- peer that was down picks up cancellations, not just new posts.
ALTER TABLE articles ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS articles_updated_idx ON articles (updated_at);

-- ── News: per-peer catch-up high-water marks ─────────────────────────────────
-- The mark is the peer's own updated_at clock, so no cross-node clock
-- comparison ever happens.
CREATE TABLE IF NOT EXISTS news_sync_state (
    peer_node      TEXT        PRIMARY KEY,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch'
);

INSERT INTO schema_migrations (version) VALUES (7) ON CONFLICT DO NOTHING;
