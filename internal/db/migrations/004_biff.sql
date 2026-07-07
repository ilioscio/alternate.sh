-- Biff-style new-mail notifications: per-user toggle, on by default.
ALTER TABLE users ADD COLUMN IF NOT EXISTS biff BOOLEAN NOT NULL DEFAULT true;

INSERT INTO schema_migrations (version) VALUES (4) ON CONFLICT DO NOTHING;
