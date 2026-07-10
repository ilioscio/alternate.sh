package shell

import (
	"fmt"
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
)

// Fortunes are a community-contributed pool (§5.8): anyone may submit,
// admins review, only approved fortunes are ever served.

const maxFortuneLen = 512

func cmdFortune(s *Session, args []string) error {
	if len(args) == 0 {
		fortune, err := db.GetRandomFortune(s.ctx, s.db)
		if err != nil {
			return nil
		}
		s.HLine()
		s.Printf("  %s\r\n", fortune)
		s.HLine()
		return nil
	}

	switch args[0] {
	case "submit":
		return fortuneSubmit(s, strings.Join(args[1:], " "))
	case "review":
		if !s.User.Admin {
			s.Println("fortune: review is admin-only")
			return nil
		}
		return fortuneReview(s)
	default:
		usageError(s, "fortune", "[submit [text] | review]")
		return nil
	}
}

func fortuneSubmit(s *Session, text string) error {
	// Anti-spam: a handful per day is plenty of wisdom from anyone.
	if !s.User.Admin {
		if n, _ := db.CountFortunesSubmittedSince(s.ctx, s.db, s.User.ID, "24 hours"); n >= 5 {
			s.Println("fortune: submission limit reached (5/day) — save some wit for tomorrow")
			return nil
		}
	}

	if text == "" {
		s.Println("Enter your fortune (one or more lines, end with '.' on a line by itself):")
		text = readBody(s, "")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		s.Println("Cancelled — empty fortune.")
		return nil
	}
	if len(text) > maxFortuneLen {
		s.Printf("fortune: too long (%d chars, max %d) — brevity is the soul of wit\r\n", len(text), maxFortuneLen)
		return nil
	}

	if err := db.SubmitFortune(s.ctx, s.db, s.User.ID, text); err != nil {
		s.Println("fortune: error submitting")
		return nil
	}
	s.Println("Fortune submitted for review. If approved, it joins the pool.")
	return nil
}

func fortuneReview(s *Session) error {
	pending, err := db.PendingFortunes(s.ctx, s.db)
	if err != nil {
		s.Println("fortune: error reading queue")
		return nil
	}
	if len(pending) == 0 {
		s.Println("No fortunes awaiting review.")
		return nil
	}

	rl := s.newRL()
	for i, f := range pending {
		s.Println("")
		s.Printf("  fortune review — %d of %d (from %s)\r\n", i+1, len(pending), orUnknown(f.Submitter))
		s.HLine()
		for _, line := range strings.Split(f.Body, "\n") {
			s.Printf("  %s\r\n", line)
		}
		s.HLine()
		s.Print("[a=approve, r=reject, s=skip, q=quit] ? ")
		line, err := rl.ReadLine("")
		if err != nil {
			return nil
		}
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "a", "r":
			approve := strings.TrimSpace(strings.ToLower(line)) == "a"
			submitter, ok, err := db.ReviewFortune(s.ctx, s.db, f.ID, approve)
			if err != nil || !ok {
				s.Println("fortune: could not update")
				continue
			}
			if approve {
				s.Println("Approved.")
				db.RecordAudit(s.ctx, s.db, s.User.ID, "fortune.approve", submitter, truncateStr(f.Body, 80))
			} else {
				s.Println("Rejected.")
				db.RecordAudit(s.ctx, s.db, s.User.ID, "fortune.reject", submitter, truncateStr(f.Body, 80))
			}
			if submitter != "" {
				word := "was not approved"
				if approve {
					word = "joined the pool!"
				}
				notifyUser(s, submitter, fmt.Sprintf("fortune: your submission %s", word))
			}
		case "q":
			return nil
		default: // skip
			continue
		}
	}
	s.Println("(end of queue)")
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
