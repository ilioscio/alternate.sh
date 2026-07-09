package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ilioscio/alternate.sh/internal/calls"
	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// newCallTestServer builds a WebSocketServer with token auth stubbed
// (token "t-<user>" authenticates as <user>) and no database.
func newCallTestServer(t *testing.T) (*WebSocketServer, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Calls = config.CallsConfig{Enabled: true, Width: 128, Height: 96, FPS: 24}
	s := NewWebSocket(cfg, nil, presence.NewHub())
	s.authFn = func(_ context.Context, token string) (*db.User, error) {
		if u, ok := strings.CutPrefix(token, "t-"); ok {
			return &db.User{Username: u}, nil
		}
		return nil, errors.New("bad token")
	}
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)
	return s, ts
}

func dialCall(t *testing.T, ts *httptest.Server, user, callID string) (*websocket.Conn, error) {
	t.Helper()
	url := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/call?token=t-" + user + "&call=" + callID
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	return conn, err
}

// mediaPacket builds a minimal well-formed packet: header + arbitrary payload.
func mediaPacket(kind, source byte, seq uint16, payload ...byte) []byte {
	return append([]byte{kind, source, byte(seq >> 8), byte(seq)}, payload...)
}

func readOne(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	return msg
}

func TestCallWSRelay(t *testing.T) {
	s, ts := newCallTestServer(t)
	c, err := s.hub.Calls.Offer("ilios", "nova", calls.MediaAV, calls.Params{Width: 128, Height: 96, FPS: 24})
	if err != nil {
		t.Fatal(err)
	}
	c.Accept()

	caller, err := dialCall(t, ts, "ilios", c.ID)
	if err != nil {
		t.Fatalf("caller dial: %v", err)
	}
	defer caller.Close()
	callee, err := dialCall(t, ts, "nova", c.ID)
	if err != nil {
		t.Fatalf("callee dial: %v", err)
	}
	defer callee.Close()

	// Caller (source 0) → callee.
	pkt := mediaPacket(0x01, 0, 0, 0, 128, 0, 96, 0xAB)
	if err := caller.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		t.Fatal(err)
	}
	if got := readOne(t, callee); string(got) != string(pkt) {
		t.Fatalf("callee received %x, want %x", got, pkt)
	}

	// Callee (source 1) → caller, audio.
	apkt := mediaPacket(0x03, 1, 7, 0, 0, 0, 0, 0x11, 0x22)
	if err := callee.WriteMessage(websocket.BinaryMessage, apkt); err != nil {
		t.Fatal(err)
	}
	if got := readOne(t, caller); string(got) != string(apkt) {
		t.Fatalf("caller received %x, want %x", got, apkt)
	}
}

func TestCallWSSourceSpoofDropped(t *testing.T) {
	s, ts := newCallTestServer(t)
	c, _ := s.hub.Calls.Offer("ilios", "nova", calls.MediaAV, calls.Params{})
	c.Accept()

	caller, err := dialCall(t, ts, "ilios", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	callee, err := dialCall(t, ts, "nova", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()

	// Caller stamps its packet with the callee's source id: dropped.
	caller.WriteMessage(websocket.BinaryMessage, mediaPacket(0x01, 1, 0, 0xEE))
	// Garbage under the 4-byte header: dropped.
	caller.WriteMessage(websocket.BinaryMessage, []byte{0x01})
	// Unknown kind: dropped.
	caller.WriteMessage(websocket.BinaryMessage, mediaPacket(0x7F, 0, 0, 0xEE))
	// Then a legitimate packet.
	good := mediaPacket(0x02, 0, 1, 0xCD)
	caller.WriteMessage(websocket.BinaryMessage, good)

	if got := readOne(t, callee); string(got) != string(good) {
		t.Fatalf("callee received %x, want only the legitimate %x", got, good)
	}
}

func TestCallWSAuthAndAccess(t *testing.T) {
	s, ts := newCallTestServer(t)
	c, _ := s.hub.Calls.Offer("ilios", "nova", calls.MediaAV, calls.Params{})

	// Ringing (not yet active): media sockets refused.
	if _, err := dialCall(t, ts, "ilios", c.ID); err == nil {
		t.Fatal("dial succeeded on a ringing call")
	}
	c.Accept()

	if _, err := dialCall(t, ts, "ilios", "no-such-call"); err == nil {
		t.Fatal("dial succeeded with a bogus call ID")
	}
	if _, err := dialCall(t, ts, "mallory", c.ID); err == nil {
		t.Fatal("dial succeeded for a non-participant")
	}
	url := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/call?token=bogus&call=" + c.ID
	if _, _, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		t.Fatal("dial succeeded with a bad token")
	}
}

func TestCallWSEndClosesSockets(t *testing.T) {
	s, ts := newCallTestServer(t)
	c, _ := s.hub.Calls.Offer("ilios", "nova", calls.MediaAV, calls.Params{})
	c.Accept()

	caller, err := dialCall(t, ts, "ilios", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()

	c.End("hung up")
	caller.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := caller.ReadMessage(); err == nil {
		t.Fatal("socket still open after call ended")
	}
}

func TestCallWSDropEndsCall(t *testing.T) {
	s, ts := newCallTestServer(t)
	c, _ := s.hub.Calls.Offer("ilios", "nova", calls.MediaAV, calls.Params{})
	c.Accept()

	caller, err := dialCall(t, ts, "ilios", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	caller.Close() // browser tab dies

	select {
	case <-c.Ended():
		if c.EndReason() != "media link lost" {
			t.Fatalf("EndReason = %q", c.EndReason())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("call did not end after its media socket dropped")
	}
}

func TestCallWSSecondSocketRejected(t *testing.T) {
	s, ts := newCallTestServer(t)
	c, _ := s.hub.Calls.Offer("ilios", "nova", calls.MediaAV, calls.Params{})
	c.Accept()

	first, err := dialCall(t, ts, "ilios", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	// A second tab: the upgrade succeeds but the server hangs up immediately.
	second, err := dialCall(t, ts, "ilios", c.ID)
	if err == nil {
		second.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, err := second.ReadMessage(); err == nil {
			t.Fatal("second media socket for the same user stayed open")
		}
		second.Close()
	}

	// The first socket still relays.
	callee, err := dialCall(t, ts, "nova", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	pkt := mediaPacket(0x01, 0, 0, 0x42)
	first.WriteMessage(websocket.BinaryMessage, pkt)
	if got := readOne(t, callee); string(got) != string(pkt) {
		t.Fatalf("relay broken after duplicate-socket rejection")
	}
}
