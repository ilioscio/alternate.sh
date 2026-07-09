// Package wumpus is Hunt the Wumpus, faithfully 1973 (Gregory Yob): twenty
// rooms on a dodecahedron, two super bats, two bottomless pits, five crooked
// arrows, and the warnings that made it famous.
package wumpus

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/games"
)

// tunnels is the classic dodecahedron: room N connects to exactly three
// others. Rooms are 1-based, as God and Gregory Yob intended.
var tunnels = [21][3]int{
	{}, // room 0 unused
	{2, 5, 8}, {1, 3, 10}, {2, 4, 12}, {3, 5, 14}, {1, 4, 6},
	{5, 7, 15}, {6, 8, 17}, {1, 7, 9}, {8, 10, 18}, {2, 9, 11},
	{10, 12, 19}, {3, 11, 13}, {12, 14, 20}, {4, 13, 15}, {6, 14, 16},
	{15, 17, 20}, {7, 16, 18}, {9, 17, 19}, {11, 18, 20}, {13, 16, 19},
}

// state is one hunt. Kept separate from I/O so the mechanics unit-test
// deterministically without seed archaeology.
type state struct {
	player int
	wumpus int
	pits   [2]int
	bats   [2]int
	arrows int
	rand   *rand.Rand

	over bool
	won  bool
	end  string // closing message
}

// newState places the player and hazards in distinct rooms.
func newState(rng *rand.Rand) *state {
	perm := rng.Perm(20) // 0..19 → rooms 1..20
	s := &state{
		player: perm[0] + 1,
		wumpus: perm[1] + 1,
		pits:   [2]int{perm[2] + 1, perm[3] + 1},
		bats:   [2]int{perm[4] + 1, perm[5] + 1},
		arrows: 5,
		rand:   rng,
	}
	return s
}

func (s *state) adjacent(room, to int) bool {
	for _, t := range tunnels[room] {
		if t == to {
			return true
		}
	}
	return false
}

// warnings lists the classic hazard hints for rooms adjacent to the player.
func (s *state) warnings() []string {
	var w []string
	smell, draft, bats := false, false, false
	for _, t := range tunnels[s.player] {
		if t == s.wumpus {
			smell = true
		}
		if t == s.pits[0] || t == s.pits[1] {
			draft = true
		}
		if t == s.bats[0] || t == s.bats[1] {
			bats = true
		}
	}
	if smell {
		w = append(w, "I smell a wumpus!")
	}
	if draft {
		w = append(w, "I feel a draft!")
	}
	if bats {
		w = append(w, "Bats nearby!")
	}
	return w
}

// enterRoom resolves hazards after the player arrives somewhere, following
// bat rides recursively.
func (s *state) enterRoom(report func(string)) {
	for {
		switch {
		case s.player == s.pits[0] || s.player == s.pits[1]:
			s.over, s.end = true, "YYYIIIIEEEE... fell in a pit!"
			return
		case s.player == s.wumpus:
			// Bumping the wumpus startles it: 75% it shambles off, else dinner.
			if s.rand.Intn(4) == 0 {
				s.over, s.end = true, "TSK TSK TSK — the wumpus got you!"
				return
			}
			report("...oops! Bumped a wumpus!")
			s.wumpus = tunnels[s.wumpus][s.rand.Intn(3)]
			if s.wumpus == s.player {
				s.over, s.end = true, "TSK TSK TSK — the wumpus got you!"
				return
			}
		case s.player == s.bats[0] || s.player == s.bats[1]:
			report("ZAP — super bat snatch! Elsewhereville for you!")
			s.player = s.rand.Intn(20) + 1
			continue // resolve the new room too
		}
		return
	}
}

// move walks to an adjacent room (already validated).
func (s *state) move(to int, report func(string)) {
	s.player = to
	s.enterRoom(report)
}

// shoot fires a crooked arrow along up to five rooms. Unconnected hops go
// to a random adjacent room, exactly as the original warned.
func (s *state) shoot(path []int, report func(string)) {
	s.arrows--
	at := s.player
	for _, want := range path {
		next := want
		if !s.adjacent(at, want) {
			next = tunnels[at][s.rand.Intn(3)]
		}
		at = next
		if at == s.wumpus {
			s.over, s.won = true, true
			s.end = "AHA! You got the wumpus!"
			return
		}
		if at == s.player {
			s.over, s.end = true, "OUCH! Arrow got you!"
			return
		}
	}
	report("Missed!")
	// A miss startles the wumpus: it moves with probability 3/4.
	if s.rand.Intn(4) != 0 {
		s.wumpus = tunnels[s.wumpus][s.rand.Intn(3)]
		if s.wumpus == s.player {
			s.over, s.end = true, "TSK TSK TSK — the wumpus got you!"
			return
		}
	}
	if s.arrows == 0 {
		s.over, s.end = true, "Out of arrows — the wumpus will get you eventually..."
	}
}

// Game implements games.Game.
type Game struct{}

func init() { games.Register(Game{}) }

func (Game) Name() string  { return "wumpus" }
func (Game) Title() string { return "Hunt the Wumpus" }
func (Game) Description() string {
	return "the 1973 classic — bats, pits, and five crooked arrows"
}

func (Game) Leaderboard(ctx context.Context, pool *pgxpool.Pool, limit int) ([]games.LeaderEntry, error) {
	rows, err := db.GameScoreTotals(ctx, pool, "wumpus", "win", limit)
	if err != nil {
		return nil, err
	}
	var out []games.LeaderEntry
	for _, r := range rows {
		label := fmt.Sprintf("%d wins", r.Total)
		if r.Total == 1 {
			label = "1 win"
		}
		out = append(out, games.LeaderEntry{Username: r.Username, Label: label})
	}
	return out, nil
}

func (Game) Play(gc *games.Context) error {
	t := gc.Term
	fmt.Fprintf(t, "\r\nHUNT THE WUMPUS (1973)\r\n")
	fmt.Fprintf(t, "Twenty rooms, two pits, two super bats, one wumpus, five arrows.\r\n")

	for {
		s := newState(gc.Rand)
		report := func(msg string) { fmt.Fprintf(t, "%s\r\n", msg) }
		s.enterRoom(report) // starting room may host bats (never fatal placement? it may — the classic allowed it)

		for !s.over {
			fmt.Fprintf(t, "\r\nYou are in room %d. Tunnels lead to %d, %d, %d.\r\n",
				s.player, tunnels[s.player][0], tunnels[s.player][1], tunnels[s.player][2])
			for _, w := range s.warnings() {
				fmt.Fprintf(t, "  %s\r\n", w)
			}
			fmt.Fprintf(t, "  Arrows: %d\r\n", s.arrows)

			line, err := t.ReadLine("move <room>, shoot <room> [room...], or q: ")
			if err != nil {
				return nil
			}
			fields := strings.Fields(strings.ToLower(strings.TrimSpace(line)))
			if len(fields) == 0 {
				continue
			}
			switch fields[0] {
			case "q", "quit":
				return nil
			case "m", "move":
				if len(fields) != 2 {
					report("move where? (move <room>)")
					continue
				}
				to, err := strconv.Atoi(fields[1])
				if err != nil || !s.adjacent(s.player, to) {
					report("No tunnel leads there.")
					continue
				}
				s.move(to, report)
			case "s", "shoot":
				if len(fields) < 2 || len(fields) > 6 {
					report("shoot 1 to 5 rooms: shoot <room> [room...]")
					continue
				}
				var path []int
				bad := false
				for _, f := range fields[1:] {
					r, err := strconv.Atoi(f)
					if err != nil || r < 1 || r > 20 {
						bad = true
						break
					}
					path = append(path, r)
				}
				if bad {
					report("Rooms are numbered 1 to 20.")
					continue
				}
				s.shoot(path, report)
			default:
				report("Commands: move <room> · shoot <room> [room...] · q")
			}
		}

		fmt.Fprintf(t, "\r\n%s\r\n", s.end)
		if s.won {
			fmt.Fprintf(t, "HEE HEE HEE — the wumpus'll get you next time!!\r\n")
			db.RecordGameScore(gc.Ctx, gc.DB, "wumpus", gc.Player.ID, "win", 1)
		}
		again, err := t.ReadLine("Play again? [y/n]: ")
		if err != nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(again)), "y") {
			return nil
		}
	}
}
