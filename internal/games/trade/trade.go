// Package trade is the TradeWars-inspired economy game (DESIGN.md §5.9),
// deliberately bounded in v1: a generated sector web, ports dealing three
// commodities at stock-driven prices, cargo holds, credits, and a daily
// turn budget. No combat yet — the economy earns its keep first.
package trade

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/games"
)

const universeSize = 500

var (
	names     = [3]string{"ore", "organics", "equipment"}
	basePrice = [3]int64{25, 45, 90}
)

// price is the stock-driven unit price: scarce stock trades at 1.2× base,
// glutted stock at 0.8×, linearly. The same curve serves both directions —
// a port low on goods charges buyers more AND pays sellers more, which is
// what makes hauling from glut to scarcity profitable.
func price(commodity, stock int) int64 {
	s := stock
	if s < 0 {
		s = 0
	}
	if s > 1000 {
		s = 1000
	}
	p := basePrice[commodity] * int64(1200-s*400/1000) / 1000
	if p < 1 {
		p = 1
	}
	return p
}

// parseCommodity accepts unambiguous names/prefixes ("ore", "org", "eq").
func parseCommodity(s string) (int, bool) {
	switch {
	case s == "ore":
		return db.Ore, true
	case strings.HasPrefix("organics", s) && len(s) >= 3:
		return db.Organics, true
	case strings.HasPrefix("equipment", s) && len(s) >= 2:
		return db.Equipment, true
	}
	return 0, false
}

// Game implements games.Game.
type Game struct{}

func init() { games.Register(Game{}) }

func (Game) Name() string  { return "trade" }
func (Game) Title() string { return "Star Trade" }
func (Game) Description() string {
	return "haul cargo between the stars; turns are precious"
}

func (Game) Leaderboard(ctx context.Context, pool *pgxpool.Pool, limit int) ([]games.LeaderEntry, error) {
	rows, err := db.TradeLeaderboard(ctx, pool, limit)
	if err != nil {
		return nil, err
	}
	var out []games.LeaderEntry
	for _, r := range rows {
		out = append(out, games.LeaderEntry{
			Username: r.Username,
			Label:    fmt.Sprintf("cr %d", r.NetWorth),
		})
	}
	return out, nil
}

func (Game) Play(gc *games.Context) error {
	t := gc.Term

	// First flight ever: chart the universe.
	if n, err := db.TradeUniverseSize(gc.Ctx, gc.DB); err != nil {
		fmt.Fprintf(t, "trade: universe unavailable\r\n")
		return nil
	} else if n == 0 {
		fmt.Fprintf(t, "Charting the universe for the first time...\r\n")
		if err := db.GenerateTradeUniverse(gc.Ctx, gc.DB, gc.Rand, universeSize); err != nil {
			// A concurrent first flight may have won the race; only fail if
			// the universe is still missing.
			if n, _ := db.TradeUniverseSize(gc.Ctx, gc.DB); n == 0 {
				fmt.Fprintf(t, "trade: could not chart universe\r\n")
				return nil
			}
		}
	}

	daily := gc.Limits.TradeTurnsPerDay
	if daily <= 0 {
		daily = 40
	}
	player, err := db.GetOrCreateTradePlayer(gc.Ctx, gc.DB, gc.Player.ID, daily)
	if err != nil {
		fmt.Fprintf(t, "trade: could not board your ship\r\n")
		return nil
	}

	fmt.Fprintf(t, "\r\nSTAR TRADE — sector %d · cr %d · %d turns today\r\n",
		player.SectorID, player.Credits, player.Turns)
	look(gc)

	for {
		line, err := t.ReadLine("trade> ")
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
		case "l", "look":
			look(gc)
		case "w", "warp":
			warp(gc, fields, daily)
		case "p", "port":
			showPort(gc, daily)
		case "buy":
			exchange(gc, fields, true, daily)
		case "sell":
			exchange(gc, fields, false, daily)
		case "st", "status":
			status(gc, daily)
		case "m", "map":
			showMap(gc, daily)
		case "rank", "r":
			rank(gc)
		case "help", "?":
			fmt.Fprintf(t, "commands: look · warp <sector> · port · buy/sell <commodity> <qty> · status · map · rank · q\r\n")
		default:
			fmt.Fprintf(t, "unknown command — try 'help'\r\n")
		}
	}
}

func loadPlayer(gc *games.Context, daily int) *db.TradePlayer {
	p, err := db.GetOrCreateTradePlayer(gc.Ctx, gc.DB, gc.Player.ID, daily)
	if err != nil {
		return nil
	}
	return p
}

func look(gc *games.Context) {
	t := gc.Term
	p := loadPlayer(gc, 1)
	if p == nil {
		return
	}
	sec, err := db.GetTradeSector(gc.Ctx, gc.DB, p.SectorID)
	if err != nil {
		return
	}
	fmt.Fprintf(t, "\r\nSector %d — warps lead to %s\r\n", sec.ID, joinInts(sec.Warps))
	if port, err := db.GetTradePort(gc.Ctx, gc.DB, p.SectorID); err == nil {
		var deals []string
		for c := range names {
			verb := "selling"
			if port.Buys[c] {
				verb = "buying"
			}
			deals = append(deals, fmt.Sprintf("%s %s", verb, names[c]))
		}
		fmt.Fprintf(t, "%s docks here — %s ('port' for prices)\r\n", port.Name, strings.Join(deals, ", "))
	}
}

func warp(gc *games.Context, fields []string, daily int) {
	t := gc.Term
	if len(fields) != 2 {
		fmt.Fprintf(t, "usage: warp <sector>\r\n")
		return
	}
	to, err := strconv.Atoi(fields[1])
	if err != nil {
		fmt.Fprintf(t, "usage: warp <sector>\r\n")
		return
	}
	p := loadPlayer(gc, daily)
	if p == nil {
		return
	}
	sec, err := db.GetTradeSector(gc.Ctx, gc.DB, p.SectorID)
	if err != nil {
		return
	}
	ok := false
	for _, w := range sec.Warps {
		if w == to {
			ok = true
		}
	}
	if !ok {
		fmt.Fprintf(t, "no warp from %d to %d\r\n", p.SectorID, to)
		return
	}
	if err := db.TradeWarp(gc.Ctx, gc.DB, gc.Player.ID, to); err != nil {
		fmt.Fprintf(t, "%v\r\n", err)
		return
	}
	look(gc)
	if p.Turns-1 <= 5 {
		fmt.Fprintf(t, "(%d turns left today)\r\n", p.Turns-1)
	}
}

func showPort(gc *games.Context, daily int) {
	t := gc.Term
	p := loadPlayer(gc, daily)
	if p == nil {
		return
	}
	port, err := db.GetTradePort(gc.Ctx, gc.DB, p.SectorID)
	if err != nil {
		fmt.Fprintf(t, "no port in this sector\r\n")
		return
	}
	fmt.Fprintf(t, "\r\n%s — sector %d\r\n", port.Name, p.SectorID)
	fmt.Fprintf(t, "  %-10s  %-8s  %6s  %6s\r\n", "commodity", "port is", "stock", "price")
	for c := range names {
		dir := "selling"
		if port.Buys[c] {
			dir = "buying"
		}
		fmt.Fprintf(t, "  %-10s  %-8s  %6d  %6d\r\n", names[c], dir, port.Stock[c], price(c, port.Stock[c]))
	}
	fmt.Fprintf(t, "  you: cr %d · holds %d/%d\r\n", p.Credits, p.Cargo[0]+p.Cargo[1]+p.Cargo[2], p.Holds)
}

func exchange(gc *games.Context, fields []string, playerBuys bool, daily int) {
	t := gc.Term
	verb := fields[0]
	if len(fields) != 3 {
		fmt.Fprintf(t, "usage: %s <commodity> <qty>\r\n", verb)
		return
	}
	c, ok := parseCommodity(fields[1])
	if !ok {
		fmt.Fprintf(t, "commodities: ore, organics, equipment\r\n")
		return
	}
	qty, err := strconv.Atoi(fields[2])
	if err != nil || qty <= 0 {
		fmt.Fprintf(t, "usage: %s <commodity> <qty>\r\n", verb)
		return
	}

	p := loadPlayer(gc, daily)
	if p == nil {
		return
	}
	port, err := db.GetTradePort(gc.Ctx, gc.DB, p.SectorID)
	if err != nil {
		fmt.Fprintf(t, "no port in this sector\r\n")
		return
	}
	if playerBuys == port.Buys[c] {
		if playerBuys {
			fmt.Fprintf(t, "%s is buying %s, not selling it\r\n", port.Name, names[c])
		} else {
			fmt.Fprintf(t, "%s is selling %s, not buying it\r\n", port.Name, names[c])
		}
		return
	}

	unit := price(c, port.Stock[c])
	if err := db.TradeExchange(gc.Ctx, gc.DB, gc.Player.ID, p.SectorID, c, qty, unit, playerBuys); err != nil {
		fmt.Fprintf(t, "%v\r\n", err)
		return
	}
	total := unit * int64(qty)
	if playerBuys {
		fmt.Fprintf(t, "bought %d %s at cr %d each — cr %d total\r\n", qty, names[c], unit, total)
	} else {
		fmt.Fprintf(t, "sold %d %s at cr %d each — cr %d total\r\n", qty, names[c], unit, total)
	}
}

func status(gc *games.Context, daily int) {
	t := gc.Term
	p := loadPlayer(gc, daily)
	if p == nil {
		return
	}
	worth := p.Credits + int64(p.Cargo[0])*basePrice[0] + int64(p.Cargo[1])*basePrice[1] + int64(p.Cargo[2])*basePrice[2]
	fmt.Fprintf(t, "\r\nship's log — %s\r\n", gc.Player.Username)
	fmt.Fprintf(t, "  sector   %d\r\n", p.SectorID)
	fmt.Fprintf(t, "  credits  %d\r\n", p.Credits)
	fmt.Fprintf(t, "  cargo    %d ore, %d organics, %d equipment (%d/%d holds)\r\n",
		p.Cargo[0], p.Cargo[1], p.Cargo[2], p.Cargo[0]+p.Cargo[1]+p.Cargo[2], p.Holds)
	fmt.Fprintf(t, "  turns    %d today\r\n", p.Turns)
	fmt.Fprintf(t, "  worth    cr %d\r\n", worth)
}

func showMap(gc *games.Context, daily int) {
	t := gc.Term
	p := loadPlayer(gc, daily)
	if p == nil {
		return
	}
	sec, err := db.GetTradeSector(gc.Ctx, gc.DB, p.SectorID)
	if err != nil {
		return
	}
	fmt.Fprintf(t, "\r\n  %d → %s\r\n", sec.ID, joinInts(sec.Warps))
	for _, w := range sec.Warps {
		if s2, err := db.GetTradeSector(gc.Ctx, gc.DB, w); err == nil {
			marker := ""
			if _, err := db.GetTradePort(gc.Ctx, gc.DB, w); err == nil {
				marker = "  [port]"
			}
			fmt.Fprintf(t, "    %d → %s%s\r\n", w, joinInts(s2.Warps), marker)
		}
	}
}

func rank(gc *games.Context) {
	t := gc.Term
	rows, err := db.TradeLeaderboard(gc.Ctx, gc.DB, 10)
	if err != nil {
		return
	}
	fmt.Fprintf(t, "\r\n  richest traders\r\n")
	for i, r := range rows {
		fmt.Fprintf(t, "  %2d. %-16s cr %d\r\n", i+1, r.Username, r.NetWorth)
	}
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ", ")
}
