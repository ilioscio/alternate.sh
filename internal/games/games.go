// Package games is the door-games framework (DESIGN.md §5.9): the Game
// interface every game implements, the registry the lobby and command
// dispatch read, and the Context a running game receives. Games live in
// subpackages (games/chess, games/wumpus, games/trade) and self-register
// from init(); the shell blank-imports them.
package games

import (
	"context"
	"io"
	"math/rand"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// Terminal is the slice of a session a game may drive: raw byte I/O for
// full-screen play, line input for command-style play, and the window size.
type Terminal interface {
	io.ReadWriter
	Size() (rows, cols int)
	ReadLine(prompt string) (string, error)
}

// Player identifies who is playing.
type Player struct {
	ID       string
	Username string
	Admin    bool
}

// LeaderEntry is one row of a game's lobby leaderboard.
type LeaderEntry struct {
	Username string
	Label    string // e.g. "12 wins", "cr 48,210"
}

// Context is everything a running game gets. Games must not reach outside it.
type Context struct {
	Ctx    context.Context
	Player Player
	Term   Terminal
	DB     *pgxpool.Pool

	// Hub gives games presence powers: Notify for write-style notices
	// (async chess moves) and Rooms for live-update signaling between two
	// sessions viewing the same game.
	Hub *presence.Hub

	// Rand is the game's randomness. Seeded from entropy in production;
	// tests inject a fixed seed for deterministic play.
	Rand *rand.Rand

	// Limits carries the node's configured gameplay limits (turn budgets).
	Limits config.LimitsConfig
}

// Notify sends a write-style notice to every session of a user (respecting
// their mesg setting) and reports how many were reached.
func (c *Context) Notify(username, message string) int {
	return c.Hub.Send(username, presence.WriteNotice{
		Kind:    presence.NoticeWrite,
		From:    "games",
		Message: message,
	})
}

// Game is one door game.
type Game interface {
	// Name is the command users type (lowercase, also the lobby key).
	Name() string
	// Title is the lobby display name.
	Title() string
	// Description is a one-line lobby blurb.
	Description() string
	// Play runs the game for one session until the player leaves.
	// The framework recovers panics so a game bug never kills a session.
	Play(ctx *Context) error
	// Leaderboard returns the lobby's top entries (best first, ≤ limit).
	Leaderboard(ctx context.Context, db *pgxpool.Pool, limit int) ([]LeaderEntry, error)
}

var registry = map[string]Game{}

// Register adds a game; called from each game package's init().
func Register(g Game) {
	registry[g.Name()] = g
}

// Get returns a registered game by command name, or nil.
func Get(name string) Game {
	return registry[name]
}

// All returns every registered game, sorted by name for stable display.
func All() []Game {
	out := make([]Game, 0, len(registry))
	for _, g := range registry {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
