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
		s.Println("Enter broadcast message (end with '.' on a line by itself):")
		rl := s.newRL()
		var lines []string
		for {
			line, err := rl.ReadLine("")
			if err != nil || line == "." {
				break
			}
			lines = append(lines, line)
		}
		message = strings.Join(lines, "\n")
	}
	if message == "" {
		return nil
	}

	entries := s.hub.List()
	sent := 0
	for _, e := range entries {
		if e.SessionID == s.ID {
			continue
		}
		// Prefix "WALL:" so the REPL renderer can style it differently.
		notice := presence.WriteNotice{
			From:    "WALL:" + s.User.Username,
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
		"finger":  "finger [user]          — show user info; no arg lists logged-in users",
		"who":     "who                    — list logged-in users",
		"w":       "w                      — list users with current activity",
		"last":    "last [user]            — show login history",
		"write":   "write <user> [msg]     — send a message to a logged-in user",
		"mesg":    "mesg [y|n]             — enable/disable incoming messages",
		"motd":    "motd                   — display message of the day",
		"msgs":    "msgs [-q]              — read system messages",
		"fortune": "fortune                — display a random fortune",
		"plan":    "plan                   — edit your ~/.plan",
		"project": "project [text]         — set your current project",
		"public":  "public [user]          — edit or read a public page",
		"passwd":  "passwd                 — change your password",
		"chfn":    "chfn                   — change finger information",
		"wall":    "wall [msg]             — broadcast to all users (admin only)",
		"clear":   "clear                  — clear the screen",
		"uptime":  "uptime                 — show server uptime and user count",
		"logout":  "logout                 — end your session",
		"help":    "help [cmd]             — show this help or help for a command",
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
