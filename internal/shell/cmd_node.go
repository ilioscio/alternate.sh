package shell

import (
	"fmt"
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
)

// cmdNode manages the federation peer registry (admin only).
//
//	node                       list peers
//	node list                  list peers
//	node add <node> [address]  add/update a peer; prompts for the shared secret
//	node remove <node>         remove a peer
func cmdNode(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("node: permission denied")
		return nil
	}

	if len(args) == 0 || args[0] == "list" {
		return nodeList(s)
	}

	switch args[0] {
	case "add":
		return nodeAdd(s, args[1:])
	case "remove", "rm", "del", "delete":
		if len(args) < 2 {
			usageError(s, "node", "remove <node>")
			return nil
		}
		ok, err := db.RemovePeer(s.ctx, s.db, args[1])
		if err != nil {
			s.Println("node: error removing peer")
			return nil
		}
		if !ok {
			s.Printf("node: %s: no such peer\r\n", args[1])
			return nil
		}
		s.Printf("Peer %s removed.\r\n", args[1])
		return nil
	default:
		usageError(s, "node", "[list | add <node> [address] | remove <node>]")
		return nil
	}
}

func nodeList(s *Session) error {
	peers, err := db.ListPeers(s.ctx, s.db)
	if err != nil {
		s.Println("node: error reading peers")
		return nil
	}
	if len(peers) == 0 {
		s.Println("No federation peers configured.")
		s.Println("Add one with: node add <node> [address]")
		return nil
	}
	s.Printf("%-32s %-28s %s\r\n", "NODE", "ADDRESS", "ADDED")
	s.HLine()
	for _, p := range peers {
		addr := p.Address
		if addr == "" {
			addr = fmt.Sprintf("%s:%d (default)", p.Node, s.cfg.Federation.ASSPPort)
		}
		s.Printf("%-32s %-28s %s\r\n", p.Node, addr, p.AddedAt.Local().Format("2006-01-02"))
	}
	s.HLine()
	return nil
}

func nodeAdd(s *Session, args []string) error {
	if len(args) < 1 {
		usageError(s, "node", "add <node> [address]")
		return nil
	}
	node := args[0]
	address := ""
	if len(args) >= 2 {
		address = args[1]
	}

	// Read the shared secret without echoing it into the terminal/scrollback.
	s.Print("Shared secret: ")
	secret, err := readPassword(s.r, s.w)
	if err != nil {
		return nil
	}
	s.Write([]byte("\r\n"))
	secret = strings.TrimSpace(secret)
	if secret == "" {
		s.Println("node: empty secret — aborted.")
		return nil
	}

	if err := db.AddPeer(s.ctx, s.db, node, address, secret); err != nil {
		s.Println("node: error saving peer")
		return nil
	}
	shown := address
	if shown == "" {
		shown = fmt.Sprintf("%s:%d (default)", node, s.cfg.Federation.ASSPPort)
	}
	s.Printf("Peer %s added (%s).\r\n", node, shown)
	s.Println("Note: peering is bilateral — the other node's admin must add this node with the same secret.")
	return nil
}
