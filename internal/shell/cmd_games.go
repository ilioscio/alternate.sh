package shell

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/games"

	// Door games self-register from init(); each import installs one.
	_ "github.com/ilioscio/alternate.sh/internal/games/chess"
	_ "github.com/ilioscio/alternate.sh/internal/games/trade"
	_ "github.com/ilioscio/alternate.sh/internal/games/wumpus"
)

// The door-games lobby and the bridge between sessions and the games
// framework. Individual games register themselves via the blank imports in
// commands.go; here we only adapt a Session into a games.Terminal and run
// whatever the registry offers.

// gameTerm adapts a Session to games.Terminal.
type gameTerm struct{ s *Session }

func (t gameTerm) Read(p []byte) (int, error)  { return t.s.r.Read(p) }
func (t gameTerm) Write(p []byte) (int, error) { return t.s.Write(p) }
func (t gameTerm) Size() (rows, cols int)      { return t.s.Size() }
func (t gameTerm) ReadLine(prompt string) (string, error) {
	return t.s.newRL().ReadLine(prompt)
}

// cmdGames shows the lobby (DESIGN.md §5.9): every installed game, who is
// playing right now, and the top of each leaderboard.
func cmdGames(s *Session, _ []string) error {
	all := games.All()
	if len(all) == 0 {
		s.Println("No games installed.")
		return nil
	}

	// Who's playing what, from presence states ("playing chess").
	playing := map[string]int{}
	for _, e := range s.hub.List() {
		if name, ok := strings.CutPrefix(e.State, "playing "); ok {
			playing[name]++
		}
	}

	s.HLine()
	s.Println("  the alternate.sh arcade — door games")
	s.HLine()
	for _, g := range all {
		now := ""
		if n := playing[g.Name()]; n > 0 {
			now = fmt.Sprintf("   [%d playing now]", n)
		}
		s.Printf("  %-8s  %s — %s%s\r\n", g.Name(), g.Title(), g.Description(), now)

		if board, err := g.Leaderboard(s.ctx, s.db, 3); err == nil && len(board) > 0 {
			var parts []string
			for _, e := range board {
				parts = append(parts, fmt.Sprintf("%s (%s)", e.Username, e.Label))
			}
			s.Printf("  %-8s  best: %s\r\n", "", strings.Join(parts, ", "))
		}
	}
	s.HLine()
	s.Println("  Type a game's name to play. Games run right here in your terminal.")
	s.HLine()
	return nil
}

// runGame executes one game session. A panicking game must never take the
// user's session down with it.
func runGame(s *Session, g games.Game) (err error) {
	gctx := &games.Context{
		Ctx: s.ctx,
		Player: games.Player{
			ID:       s.User.ID,
			Username: s.User.Username,
			Admin:    s.User.Admin,
		},
		Term:   gameTerm{s},
		DB:     s.db,
		Hub:    s.hub,
		Rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
		Limits: s.cfg.Limits,
	}

	defer func() {
		if r := recover(); r != nil {
			s.Printf("\r\n[%s hit a bug and had to stop — your session is fine]\r\n", g.Name())
			err = nil
		}
	}()
	return g.Play(gctx)
}

// registerGames wires every registered game up as a command. Called from
// the command registry's init.
func registerGames() {
	register("games", cmdGames, "arcade")
	for _, g := range games.All() {
		g := g
		register(g.Name(), func(s *Session, _ []string) error {
			return runGame(s, g)
		})
	}
}
