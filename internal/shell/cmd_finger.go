package shell

import (
	"fmt"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

func cmdFinger(s *Session, args []string) error {
	if len(args) == 0 {
		return fingerList(s)
	}
	target := args[0]
	// TODO: support user@host for cross-server finger
	if strings.Contains(target, "@") {
		s.Println("remote finger not yet implemented")
		return nil
	}
	return fingerUser(s, target)
}

func fingerList(s *Session) error {
	entries := s.hub.List()
	if len(entries) == 0 {
		s.Println("No users logged in.")
		return nil
	}

	s.Printf("%-16s %-10s %-20s %s\r\n", "Login", "TTY", "Login time", "From")
	s.HLine()
	for _, e := range entries {
		idle := idleStr(e.LastActivity)
		from := e.FromAddr
		if from == "" {
			from = "local"
		}
		s.Printf("%-16s %-10s %-20s %s  idle %s\r\n",
			e.Username, e.TTY,
			e.LoginAt.Format("Mon Jan  2 15:04"),
			from, idle,
		)
	}
	return nil
}

func fingerUser(s *Session, username string) error {
	u, err := db.GetUserByUsername(s.ctx, s.db, username)
	if err != nil {
		s.Printf("finger: %s: no such user\r\n", username)
		return nil
	}

	sessions := s.hub.FindByUsername(username)

	name := u.DisplayName
	if name == "" {
		name = u.Username
	}

	s.Printf("Login: %-24s Name: %s\r\n", u.Username, name)
	s.Printf("Directory: /home/%-15s Shell: /bin/sh\r\n", u.Username)

	if len(sessions) == 0 {
		if u.LastLogin != nil {
			s.Printf("Last login %s\r\n", u.LastLogin.Format("Mon Jan  2 15:04 2006 (MST)"))
		} else {
			s.Println("Never logged in.")
		}
	} else {
		for _, e := range sessions {
			idle := idleStr(e.LastActivity)
			s.Printf("On since %s on %s, idle %s\r\n",
				e.LoginAt.Format("Mon Jan  2 15:04 (MST)"),
				e.TTY, idle,
			)
		}
	}

	if !u.MesgOn {
		s.Println("Messages are off.")
	}

	if u.Plan == "" {
		s.Println("No plan.")
	} else {
		s.Println("Plan:")
		printWrapped(s, u.Plan)
	}

	if u.Project != "" {
		s.Printf("Project: %s\r\n", u.Project)
	}

	return nil
}

func idleStr(since time.Time) string {
	d := time.Since(since).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func printWrapped(s *Session, text string) {
	for _, line := range strings.Split(text, "\n") {
		s.Printf("   %s\r\n", line)
	}
}

// fingerEntry is used by cmd_who to reference presence.Entry without importing it
// elsewhere in the package (it's already imported here).
var _ = (*presence.Entry)(nil)
