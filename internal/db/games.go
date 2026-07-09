package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RecordGameScore appends one score event (a win, a high score) to the
// shared game score log.
func RecordGameScore(ctx context.Context, pool *pgxpool.Pool, game, userID, kind string, value int64) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO game_scores (game, user_id, kind, value) VALUES ($1, $2, $3, $4)`,
		game, userID, kind, value)
	return err
}

// GameScoreRow is one aggregated leaderboard row.
type GameScoreRow struct {
	Username string
	Total    int64
}

// GameScoreTotals aggregates a game's score log per user, best first.
func GameScoreTotals(ctx context.Context, pool *pgxpool.Pool, game, kind string, limit int) ([]GameScoreRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.username, SUM(s.value) AS total
		FROM game_scores s
		JOIN users u ON u.id = s.user_id
		WHERE s.game = $1 AND s.kind = $2
		GROUP BY u.username
		ORDER BY total DESC, u.username
		LIMIT $3`, game, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GameScoreRow
	for rows.Next() {
		var r GameScoreRow
		if err := rows.Scan(&r.Username, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
