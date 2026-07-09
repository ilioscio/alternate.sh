package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Chess game persistence (DESIGN.md §5.9). Moves are stored as a
// space-separated coordinate-notation list and replayed through the engine;
// the position is never stored, so the move list is always the truth.

type ChessGame struct {
	ID        int64
	WhiteID   string
	WhiteName string
	BlackID   string
	BlackName string
	Moves     string
	Status    string // pending | active | checkmate | stalemate | draw | resigned | declined
	WinnerID  *string
	DrawOffer *string // user id of the player currently offering a draw
	CreatedAt time.Time
	UpdatedAt time.Time
}

const chessGameCols = `
	g.id, g.white_id, wu.username, g.black_id, bu.username,
	g.moves, g.status, g.winner_id, g.draw_offer, g.created_at, g.updated_at`

func scanChessGame(row interface{ Scan(...any) error }) (*ChessGame, error) {
	g := &ChessGame{}
	err := row.Scan(&g.ID, &g.WhiteID, &g.WhiteName, &g.BlackID, &g.BlackName,
		&g.Moves, &g.Status, &g.WinnerID, &g.DrawOffer, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// CreateChessGame opens a pending challenge with assigned colors. By
// convention the winner_id column holds the challenger's id while the game
// is pending (it clears on accept), so consent stays with the challenged
// player without an extra column.
func CreateChessGame(ctx context.Context, pool *pgxpool.Pool, whiteID, blackID, challengerID string) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO chess_games (white_id, black_id, winner_id) VALUES ($1, $2, $3) RETURNING id`,
		whiteID, blackID, challengerID).Scan(&id)
	return id, err
}

func GetChessGame(ctx context.Context, pool *pgxpool.Pool, id int64) (*ChessGame, error) {
	return scanChessGame(pool.QueryRow(ctx, `
		SELECT `+chessGameCols+`
		FROM chess_games g
		JOIN users wu ON wu.id = g.white_id
		JOIN users bu ON bu.id = g.black_id
		WHERE g.id = $1`, id))
}

// ListChessGamesFor returns a user's pending and active games plus their
// recently finished ones, most recently touched first.
func ListChessGamesFor(ctx context.Context, pool *pgxpool.Pool, userID string, finished int) ([]ChessGame, error) {
	rows, err := pool.Query(ctx, `
		(SELECT `+chessGameCols+`
		 FROM chess_games g
		 JOIN users wu ON wu.id = g.white_id
		 JOIN users bu ON bu.id = g.black_id
		 WHERE (g.white_id = $1 OR g.black_id = $1) AND g.status IN ('pending','active')
		 ORDER BY g.updated_at DESC)
		UNION ALL
		(SELECT `+chessGameCols+`
		 FROM chess_games g
		 JOIN users wu ON wu.id = g.white_id
		 JOIN users bu ON bu.id = g.black_id
		 WHERE (g.white_id = $1 OR g.black_id = $1)
		   AND g.status NOT IN ('pending','active','declined')
		 ORDER BY g.updated_at DESC LIMIT $2)`,
		userID, finished)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChessGame
	for rows.Next() {
		g, err := scanChessGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// UpdateChessGame persists the mutable fields after a move or state change.
func UpdateChessGame(ctx context.Context, pool *pgxpool.Pool, id int64, moves, status string, winnerID, drawOffer *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE chess_games
		SET moves = $2, status = $3, winner_id = $4, draw_offer = $5, updated_at = NOW()
		WHERE id = $1`,
		id, moves, status, winnerID, drawOffer)
	return err
}
