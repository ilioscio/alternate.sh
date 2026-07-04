package shell

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// Run is the main entry point for a user session. It handles login
// bookkeeping, prints the banner and MOTD, runs the REPL, and cleans up.
func Run(s *Session) {
	defer s.Unregister()
	s.Register()

	if id, err := db.RecordLogin(s.ctx, s.db, s.User.Username, s.TTY, s.From); err == nil {
		s.LoginID = id
	}
	defer func() {
		if s.LoginID != "" {
			db.RecordLogout(s.ctx, s.db, s.LoginID)
		}
		db.UpdateLastLogin(s.ctx, s.db, s.User.ID)
	}()

	printBanner(s)
	if !s.User.HushLogin {
		printMOTD(s)
		s.Println("")
		printMsgsHint(s)
	}
	cmdFortune(s, nil)
	s.Println("")

	// Background goroutine: forward write/wall notices to the terminal.
	// Real Unix write interrupts your terminal mid-line — we do the same.
	// The session mutex ensures our notice bytes don't split an escape sequence
	// that the readline is currently emitting.
	go func() {
		for {
			select {
			case notice := <-s.writeCh:
				s.mu.Lock()
				s.w.Write([]byte(renderNotice(notice)))
				s.mu.Unlock()
			case <-s.ctx.Done():
				return
			}
		}
	}()

	rl := NewReadline(s.r, s.w)
	for {
		select {
		case <-s.ctx.Done():
			s.Println("\r\nServer shutting down. Goodbye.")
			return
		default:
		}

		line, err := rl.ReadLine(buildPrompt(s))
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if err := dispatch(s, line); errors.Is(err, io.EOF) {
			return
		}
	}
}

func printBanner(s *Session) {
	s.Println("")
	s.Printf("  alternate.sh — %s\r\n", s.cfg.Server.Hostname)
	s.Printf("  Logged in as: %s", s.User.Username)
	if s.User.DisplayName != "" {
		s.Printf(" (%s)", s.User.DisplayName)
	}
	s.Println("")
	s.Printf("  %s\r\n", time.Now().Format("Monday, January 2, 2006  15:04 MST"))
	s.Println("")
}

func printMsgsHint(s *Session) {
	n, err := db.CountSystemMessages(s.ctx, s.db, s.User.ID)
	if err != nil || n == 0 {
		return
	}
	s.Printf("  [%d new system message(s) — type 'msgs' to read]\r\n\r\n", n)
}

func buildPrompt(s *Session) string {
	return fmt.Sprintf("%s@%s:~$ ", s.User.Username, s.cfg.Server.Hostname)
}

func renderNotice(n presence.WriteNotice) string {
	ts := time.Now().Format("15:04:05")
	var sb strings.Builder
	sb.WriteString("\r\n")
	if strings.HasPrefix(n.From, "WALL:") {
		from := strings.TrimPrefix(n.From, "WALL:")
		sb.WriteString("\x1b[31m") // red for wall
		sb.WriteString("Broadcast from ")
		sb.WriteString(from)
	} else {
		sb.WriteString("\x1b[33m") // yellow for write
		sb.WriteString("Message from ")
		sb.WriteString(n.From)
	}
	sb.WriteString(" [")
	sb.WriteString(ts)
	sb.WriteString("]...\x1b[0m\r\n")
	for _, line := range strings.Split(n.Message, "\n") {
		sb.WriteString(line)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\x1b[2m(EOF)\x1b[0m\r\n")
	return sb.String()
}
