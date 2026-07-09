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
	defer conn.Close()

	tc, ok := conn.(*tls.Conn)
	if !ok {
		return
	}
	// Bound the handshake; clear the deadline once authenticated.
	tc.SetDeadline(time.Now().Add(15 * time.Second))
	if err := tc.Handshake(); err != nil {
		return
	}
	binding := assp.ChannelBinding(tc)

	ac := assp.NewConn(tc)
	peer, err := assp.Handshake(ac, s.node, s.secret, false, binding)
	if err != nil {
		return
	}
	_ = peer // authenticated peer node; could be used for authz/logging

	tc.SetDeadline(time.Time{})
	s.serve(ac)
}

// serve reads control requests until the peer disconnects.
func (s *Server) serve(ac *assp.Conn) {
	for {
		f, err := ac.ReadFrame()
		if err != nil {
			return
		}
		if f.Channel != assp.ControlChannel || f.Type != assp.TypeRequest {
			continue // ignore stream/unknown frames for now
		}
		var req Request
		if json.Unmarshal(f.Payload, &req) != nil {
			continue
		}
		s.dispatch(ac, req)
	}
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
