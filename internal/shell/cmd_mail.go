package shell

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
)

// readBody reads lines until '.' on its own line or EOF/error.
func readBody(s *Session, hint string) string {
	if hint != "" {
		s.Println(hint)
	}
	rl := s.newRL()
	var lines []string
	for {
		line, err := rl.ReadLine("")
		if err != nil || line == "." {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// confirm asks a yes/no question and returns true for 'y'.
func confirm(s *Session, prompt string) bool {
	s.Print(prompt)
	rl := s.newRL()
	ans, _ := rl.ReadLine("")
	return strings.ToLower(strings.TrimSpace(ans)) == "y"
}

func cmdMail(s *Session, args []string) error {
	if len(args) > 0 {
		return composeMail(s, args[0], "", nil)
	}
	return mailbox(s)
}

func mailbox(s *Session) error {
	msgs, err := db.GetInbox(s.ctx, s.db, s.User.ID)
	if err != nil {
		s.Println("mail: error reading inbox")
		return nil
	}
	if len(msgs) == 0 {
		s.Println("No messages.")
		return nil
	}

	unread := 0
	for _, m := range msgs {
		if m.ReadAt == nil {
			unread++
		}
	}

	s.Printf("Mailbox — %d message(s), %d unread\r\n\r\n", len(msgs), unread)
	printMailList(s, msgs)

	rl := s.newRL()
	current := -1

	for {
		s.Print("\r\n[number, d<n>=delete, r<n>=reply, n=next unread, l=list, q=quit]\r\n? ")
		line, err := rl.ReadLine("")
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "q" || line == "quit":
			return nil

		case line == "l" || line == "list":
			printMailList(s, msgs)

		case line == "n":
			start := current + 1
			found := false
			for i := start; i < len(msgs); i++ {
				if msgs[i].ReadAt == nil {
					current = i
					showMessage(s, &msgs[i])
					db.MarkMailRead(s.ctx, s.db, msgs[i].ID)
					now := time.Now()
					msgs[i].ReadAt = &now
					found = true
					break
				}
			}
			if !found {
				s.Println("No more unread messages.")
			}

		case strings.HasPrefix(line, "d"):
			n, ok := parseMailNum(strings.TrimSpace(line[1:]), len(msgs))
			if !ok && current >= 0 {
				n, ok = current+1, true
			}
			if ok {
				db.DeleteMailForRecipient(s.ctx, s.db, msgs[n-1].ID, s.User.ID)
				s.Printf("Message %d deleted.\r\n", n)
				msgs = append(msgs[:n-1], msgs[n:]...)
				current = -1
				if len(msgs) == 0 {
					s.Println("No more messages.")
					return nil
				}
				printMailList(s, msgs)
			}

		case strings.HasPrefix(line, "r"):
			n, ok := parseMailNum(strings.TrimSpace(line[1:]), len(msgs))
			if !ok && current >= 0 {
				n, ok = current+1, true
			}
			if ok {
				m := msgs[n-1]
				subj := m.Subject
				if !strings.HasPrefix(strings.ToLower(subj), "re:") {
					subj = "Re: " + subj
				}
				composeMail(s, m.SenderName, subj, &m.ID)
				// Refresh so any new messages (including self-replies) appear.
				msgs, _ = db.GetInbox(s.ctx, s.db, s.User.ID)
				printMailList(s, msgs)
				current = -1
			}

		default:
			if n, ok := parseMailNum(line, len(msgs)); ok {
				current = n - 1
				showMessage(s, &msgs[current])
				db.MarkMailRead(s.ctx, s.db, msgs[current].ID)
				now := time.Now()
				msgs[current].ReadAt = &now
			} else if line != "" {
				s.Println("Unknown command. Type 'q' to quit.")
			}
		}
	}
	return nil
}

func printMailList(s *Session, msgs []db.MailMessage) {
	s.Printf("  %-3s  %-16s  %-36s  %s\r\n", "N", "From", "Subject", "Date")
	s.HLine()
	for i, m := range msgs {
		unread := " "
		if m.ReadAt == nil {
			unread = "*"
		}
		subj := m.Subject
		if len(subj) > 36 {
			subj = subj[:33] + "..."
		}
		from := m.SenderName
		if len(from) > 16 {
			from = from[:16]
		}
		s.Printf("  %s%3d  %-16s  %-36s  %s\r\n",
			unread, i+1, from, subj,
			m.CreatedAt.Local().Format("Jan 2 15:04"),
		)
	}
	s.HLine()
}

func showMessage(s *Session, m *db.MailMessage) {
	s.Println("")
	s.HLine()
	s.Printf("  From:    %s\r\n", m.SenderName)
	s.Printf("  Date:    %s\r\n", m.CreatedAt.Local().Format("Mon Jan 2 15:04:05 MST 2006"))
	s.Printf("  Subject: %s\r\n", m.Subject)
	s.HLine()
	s.Println("")
	for _, line := range strings.Split(m.Body, "\n") {
		s.Printf("  %s\r\n", line)
	}
	s.Println("")
}

func composeMail(s *Session, recipientName, subject string, inReplyTo *string) error {
	// Look up recipient
	u, err := db.GetUserByUsername(s.ctx, s.db, recipientName)
	if err != nil {
		s.Printf("mail: %s: no such user\r\n", recipientName)
		return nil
	}

	s.Printf("To: %s\r\n", u.Username)

	if subject == "" {
		s.Print("Subject: ")
		rl := s.newRL()
		subject, _ = rl.ReadLine("")
		if subject == "" {
			subject = "(no subject)"
		}
	} else {
		s.Printf("Subject: %s\r\n", subject)
	}

	body := readBody(s, "Message (end with '.' on a line by itself):")
	if body == "" {
		s.Println("Cancelled — empty message.")
		return nil
	}

	// Append signature
	if s.User.Signature != "" {
		body += "\n\n-- \n" + s.User.Signature
	}

	if !confirm(s, fmt.Sprintf("Send to %s? [y/n]: ", u.Username)) {
		s.Println("Cancelled.")
		return nil
	}

	if _, err := db.SendMail(s.ctx, s.db, s.User.ID, u.ID, subject, body, inReplyTo); err != nil {
		s.Println("mail: error sending message")
		return nil
	}
	s.Printf("Message sent to %s.\r\n", u.Username)

	// Vacation auto-reply
	if u.Vacation && u.VacationMessage != "" {
		if ok, _ := db.ShouldSendVacationReply(s.ctx, s.db, u.ID, s.User.ID); ok {
			vacSubj := "Auto-reply: " + subject
			db.SendMail(s.ctx, s.db, u.ID, s.User.ID, vacSubj, u.VacationMessage, nil)
			db.RecordVacationReply(s.ctx, s.db, u.ID, s.User.ID)
			s.Printf("[Auto-reply from %s received]\r\n", u.Username)
		}
	}
	return nil
}

func cmdVacation(s *Session, args []string) error {
	if len(args) == 0 {
		status := "off"
		if s.User.Vacation {
			status = "on"
		}
		s.Printf("Vacation mode: %s\r\n", status)
		if s.User.VacationMessage != "" {
			s.Println("Auto-reply message:")
			for _, line := range strings.Split(s.User.VacationMessage, "\n") {
				s.Printf("  %s\r\n", line)
			}
		}
		s.Println("Usage: vacation on|off|msg")
		return nil
	}

	switch args[0] {
	case "on":
		if err := db.UpdateVacation(s.ctx, s.db, s.User.ID, true, s.User.VacationMessage); err != nil {
			s.Println("vacation: error updating")
			return nil
		}
		s.User.Vacation = true
		s.Println("Vacation mode enabled.")
	case "off":
		if err := db.UpdateVacation(s.ctx, s.db, s.User.ID, false, s.User.VacationMessage); err != nil {
			s.Println("vacation: error updating")
			return nil
		}
		s.User.Vacation = false
		s.Println("Vacation mode disabled.")
	case "msg":
		msg := readBody(s, "Enter auto-reply message (end with '.' on a line by itself):")
		if err := db.UpdateVacation(s.ctx, s.db, s.User.ID, s.User.Vacation, msg); err != nil {
			s.Println("vacation: error saving message")
			return nil
		}
		s.User.VacationMessage = msg
		s.Println("Vacation message saved.")
	default:
		usageError(s, "vacation", "on|off|msg")
	}
	return nil
}

func parseMailNum(s string, max int) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > max {
		return 0, false
	}
	return n, true
}
