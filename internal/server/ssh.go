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
			fmt.Printf("[pubkey] user=%q offered key type=%s\n", ctx.User(), key.Type())
			u, err := db.GetUserByUsername(ctx, pool, ctx.User())
			if err != nil {
				fmt.Printf("[pubkey] user lookup failed: %v\n", err)
				return false
			}
			keys, err := db.GetSSHKeys(ctx, pool, u.ID)
			if err != nil {
				fmt.Printf("[pubkey] key lookup failed: %v\n", err)
				return false
			}
			fmt.Printf("[pubkey] found %d stored key(s) for user %s\n", len(keys), u.Username)
			for _, k := range keys {
				allowed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.KeyData))
				if err != nil {
					fmt.Printf("[pubkey] parse error for stored key: %v | raw=%q\n", err, k.KeyData[:min(60, len(k.KeyData))])
					continue
				}
				eq := gossh.KeysEqual(key, allowed)
				fmt.Printf("[pubkey] comparing: stored_type=%s equal=%v\n", allowed.Type(), eq)
				if eq {
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

	pty, _, hasPTY := gs.Pty()
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
