package shell

import (
	"fmt"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
)

func cmdWho(s *Session, args []string) error {
	entries := s.hub.List()
	if len(entries) == 0 {
		s.Println("No users logged in.")
		return nil
	}
	for _, e := range entries {
		from := e.FromAddr
		if from == "" {
			from = "local"
		}
		s.Printf("%-16s %-10s %s  (%s)\r\n",
			e.Username, e.TTY,
			e.LoginAt.Format("Jan  2 15:04"),
			from,
		)
	}
	return nil
}

func cmdW(s *Session, args []string) error {
	entries := s.hub.List()
	now := time.Now()

	s.Printf("%s  up %s,  %d user%s\r\n",
		now.Format("15:04:05"),
		uptime(now),
		len(entries),
		plural(len(entries)),
	)
	s.Printf("%-16s %-10s %-20s %-8s %s\r\n", "USER", "TTY", "FROM", "LOGIN@", "WHAT")
	s.HLine()

	for _, e := range entries {
		from := e.FromAddr
		if from == "" {
			from = "local"
		}
		state := e.State
		if state == "" {
			state = "shell"
		}
		s.Printf("%-16s %-10s %-20s %-8s %s\r\n",
			e.Username, e.TTY,
			truncate(from, 20),
			e.LoginAt.Format("15:04"),
			state,
		)
	}
	return nil
}

func cmdLast(s *Session, args []string) error {
	username := ""
	if len(args) > 0 {
		username = args[0]
	}

	records, err := db.GetLoginHistory(s.ctx, s.db, username, 20)
	if err != nil {
		s.Println("error retrieving login history")
		return nil
	}
	if len(records) == 0 {
		s.Println("no login records found")
		return nil
	}

	for _, r := range records {
		var duration string
		if r.LoggedOutAt == nil {
			duration = "still logged in"
		} else {
			d := r.LoggedOutAt.Sub(r.LoggedInAt).Round(time.Second)
			duration = fmt.Sprintf("- %s  (%s)", r.LoggedOutAt.Format("15:04"), formatDuration(d))
		}
		s.Printf("%-16s %-10s %-16s %-20s %s\r\n",
			r.Username, r.TTY, r.FromAddr,
			r.LoggedInAt.Format("Mon Jan  2 15:04"),
			duration,
		)
	}
	return nil
}

func uptime(now time.Time) string {
	// Placeholder — in a real deployment we'd track server start time.
	return "?"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d", h, m)
	}
	return fmt.Sprintf("00:%02d", m)
}

func strings_repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
