package db

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The trade game's persistent universe (DESIGN.md §5.9): sectors, warps,
// ports, and player ships. Generated once at first play, owned by play
// thereafter.

type TradeSector struct {
	ID    int
	Warps []int
}

type TradePort struct {
	SectorID  int
	Name      string
	Stock     [3]int  // ore, organics, equipment
	Buys      [3]bool // true: port buys (player sells); false: port sells
	UpdatedAt time.Time
}

type TradePlayer struct {
	UserID   string
	SectorID int
	Credits  int64
	Holds    int
	Cargo    [3]int
	Turns    int
	TurnsDay time.Time
}

// Commodity indexing shared with the game layer.
const (
	Ore = iota
	Organics
	Equipment
)

// TradeUniverseSize reports how many sectors exist (0 = not generated).
func TradeUniverseSize(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM trade_sectors`).Scan(&n)
	return n, err
}

var portSyllables = []string{"Al", "Bex", "Cor", "Dan", "Eri", "Fen", "Gol", "Hax", "Ith", "Jov", "Kel", "Lun", "Mir", "Nox", "Oph", "Pra", "Qua", "Rig", "Sol", "Tau", "Umb", "Vex", "Wul", "Xen", "Yor", "Zet"}

// buildTradeWarps builds a connected warp graph: a random spanning tree
// (guaranteeing every sector is reachable) plus chords for loops, degree
// bounded so sectors stay readable. Index 0 is unused; ids are 1-based.
// Pure, so connectivity is unit-testable without a database.
func buildTradeWarps(rng *rand.Rand, n int) [][]int {
	warps := make([]map[int]bool, n+1)
	for i := 1; i <= n; i++ {
		warps[i] = map[int]bool{}
	}
	link := func(a, b int) bool {
		if a != b && len(warps[a]) < 6 && len(warps[b]) < 6 {
			warps[a][b] = true
			warps[b][a] = true
			return true
		}
		return false
	}
	order := rng.Perm(n) // 0-based; +1 for sector ids
	for i := 1; i < n; i++ {
		// Attach to a random earlier tree node; degree caps can refuse, so
		// retry against other earlier nodes until one accepts.
		for tries := 0; ; tries++ {
			if link(order[i]+1, order[rng.Intn(i)]+1) {
				break
			}
			if tries > 4*n {
				// Degenerate degree exhaustion cannot happen with cap 6 and
				// tree degree ≤ 2 average, but never loop forever.
				link(order[i]+1, order[i-1]+1)
				break
			}
		}
	}
	for i := 0; i < n; i++ { // chords make it a web, not a tree
		link(rng.Intn(n)+1, rng.Intn(n)+1)
	}

	out := make([][]int, n+1)
	for i := 1; i <= n; i++ {
		list := make([]int, 0, len(warps[i]))
		for w := range warps[i] {
			list = append(list, w)
		}
		sort.Ints(list)
		out[i] = list
	}
	return out
}

// GenerateTradeUniverse creates n sectors as a connected warp graph with
// ports scattered through it. Runs in one transaction; safe to race (the
// second generator loses on the primary key and rolls back).
func GenerateTradeUniverse(ctx context.Context, pool *pgxpool.Pool, rng *rand.Rand, n int) error {
	if n < 10 {
		return errors.New("universe too small")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for i, list := range buildTradeWarps(rng, n) {
		if i == 0 {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO trade_sectors (id, warps) VALUES ($1, $2)`, i, list); err != nil {
			return err
		}
	}

	// Ports in ~one quarter of sectors, never sector 1 (home is neutral).
	for i := 2; i <= n; i++ {
		if rng.Intn(4) != 0 {
			continue
		}
		name := portSyllables[rng.Intn(len(portSyllables))] +
			portSyllables[rng.Intn(len(portSyllables))] + " Station"
		if _, err := tx.Exec(ctx, `
			INSERT INTO trade_ports (sector_id, name, ore, organics, equipment,
			                         buys_ore, buys_organics, buys_equipment)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			i, name,
			300+rng.Intn(600), 300+rng.Intn(600), 300+rng.Intn(600),
			rng.Intn(2) == 0, rng.Intn(2) == 0, rng.Intn(2) == 0); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func GetTradeSector(ctx context.Context, pool *pgxpool.Pool, id int) (*TradeSector, error) {
	s := &TradeSector{ID: id}
	err := pool.QueryRow(ctx,
		`SELECT warps FROM trade_sectors WHERE id = $1`, id).Scan(&s.Warps)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// GetTradePort loads a port, applying lazy stock drift toward equilibrium
// (sold goods restock, bought goods ship out) since it was last touched.
func GetTradePort(ctx context.Context, pool *pgxpool.Pool, sector int) (*TradePort, error) {
	p := &TradePort{SectorID: sector}
	err := pool.QueryRow(ctx, `
		SELECT name, ore, organics, equipment, buys_ore, buys_organics, buys_equipment, updated_at
		FROM trade_ports WHERE sector_id = $1`, sector,
	).Scan(&p.Name, &p.Stock[0], &p.Stock[1], &p.Stock[2],
		&p.Buys[0], &p.Buys[1], &p.Buys[2], &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	hours := int(time.Since(p.UpdatedAt).Hours())
	if hours > 0 {
		if hours > 24 {
			hours = 24
		}
		for c := 0; c < 3; c++ {
			eq := 800 // ports that sell restock toward plenty
			if p.Buys[c] {
				eq = 200 // ports that buy ship inventory out
			}
			p.Stock[c] += (eq - p.Stock[c]) * hours / 12
		}
		pool.Exec(ctx, `
			UPDATE trade_ports SET ore=$2, organics=$3, equipment=$4, updated_at=NOW()
			WHERE sector_id=$1`,
			sector, p.Stock[0], p.Stock[1], p.Stock[2])
	}
	return p, nil
}

// GetOrCreateTradePlayer loads a ship, minting a fresh one (or a fresh
// daily turn budget) as needed.
func GetOrCreateTradePlayer(ctx context.Context, pool *pgxpool.Pool, userID string, dailyTurns int) (*TradePlayer, error) {
	p := &TradePlayer{UserID: userID}
	err := pool.QueryRow(ctx, `
		SELECT sector_id, credits, holds, ore, organics, equipment, turns, turns_day
		FROM trade_players WHERE user_id = $1`, userID,
	).Scan(&p.SectorID, &p.Credits, &p.Holds, &p.Cargo[0], &p.Cargo[1], &p.Cargo[2], &p.Turns, &p.TurnsDay)
	if errors.Is(err, pgx.ErrNoRows) {
		p = &TradePlayer{UserID: userID, SectorID: 1, Credits: 1000, Holds: 30, Turns: dailyTurns, TurnsDay: today()}
		_, err = pool.Exec(ctx, `
			INSERT INTO trade_players (user_id, sector_id, credits, holds, turns, turns_day)
			VALUES ($1, 1, $2, $3, $4, CURRENT_DATE)`,
			userID, p.Credits, p.Holds, p.Turns)
		return p, err
	}
	if err != nil {
		return nil, err
	}
	if p.TurnsDay.Before(today()) {
		p.Turns = dailyTurns
		p.TurnsDay = today()
		_, err = pool.Exec(ctx, `
			UPDATE trade_players SET turns = $2, turns_day = CURRENT_DATE WHERE user_id = $1`,
			userID, dailyTurns)
	}
	return p, err
}

func today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TradeWarp moves a player to an adjacent sector, spending one turn.
// Guards in SQL keep concurrent sessions honest.
func TradeWarp(ctx context.Context, pool *pgxpool.Pool, userID string, to int) error {
	tag, err := pool.Exec(ctx, `
		UPDATE trade_players SET sector_id = $2, turns = turns - 1
		WHERE user_id = $1 AND turns > 0`, userID, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("no turns left today")
	}
	return nil
}

var commodityCols = [3]string{"ore", "organics", "equipment"}

// TradeExchange executes a buy (player buys from port, qty > 0 toward
// player) or sell in one transaction, with stock/credits/holds guarded.
func TradeExchange(ctx context.Context, pool *pgxpool.Pool, userID string, sector int, commodity int, qty int, unitPrice int64, playerBuys bool) error {
	if qty <= 0 {
		return errors.New("quantity must be positive")
	}
	col := commodityCols[commodity]
	cost := unitPrice * int64(qty)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if playerBuys {
		// Port stock down, player credits down, cargo up (bounded by holds).
		tag, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE trade_ports SET %s = %s - $2, updated_at = NOW()
			             WHERE sector_id = $1 AND %s >= $2`, col, col, col), sector, qty)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("the port doesn't have that much in stock")
		}
		tag, err = tx.Exec(ctx, fmt.Sprintf(`
			UPDATE trade_players SET credits = credits - $2, %s = %s + $3
			WHERE user_id = $1 AND credits >= $2
			  AND ore + organics + equipment + $3 <= holds`, col, col),
			userID, cost, qty)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("not enough credits or cargo space")
		}
	} else {
		tag, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE trade_players SET credits = credits + $2, %s = %s - $3
			WHERE user_id = $1 AND %s >= $3`, col, col, col),
			userID, cost, qty)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("you don't have that much aboard")
		}
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE trade_ports SET %s = %s + $2, updated_at = NOW()
			             WHERE sector_id = $1`, col, col), sector, qty); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// TradeNetWorthRow is one leaderboard line: credits plus cargo at base value.
type TradeNetWorthRow struct {
	Username string
	NetWorth int64
}

func TradeLeaderboard(ctx context.Context, pool *pgxpool.Pool, limit int) ([]TradeNetWorthRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.username, p.credits + p.ore*25 + p.organics*45 + p.equipment*90 AS worth
		FROM trade_players p JOIN users u ON u.id = p.user_id
		ORDER BY worth DESC, u.username LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TradeNetWorthRow
	for rows.Next() {
		var r TradeNetWorthRow
		if err := rows.Scan(&r.Username, &r.NetWorth); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
