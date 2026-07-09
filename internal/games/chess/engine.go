// Package chess implements full chess rules from scratch (DESIGN.md §5.9):
// legal move generation (castling, en passant, promotion, pins), check,
// checkmate, stalemate, and draw detection. Correctness is proven by perft
// tests against the standard published node counts.
//
// The representation favors legibility over speed: a 64-square mailbox
// board and copy-make (Apply returns a new Position), which eliminates the
// entire class of unmake bugs and handles subtleties like en-passant pins
// for free — the legality filter simply looks at the resulting position.
package chess

// Color of a side or piece.
type Color uint8

const (
	White Color = iota
	Black
)

func (c Color) Other() Color { return 1 - c }

// PieceType is the kind of piece; Empty marks a vacant square.
type PieceType uint8

const (
	Empty PieceType = iota
	Pawn
	Knight
	Bishop
	Rook
	Queen
	King
)

// Piece is a colored piece (or the empty square).
type Piece struct {
	Type  PieceType
	Color Color
}

// Square indexes the board: a1=0 … h1=7, a8=56 … h8=63. -1 means "none".
type Square int8

const NoSquare Square = -1

func sqAt(file, rank int) Square { return Square(rank*8 + file) }
func fileOf(s Square) int        { return int(s) % 8 }
func rankOf(s Square) int        { return int(s) / 8 }

// Name returns algebraic square names ("e4").
func (s Square) Name() string {
	if s == NoSquare {
		return "-"
	}
	return string(rune('a'+fileOf(s))) + string(rune('1'+rankOf(s)))
}

// Move is from→to with an optional promotion piece type.
type Move struct {
	From, To Square
	Promo    PieceType
}

// Position is a full game state. It is a value type: Apply copies.
type Position struct {
	Board [64]Piece
	Turn  Color

	// Castling rights (king/queen side per color).
	WK, WQ, BK, BQ bool

	// EnPassant is the capture square behind a just-double-pushed pawn.
	EnPassant Square

	HalfMove int // fifty-move-rule clock (plies since pawn move or capture)
	FullMove int
}

// pieceAt is sugar for reading the board.
func (p *Position) pieceAt(s Square) Piece { return p.Board[s] }

// kingSquare finds c's king.
func (p *Position) kingSquare(c Color) Square {
	for s := Square(0); s < 64; s++ {
		if p.Board[s] == (Piece{King, c}) {
			return s
		}
	}
	return NoSquare
}

var knightSteps = [8][2]int{{1, 2}, {2, 1}, {2, -1}, {1, -2}, {-1, -2}, {-2, -1}, {-2, 1}, {-1, 2}}
var kingSteps = [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
var bishopDirs = [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
var rookDirs = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// IsAttacked reports whether square s is attacked by any piece of color by.
func (p *Position) IsAttacked(s Square, by Color) bool {
	f, r := fileOf(s), rankOf(s)

	// Pawns: a white pawn on (f±1, r-1) attacks s; black from r+1.
	pr := r - 1
	if by == Black {
		pr = r + 1
	}
	if pr >= 0 && pr < 8 {
		for _, df := range []int{-1, 1} {
			if pf := f + df; pf >= 0 && pf < 8 {
				if p.Board[sqAt(pf, pr)] == (Piece{Pawn, by}) {
					return true
				}
			}
		}
	}

	for _, st := range knightSteps {
		nf, nr := f+st[0], r+st[1]
		if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 && p.Board[sqAt(nf, nr)] == (Piece{Knight, by}) {
			return true
		}
	}
	for _, st := range kingSteps {
		nf, nr := f+st[0], r+st[1]
		if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 && p.Board[sqAt(nf, nr)] == (Piece{King, by}) {
			return true
		}
	}

	slide := func(dirs [4][2]int, straight bool) bool {
		for _, d := range dirs {
			nf, nr := f+d[0], r+d[1]
			for nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
				pc := p.Board[sqAt(nf, nr)]
				if pc.Type != Empty {
					if pc.Color == by && (pc.Type == Queen ||
						(straight && pc.Type == Rook) || (!straight && pc.Type == Bishop)) {
						return true
					}
					break
				}
				nf += d[0]
				nr += d[1]
			}
		}
		return false
	}
	return slide(rookDirs, true) || slide(bishopDirs, false)
}

// InCheck reports whether c's king is attacked.
func (p *Position) InCheck(c Color) bool {
	return p.IsAttacked(p.kingSquare(c), c.Other())
}

// pseudoMoves generates all moves obeying piece movement (but possibly
// leaving the king in check — LegalMoves filters those).
func (p *Position) pseudoMoves() []Move {
	moves := make([]Move, 0, 48)
	us := p.Turn

	appendMove := func(from, to Square) {
		moves = append(moves, Move{From: from, To: to})
	}
	appendPawn := func(from, to Square) {
		if rk := rankOf(to); rk == 0 || rk == 7 {
			for _, pr := range []PieceType{Queen, Rook, Bishop, Knight} {
				moves = append(moves, Move{From: from, To: to, Promo: pr})
			}
		} else {
			appendMove(from, to)
		}
	}

	for s := Square(0); s < 64; s++ {
		pc := p.Board[s]
		if pc.Type == Empty || pc.Color != us {
			continue
		}
		f, r := fileOf(s), rankOf(s)

		switch pc.Type {
		case Pawn:
			dir, start := 1, 1
			if us == Black {
				dir, start = -1, 6
			}
			// Pushes.
			if one := sqAt(f, r+dir); p.Board[one].Type == Empty {
				appendPawn(s, one)
				if r == start {
					if two := sqAt(f, r+2*dir); p.Board[two].Type == Empty {
						appendMove(s, two)
					}
				}
			}
			// Captures (including en passant).
			for _, df := range []int{-1, 1} {
				nf := f + df
				if nf < 0 || nf > 7 {
					continue
				}
				to := sqAt(nf, r+dir)
				target := p.Board[to]
				if (target.Type != Empty && target.Color != us) || to == p.EnPassant {
					appendPawn(s, to)
				}
			}

		case Knight:
			for _, st := range knightSteps {
				nf, nr := f+st[0], r+st[1]
				if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
					if t := p.Board[sqAt(nf, nr)]; t.Type == Empty || t.Color != us {
						appendMove(s, sqAt(nf, nr))
					}
				}
			}

		case Bishop, Rook, Queen:
			var dirs [][2]int
			if pc.Type != Rook {
				dirs = append(dirs, bishopDirs[:]...)
			}
			if pc.Type != Bishop {
				dirs = append(dirs, rookDirs[:]...)
			}
			for _, d := range dirs {
				nf, nr := f+d[0], r+d[1]
				for nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
					t := p.Board[sqAt(nf, nr)]
					if t.Type == Empty {
						appendMove(s, sqAt(nf, nr))
					} else {
						if t.Color != us {
							appendMove(s, sqAt(nf, nr))
						}
						break
					}
					nf += d[0]
					nr += d[1]
				}
			}

		case King:
			for _, st := range kingSteps {
				nf, nr := f+st[0], r+st[1]
				if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
					if t := p.Board[sqAt(nf, nr)]; t.Type == Empty || t.Color != us {
						appendMove(s, sqAt(nf, nr))
					}
				}
			}
			// Castling: rights present, path empty, king not in or through
			// check. (Landing in check is caught by the legality filter,
			// but we check it here too for clarity.)
			rank := 0
			ks, qs := p.WK, p.WQ
			if us == Black {
				rank, ks, qs = 7, p.BK, p.BQ
			}
			if s == sqAt(4, rank) && !p.IsAttacked(s, us.Other()) {
				if ks &&
					p.Board[sqAt(5, rank)].Type == Empty &&
					p.Board[sqAt(6, rank)].Type == Empty &&
					p.Board[sqAt(7, rank)] == (Piece{Rook, us}) &&
					!p.IsAttacked(sqAt(5, rank), us.Other()) &&
					!p.IsAttacked(sqAt(6, rank), us.Other()) {
					appendMove(s, sqAt(6, rank))
				}
				if qs &&
					p.Board[sqAt(3, rank)].Type == Empty &&
					p.Board[sqAt(2, rank)].Type == Empty &&
					p.Board[sqAt(1, rank)].Type == Empty &&
					p.Board[sqAt(0, rank)] == (Piece{Rook, us}) &&
					!p.IsAttacked(sqAt(3, rank), us.Other()) &&
					!p.IsAttacked(sqAt(2, rank), us.Other()) {
					appendMove(s, sqAt(2, rank))
				}
			}
		}
	}
	return moves
}

// LegalMoves returns every legal move for the side to move.
func (p *Position) LegalMoves() []Move {
	pseudo := p.pseudoMoves()
	legal := pseudo[:0]
	for _, m := range pseudo {
		next := p.Apply(m)
		if !next.IsAttacked(next.kingSquare(p.Turn), next.Turn) {
			legal = append(legal, m)
		}
	}
	return legal
}

// Apply plays a move (assumed pseudo-legal) and returns the new position.
func (p Position) Apply(m Move) Position {
	pc := p.Board[m.From]
	isCapture := p.Board[m.To].Type != Empty

	// En passant capture: the victim pawn is beside, not on, the target.
	if pc.Type == Pawn && m.To == p.EnPassant {
		isCapture = true
		p.Board[sqAt(fileOf(m.To), rankOf(m.From))] = Piece{}
	}

	// Castling: the king moves two files; bring the rook across.
	if pc.Type == King && abs(fileOf(m.To)-fileOf(m.From)) == 2 {
		rank := rankOf(m.From)
		if fileOf(m.To) == 6 { // king side
			p.Board[sqAt(5, rank)] = p.Board[sqAt(7, rank)]
			p.Board[sqAt(7, rank)] = Piece{}
		} else { // queen side
			p.Board[sqAt(3, rank)] = p.Board[sqAt(0, rank)]
			p.Board[sqAt(0, rank)] = Piece{}
		}
	}

	p.Board[m.To] = pc
	p.Board[m.From] = Piece{}
	if m.Promo != Empty {
		p.Board[m.To] = Piece{m.Promo, pc.Color}
	}

	// New en-passant square only right after a double push.
	p.EnPassant = NoSquare
	if pc.Type == Pawn && abs(rankOf(m.To)-rankOf(m.From)) == 2 {
		p.EnPassant = sqAt(fileOf(m.From), (rankOf(m.From)+rankOf(m.To))/2)
	}

	// Castling rights die when kings or rooks move, or rooks are captured.
	touch := func(s Square) {
		switch s {
		case sqAt(4, 0):
			p.WK, p.WQ = false, false
		case sqAt(7, 0):
			p.WK = false
		case sqAt(0, 0):
			p.WQ = false
		case sqAt(4, 7):
			p.BK, p.BQ = false, false
		case sqAt(7, 7):
			p.BK = false
		case sqAt(0, 7):
			p.BQ = false
		}
	}
	touch(m.From)
	touch(m.To)

	if pc.Type == Pawn || isCapture {
		p.HalfMove = 0
	} else {
		p.HalfMove++
	}
	if p.Turn == Black {
		p.FullMove++
	}
	p.Turn = p.Turn.Other()
	return p
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Status is the game-theoretic state of a position.
type Status int

const (
	Ongoing   Status = iota
	Checkmate        // side to move is mated
	Stalemate
	DrawFifty
	DrawMaterial
)

// PositionStatus evaluates mate/stalemate and automatic draws (fifty-move,
// insufficient material). Threefold repetition is the game layer's job via
// RepetitionKey, since it needs history.
func (p *Position) PositionStatus() Status {
	if len(p.LegalMoves()) == 0 {
		if p.InCheck(p.Turn) {
			return Checkmate
		}
		return Stalemate
	}
	if p.HalfMove >= 100 {
		return DrawFifty
	}
	if p.insufficientMaterial() {
		return DrawMaterial
	}
	return Ongoing
}

// insufficientMaterial: K vs K, K+minor vs K, and K+B vs K+B with bishops
// on the same square color.
func (p *Position) insufficientMaterial() bool {
	var bishops []Square
	knights := 0
	for s := Square(0); s < 64; s++ {
		switch p.Board[s].Type {
		case Empty, King:
		case Bishop:
			bishops = append(bishops, s)
		case Knight:
			knights++
		default:
			return false // pawn, rook, or queen on the board
		}
	}
	switch {
	case len(bishops) == 0 && knights == 0:
		return true
	case len(bishops) == 1 && knights == 0:
		return true
	case len(bishops) == 0 && knights == 1:
		return true
	case len(bishops) == 2 && knights == 0:
		a, b := bishops[0], bishops[1]
		// Same-colored squares and opposite owners.
		if (fileOf(a)+rankOf(a))%2 == (fileOf(b)+rankOf(b))%2 &&
			p.Board[a].Color != p.Board[b].Color {
			return true
		}
	}
	return false
}

// RepetitionKey identifies a position for threefold detection: board,
// turn, castling rights, and en-passant square (the FEN-relevant core).
func (p *Position) RepetitionKey() string {
	buf := make([]byte, 0, 72)
	for s := Square(0); s < 64; s++ {
		pc := p.Board[s]
		buf = append(buf, byte(pc.Type)|byte(pc.Color)<<3)
	}
	var flags byte
	if p.WK {
		flags |= 1
	}
	if p.WQ {
		flags |= 2
	}
	if p.BK {
		flags |= 4
	}
	if p.BQ {
		flags |= 8
	}
	if p.Turn == Black {
		flags |= 16
	}
	buf = append(buf, flags, byte(p.EnPassant+1))
	return string(buf)
}

// Perft counts leaf nodes of the legal move tree — the standard engine
// correctness oracle.
func Perft(p Position, depth int) uint64 {
	if depth == 0 {
		return 1
	}
	moves := p.LegalMoves()
	if depth == 1 {
		return uint64(len(moves))
	}
	var n uint64
	for _, m := range moves {
		n += Perft(p.Apply(m), depth-1)
	}
	return n
}
