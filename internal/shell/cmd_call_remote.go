package shell

import (
	"context"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/calls"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/federation"
)

// callRemote handles `call user@host` — a cross-node call over a dedicated
// ASSP connection (CALL_OPEN, DESIGN.md §9.6). Two modes, mirroring talk:
//
//   - Answering: a peer node has already rung us (there is a pending offer
//     from user@host); typing the same command accepts it. The federation
//     handler holding the open connection sees the accept and bridges.
//   - Initiating: dial the peer, send CALL_OPEN, and wait — the response is
//     deferred until the remote human answers. On acceptance, bridge a local
//     call room to the connection and run the same terminal UI as a local
//     call. Ctrl+C during the ring closes the connection, which the peer
//     reads as a cancel.
func callRemote(s *Session, target, media string) error {
	at := strings.LastIndex(target, "@")
	remoteUser, host := target[:at], target[at+1:]
	if remoteUser == "" || host == "" {
		usageError(s, "call", "[-a] <user[@host]>")
		return nil
	}

	// Answering an inbound cross-node call.
	if c := s.hub.Calls.PendingFor(s.User.Username, target); c != nil {
		if !c.Accept() {
			s.Println("call: too late — that call already ended")
			return nil
		}
		runCall(s, c, calls.SourceCallee, target)
		return nil
	}

	// Initiating an outbound call to a peer node.
	peer, err := db.GetPeer(s.ctx, s.db, host)
	if err != nil {
		s.Printf("call: %s: not a federation peer\r\n", host)
		return nil
	}
	fp := federation.Peer{
		Node:    peer.Node,
		Address: federation.ResolveAddress(peer.Address, peer.Node, s.cfg.Federation.ASSPPort),
		Secret:  peer.Secret,
	}

	// The local call object carries ring state, the room, and busy/rate
	// gating; its ring timer mirrors the remote node's.
	c, err := s.hub.Calls.Offer(s.User.Username, target, media, callParams(s.cfg))
	if err != nil {
		s.Println("call: " + err.Error())
		return nil
	}

	dialCtx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	ac, err := federation.InitiateCall(dialCtx, s.cfg.Server.Hostname, fp, federation.CallOpenRequest{
		From:   s.User.Username,
		Target: remoteUser,
		Media:  media,
		Params: c.Params,
	})
	cancel()
	if err != nil {
		c.End("peer unreachable")
		s.Printf("call: %s: unreachable\r\n", host)
		return nil
	}

	// However the call ends — Ctrl+C here, remote decline, timeout — closing
	// the connection unblocks every phase on both nodes.
	go func() {
		<-c.Ended()
		ac.Close()
	}()

	// Answer waiter: consume the deferred response, then either bridge and
	// activate, or end with the peer's reason.
	frames := federation.ReadFrames(ac)
	go func() {
		resp, err := federation.AwaitCallAnswer(frames)
		if err != nil {
			c.End("connection to " + host + " lost")
			return
		}
		if !resp.Accepted {
			reason := resp.Reason
			if reason == "" {
				reason = "declined"
			}
			c.End(reason)
			return
		}
		// Adopt the negotiated parameters, stand in for the remote user in
		// the call room, and bridge — all before Accept, so the browser's
		// media socket finds the room populated.
		c.Params = resp.Params
		pseudo, _, ok := s.hub.Rooms.JoinID(
			c.RoomID(),
			[]string{c.Caller, c.Callee},
			"relay:"+s.User.Username+"->"+target,
			target,
		)
		if !ok {
			c.End("relay setup failed")
			return
		}
		go federation.RelayCallRoomToStream(c, pseudo, ac, frames, calls.SourceCallee)
		if !c.Accept() {
			pseudo.Leave()
		}
	}()

	s.Printf("Ringing %s... (Ctrl+C to cancel)\r\n", target)
	runCall(s, c, calls.SourceCaller, target)
	return nil
}
