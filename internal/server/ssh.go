package server

import (
	"context"
	"fmt"
	"net"

	gossh "github.com/gliderlabs/ssh"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
	"github.com/ilioscio/alternate.sh/internal/shell"
)

type SSHServer struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	hub  *presence.Hub
	srv  *gossh.Server
}

func NewSSH(cfg *config.Config, pool *pgxpool.Pool, hub *presence.Hub) *SSHServer {
	s := &SSHServer{cfg: cfg, pool: pool, hub: hub}

	s.srv = &gossh.Server{
		Addr:    fmt.Sprintf(":%d", cfg.SSH.Port),
		Handler: s.handle,

		PasswordHandler: func(ctx gossh.Context, password string) bool {
			u, err := db.GetUserByUsername(ctx, pool, ctx.User())
			if err != nil {
				return false
			}
			return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
		},

		PublicKeyHandler: func(ctx gossh.Context, key gossh.PublicKey) bool {
			u, err := db.GetUserByUsername(ctx, pool, ctx.User())
			if err != nil {
				return false
			}
			keys, err := db.GetSSHKeys(ctx, pool, u.ID)
			if err != nil {
				return false
			}
			for _, k := range keys {
				allowed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.KeyData))
				if err != nil {
					continue
				}
				if gossh.KeysEqual(key, allowed) {
					return true
				}
			}
			return false
		},
	}

	if cfg.SSH.HostKey != "" {
		gossh.HostKeyFile(cfg.SSH.HostKey)(s.srv)
	} else {
		// Generate an ephemeral host key (dev/test only).
		gossh.NoPty()(s.srv) // suppress warning
	}

	return s
}

func (s *SSHServer) ListenAndServe() error {
	fmt.Printf("SSH server listening on %s\n", s.srv.Addr)
	return s.srv.ListenAndServe()
}

func (s *SSHServer) handle(gs gossh.Session) {
	ctx := context.Background()

	u, err := db.GetUserByUsername(ctx, s.pool, gs.User())
	if err != nil {
		gs.Write([]byte("error: could not load user\r\n"))
		return
	}

	pty, winCh, hasPTY := gs.Pty()
	rows, cols := 24, 80
	if hasPTY {
		rows = pty.Window.Height
		cols = pty.Window.Width
	}

	from := remoteAddr(gs.RemoteAddr())
	tty := s.hub.AllocateTTY()
	sessionID := gs.Context().SessionID()

	sess := shell.NewSession(ctx, sessionID, tty, from,
		gs, gs, rows, cols,
		u, s.hub, s.pool, s.cfg,
	)

	// Forward SSH window-change requests to the session so line editing
	// always knows the real terminal width.
	if hasPTY {
		go func() {
			for win := range winCh {
				sess.Resize(win.Width, win.Height)
			}
		}()
	}

	shell.Run(sess)
}

func remoteAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
