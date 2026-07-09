package server

import (
	"context"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/assp"
	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/federation"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// FederationServer runs the ASSP listener and bridges local presence/finger
// data to peers. It is only started when federation is enabled.
type FederationServer struct {
	addr string
	srv  *federation.Server
}

// NewFederation constructs the ASSP server: an ephemeral self-signed TLS cert
// (trust is the peering secret + channel binding, not PKI), a per-peer secret
// resolver backed by the DB, and a LocalSource over the presence hub + DB.
func NewFederation(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, hub *presence.Hub) (*FederationServer, error) {
	node := cfg.Server.Hostname
	tlsCfg, err := assp.SelfSignedConfig(node)
	if err != nil {
		return nil, fmt.Errorf("federation tls: %w", err)
	}

	secretFor := func(peerNode string) (string, bool) {
		secret, ok, err := db.GetPeerSecret(ctx, pool, peerNode)
		if err != nil {
			return "", false
		}
		return secret, ok
	}

	src := &fedSource{ctx: ctx, pool: pool, hub: hub}
	srv := federation.NewServer(node, src, secretFor, tlsCfg)
	return &FederationServer{
		addr: fmt.Sprintf(":%d", cfg.Federation.ASSPPort),
		srv:  srv,
	}, nil
}

func (f *FederationServer) ListenAndServe() error {
	ln, err := net.Listen("tcp", f.addr)
	if err != nil {
		return err
	}
	fmt.Printf("ASSP federation server listening on %s\n", f.addr)
	return f.srv.Serve(ln)
}

// fedSource adapts the presence hub and DB to federation.LocalSource.
type fedSource struct {
	ctx  context.Context
	pool *pgxpool.Pool
	hub  *presence.Hub
}

func (s *fedSource) Who() []federation.PresenceEntry {
	entries := s.hub.List()
	out := make([]federation.PresenceEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, federation.PresenceEntry{
			Username: e.Username,
			TTY:      e.TTY,
			LoginAt:  e.LoginAt.Unix(),
			From:     e.FromAddr,
			State:    e.State,
		})
	}
	return out
}

func (s *fedSource) Finger(username string) (federation.FingerResponse, bool) {
	u, err := db.GetUserByUsername(s.ctx, s.pool, username)
	if err != nil {
		return federation.FingerResponse{Found: false}, false
	}
	resp := federation.FingerResponse{
		Found:   true,
		Login:   u.Username,
		Name:    u.DisplayName,
		Office:  u.Office,
		Plan:    u.Plan,
		Project: u.Project,
		Online:  len(s.hub.FindByUsername(username)) > 0,
	}
	if u.LastLogin != nil {
		resp.LastLogin = u.LastLogin.Unix()
	}
	return resp, true
}
