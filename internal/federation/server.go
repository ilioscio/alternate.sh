package federation

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/ilioscio/alternate.sh/internal/assp"
)

// Server accepts ASSP connections from peers, authenticates them, and answers
// control queries (WHO, FINGER) from the LocalSource.
type Server struct {
	node   string
	src    LocalSource
	secret assp.SecretFunc
	tlsCfg *tls.Config

	// OnTalkOpen, if set, handles an inbound cross-node talk. It receives the
	// authenticated peer node, the request, and the (now dedicated) connection;
	// it is responsible for responding and for bridging or closing the conn.
	// The serve loop hands the connection off and stops reading it.
	OnTalkOpen func(peerNode string, req TalkOpenRequest, ac *assp.Conn)
}

// NewServer builds a federation server. node is this node's ASSP identity,
// secret resolves per-peer shared secrets, tlsCfg terminates TLS.
func NewServer(node string, src LocalSource, secret assp.SecretFunc, tlsCfg *tls.Config) *Server {
	return &Server{node: node, src: src, secret: secret, tlsCfg: tlsCfg}
}

// Serve accepts connections on ln (a plain TCP listener; TLS is applied here)
// until ln is closed.
func (s *Server) Serve(ln net.Listener) error {
	tln := tls.NewListener(ln, s.tlsCfg)
	for {
		conn, err := tln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	tc, ok := conn.(*tls.Conn)
	if !ok {
		conn.Close()
		return
	}
	// Bound the handshake; clear the deadline once authenticated.
	tc.SetDeadline(time.Now().Add(15 * time.Second))
	if err := tc.Handshake(); err != nil {
		conn.Close()
		return
	}
	binding := assp.ChannelBinding(tc)

	ac := assp.NewConn(tc)
	peer, err := assp.Handshake(ac, s.node, s.secret, false, binding)
	if err != nil {
		conn.Close()
		return
	}

	tc.SetDeadline(time.Time{})
	// serve returns true if it handed the connection off (talk relay owns it).
	if !s.serve(ac, peer) {
		conn.Close()
	}
}

// serve reads control requests until the peer disconnects. It returns true if
// the connection was handed off to a talk relay (which now owns its lifecycle).
func (s *Server) serve(ac *assp.Conn, peer string) bool {
	for {
		f, err := ac.ReadFrame()
		if err != nil {
			return false
		}
		if f.Channel != assp.ControlChannel || f.Type != assp.TypeRequest {
			continue // ignore stream/unknown frames for now
		}
		var req Request
		if json.Unmarshal(f.Payload, &req) != nil {
			continue
		}
		if req.Verb == VerbTalkOpen {
			s.handleTalkOpen(ac, peer, req)
			return true // talk relay (or its rejection) owns the connection now
		}
		s.dispatch(ac, req)
	}
}

// handleTalkOpen dispatches an inbound talk to OnTalkOpen, or rejects it if
// talk isn't wired up.
func (s *Server) handleTalkOpen(ac *assp.Conn, peer string, req Request) {
	if s.OnTalkOpen == nil {
		s.respond(ac, req.ID, TalkOpenResponse{Accepted: false, Reason: "talk not available"})
		ac.Close()
		return
	}
	s.OnTalkOpen(peer, TalkOpenRequest{From: req.Arg, Target: req.Target}, ac)
}

func (s *Server) dispatch(ac *assp.Conn, req Request) {
	switch req.Verb {
	case VerbWho:
		s.respond(ac, req.ID, WhoResponse{Node: s.node, Users: s.src.Who()})
	case VerbFinger:
		resp, _ := s.src.Finger(req.Arg)
		s.respond(ac, req.ID, resp)
	default:
		ac.Write(assp.ControlChannel, assp.TypeError, 0,
			[]byte(fmt.Sprintf("unknown verb %q", req.Verb)))
	}
}

func (s *Server) respond(ac *assp.Conn, id uint32, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	ac.Write(assp.ControlChannel, assp.TypeResponse, 0, b)
}
