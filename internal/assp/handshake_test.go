package assp

import (
	"net"
	"testing"
	"time"
)

// handshakePair runs a dialer and listener handshake over an in-memory pipe and
// returns their results.
func handshakePair(t *testing.T, dialerNode, listenerNode string, dialerSecret, listenerSecret SecretFunc) (dPeer, lPeer string, dErr, lErr error) {
	t.Helper()
	c1, c2 := net.Pipe()
	dConn, lConn := NewConn(c1), NewConn(c2)

	type res struct {
		peer string
		err  error
	}
	dCh := make(chan res, 1)
	lCh := make(chan res, 1)

	go func() {
		p, e := Handshake(dConn, dialerNode, dialerSecret, true, nil)
		dCh <- res{p, e}
	}()
	go func() {
		p, e := Handshake(lConn, listenerNode, listenerSecret, false, nil)
		lCh <- res{p, e}
	}()

	select {
	case r := <-dCh:
		dPeer, dErr = r.peer, r.err
	case <-time.After(3 * time.Second):
		t.Fatal("dialer handshake timed out")
	}
	select {
	case r := <-lCh:
		lPeer, lErr = r.peer, r.err
	case <-time.After(3 * time.Second):
		t.Fatal("listener handshake timed out")
	}
	return
}

func fixedSecret(s string) SecretFunc {
	return func(string) (string, bool) { return s, true }
}

func TestHandshakeSuccess(t *testing.T) {
	dPeer, lPeer, dErr, lErr := handshakePair(t, "a.example", "b.example",
		fixedSecret("shared-peer-secret"), fixedSecret("shared-peer-secret"))
	if dErr != nil || lErr != nil {
		t.Fatalf("handshake errors: dialer=%v listener=%v", dErr, lErr)
	}
	if dPeer != "b.example" {
		t.Errorf("dialer learned peer %q, want b.example", dPeer)
	}
	if lPeer != "a.example" {
		t.Errorf("listener learned peer %q, want a.example", lPeer)
	}
}

func TestHandshakeSecretMismatch(t *testing.T) {
	_, _, dErr, lErr := handshakePair(t, "a.example", "b.example",
		fixedSecret("secret-A"), fixedSecret("secret-B"))
	if dErr == nil && lErr == nil {
		t.Fatal("expected authentication failure with mismatched secrets")
	}
}

func TestHandshakeUnknownPeer(t *testing.T) {
	// Listener doesn't recognize the dialer's node.
	deny := func(string) (string, bool) { return "", false }
	_, _, dErr, lErr := handshakePair(t, "stranger.example", "b.example",
		fixedSecret("s"), deny)
	if lErr == nil {
		t.Error("listener should reject unknown peer")
	}
	if dErr == nil {
		t.Error("dialer should fail when listener rejects it")
	}
}

func TestHandshakeChannelBindingMismatch(t *testing.T) {
	// Same secret, but the two sides mix in different channel-binding material
	// — as a MITM terminating separate TLS sessions would. Auth must fail even
	// though the shared secret is correct.
	c1, c2 := net.Pipe()
	dConn, lConn := NewConn(c1), NewConn(c2)

	dErr := make(chan error, 1)
	lErr := make(chan error, 1)
	go func() {
		_, e := Handshake(dConn, "a.example", fixedSecret("same-secret"), true, []byte("binding-LEG-1"))
		dErr <- e
	}()
	go func() {
		_, e := Handshake(lConn, "b.example", fixedSecret("same-secret"), false, []byte("binding-LEG-2"))
		lErr <- e
	}()

	if err := <-lErr; err == nil {
		t.Error("listener should reject mismatched channel binding")
	}
	if err := <-dErr; err == nil {
		t.Error("dialer should fail when binding differs")
	}
}

func TestHandshakeMatchingBinding(t *testing.T) {
	// Identical binding on both sides (as a real shared TLS session yields) →
	// success.
	dPeer, lPeer, dErr, lErr := handshakePairBinding(t, "a.example", "b.example",
		fixedSecret("s"), fixedSecret("s"), []byte("same-binding"))
	if dErr != nil || lErr != nil {
		t.Fatalf("errors: d=%v l=%v", dErr, lErr)
	}
	if dPeer != "b.example" || lPeer != "a.example" {
		t.Errorf("peers: d=%q l=%q", dPeer, lPeer)
	}
}

func handshakePairBinding(t *testing.T, dNode, lNode string, dSec, lSec SecretFunc, binding []byte) (dPeer, lPeer string, dErr, lErr error) {
	t.Helper()
	c1, c2 := net.Pipe()
	dConn, lConn := NewConn(c1), NewConn(c2)
	type res struct {
		peer string
		err  error
	}
	dCh, lCh := make(chan res, 1), make(chan res, 1)
	go func() { p, e := Handshake(dConn, dNode, dSec, true, binding); dCh <- res{p, e} }()
	go func() { p, e := Handshake(lConn, lNode, lSec, false, binding); lCh <- res{p, e} }()
	r := <-dCh
	dPeer, dErr = r.peer, r.err
	r = <-lCh
	lPeer, lErr = r.peer, r.err
	return
}

func TestHandshakeVersionMismatch(t *testing.T) {
	// Simulate a peer sending a wrong version by writing a raw HELLO.
	c1, c2 := net.Pipe()
	dConn, lConn := NewConn(c1), NewConn(c2)

	go func() {
		// Bogus dialer: sends a HELLO with a future version.
		writeControl(dConn, TypeHello, helloMsg{Version: 999, Node: "x.example", Nonce: make([]byte, nonceLen)})
		// Drain whatever comes back so the listener isn't blocked on write.
		dConn.ReadFrame()
	}()

	_, err := Handshake(lConn, "b.example", fixedSecret("s"), false, nil)
	if err == nil {
		t.Error("listener should reject version mismatch")
	}
}
