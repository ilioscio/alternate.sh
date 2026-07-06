package shell

import (
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/presence"
)

func cmdWrite(s *Session, args []string) error {
	if len(args) == 0 {
		usageError(s, "write", "<user> [message]")
		return nil
	}
	target := args[0]

	// Can't write to yourself via this path — use talk instead.
	if target == s.User.Username {
		s.Println("write: you can't send a message to yourself")
		return nil
	}

	entries := s.hub.FindByUsername(target)
	if len(entries) == 0 {
		s.Printf("write: %s is not logged in\r\n", target)
		return nil
	}

	// Check mesg status on at least one of their sessions.
	allOff := true
	for _, e := range entries {
		if e.MesgOn {
			allOff = false
			break
		}
	}
	if allOff {
		s.Printf("write: %s has messages turned off\r\n", target)
		return nil
	}

	var message string
	if len(args) > 1 {
		message = strings.Join(args[1:], " ")
	} else {
		// Interactive mode: read until EOF or "."
		s.Printf("Message to %s (end with '.' on a line by itself or Ctrl+D):\r\n", target)
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

	notice := presence.WriteNotice{
		From:    s.User.Username,
		Message: message,
	}
	n := s.hub.Send(target, notice)
	if n == 0 {
		s.Printf("write: could not deliver message to %s\r\n", target)
	}
	return nil
}

func cmdMesg(s *Session, args []string) error {
	if len(args) == 0 {
		if s.User.MesgOn {
			s.Println("mesg: messages are on")
		} else {
			s.Println("mesg: messages are off")
		}
		return nil
	}

	switch args[0] {
	case "y", "yes", "on":
		s.User.MesgOn = true
		s.hub.SetMesg(s.ID, true)
		s.Println("mesg: messages enabled")
	case "n", "no", "off":
		s.User.MesgOn = false
		s.hub.SetMesg(s.ID, false)
		s.Println("mesg: messages disabled")
	default:
		usageError(s, "mesg", "[y|n]")
	}
	return nil
}

// formatWriteNotice formats an incoming write notice for display.
func formatWriteNotice(notice presence.WriteNotice) string {
	ts := time.Now().Format("15:04:05")
	var sb strings.Builder
	sb.WriteString("\r\n")
	sb.WriteString("\x1b[33m") // yellow
	sb.WriteString("Message from ")
	sb.WriteString(notice.From)
	sb.WriteString(" [")
	sb.WriteString(ts)
	sb.WriteString("]...\x1b[0m\r\n")
	for _, line := range strings.Split(notice.Message, "\n") {
		sb.WriteString(line)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\x1b[33mEOF\x1b[0m\r\n")
	return sb.String()
}
