package wumpus

import (
	"math/rand"
	"testing"
)

func silent(string) {}

// fixed builds a hand-placed cave so mechanics test deterministically.
func fixed(player, wumpus int, pits, bats [2]int, seed int64) *state {
	return &state{
		player: player, wumpus: wumpus, pits: pits, bats: bats,
		arrows: 5, rand: rand.New(rand.NewSource(seed)),
	}
}

func TestDodecahedronIsSane(t *testing.T) {
	for room := 1; room <= 20; room++ {
		seen := map[int]bool{}
		for _, to := range tunnels[room] {
			if to < 1 || to > 20 || to == room || seen[to] {
				t.Fatalf("room %d has bad tunnel %d", room, to)
			}
			seen[to] = true
			// Symmetry: every tunnel goes both ways.
			back := false
			for _, ret := range tunnels[to] {
				if ret == room {
					back = true
				}
			}
			if !back {
				t.Fatalf("tunnel %d→%d is one-way", room, to)
			}
		}
	}
}

func TestWarnings(t *testing.T) {
	// Player in 1 (tunnels 2,5,8): wumpus in 2, pit in 5, bats in 8 —
	// all three warnings at once.
	s := fixed(1, 2, [2]int{5, 19}, [2]int{8, 20}, 1)
	w := s.warnings()
	if len(w) != 3 {
		t.Fatalf("warnings = %v, want all three", w)
	}
	// Nothing adjacent: no warnings.
	s = fixed(1, 13, [2]int{12, 19}, [2]int{16, 20}, 1)
	if w := s.warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v, want none", w)
	}
}

func TestPitKills(t *testing.T) {
	s := fixed(1, 13, [2]int{2, 19}, [2]int{16, 20}, 1)
	s.move(2, silent)
	if !s.over || s.won {
		t.Fatal("walking into a pit must end the hunt")
	}
}

func TestBatsCarry(t *testing.T) {
	// Bats in room 2; wherever they drop the player it must not be room 2
	// (the bat room resolution loops until a non-bat outcome).
	s := fixed(1, 13, [2]int{19, 18}, [2]int{2, 20}, 7)
	s.move(2, silent)
	if s.over {
		// Possible: dropped into a pit or the wumpus — with this seed it
		// shouldn't happen; if it does the placement below catches it.
		t.Fatalf("unexpected end: %s", s.end)
	}
	if s.player == 2 || s.player == 20 {
		t.Fatalf("bats left the player in a bat room (%d)", s.player)
	}
}

func TestArrowHitsWumpus(t *testing.T) {
	// Player 1, wumpus 2 (adjacent): shoot 2 wins.
	s := fixed(1, 2, [2]int{12, 19}, [2]int{16, 20}, 1)
	s.shoot([]int{2}, silent)
	if !s.won {
		t.Fatal("direct arrow missed the wumpus")
	}
}

func TestCrookedArrowPath(t *testing.T) {
	// A non-adjacent hop gets redirected to a random adjacent room; the
	// arrow still flies its full length. Player 1: request room 13
	// (not adjacent) — arrow must land in one of 1's tunnels first.
	s := fixed(1, 19, [2]int{12, 18}, [2]int{16, 20}, 3)
	s.shoot([]int{13}, silent)
	if s.won {
		t.Fatal("crooked arrow cannot reach room 19 from room 1 in one hop")
	}
	if s.arrows != 4 {
		t.Fatalf("arrows = %d, want 4", s.arrows)
	}
}

func TestOutOfArrows(t *testing.T) {
	// Missing five times ends the hunt. Pin the wumpus far away and keep
	// it stationary by seeding rand so... rather: just verify arrows hit 0
	// ⇒ over (the wumpus may also wander onto the player, which also ends).
	s := fixed(1, 13, [2]int{12, 19}, [2]int{16, 20}, 42)
	for i := 0; i < 5 && !s.over; i++ {
		s.shoot([]int{5}, silent)
	}
	if !s.over {
		t.Fatal("hunt continued with no arrows")
	}
}

func TestNewStatePlacesDistinct(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		s := newState(rand.New(rand.NewSource(seed)))
		seen := map[int]bool{}
		for _, r := range []int{s.player, s.wumpus, s.pits[0], s.pits[1], s.bats[0], s.bats[1]} {
			if r < 1 || r > 20 || seen[r] {
				t.Fatalf("seed %d: bad placement %+v", seed, s)
			}
			seen[r] = true
		}
	}
}
