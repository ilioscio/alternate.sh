package chess

import (
	"testing"
)

// Perft node counts from the standard published tables (chessprogramming
// wiki). These positions are chosen adversarially: Kiwipete for castling
// and pins, position 3 for en-passant pins, position 4 for promotions,
// positions 5–6 as broad regressions. If the engine agrees with all of
// these, its move generation is correct for practical purposes.
func TestPerft(t *testing.T) {
	cases := []struct {
		name  string
		fen   string
		depth int
		want  uint64
	}{
		{"startpos d1", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 1, 20},
		{"startpos d2", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 2, 400},
		{"startpos d3", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 3, 8902},
		{"startpos d4", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 4, 197281},
		{"startpos d5", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 5, 4865609},
		{"kiwipete d1", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 1, 48},
		{"kiwipete d2", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 2, 2039},
		{"kiwipete d3", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 3, 97862},
		{"kiwipete d4", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 4, 4085603},
		{"pos3 d5", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 5, 674624},
		{"pos4 d4", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 4, 422333},
		{"pos5 d4", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8", 4, 2103487},
		{"pos6 d4", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 4, 3894594},
	}
	for _, c := range cases {
		if testing.Short() && c.want > 500000 {
			continue
		}
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pos, err := ParseFEN(c.fen)
			if err != nil {
				t.Fatal(err)
			}
			if got := Perft(pos, c.depth); got != c.want {
				t.Fatalf("perft(%d) = %d, want %d", c.depth, got, c.want)
			}
		})
	}
}

func mustFEN(t *testing.T, fen string) Position {
	t.Helper()
	p, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func play(t *testing.T, p Position, moves ...string) Position {
	t.Helper()
	for _, ms := range moves {
		m, err := ParseMove(&p, ms)
		if err != nil {
			t.Fatalf("move %q: %v", ms, err)
		}
		p = p.Apply(m)
	}
	return p
}

func TestFoolsMate(t *testing.T) {
	p := play(t, StartPos(), "f2f3", "e7e5", "g2g4", "d8h4")
	if p.PositionStatus() != Checkmate {
		t.Fatal("fool's mate not detected")
	}
	if !p.InCheck(White) {
		t.Fatal("white not in check at mate")
	}
}

func TestStalemate(t *testing.T) {
	// Classic: black king a8, white queen b6, white king a6? — use a known
	// stalemate: black to move, king h8, white queen g6, white king g5? h8/g6
	// leaves Kh8 no moves and not in check.
	p := mustFEN(t, "7k/8/6QK/8/8/8/8/8 b - - 0 1")
	if got := p.PositionStatus(); got != Stalemate {
		t.Fatalf("status = %v, want stalemate", got)
	}
}

func TestEnPassantAndPromotion(t *testing.T) {
	// White pawn e5; black plays d7d5; exd6 en passant must be legal, and
	// only immediately.
	p := play(t, StartPos(), "e2e4", "a7a6", "e4e5", "d7d5")
	m, err := ParseMove(&p, "e5d6")
	if err != nil {
		t.Fatalf("en passant rejected: %v", err)
	}
	next := p.Apply(m)
	if next.pieceAt(sqAt(3, 4)).Type != Empty {
		t.Fatal("en passant did not remove the captured pawn")
	}
	// After an intervening move, the ep capture is gone.
	p2 := play(t, p, "g1f3", "a6a5")
	if _, err := ParseMove(&p2, "e5d6"); err == nil {
		t.Fatal("stale en passant accepted")
	}

	// Promotion: bare suffix and defaulted queen.
	pp := mustFEN(t, "8/P6k/8/8/8/8/7K/8 w - - 0 1")
	m, err = ParseMove(&pp, "a7a8n")
	if err != nil || m.Promo != Knight {
		t.Fatalf("knight promotion: %v %v", m, err)
	}
	m, err = ParseMove(&pp, "a7a8")
	if err != nil || m.Promo != Queen {
		t.Fatalf("default queen promotion: %v %v", m, err)
	}
}

func TestCastlingRightsLost(t *testing.T) {
	// Moving the king kills both rights; moving/capturing a rook kills one.
	p := mustFEN(t, "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1")
	if _, err := ParseMove(&p, "o-o"); err != nil {
		t.Fatalf("castling should be legal: %v", err)
	}
	afterKing := play(t, p, "e1e2", "e8e7")
	if afterKing.WK || afterKing.WQ || afterKing.BK || afterKing.BQ {
		t.Fatal("king moves did not clear castling rights")
	}
	afterRook := play(t, p, "h1h8") // rook takes rook
	if afterRook.WK || afterRook.BK {
		t.Fatal("rook move/capture did not clear king-side rights")
	}
	if !afterRook.WQ || !afterRook.BQ {
		t.Fatal("queen-side rights wrongly cleared")
	}
}

func TestIllegalMovesRejected(t *testing.T) {
	p := StartPos()
	for _, bad := range []string{"e2e5", "e7e5", "b1b3", "e1g1", "d1d5", "zzzz", "e2"} {
		if _, err := ParseMove(&p, bad); err == nil {
			t.Errorf("%q accepted from the start position", bad)
		}
	}
	// Pinned piece may not move: after e4/e5, Bb5 pins the d7 pawn once
	// it's set up — use a direct FEN: black knight on d7 pinned by rook.
	pinned := mustFEN(t, "4k3/3n4/8/8/8/8/3R4/4K3 b - - 0 1")
	// The d7 knight is NOT pinned (rook on d-file, king on e8) — adjust:
	pinned = mustFEN(t, "3k4/3n4/8/8/8/8/3R4/3K4 b - - 0 1")
	if _, err := ParseMove(&pinned, "d7e5"); err == nil {
		t.Error("moving a pinned knight was accepted")
	}
}

func TestDraws(t *testing.T) {
	status := func(fen string) Status {
		p := mustFEN(t, fen)
		return p.PositionStatus()
	}
	if got := status("4k3/8/8/8/8/8/8/4K3 w - - 0 1"); got != DrawMaterial {
		t.Fatalf("K vs K: %v", got)
	}
	if got := status("4k3/8/8/8/8/8/4B3/4K3 w - - 0 1"); got != DrawMaterial {
		t.Fatalf("K+B vs K: %v", got)
	}
	if got := status("4k3/8/8/8/8/8/4P3/4K3 w - - 99 1"); got != Ongoing {
		t.Fatalf("halfmove 99: %v", got)
	}
	if got := status("4k3/8/8/8/8/8/4R3/4K3 w - - 100 1"); got != DrawFifty {
		t.Fatalf("halfmove 100: %v", got)
	}

	// Threefold via repetition keys: shuffle knights back and forth.
	p := StartPos()
	counts := map[string]int{p.RepetitionKey(): 1}
	seen3 := false
	for i := 0; i < 2; i++ {
		for _, ms := range []string{"g1f3", "g8f6", "f3g1", "f6g8"} {
			m, _ := ParseMove(&p, ms)
			p = p.Apply(m)
			counts[p.RepetitionKey()]++
			if counts[p.RepetitionKey()] >= 3 {
				seen3 = true
			}
		}
	}
	if !seen3 {
		t.Fatal("threefold repetition not observable via RepetitionKey")
	}
}

func TestSAN(t *testing.T) {
	p := StartPos()
	cases := []struct{ move, san string }{
		{"e2e4", "e4"}, {"g1f3", "Nf3"},
	}
	for _, c := range cases {
		m, err := ParseMove(&p, c.move)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.SAN(m); got != c.san {
			t.Errorf("SAN(%s) = %q, want %q", c.move, got, c.san)
		}
	}

	// Capture and disambiguation. (Rook on d5 checks nothing from a8's
	// point of view — no shared line.)
	pos := mustFEN(t, "k7/8/8/3q4/8/8/4P3/K2R3R w - - 0 1")
	m, _ := ParseMove(&pos, "d1d5")
	if got := pos.SAN(m); got != "Rxd5" {
		t.Errorf("SAN(d1d5) = %q, want Rxd5", got)
	}
	// An actual check: the h-rook reaches h8 (the d-file is blocked by the
	// queen), checking the a8 king along rank 8.
	m, err := ParseMove(&pos, "h1h8")
	if err != nil {
		t.Fatalf("h1h8: %v", err)
	}
	if got := pos.SAN(m); got != "Rh8+" {
		t.Errorf("SAN(h1h8) = %q, want Rh8+", got)
	}
	m, _ = ParseMove(&pos, "h1f1")
	if got := pos.SAN(m); got != "Rhf1" {
		t.Errorf("SAN(h1f1) = %q, want Rhf1", got)
	}

	// Mate suffix.
	mate := play(t, StartPos(), "f2f3", "e7e5", "g2g4")
	m, _ = ParseMove(&mate, "d8h4")
	if got := mate.SAN(m); got != "Qh4#" {
		t.Errorf("SAN(d8h4) = %q, want Qh4#", got)
	}

	// Castling SAN.
	c := mustFEN(t, "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1")
	m, _ = ParseMove(&c, "o-o")
	if got := c.SAN(m); got != "O-O" {
		t.Errorf("castle SAN = %q", got)
	}
}
