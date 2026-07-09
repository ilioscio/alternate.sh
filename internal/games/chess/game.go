package chess

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/games"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// Game is the chess door game: correspondence-style by default (make a move,
// log off; your opponent gets a notice), live when both players are looking
// at the same board.
type Game struct{}

func init() { games.Register(Game{}) }

func (Game) Name() string  { return "chess" }
func (Game) Title() string { return "Chess" }
func (Game) Description() string {
	return "full rules; challenge anyone, play async or live"
}

func (Game) Leaderboard(ctx context.Context, pool *pgxpool.Pool, limit int) ([]games.LeaderEntry, error) {
	rows, err := db.GameScoreTotals(ctx, pool, "chess", "win", limit)
	if err != nil {
		return nil, err
	}
	var out []games.LeaderEntry
	for _, r := range rows {
		out = append(out, games.LeaderEntry{
			Username: r.Username,
			Label:    fmt.Sprintf("%d win%s", r.Total, plural(r.Total)),
		})
	}
	return out, nil
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// maxOpenGames bounds a user's simultaneous games (anti-spam and sanity).
const maxOpenGames = 10

func (Game) Play(gc *games.Context) error {
	t := gc.Term
	for {
		list, err := db.ListChessGamesFor(gc.Ctx, gc.DB, gc.Player.ID, 5)
		if err != nil {
			fmt.Fprintf(t, "chess: error loading games\r\n")
			return nil
		}
		renderGameList(gc, list)

		line, err := t.ReadLine("chess> ")
		if err != nil {
			return nil
		}
		fields := strings.Fields(strings.ToLower(strings.TrimSpace(line)))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "q", "quit", "exit":
			return nil
		case "challenge", "c":
			if len(fields) != 2 {
				fmt.Fprintf(t, "usage: challenge <user>\r\n")
				continue
			}
			challenge(gc, fields[1])
		case "accept", "a":
			respondChallenge(gc, fields, true)
		case "decline", "d":
			respondChallenge(gc, fields, false)
		case "help", "?":
			fmt.Fprintf(t, "commands: <game#> view/play · challenge <user> · accept <game#> · decline <game#> · q\r\n")
		default:
			id, err := strconv.ParseInt(strings.TrimPrefix(fields[0], "#"), 10, 64)
			if err != nil {
				fmt.Fprintf(t, "chess: type a game number, 'challenge <user>', or 'q'\r\n")
				continue
			}
			playGame(gc, id)
		}
	}
}

func renderGameList(gc *games.Context, list []db.ChessGame) {
	t := gc.Term
	fmt.Fprintf(t, "\r\n  chess — %s's games\r\n", gc.Player.Username)
	if len(list) == 0 {
		fmt.Fprintf(t, "  no games yet — 'challenge <user>' to start one\r\n")
	}
	for _, g := range list {
		fmt.Fprintf(t, "  %s\r\n", describeGame(gc, &g))
	}
	fmt.Fprintf(t, "  [<game#> play · challenge <user> · accept/decline <game#> · q]\r\n")
}

// describeGame renders one list line from the player's perspective.
func describeGame(gc *games.Context, g *db.ChessGame) string {
	me := gc.Player.ID
	opp, myColor := g.BlackName, "white"
	if g.BlackID == me {
		opp, myColor = g.WhiteName, "black"
	}

	switch g.Status {
	case "pending":
		if challengedMe(gc, g) {
			return fmt.Sprintf("#%-4d %s challenges you! — 'accept %d' or 'decline %d'", g.ID, opp, g.ID, g.ID)
		}
		return fmt.Sprintf("#%-4d challenge sent to %s — waiting", g.ID, opp)
	case "active":
		pos, _, _, err := replay(g.Moves)
		if err != nil {
			return fmt.Sprintf("#%-4d vs %s — corrupt game", g.ID, opp)
		}
		turn := "their move"
		if (pos.Turn == White) == (myColor == "white") {
			turn = "YOUR MOVE"
		}
		return fmt.Sprintf("#%-4d vs %-16s (you: %s)  %s", g.ID, opp, myColor, turn)
	default:
		return fmt.Sprintf("#%-4d vs %-16s — %s", g.ID, opp, resultText(g, me))
	}
}

// challengedMe reports whether this pending game is waiting on ME: while a
// game is pending, winner_id holds the challenger's id (see
// db.CreateChessGame), so the challenged party is the other player.
func challengedMe(gc *games.Context, g *db.ChessGame) bool {
	return g.Status == "pending" && g.WinnerID != nil && *g.WinnerID != gc.Player.ID
}

func challenge(gc *games.Context, username string) {
	t := gc.Term
	if username == gc.Player.Username {
		fmt.Fprintf(t, "chess: you can't challenge yourself\r\n")
		return
	}
	opp, err := db.GetUserByUsername(gc.Ctx, gc.DB, username)
	if err != nil {
		fmt.Fprintf(t, "chess: %s: no such user\r\n", username)
		return
	}

	open, err := db.ListChessGamesFor(gc.Ctx, gc.DB, gc.Player.ID, 0)
	if err == nil {
		active := 0
		for _, g := range open {
			if g.Status == "pending" || g.Status == "active" {
				active++
				if g.Status == "pending" && (g.WhiteID == opp.ID || g.BlackID == opp.ID) {
					fmt.Fprintf(t, "chess: you already have a pending game with %s (#%d)\r\n", username, g.ID)
					return
				}
			}
		}
		if active >= maxOpenGames {
			fmt.Fprintf(t, "chess: you have %d open games — finish some first\r\n", active)
			return
		}
	}

	// Coin flip for colors.
	whiteID, blackID := gc.Player.ID, opp.ID
	if gc.Rand.Intn(2) == 1 {
		whiteID, blackID = opp.ID, gc.Player.ID
	}
	id, err := db.CreateChessGame(gc.Ctx, gc.DB, whiteID, blackID, gc.Player.ID)
	if err != nil {
		fmt.Fprintf(t, "chess: could not create game\r\n")
		return
	}

	gc.Notify(username, fmt.Sprintf("chess: %s challenges you to a game (#%d) — type 'chess' to respond",
		gc.Player.Username, id))
	fmt.Fprintf(t, "Challenge sent to %s (game #%d). You play %s.\r\n",
		username, id, colorName(whiteID == gc.Player.ID))
}

func colorName(white bool) string {
	if white {
		return "white"
	}
	return "black"
}

func respondChallenge(gc *games.Context, fields []string, accept bool) {
	t := gc.Term
	if len(fields) != 2 {
		fmt.Fprintf(t, "usage: %s <game#>\r\n", fields[0])
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(fields[1], "#"), 10, 64)
	if err != nil {
		fmt.Fprintf(t, "usage: %s <game#>\r\n", fields[0])
		return
	}
	g, err := db.GetChessGame(gc.Ctx, gc.DB, id)
	if err != nil || (g.WhiteID != gc.Player.ID && g.BlackID != gc.Player.ID) {
		fmt.Fprintf(t, "chess: no such game\r\n")
		return
	}
	if g.Status != "pending" {
		fmt.Fprintf(t, "chess: game #%d is not awaiting a response\r\n", id)
		return
	}
	if !challengedMe(gc, g) {
		fmt.Fprintf(t, "chess: you issued this challenge — waiting on your opponent\r\n")
		return
	}

	opp := g.WhiteName
	if g.WhiteID == gc.Player.ID {
		opp = g.BlackName
	}
	if accept {
		if err := db.UpdateChessGame(gc.Ctx, gc.DB, id, "", "active", nil, nil); err != nil {
			fmt.Fprintf(t, "chess: error accepting\r\n")
			return
		}
		gc.Notify(opp, fmt.Sprintf("chess: %s accepted your challenge — game #%d is on", gc.Player.Username, id))
		fmt.Fprintf(t, "Game #%d is on — you play %s.\r\n", id,
			colorName(g.WhiteID == gc.Player.ID))
		playGame(gc, id)
		return
	}
	db.UpdateChessGame(gc.Ctx, gc.DB, id, "", "declined", nil, nil)
	gc.Notify(opp, fmt.Sprintf("chess: %s declined your challenge (#%d)", gc.Player.Username, id))
	fmt.Fprintf(t, "Challenge #%d declined.\r\n", id)
}

// replay rebuilds a position from the stored move list, returning the
// repetition counts and SAN transcript along the way.
func replay(moveStr string) (Position, map[string]int, []string, error) {
	p := StartPos()
	counts := map[string]int{p.RepetitionKey(): 1}
	var sans []string
	for _, tok := range strings.Fields(moveStr) {
		m, err := ParseMove(&p, tok)
		if err != nil {
			return p, nil, nil, errors.New("stored move list is invalid: " + err.Error())
		}
		sans = append(sans, p.SAN(m))
		p = p.Apply(m)
		counts[p.RepetitionKey()]++
	}
	return p, counts, sans, nil
}

func resultText(g *db.ChessGame, me string) string {
	switch g.Status {
	case "checkmate", "resigned":
		how := "checkmate"
		if g.Status == "resigned" {
			how = "resignation"
		}
		if g.WinnerID != nil && *g.WinnerID == me {
			return "you won by " + how
		}
		return "you lost by " + how
	case "stalemate":
		return "draw (stalemate)"
	case "draw":
		return "draw"
	case "declined":
		return "challenge declined"
	}
	return g.Status
}

// playGame is the board view: renders, takes moves when it's your turn,
// watches for the opponent when it isn't.
func playGame(gc *games.Context, id int64) {
	t := gc.Term

	g, err := db.GetChessGame(gc.Ctx, gc.DB, id)
	if err != nil || (g.WhiteID != gc.Player.ID && g.BlackID != gc.Player.ID) {
		fmt.Fprintf(t, "chess: no such game\r\n")
		return
	}
	if g.Status == "pending" {
		if challengedMe(gc, g) {
			fmt.Fprintf(t, "chess: game #%d awaits your answer — 'accept %d' or 'decline %d'\r\n", id, id, id)
		} else {
			fmt.Fprintf(t, "chess: game #%d awaits your opponent's answer\r\n", id)
		}
		return
	}

	// Join the game's live room: when both players are here, moves land
	// instantly; the stand-in notice system covers everyone else.
	member, _, ok := gc.Hub.Rooms.JoinID(
		fmt.Sprintf("game:chess:%d", id),
		[]string{g.WhiteName, g.BlackName},
		fmt.Sprintf("chess-%s-%d", gc.Player.Username, id),
		gc.Player.Username,
	)
	if !ok {
		fmt.Fprintf(t, "chess: could not join game room\r\n")
		return
	}
	defer member.Leave()

	// Live watcher: a one-line nudge, never a full redraw mid-prompt.
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		for ev := range member.Recv {
			if ev.Kind == presence.EventData {
				fmt.Fprintf(t, "\r\n[%s — press Enter to refresh]\r\n", string(ev.Data))
			}
		}
	}()

	for {
		g, err = db.GetChessGame(gc.Ctx, gc.DB, id)
		if err != nil {
			return
		}
		pos, counts, sans, rerr := replay(g.Moves)
		if rerr != nil {
			fmt.Fprintf(t, "chess: %v\r\n", rerr)
			return
		}

		iAmWhite := g.WhiteID == gc.Player.ID
		renderBoard(t, &pos, !iAmWhite, g, sans)

		if g.Status != "active" {
			fmt.Fprintf(t, "  %s\r\n", resultText(g, gc.Player.ID))
			return
		}

		myTurn := (pos.Turn == White) == iAmWhite
		if !myTurn {
			line, err := t.ReadLine("waiting — Enter to refresh, q to leave: ")
			if err != nil || strings.TrimSpace(strings.ToLower(line)) == "q" {
				return
			}
			continue
		}

		if g.DrawOffer != nil && *g.DrawOffer != gc.Player.ID {
			fmt.Fprintf(t, "  your opponent offers a draw — type 'draw' to accept, or just move to play on\r\n")
		}
		line, err := t.ReadLine("your move (e2e4 · o-o · resign · draw · q): ")
		if err != nil {
			return
		}
		input := strings.TrimSpace(strings.ToLower(line))
		switch input {
		case "":
			continue
		case "q", "quit":
			return
		case "resign":
			finishGame(gc, g, "resigned", opponentID(g, gc.Player.ID),
				fmt.Sprintf("chess: %s resigned game #%d — you win!", gc.Player.Username, id), member)
			continue
		case "draw":
			if g.DrawOffer != nil && *g.DrawOffer != gc.Player.ID {
				finishGame(gc, g, "draw", "",
					fmt.Sprintf("chess: %s accepted your draw offer — game #%d is drawn", gc.Player.Username, id), member)
				continue
			}
			me := gc.Player.ID
			db.UpdateChessGame(gc.Ctx, gc.DB, id, g.Moves, g.Status, g.WinnerID, &me)
			gc.Notify(opponentName(g, gc.Player.ID),
				fmt.Sprintf("chess: %s offers a draw in game #%d", gc.Player.Username, id))
			member.Send([]byte(gc.Player.Username + " offers a draw"))
			fmt.Fprintf(t, "  draw offered — it stands until %s moves\r\n", opponentName(g, gc.Player.ID))
			continue
		}

		m, err := ParseMove(&pos, input)
		if err != nil {
			fmt.Fprintf(t, "  %v\r\n", err)
			continue
		}
		san := pos.SAN(m)
		next := pos.Apply(m)
		counts[next.RepetitionKey()]++

		newMoves := strings.TrimSpace(g.Moves + " " + m.Format())
		status, winner := "active", ""
		switch next.PositionStatus() {
		case Checkmate:
			status, winner = "checkmate", gc.Player.ID
		case Stalemate:
			status = "stalemate"
		case DrawFifty, DrawMaterial:
			status = "draw"
		}
		if status == "active" && counts[next.RepetitionKey()] >= 3 {
			status = "draw"
		}

		// A move by the non-offerer sweeps any standing draw offer.
		var offer *string
		if g.DrawOffer != nil && *g.DrawOffer == gc.Player.ID {
			offer = g.DrawOffer
		}
		var winPtr *string
		if winner != "" {
			winPtr = &winner
		}
		if err := db.UpdateChessGame(gc.Ctx, gc.DB, id, newMoves, status, winPtr, offer); err != nil {
			fmt.Fprintf(t, "  chess: error saving move\r\n")
			continue
		}

		oppName := opponentName(g, gc.Player.ID)
		switch status {
		case "active":
			notifyUnlessPresent(gc, member, oppName,
				fmt.Sprintf("chess: %s played %s in game #%d — type 'chess' to view", gc.Player.Username, san, id))
			member.Send([]byte(gc.Player.Username + " played " + san))
		case "checkmate":
			db.RecordGameScore(gc.Ctx, gc.DB, "chess", gc.Player.ID, "win", 1)
			gc.Notify(oppName, fmt.Sprintf("chess: %s played %s — checkmate, game #%d", gc.Player.Username, san, id))
			member.Send([]byte(gc.Player.Username + " played " + san + " — checkmate"))
		default:
			gc.Notify(oppName, fmt.Sprintf("chess: game #%d ended in a draw (%s)", id, san))
			member.Send([]byte("game drawn"))
		}
	}
}

func opponentID(g *db.ChessGame, me string) string {
	if g.WhiteID == me {
		return g.BlackID
	}
	return g.WhiteID
}

func opponentName(g *db.ChessGame, me string) string {
	if g.WhiteID == me {
		return g.BlackName
	}
	return g.WhiteName
}

// notifyUnlessPresent skips the write-style notice when the opponent is in
// the game room watching live (they just saw the move land).
func notifyUnlessPresent(gc *games.Context, member *presence.RoomMember, opp, message string) {
	for _, p := range member.Peers() {
		if p == opp {
			return
		}
	}
	gc.Notify(opp, message)
}

// finishGame ends a game with a terminal status and tells the opponent.
func finishGame(gc *games.Context, g *db.ChessGame, status, winnerID, notice string, member *presence.RoomMember) {
	var winPtr *string
	if winnerID != "" {
		winPtr = &winnerID
	}
	db.UpdateChessGame(gc.Ctx, gc.DB, g.ID, g.Moves, status, winPtr, nil)
	if winnerID != "" && winnerID != gc.Player.ID {
		db.RecordGameScore(gc.Ctx, gc.DB, "chess", winnerID, "win", 1)
	}
	gc.Notify(opponentName(g, gc.Player.ID), notice)
	member.Send([]byte(notice))
}

// renderBoard draws the position with inverse-video dark squares, flipped
// for black, plus the game header and recent moves.
func renderBoard(t games.Terminal, p *Position, flip bool, g *db.ChessGame, sans []string) {
	fmt.Fprintf(t, "\r\n  game #%d — %s (white) vs %s (black)\r\n\r\n", g.ID, g.WhiteName, g.BlackName)

	files := "    a  b  c  d  e  f  g  h"
	if flip {
		files = "    h  g  f  e  d  c  b  a"
	}
	fmt.Fprintf(t, "%s\r\n", files)

	for row := 0; row < 8; row++ {
		rank := 7 - row
		if flip {
			rank = row
		}
		fmt.Fprintf(t, "  %d ", rank+1)
		for col := 0; col < 8; col++ {
			file := col
			if flip {
				file = 7 - col
			}
			pc := p.Board[sqAt(file, rank)]
			glyph := " "
			if pc.Type != Empty {
				glyph = pieceGlyph(pc)
			}
			if (file+rank)%2 == 0 { // dark square
				fmt.Fprintf(t, "\x1b[7m %s \x1b[0m", glyph)
			} else {
				fmt.Fprintf(t, " %s ", glyph)
			}
		}
		fmt.Fprintf(t, " %d\r\n", rank+1)
	}
	fmt.Fprintf(t, "%s\r\n", files)

	// Recent moves in SAN, numbered.
	if len(sans) > 0 {
		start := 0
		if len(sans) > 12 {
			start = len(sans) - 12
		}
		var parts []string
		for i := start; i < len(sans); i++ {
			if i%2 == 0 {
				parts = append(parts, fmt.Sprintf("%d.%s", i/2+1, sans[i]))
			} else {
				parts = append(parts, sans[i])
			}
		}
		fmt.Fprintf(t, "\r\n  %s\r\n", strings.Join(parts, " "))
	}
	if p.InCheck(p.Turn) {
		fmt.Fprintf(t, "  CHECK\r\n")
	}
	fmt.Fprintf(t, "\r\n")
}

var glyphs = map[PieceType]string{
	Pawn: "P", Knight: "N", Bishop: "B", Rook: "R", Queen: "Q", King: "K",
}

func pieceGlyph(pc Piece) string {
	g := glyphs[pc.Type]
	if pc.Color == Black {
		return strings.ToLower(g)
	}
	return g
}
