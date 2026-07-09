package federation

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ilioscio/alternate.sh/internal/assp"
)

type fakeSource struct{}

func (fakeSource) Who() []PresenceEntry {
	return []PresenceEntry{
		{Username: "alice", TTY: "pts/0", LoginAt: 1000, From: "web", State: "shell"},
		{Username: "bob", TTY: "pts/1", LoginAt: 1050, From: "1.2.3.4", State: "reading news"},
	}
}

func (fakeSource) Finger(u string) (FingerResponse, bool) {
	if u == "alice" {
		return FingerResponse{
			Found: true, Login: "alice", Name: "Alice Example",
			Plan: "federating.", Online: true, LastLogin: 900,
		}, true
	}
	return FingerResponse{Found: false}, false
}

const testSecret = "shared-peering-secret-xyz"

// startTestServer runs a federation Server on a random port and returns its
// address plus a cleanup func.
func startTestServer(t *testing.T, node string) string {
	t.Helper()
	tlsCfg, err := assp.SelfSignedConfig(node)
	if err != nil {
		t.Fatal(err)
	}
	secretFor := func(peer string) (string, bool) {
		if peer == "client.test" {
			return testSecret, true
		}
		return "", false
	}
	srv := NewServer(node, fakeSource{}, secretFor, tlsCfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func TestFederationWho(t *testing.T) {
	addr := startTestServer(t, "server.test")
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := QueryWho(ctx, "client.test", peer)
	if err != nil {
		t.Fatalf("QueryWho: %v", err)
	}
	if resp.Node != "server.test" {
		t.Errorf("node = %q, want server.test", resp.Node)
	}
	if len(resp.Users) != 2 || resp.Users[0].Username != "alice" || resp.Users[1].Username != "bob" {
		t.Errorf("unexpected users: %+v", resp.Users)
	}
}

func TestFederationFinger(t *testing.T) {
	addr := startTestServer(t, "server.test")
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := QueryFinger(ctx, "client.test", peer, "alice")
	if err != nil {
		t.Fatalf("QueryFinger: %v", err)
	}
	if !resp.Found || resp.Name != "Alice Example" || !resp.Online {
		t.Errorf("unexpected finger response: %+v", resp)
	}

	resp, err = QueryFinger(ctx, "client.test", peer, "nobody")
	if err != nil {
		t.Fatalf("QueryFinger(nobody): %v", err)
	}
	if resp.Found {
		t.Errorf("expected not found for unknown user, got %+v", resp)
	}
}

func TestFederationWrongSecret(t *testing.T) {
	addr := startTestServer(t, "server.test")
	peer := Peer{Node: "server.test", Address: addr, Secret: "wrong-secret"}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := QueryWho(ctx, "client.test", peer); err == nil {
		t.Error("expected auth failure with wrong secret")
	}
}

func TestFederationUnknownClientRejected(t *testing.T) {
	addr := startTestServer(t, "server.test")
	// The server only knows "client.test"; dial as a different node.
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := QueryWho(ctx, "stranger.test", peer); err == nil {
		t.Error("expected rejection of unknown client node")
	}
}

func TestResolveAddress(t *testing.T) {
	if got := ResolveAddress("", "node.example", 4119); got != "node.example:4119" {
		t.Errorf("default address = %q", got)
	}
	if got := ResolveAddress("1.2.3.4:5000", "node.example", 4119); got != "1.2.3.4:5000" {
		t.Errorf("explicit address = %q", got)
	}
}
