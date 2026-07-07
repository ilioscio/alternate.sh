package shell

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session holds all state for a connected user.
type Session struct {
	ID      string
	User    *db.User
	TTY     string
	From    string
	LoginID string // row ID in login_history

	mu   sync.Mutex // guards terminal writes
	r    io.Reader
	w    io.Writer
	rows int
	cols int

	writeCh chan presence.WriteNotice

	hub *presence.Hub
	db  *pgxpool.Pool
	cfg *config.Config
	ctx context.Context
}

func NewSession(
	ctx context.Context,
	id, tty, from string,
	r io.Reader, w io.Writer,
	rows, cols int,
	user *db.User,
	hub *presence.Hub,
	pool *pgxpool.Pool,
	cfg *config.Config,
) *Session {
	return &Session{
		ID:      id,
		User:    user,
		TTY:     tty,
		From:    from,
		r:       r,
		w:       w,
		rows:    rows,
		cols:    cols,
		writeCh: make(chan presence.WriteNotice, 8),
		hub:     hub,
		db:      pool,
		cfg:     cfg,
		ctx:     ctx,
	}
}

// Write sends bytes to the terminal under the session mutex so that background
// write/talk notifications never interleave with REPL output.
func (s *Session) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(b)
}

func (s *Session) Print(a ...any) {
	s.Write([]byte(fmt.Sprint(a...)))
}

func (s *Session) Println(a ...any) {
	s.Write([]byte(fmt.Sprint(a...) + "\r\n"))
}

func (s *Session) Printf(format string, a ...any) {
	s.Write([]byte(fmt.Sprintf(format, a...)))
}

func (s *Session) HLine() {
	w := s.cols
	if w <= 0 {
		w = 72
	}
	line := make([]byte, w)
	for i := range line {
		line[i] = '-'
	}
	s.Write(line)
	s.Write([]byte("\r\n"))
}

// SetState updates what this session is "doing" (shown in w).
func (s *Session) SetState(state string) {
	s.hub.SetState(s.ID, state)
}

// Register adds this session to the presence hub.
func (s *Session) Register() {
	s.hub.Register(&presence.Entry{
		SessionID: s.ID,
		Username:  s.User.Username,
		TTY:       s.TTY,
		FromAddr:  s.From,
		LoginAt:   time.Now(),
		State:     "shell",
		MesgOn:    s.User.MesgOn,
		WriteCh:   s.writeCh,
	})
}

// Unregister removes this session from the presence hub.
func (s *Session) Unregister() {
	s.hub.Unregister(s.ID)
}

// Resize updates the terminal dimensions. Called from WebSocket resize messages.
func (s *Session) Resize(cols, rows int) {
	s.mu.Lock()
	s.cols = cols
	s.rows = rows
	s.mu.Unlock()
}

// newRL creates a Readline that queries the terminal width live, so resizes
// that happen between keystrokes (or after the readline was created) are
// always picked up when redrawing wrapped lines.
func (s *Session) newRL() *Readline {
	return &Readline{r: s.r, w: s.w, widthFn: func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cols
	}}
}
