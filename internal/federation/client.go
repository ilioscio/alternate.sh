package federation

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"

	"github.com/ilioscio/alternate.sh/internal/assp"
)

// Peer is the minimal dialing info the client needs (mirrors db.Peer without a
// db import, keeping this package dependency-light).
type Peer struct {
	Node    string
	Address string // host:port; caller resolves the default before calling
	Secret  string
}

// dial establishes an authenticated ASSP connection to peer as the dialer.
func dial(ctx context.Context, localNode string, peer Peer) (*assp.Conn, error) {
	d := tls.Dialer{Config: assp.ClientTLSConfig()}
	conn, err := d.DialContext(ctx, "tcp", peer.Address)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", peer.Address, err)
	}
	tc, ok := conn.(*tls.Conn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("federation: expected TLS connection")
	}
	binding := assp.ChannelBinding(tc)

	ac := assp.NewConn(tc)
	secretFor := func(n string) (string, bool) {
		if n == peer.Node {
			return peer.Secret, true
		}
		return "", false // we dialed a specific peer; reject any other identity
	}
	if _, err := assp.Handshake(ac, localNode, secretFor, true, binding); err != nil {
		ac.Close()
		return nil, fmt.Errorf("handshake with %s: %w", peer.Node, err)
	}
	return ac, nil
}

// QueryWho dials the peer and returns its logged-in users.
func QueryWho(ctx context.Context, localNode string, peer Peer) (WhoResponse, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return WhoResponse{}, err
	}
	defer ac.Close()

	var resp WhoResponse
	err = request(ac, Request{Verb: VerbWho}, &resp)
	return resp, err
}

// QueryFinger dials the peer and returns finger info for a user on that node.
func QueryFinger(ctx context.Context, localNode string, peer Peer, user string) (FingerResponse, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return FingerResponse{}, err
	}
	defer ac.Close()

	var resp FingerResponse
	err = request(ac, Request{Verb: VerbFinger, Arg: user}, &resp)
	return resp, err
}

// InitiateTalk opens a cross-node talk: it dials the peer, sends TALK_OPEN, and
// on acceptance returns the live connection (dedicated to this talk's stream)
// for the caller to bridge. On rejection it returns the reason and a nil conn.
func InitiateTalk(ctx context.Context, localNode string, peer Peer, from, target string) (*assp.Conn, string, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return nil, "", err
	}
	var resp TalkOpenResponse
	if err := request(ac, Request{Verb: VerbTalkOpen, Arg: from, Target: target}, &resp); err != nil {
		ac.Close()
		return nil, "", err
	}
	if !resp.Accepted {
		ac.Close()
		reason := resp.Reason
		if reason == "" {
			reason = "declined"
		}
		return nil, reason, nil
	}
	return ac, "", nil
}

// request sends a control request and decodes the single response.
func request(ac *assp.Conn, req Request, out any) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := ac.Write(assp.ControlChannel, assp.TypeRequest, 0, b); err != nil {
		return err
	}
	f, err := ac.ReadFrame()
	if err != nil {
		return err
	}
	if f.Channel != assp.ControlChannel {
		return fmt.Errorf("federation: unexpected channel %d", f.Channel)
	}
	if f.Type == assp.TypeError {
		return fmt.Errorf("federation: peer error: %s", f.Payload)
	}
	if f.Type != assp.TypeResponse {
		return fmt.Errorf("federation: unexpected frame type 0x%02x", f.Type)
	}
	return json.Unmarshal(f.Payload, out)
}

// ResolveAddress returns the host:port to dial for a peer, applying the default
// port when the stored address is empty.
func ResolveAddress(address, node string, defaultPort int) string {
	if address != "" {
		return address
	}
	return net.JoinHostPort(node, fmt.Sprintf("%d", defaultPort))
}
