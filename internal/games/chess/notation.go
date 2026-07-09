package chess

import (
	"fmt"
	"strconv"
	"strings"
)

// StartPos returns the standard initial position.
func StartPos() Position {
	p, _ := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	return p
}

var fenPieces = map[byte]Piece{
	'P': {Pawn, White}, 'N': {Knight, White}, 'B': {Bishop, White},
	'R': {Rook, White}, 'Q': {Queen, White}, 'K': {King, White},
	'p': {Pawn, Black}, 'n': {Knight, Black}, 'b': {Bishop, Black},
	'r': {Rook, Black}, 'q': {Queen, Black}, 'k': {King, Black},
}

var pieceLetters = map[PieceType]string{
	Pawn: "", Knight: "N", Bishop: "B", Rook: "R", Queen: "Q", King: "K",
}

// ParseFEN loads a position from Forsyth–Edwards notation (used by the
// perft test positions; games themselves store move lists).
func ParseFEN(fen string) (Position, error) {
	var p Position
	p.EnPassant = NoSquare
	p.FullMove = 1

	fields := strings.Fields(fen)
	if len(fields) < 4 {
		return p, fmt.Errorf("chess: FEN needs at least 4 fields")
	}

	ranks := strings.Split(fields[0], "/")
	if len(ranks) != 8 {
		return p, fmt.Errorf("chess: FEN board needs 8 ranks")
	}
	for i, rankStr := range ranks {
		r := 7 - i // FEN starts at rank 8
		f := 0
		for j := 0; j < len(rankStr); j++ {
			c := rankStr[j]
			if c >= '1' && c <= '8' {
				f += int(c - '0')
				continue
			}
			pc, ok := fenPieces[c]
			if !ok || f > 7 {
				return p, fmt.Errorf("chess: bad FEN rank %q", rankStr)
			}
			p.Board[sqAt(f, r)] = pc
			f++
		}
		if f != 8 {
			return p, fmt.Errorf("chess: bad FEN rank %q", rankStr)
		}
	}

	switch fields[1] {
	case "w":
		p.Turn = White
	case "b":
		p.Turn = Black
	default:
		return p, fmt.Errorf("chess: bad FEN turn %q", fields[1])
	}

	p.WK = strings.Contains(fields[2], "K")
	p.WQ = strings.Contains(fields[2], "Q")
	p.BK = strings.Contains(fields[2], "k")
	p.BQ = strings.Contains(fields[2], "q")

	if fields[3] != "-" {
		s, err := parseSquare(fields[3])
		if err != nil {
			return p, err
		}
		p.EnPassant = s
	}
	if len(fields) > 4 {
		p.HalfMove, _ = strconv.Atoi(fields[4])
	}
	if len(fields) > 5 {
		p.FullMove, _ = strconv.Atoi(fields[5])
	}
	return p, nil
}

func parseSquare(s string) (Square, error) {
	if len(s) != 2 || s[0] < 'a' || s[0] > 'h' || s[1] < '1' || s[1] > '8' {
		return NoSquare, fmt.Errorf("chess: bad square %q", s)
	}
	return sqAt(int(s[0]-'a'), int(s[1]-'1')), nil
}

// Format renders a move in coordinate notation ("e2e4", "e7e8q").
func (m Move) Format() string {
	out := m.From.Name() + m.To.Name()
	if m.Promo != Empty {
		out += strings.ToLower(pieceLetters[m.Promo])
	}
	return out
}

// ParseMove resolves player input against the legal moves of a position.
// Accepted forms: coordinate ("e2e4", "e7e8q") and castling ("O-O",
// "o-o-o", "0-0"). Returns a descriptive error for anything else.
func ParseMove(p *Position, input string) (Move, error) {
	in := strings.TrimSpace(strings.ToLower(input))
	in = strings.ReplaceAll(in, "0", "o")

	legal := p.LegalMoves()

	if in == "o-o" || in == "o-o-o" {
		rank := 0
		if p.Turn == Black {
			rank = 7
		}
		toFile := 6
		if in == "o-o-o" {
			toFile = 2
		}
		want := Move{From: sqAt(4, rank), To: sqAt(toFile, rank)}
		for _, m := range legal {
			if m == want {
				return m, nil
			}
		}
		return Move{}, fmt.Errorf("castling is not legal here")
	}

	if len(in) < 4 || len(in) > 5 {
		return Move{}, fmt.Errorf("moves look like e2e4 (or e7e8q to promote, o-o to castle)")
	}
	from, err := parseSquare(in[0:2])
	if err != nil {
		return Move{}, fmt.Errorf("moves look like e2e4 (or e7e8q to promote, o-o to castle)")
	}
	to, err := parseSquare(in[2:4])
	if err != nil {
		return Move{}, fmt.Errorf("moves look like e2e4 (or e7e8q to promote, o-o to castle)")
	}
	promo := Empty
	if len(in) == 5 {
		switch in[4] {
		case 'q':
			promo = Queen
		case 'r':
			promo = Rook
		case 'b':
			promo = Bishop
		case 'n':
			promo = Knight
		default:
			return Move{}, fmt.Errorf("promotion piece must be q, r, b, or n")
		}
	}

	want := Move{From: from, To: to, Promo: promo}
	for _, m := range legal {
		if m == want {
			return m, nil
		}
	}
	// A promotion move without the piece suffix: default to queen if that
	// is the only ambiguity.
	if promo == Empty {
		if m := (Move{From: from, To: to, Promo: Queen}); containsMove(legal, m) {
			return m, nil
		}
	}
	if p.pieceAt(from).Type == Empty || p.pieceAt(from).Color != p.Turn {
		return Move{}, fmt.Errorf("no piece of yours on %s", from.Name())
	}
	return Move{}, fmt.Errorf("%s is not a legal move", input)
}

func containsMove(list []Move, m Move) bool {
	for _, x := range list {
		if x == m {
			return true
		}
	}
	return false
}

// SAN renders a move in standard algebraic notation for the position it is
// played in ("Nf3", "exd5", "O-O", "e8=Q+", "Qxf7#").
func (p *Position) SAN(m Move) string {
	pc := p.pieceAt(m.From)
	next := p.Apply(m)

	suffix := ""
	if next.InCheck(next.Turn) {
		if len(next.LegalMoves()) == 0 {
			suffix = "#"
		} else {
			suffix = "+"
		}
	}

	if pc.Type == King && abs(fileOf(m.To)-fileOf(m.From)) == 2 {
		if fileOf(m.To) == 6 {
			return "O-O" + suffix
		}
		return "O-O-O" + suffix
	}

	isCapture := p.pieceAt(m.To).Type != Empty || (pc.Type == Pawn && m.To == p.EnPassant)

	if pc.Type == Pawn {
		out := ""
		if isCapture {
			out = string(rune('a'+fileOf(m.From))) + "x"
		}
		out += m.To.Name()
		if m.Promo != Empty {
			out += "=" + pieceLetters[m.Promo]
		}
		return out + suffix
	}

	// Disambiguate among same-type pieces that can also reach the target.
	var rivals []Square
	for _, other := range p.LegalMoves() {
		if other.To == m.To && other.From != m.From && p.pieceAt(other.From).Type == pc.Type {
			rivals = append(rivals, other.From)
		}
	}
	disamb := ""
	if len(rivals) > 0 {
		sameFile, sameRank := false, false
		for _, r := range rivals {
			if fileOf(r) == fileOf(m.From) {
				sameFile = true
			}
			if rankOf(r) == rankOf(m.From) {
				sameRank = true
			}
		}
		switch {
		case !sameFile:
			disamb = string(rune('a' + fileOf(m.From)))
		case !sameRank:
			disamb = string(rune('1' + rankOf(m.From)))
		default:
			disamb = m.From.Name()
		}
	}

	out := pieceLetters[pc.Type] + disamb
	if isCapture {
		out += "x"
	}
	return out + m.To.Name() + suffix
}
