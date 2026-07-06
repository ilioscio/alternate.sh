package shell

import (
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
)

func cmdMotd(s *Session, args []string) error {
	if len(args) > 0 && args[0] == "set" {
		if !s.User.Admin {
			s.Println("motd set: permission denied")
			return nil
		}
		return motdSet(s)
	}
	return printMOTD(s)
}

func printMOTD(s *Session) error {
	motd, err := db.GetMOTD(s.ctx, s.db)
	if err != nil {
		return nil
	}
	s.HLine()
	for _, line := range strings.Split(motd, "\n") {
		s.Printf("  %s\r\n", line)
	}
	s.HLine()
	return nil
}

func cmdMsgs(s *Session, args []string) error {
	quiet := len(args) > 0 && args[0] == "-q"

	if quiet {
		n, err := db.CountSystemMessages(s.ctx, s.db, s.User.ID)
		if err != nil || n == 0 {
			return nil
		}
		s.Printf("[%d new system message(s). Type 'msgs' to read.]\r\n", n)
		return nil
	}

	messages, err := db.GetSystemMessages(s.ctx, s.db, s.User.ID)
	if err != nil || len(messages) == 0 {
		s.Println("No new system messages.")
		return nil
	}

	for i, msg := range messages {
		s.Printf("--- Message %d ---\r\n", i+1)
		for _, line := range strings.Split(msg, "\n") {
			s.Printf("  %s\r\n", line)
		}
	}
	return nil
}

func motdSet(s *Session) error {
	s.Println("Enter new MOTD (end with '.' on a line by itself):")
	rl := s.newRL()
	var lines []string
	for {
		line, err := rl.ReadLine("")
		if err != nil || line == "." {
			break
		}
		lines = append(lines, line)
	}
	body := strings.Join(lines, "\n")
	if err := db.SetMOTD(s.ctx, s.db, body); err != nil {
		s.Println("error saving MOTD")
		return nil
	}
	s.Println("MOTD updated.")
	return nil
}
