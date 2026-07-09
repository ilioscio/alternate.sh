-- Door games framework (DESIGN.md §5.9).

-- Shared event-style score log: games append rows (a wumpus win, a chess
-- victory) and compute leaderboards from aggregates. Games with richer
-- economies (trade) keep their own tables and derive standings live.
CREATE TABLE IF NOT EXISTS game_scores (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    game        TEXT        NOT NULL,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL DEFAULT 'win',
    value       BIGINT      NOT NULL DEFAULT 1,
    achieved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS game_scores_game_idx ON game_scores (game, kind, user_id);

-- Chess: one row per game; moves in coordinate notation, space-separated.
CREATE TABLE IF NOT EXISTS chess_games (
    id         BIGSERIAL   PRIMARY KEY,          -- small, human-readable game numbers
    white_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    black_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    moves      TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL DEFAULT 'pending', -- pending|active|checkmate|stalemate|draw|resigned|declined
    winner_id  UUID        REFERENCES users(id) ON DELETE SET NULL,
    draw_offer UUID        REFERENCES users(id) ON DELETE SET NULL, -- who currently offers a draw
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS chess_games_white_idx ON chess_games (white_id, status);
CREATE INDEX IF NOT EXISTS chess_games_black_idx ON chess_games (black_id, status);

-- Trade: the persistent universe. Generated once at first run (seeded),
-- then owned by play.
CREATE TABLE IF NOT EXISTS trade_sectors (
    id     INT  PRIMARY KEY,           -- sector number, 1..N
    warps  INT[] NOT NULL              -- adjacent sector numbers
);

CREATE TABLE IF NOT EXISTS trade_ports (
    sector_id  INT  PRIMARY KEY REFERENCES trade_sectors(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- Stock per commodity; price is derived from stock level.
    ore        INT  NOT NULL,
    organics   INT  NOT NULL,
    equipment  INT  NOT NULL,
    -- Whether the port buys (true) or sells (false) each commodity.
    buys_ore       BOOLEAN NOT NULL,
    buys_organics  BOOLEAN NOT NULL,
    buys_equipment BOOLEAN NOT NULL,
    -- Lazy stock regeneration: drift toward equilibrium since last touch.
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trade_players (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    sector_id  INT  NOT NULL REFERENCES trade_sectors(id),
    credits    BIGINT NOT NULL,
    holds      INT  NOT NULL,
    ore        INT  NOT NULL DEFAULT 0,
    organics   INT  NOT NULL DEFAULT 0,
    equipment  INT  NOT NULL DEFAULT 0,
    turns      INT  NOT NULL,
    turns_day  DATE NOT NULL DEFAULT CURRENT_DATE  -- day the turn budget belongs to
);

INSERT INTO schema_migrations (version) VALUES (8) ON CONFLICT DO NOTHING;
