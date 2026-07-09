package server

import (
	"context"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/assp"
	"github.com/ilioscio/alternate.sh/internal/calls"
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
	sync *FedSync
}

// Sync exposes the outbound mail/news workers (started by main, and handed
// to the shell as its FederationNotifier).
func (f *FederationServer) Sync() *FedSync { return f.sync }

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
	srv.OnTalkOpen = func(peerNode string, req federation.TalkOpenRequest, ac *assp.Conn) {
		handleIncomingTalk(hub, peerNode, req, ac)
	}
	srv.OnCallOpen = func(peerNode string, req federation.CallOpenRequest, ac *assp.Conn) {
		handleIncomingCall(cfg, hub, peerNode, req, ac)
	}

	// Mail & news sync (§8.4): inbound handlers here, outbound workers in
	// FedSync.Run (started by main).
	fs := NewFedSync(ctx, cfg, pool, hub)
	srv.OnMailDeliver = fs.handleMailDeliver
	srv.OnNewsArticle = fs.handleNewsArticle
	srv.OnNewsCancel = fs.handleNewsCancel
	srv.OnNewsSince = fs.handleNewsSince

	return &FederationServer{
		addr: fmt.Sprintf(":%d", cfg.Federation.ASSPPort),
		srv:  srv,
		sync: fs,
	}, nil
}

// handleIncomingTalk sets up the receiving side of a cross-node talk: it
// verifies the target is reachable, creates a local relay room with a
// stand-in member for the remote user, notifies the target, accepts, and
// bridges the connection to the room until the talk ends.
func handleIncomingTalk(hub *presence.Hub, peerNode string, req federation.TalkOpenRequest, ac *assp.Conn) {
	target := req.Target
	fromQualified := req.From + "@" + peerNode

	// Target must be online with messages enabled.
	available := false
	for _, e := range hub.FindByUsername(target) {
		if e.MesgOn {
			available = true
			break
		}
	}
	if !available {
		federation.WriteResponse(ac, federation.TalkOpenResponse{Accepted: false, Reason: "user not available"})
		ac.Close()
		return
	}

	participants := []string{target, fromQualified}
	relaySession := "relay:" + fromQualified + "->" + target
	pseudo, _, ok := hub.Rooms.Join(participants, relaySession, fromQualified)
	if !ok {
		federation.WriteResponse(ac, federation.TalkOpenResponse{Accepted: false, Reason: "could not set up room"})
		ac.Close()
		return
	}

	hub.AddIncomingTalk(target, fromQualified)
	hub.Send(target, presence.WriteNotice{
		Kind:    presence.NoticeTalk,
		From:    fromQualified,
		Message: "talk request from " + fromQualified + " — respond with: talk " + fromQualified,
	})
	federation.WriteResponse(ac, federation.TalkOpenResponse{Accepted: true})

	// Bridge until the talk ends, then clear the pending invitation.
	federation.RelayRoomToStream(pseudo, ac)
	hub.RemoveIncomingTalk(target, fromQualified)
}

// handleIncomingCall runs the receiving side of a cross-node call: it rings
// the target, holds the connection open until a human answers (the deferred
// CALL_OPEN response), then bridges the call room to the connection's media
// channels. The caller's node cancels a ring by closing the connection.
func handleIncomingCall(cfg *config.Config, hub *presence.Hub, peerNode string, req federation.CallOpenRequest, ac *assp.Conn) {
	reject := func(reason string) {
		federation.WriteResponse(ac, federation.CallOpenResponse{Accepted: false, Reason: reason})
		ac.Close()
	}
	if !cfg.Calls.Enabled {
		reject("calls are disabled on this node")
		return
	}
	fromQualified := req.From + "@" + peerNode
	target := req.Target

	available := false
	for _, e := range hub.FindByUsername(target) {
		if e.MesgOn {
			available = true
			break
		}
	}
	if !available {
		reject("user not available")
		return
	}

	// Clamp the caller's proposal to this node's ceiling; the response
	// carries the final values back.
	params := req.Params.Clamp(cfg.Calls.Width, cfg.Calls.Height, cfg.Calls.FPS)
	c, err := hub.Calls.Offer(fromQualified, target, req.Media, params)
	if err != nil {
		reject(err.Error())
		return
	}

	kind := "video call"
	if req.Media == calls.MediaAudio {
		kind = "voice call"
	}
	notified := hub.Send(target, presence.WriteNotice{
		Kind: presence.NoticeCall,
		From: fromQualified,
		Message: fmt.Sprintf("Incoming %s from %s. Type 'call %s' to answer (web client).",
			kind, fromQualified, fromQualified),
	})
	if notified == 0 {
		c.End("unreachable")
		reject("user not available")
		return
	}

	// Single reader for the connection's whole life: nothing legitimate
	// arrives before our response, so any frame — or the channel closing —
	// during the ring means the caller hung up.
	frames := federation.ReadFrames(ac)

	select {
	case <-c.Accepted():
		// The callee answered (their `call user@host` matched the pending
		// offer). Stand in for the remote caller in the call room and bridge.
		pseudo, _, ok := hub.Rooms.JoinID(
			c.RoomID(),
			[]string{c.Caller, c.Callee},
			"relay:"+fromQualified+"->"+target,
			fromQualified,
		)
		if !ok {
			c.End("relay setup failed")
			reject("could not set up relay")
			return
		}
		if err := federation.WriteResponse(ac, federation.CallOpenResponse{Accepted: true, Params: params}); err != nil {
			c.End("connection to peer lost")
			pseudo.Leave()
			ac.Close()
			return
		}
		federation.RelayCallRoomToStream(c, pseudo, ac, frames, calls.SourceCaller)

	case <-c.Ended():
		// Ring timeout, or the target went unavailable.
		reject(c.EndReason())

	case <-frames:
		// A frame before our response is a protocol violation, and the
		// channel closing means the connection died: either way, the
		// caller's node is gone.
		c.End("canceled by caller")
		ac.Close()
	}
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
