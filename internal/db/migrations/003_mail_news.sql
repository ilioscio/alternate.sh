-- Add calendar to user profile
ALTER TABLE users ADD COLUMN IF NOT EXISTS calendar TEXT NOT NULL DEFAULT '';

-- Internal mail
CREATE TABLE IF NOT EXISTS mail (
    id                   UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id            UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject              VARCHAR(512) NOT NULL DEFAULT '(no subject)',
    body                 TEXT         NOT NULL DEFAULT '',
    in_reply_to          UUID         REFERENCES mail(id) ON DELETE SET NULL,
    read_at              TIMESTAMPTZ,
    deleted_by_recipient BOOLEAN      NOT NULL DEFAULT false,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mail_recipient_idx ON mail (recipient_id, created_at DESC);
CREATE INDEX IF NOT EXISTS mail_sender_idx    ON mail (sender_id,    created_at DESC);

-- Prevent duplicate vacation auto-replies within 7 days
CREATE TABLE IF NOT EXISTS vacation_replies (
    vacationer_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (vacationer_id, sender_id)
);

-- Newsgroups
CREATE TABLE IF NOT EXISTS newsgroups (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) UNIQUE NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    moderated   BOOLEAN      NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Articles (parent_id IS NULL = root post; set = reply)
CREATE TABLE IF NOT EXISTS articles (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    newsgroup_id UUID         NOT NULL REFERENCES newsgroups(id) ON DELETE CASCADE,
    author_id    UUID         NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    subject      VARCHAR(512) NOT NULL,
    body         TEXT         NOT NULL,
    parent_id    UUID         REFERENCES articles(id) ON DELETE SET NULL,
    cancelled    BOOLEAN      NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS articles_group_idx  ON articles (newsgroup_id, created_at);
CREATE INDEX IF NOT EXISTS articles_parent_idx ON articles (parent_id);

-- Per-user read tracking
CREATE TABLE IF NOT EXISTS article_reads (
    user_id    UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    article_id UUID NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, article_id)
);

-- Seed default newsgroups
INSERT INTO newsgroups (name, description) VALUES
    ('alt.announce',         'System announcements'),
    ('alt.chat',             'General off-topic discussion'),
    ('alt.dreams.computing', 'The alternate history of the net')
ON CONFLICT (name) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES (3) ON CONFLICT DO NOTHING;
