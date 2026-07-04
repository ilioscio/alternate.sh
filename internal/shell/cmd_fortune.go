package shell

import (
	"github.com/ilioscio/alternate.sh/internal/db"
)

func cmdFortune(s *Session, _ []string) error {
	fortune, err := db.GetRandomFortune(s.ctx, s.db)
	if err != nil {
		return nil
	}
	s.HLine()
	s.Printf("  %s\r\n", fortune)
	s.HLine()
	return nil
}
