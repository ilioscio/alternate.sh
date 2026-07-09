package shell

import (
	"context"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/federation"
)

// cmdRwho lists logged-in users across this node and all federation peers.
// Local users are shown first, then each reachable peer's users; unreachable
// peers are noted but don't fail the command.
func cmdRwho(s *Session, args []string) error {
	localNode := s.cfg.Server.Hostname

	s.Printf("%-16s %-20s %-10s %s\r\n", "USER", "NODE", "TTY", "LOGIN@")
	s.HLine()

	// Local users.
	for _, e := range s.hub.List() {
		s.Printf("%-16s %-20s %-10s %s\r\n",
			e.Username, localNode+" (local)", e.TTY, e.LoginAt.Local().Format("Jan 2 15:04"))
	}

	peers, err := db.ListPeers(s.ctx, s.db)
	if err != nil {
		s.Println("rwho: error reading peers")
		return nil
	}

	for _, p := range peers {
		fp := federation.Peer{
			Node:    p.Node,
			Address: federation.ResolveAddress(p.Address, p.Node, s.cfg.Federation.ASSPPort),
			Secret:  p.Secret,
		}
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		resp, err := federation.QueryWho(ctx, localNode, fp)
		cancel()
		if err != nil {
			s.Printf("%-16s %-20s %s\r\n", "—", p.Node, "(unreachable)")
			continue
		}
		for _, u := range resp.Users {
			s.Printf("%-16s %-20s %-10s %s\r\n",
				u.Username, p.Node, u.TTY, time.Unix(u.LoginAt, 0).Local().Format("Jan 2 15:04"))
		}
	}
	s.HLine()
	return nil
}
