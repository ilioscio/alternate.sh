-- Phase 7.1: community polish (DESIGN.md §5.4, §5.8, §10.5) —
-- fortune review queue, mailing lists, moderated-group approval queue,
-- ban system, admin audit log.

-- ── Fortunes: review queue ────────────────────────────────────────────────────
-- Existing rows (the seeds) are grandfathered in as approved.
ALTER TABLE fortunes ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'approved';
CREATE INDEX IF NOT EXISTS fortunes_status_idx ON fortunes (status);

-- ── Mailing lists ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS mailing_lists (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(32)  UNIQUE NOT NULL, -- shares the username namespace (guarded in code)
    description     TEXT         NOT NULL DEFAULT '',
    admin_only_post BOOLEAN      NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS list_members (
    list_id UUID NOT NULL REFERENCES mailing_lists(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id)         ON DELETE CASCADE,
    PRIMARY KEY (list_id, user_id)
);

-- ── Moderated groups: approval queue ─────────────────────────────────────────
-- Articles are approved by default; a non-admin post into a moderated group
-- lands unapproved and stays invisible (and unfederated) until reviewed.
ALTER TABLE articles ADD COLUMN IF NOT EXISTS approved BOOLEAN NOT NULL DEFAULT true;
CREATE INDEX IF NOT EXISTS articles_pending_idx ON articles (approved) WHERE NOT approved;

-- ── Ban system ────────────────────────────────────────────────────────────────
ALTER TABLE users ADD COLUMN IF NOT EXISTS banned     BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS ban_reason TEXT    NOT NULL DEFAULT '';

-- ── Admin audit log ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id   UUID        REFERENCES users(id) ON DELETE SET NULL,
    action     TEXT        NOT NULL, -- e.g. 'ban', 'unban', 'article.approve', 'fortune.reject'
    target     TEXT        NOT NULL DEFAULT '', -- who/what it hit
    detail     TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS audit_log_time_idx ON audit_log (created_at DESC);

INSERT INTO schema_migrations (version) VALUES (9) ON CONFLICT DO NOTHING;
