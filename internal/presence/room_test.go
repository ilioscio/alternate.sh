package presence

import (
	"testing"
	"time"
)

func recvEvent(t *testing.T, m *RoomMember) RoomEvent {
	t.Helper()
	select {
	case ev, ok := <-m.Recv:
		if !ok {
			t.Fatal("Recv closed unexpectedly")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room event")
	}
	panic("unreachable")
}

func TestRoomID(t *testing.T) {
	a := RoomID([]string{"bob", "alice", "bob"})
	b := RoomID([]string{"alice", "bob"})
	if a != b {
		t.Errorf("RoomID not canonical: %q vs %q", a, b)
	}
	if a != "talk:alice+bob" {
		t.Errorf("RoomID = %q", a)
	}
}

func TestJoinAndDataRouting(t *testing.T) {
	b := NewRoomBroker()
	parts := []string{"alice", "bob"}

	ma, peers, ok := b.Join(parts, "s-alice", "alice")
	if !ok || len(peers) != 0 {
		t.Fatalf("first join: ok=%v peers=%v", ok, peers)
	}

	mb, peers, ok := b.Join(parts, "s-bob", "bob")
	if !ok || len(peers) != 1 || peers[0] != "alice" {
		t.Fatalf("second join: ok=%v peers=%v", ok, peers)
	}

	// Alice sees bob's join.
	ev := recvEvent(t, ma)
	if ev.Kind != EventJoin || ev.From != "bob" {
		t.Fatalf("expected join from bob, got %+v", ev)
	}

	// Data flows alice → bob but not back to alice.
	ma.Send([]byte("hello"))
	ev = recvEvent(t, mb)
	if ev.Kind != EventData || string(ev.Data) != "hello" || ev.From != "alice" {
		t.Fatalf("bob got %+v", ev)
	}
	select {
	case ev := <-ma.Recv:
		t.Fatalf("alice received her own data: %+v", ev)
	default:
	}
}

func TestSendCopiesBuffer(t *testing.T) {
	b := NewRoomBroker()
	parts := []string{"alice", "bob"}
	ma, _, _ := b.Join(parts, "s-alice", "alice")
	mb, _, _ := b.Join(parts, "s-bob", "bob")
	recvEvent(t, ma) // drain join

	buf := []byte("AAAA")
	ma.Send(buf)
	copy(buf, "BBBB") // caller reuses its read buffer
	ev := recvEvent(t, mb)
	if string(ev.Data) != "AAAA" {
		t.Errorf("Send aliased the caller's buffer: got %q", ev.Data)
	}
}

func TestLeaveNotifiesAndTearsDown(t *testing.T) {
	b := NewRoomBroker()
	parts := []string{"alice", "bob"}
	ma, _, _ := b.Join(parts, "s-alice", "alice")
	mb, _, _ := b.Join(parts, "s-bob", "bob")
	recvEvent(t, ma) // join event

	mb.Leave()
	ev := recvEvent(t, ma)
	if ev.Kind != EventLeave || ev.From != "bob" {
		t.Fatalf("expected leave from bob, got %+v", ev)
	}

	// bob's channel is closed.
	if _, ok := <-mb.Recv; ok {
		t.Error("bob's Recv still open after Leave")
	}

	// Double-leave is safe.
	mb.Leave()

	// Last member out deletes the room; a rejoin starts fresh.
	ma.Leave()
	b.mu.Lock()
	if len(b.rooms) != 0 {
		t.Errorf("room not deleted after last leave: %v", b.rooms)
	}
	b.mu.Unlock()

	m2, peers, ok := b.Join(parts, "s-alice-2", "alice")
	if !ok || len(peers) != 0 {
		t.Fatalf("rejoin after teardown: ok=%v peers=%v", ok, peers)
	}
	m2.Leave()
}

func TestUninvitedRejected(t *testing.T) {
	b := NewRoomBroker()
	ma, _, _ := b.Join([]string{"alice", "bob"}, "s-alice", "alice")
	defer ma.Leave()

	// mallory tries to join alice+bob's room by guessing the participant set —
	// Join recomputes the same ID but mallory is not in the invited set.
	room := b.rooms[RoomID([]string{"alice", "bob"})]
	room.mu.Lock()
	invited := room.invited["mallory"]
	room.mu.Unlock()
	if invited {
		t.Fatal("mallory should not be invited")
	}
	if m, _, ok := b.Join([]string{"alice", "bob"}, "s-mallory", "mallory"); ok {
		m.Leave()
		t.Error("uninvited user was allowed to join")
	}
}

func TestPeers(t *testing.T) {
	b := NewRoomBroker()
	parts := []string{"alice", "bob", "carol"}
	ma, _, _ := b.Join(parts, "s-alice", "alice")
	mb, _, _ := b.Join(parts, "s-bob", "bob")
	mc, _, _ := b.Join(parts, "s-carol", "carol")

	got := ma.Peers()
	if len(got) != 2 || got[0] != "bob" || got[1] != "carol" {
		t.Errorf("alice's peers = %v", got)
	}
	ma.Leave()
	mb.Leave()
	mc.Leave()
}
