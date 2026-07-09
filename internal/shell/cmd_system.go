package shell

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/presence"
)

var serverStart = time.Now()

func cmdClear(s *Session, _ []string) error {
	s.Write([]byte("\x1b[2J\x1b[H"))
	return nil
}

func cmdUptime(s *Session, _ []string) error {
	d := time.Since(serverStart).Round(time.Second)
	n := s.hub.Count()
	s.Printf("up %s,  %d user%s online\r\n", fmtUptime(d), n, plural(n))
	return nil
}

func cmdLogout(s *Session, _ []string) error {
	s.Println("Goodbye.")
	return io.EOF
}

func cmdWall(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("wall: permission denied")
		return nil
	}
	var message string
	if len(args) > 0 {
		message = strings.Join(args, " ")
	} else {
		message = readBody(s, "Enter broadcast message (end with '.' on a line by itself):")
	}
	if message == "" {
		return nil
	}
	message = sanitizeMessage(message)

	entries := s.hub.List()
	sent := 0
	for _, e := range entries {
		if e.SessionID == s.ID {
			continue
		}
		notice := presence.WriteNotice{
			Kind:    presence.NoticeWall,
			From:    s.User.Username,
			Message: message,
		}
		select {
		case e.WriteCh <- notice:
			sent++
		default:
		}
	}
	s.Printf("wall: broadcast sent to %d user%s\r\n", sent, plural(sent))
	return nil
}

func cmdHelp(s *Session, args []string) error {
	if len(args) > 0 {
		return helpForCommand(s, args[0])
	}

	s.HLine()
	s.Println("  alternate.sh — available commands")
	s.HLine()

	cmds := commandList()
	sort.Strings(cmds)
	printColumns(s, cmds, 4)

	s.Println("")
	s.Println("  Type 'help <command>' for details.")
	s.HLine()
	return nil
}

func helpForCommand(s *Session, cmd string) error {
	help := map[string]string{
		"finger":   "finger [user[@host]]   — show user info; @host queries a federated node",
		"who":      "who                    — list logged-in users",
		"rwho":     "rwho                   — list logged-in users across all federated nodes",
		"w":        "w                      — list users with current activity",
		"last":     "last [user]            — show login history",
		"write":    "write <user> [msg]     — send a message to a logged-in user",
		"talk":     "talk <user> [user...]  — split-screen live chat; each party runs 'talk <the others>'",
		"call":     "call [-a|-r] <user[@host]> — live A/V call (web client); -a voice only; answer with 'call <caller>', decline with -r",
		"mesg":     "mesg [y|n]             — enable/disable incoming messages",
		"motd":     "motd [set]             — display the message of the day; 'set' edits it (admin)",
		"msgs":     "msgs [-q]              — read system messages",
		"fortune":  "fortune                — display a random fortune",
		"plan":     "plan                   — edit your ~/.plan",
		"project":  "project [text]         — set your current project",
		"public":   "public [user]          — edit or read a public page",
		"passwd":   "passwd                 — change your password",
		"chfn":     "chfn                   — change finger information",
		"mail":     "mail [user[@host]]     — read your mailbox, or send mail (cross-node mail is queued)",
		"biff":     "biff [y|n]             — toggle new-mail notifications during your session",
		"vacation": "vacation [on|off|msg]  — manage your vacation auto-reply",
		"news":     "news                   — browse newsgroups and read articles",
		"post":     "post [group]           — post an article to a newsgroup",
		"calendar": "calendar [edit]        — show upcoming events, or edit your calendar",
		"wall":     "wall [msg]             — broadcast to all users (admin only)",
		"node":     "node [list|add|remove] — manage federation peers (admin only)",
		"clear":    "clear                  — clear the screen",
		"uptime":   "uptime                 — show server uptime and user count",
		"logout":   "logout                 — end your session",
		"help":     "help [cmd]             — show this help or help for a command",
	}

	// Resolve aliases so e.g. 'help rn' works.
	if canonical, ok := aliases[cmd]; ok {
		cmd = canonical
	}

	if h, ok := help[cmd]; ok {
		s.Println(h)
	} else {
		s.Printf("help: no help available for '%s'\r\n", cmd)
	}
	return nil
}

func fmtUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d day%s %02d:%02d", days, plural(days), hours, mins)
	}
	return fmt.Sprintf("%02d:%02d", hours, mins)
}
