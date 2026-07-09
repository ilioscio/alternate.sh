package federation

import (
	"net"
	"testing"
	"time"

	"github.com/ilioscio/alternate.sh/internal/assp"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// readData waits for the next EventData on m, skipping join/leave, and checks it.
func readData(t *testing.T, m *presence.RoomMember, want string) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-m.Recv:
			if !ok {
				t.Fatal("room channel closed while waiting for data")
			}
			if ev.Kind == presence.EventData {
				if string(ev.Data) != want {
					t.Fatalf("got %q, want %q", ev.Data, want)
				}
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for data %q", want)
		}
	}
}

// readLeave waits for an EventLeave on m, skipping other events.
func readLeave(t *testing.T, m *presence.RoomMember) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-m.Recv:
			if !ok {
				return // channel closed also signals teardown
			}
			if ev.Kind == presence.EventLeave {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for leave")
		}
	}
}

// TestCrossNodeTalkRelay wires two nodes' relays over a pipe and verifies that
// bytes typed by a user on one node reach the user on the other, in both
// directions, and that one side leaving tears the other down.
func TestCrossNodeTalkRelay(t *testing.T) {
	pa, pb := net.Pipe()
	acA := assp.NewConn(pa)
	acB := assp.NewConn(pb)

	// Node A room: [userA, userB@nodeB]. userA is real; pseudoA stands in for
	// the remote user and is bridged to the wire.
	brokerA := presence.NewRoomBroker()
	partsA := []string{"userA", "userB@nodeB"}
	userA, _, _ := brokerA.Join(partsA, "sessA", "userA")
	pseudoA, _, _ := brokerA.Join(partsA, "relayA", "userB@nodeB")

	// Node B room: [userB, userA@nodeA].
	brokerB := presence.NewRoomBroker()
	partsB := []string{"userB", "userA@nodeA"}
	userB, _, _ := brokerB.Join(partsB, "sessB", "userB")
	pseudoB, _, _ := brokerB.Join(partsB, "relayB", "userA@nodeA")

	go RelayRoomToStream(pseudoA, acA)
	go RelayRoomToStream(pseudoB, acB)

	// A → B
	userA.Send([]byte("hello from A"))
	readData(t, userB, "hello from A")

	// B → A
	userB.Send([]byte("hi back from B"))
	readData(t, userA, "hi back from B")

	// Several rapid messages preserve order and framing.
	userA.Send([]byte("one"))
	userA.Send([]byte("two"))
	readData(t, userB, "one")
	readData(t, userB, "two")

	// userA leaves → userB's side is torn down (sees a leave / channel closes).
	userA.Leave()
	readLeave(t, userB)
}
