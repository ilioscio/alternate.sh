package shell

import (
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
)

// Admin moderation commands (§5.10, §10.5): ban/unban and the audit log.

func cmdBan(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("ban: permission denied")
		return nil
	}
	if len(args) < 1 {
		usageError(s, "ban", "<username> [reason]")
		return nil
	}
	username := args[0]
	reason := strings.Join(args[1:], " ")

	if username == s.User.Username {
		s.Println("ban: you can't ban yourself")
		return nil
	}
	target, err := db.GetUserByUsername(s.ctx, s.db, username)
	if err != nil {
		s.Printf("ban: %s: no such user\r\n", username)
		return nil
	}
	if target.Admin {
		s.Println("ban: admins can't be banned — demote them first")
		return nil
	}

	if _, err := db.SetBanned(s.ctx, s.db, username, true, reason); err != nil {
		s.Println("ban: error updating user")
		return nil
	}
	db.RecordAudit(s.ctx, s.db, s.User.ID, "ban", username, reason)
	s.Printf("%s is banned. New logins see the ban notice; active sessions end at logout.\r\n", username)
	return nil
}

func cmdUnban(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("unban: permission denied")
		return nil
	}
	if len(args) != 1 {
		usageError(s, "unban", "<username>")
		return nil
	}
	ok, err := db.SetBanned(s.ctx, s.db, args[0], false, "")
	if err != nil || !ok {
		s.Printf("unban: %s: no such user\r\n", args[0])
		return nil
	}
	db.RecordAudit(s.ctx, s.db, s.User.ID, "unban", args[0], "")
	s.Printf("%s may log in again.\r\n", args[0])
	return nil
}

func cmdAudit(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("audit: permission denied")
		return nil
	}
	limit := 30
	if len(args) == 1 {
		if n, ok := parseMailNum(args[0], 500); ok {
			limit = n
		}
	}
	entries, err := db.ListAudit(s.ctx, s.db, limit)
	if err != nil {
		s.Println("audit: error reading log")
		return nil
	}
	if len(entries) == 0 {
		s.Println("Audit log is empty.")
		return nil
	}
	s.Printf("  %-16s %-16s %-16s %-16s %s\r\n", "when", "admin", "action", "target", "detail")
	s.HLine()
	for _, e := range entries {
		detail := e.Detail
		if len(detail) > 40 {
			detail = detail[:37] + "..."
		}
		s.Printf("  %-16s %-16s %-16s %-16s %s\r\n",
			e.CreatedAt.Local().Format("Jan 2 15:04"), e.Actor, e.Action, e.Target, detail)
	}
	s.HLine()
	return nil
}
