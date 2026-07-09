package presence

import (
	"sort"
	"strings"
	"sync"
)

// Rooms carry the real-time byte streams for talk/ytalk sessions. A room is
// keyed by its full participant set, so every participant independently
// arrives at the same room ID regardless of who initiated. Content is
// ephemeral — nothing is logged or stored.

type RoomEventKind int

const (
	EventData RoomEventKind = iota
	EventJoin
	EventLeave
)

type RoomEvent struct {
	Kind      RoomEventKind
	SessionID string // originating member
	From      string // originating username
	Data      []byte // EventData payload
}

type RoomMember struct {
	SessionID string
	Username  string
	Recv      chan RoomEvent
	room      *Room
	closed    bool
}

type Room struct {
	ID      string
	mu      sync.Mutex
	members []*RoomMember // in join order
	invited map[string]bool
	broker  *RoomBroker
}

type RoomBroker struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

func NewRoomBroker() *RoomBroker {
	return &RoomBroker{rooms: make(map[string]*Room)}
}

// RoomID computes the canonical room ID for a participant set.
func RoomID(usernames []string) string {
	seen := map[string]bool{}
	var uniq []string
	for _, u := range usernames {
		if !seen[u] {
			seen[u] = true
			uniq = append(uniq, u)
		}
	}
	sort.Strings(uniq)
	return "talk:" + strings.Join(uniq, "+")
}

// Join adds a session to the talk room for the given participant set,
// creating the room if needed. Only listed participants may join. Returns
// the member handle and the usernames of peers already present.
func (b *RoomBroker) Join(participants []string, sessionID, username string) (*RoomMember, []string, bool) {
	return b.JoinID(RoomID(participants), participants, sessionID, username)
}

// JoinID is Join with an explicit room ID — used by calls, whose rooms are
// keyed by call ID ("call:<id>") rather than by participant set, since a
// call is a negotiated object with its own lifecycle rather than a
// rendezvous. The invited list still gates membership.
//
// Locking: membership changes happen under broker.mu → room.mu (in that
// order, always), so a join can never race a concurrent teardown into an
// orphaned room object.
func (b *RoomBroker) JoinID(id string, participants []string, sessionID, username string) (*RoomMember, []string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	room, ok := b.rooms[id]
	if !ok {
		room = &Room{
			ID:      id,
			invited: make(map[string]bool),
			broker:  b,
		}
		for _, u := range participants {
			room.invited[u] = true
		}
		b.rooms[id] = room
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	if !room.invited[username] {
		// Don't leave behind an empty room created by a rejected join.
		if len(room.members) == 0 {
			delete(b.rooms, id)
		}
		return nil, nil, false
	}

	m := &RoomMember{
		SessionID: sessionID,
		Username:  username,
		Recv:      make(chan RoomEvent, 256),
		room:      room,
	}

	var peers []string
	for _, other := range room.members {
		peers = append(peers, other.Username)
		select {
		case other.Recv <- RoomEvent{Kind: EventJoin, SessionID: sessionID, From: username}:
		default:
		}
	}
	room.members = append(room.members, m)
	return m, peers, true
}

// Send broadcasts data to every other member of the room.
func (m *RoomMember) Send(data []byte) {
	// Copy: the caller reuses its read buffer.
	cp := make([]byte, len(data))
	copy(cp, data)

	m.room.mu.Lock()
	defer m.room.mu.Unlock()
	for _, other := range m.room.members {
		if other.SessionID == m.SessionID {
			continue
		}
		select {
		case other.Recv <- RoomEvent{Kind: EventData, SessionID: m.SessionID, From: m.Username, Data: cp}:
		default:
			// Slow consumer: drop. Talk is ephemeral by design.
		}
	}
}

// Leave removes the member, notifies peers, closes the member's Recv
// channel, and tears the room down when the last member departs.
func (m *RoomMember) Leave() {
	room := m.room

	room.broker.mu.Lock()
	defer room.broker.mu.Unlock()
	room.mu.Lock()
	defer room.mu.Unlock()

	if m.closed {
		return
	}
	m.closed = true

	// Remove by identity, not SessionID: two members may share a session ID
	// (e.g. a rejected duplicate join), and removing the wrong entry would
	// leave a closed Recv in the list for Send to panic on.
	for i, other := range room.members {
		if other == m {
			room.members = append(room.members[:i], room.members[i+1:]...)
			break
		}
	}
	for _, other := range room.members {
		select {
		case other.Recv <- RoomEvent{Kind: EventLeave, SessionID: m.SessionID, From: m.Username}:
		default:
		}
	}
	close(m.Recv)

	if len(room.members) == 0 {
		delete(room.broker.rooms, room.ID)
	}
}

// Peers returns the usernames of the other current members, in join order.
func (m *RoomMember) Peers() []string {
	m.room.mu.Lock()
	defer m.room.mu.Unlock()
	var out []string
	for _, other := range m.room.members {
		if other.SessionID != m.SessionID {
			out = append(out, other.Username)
		}
	}
	return out
}
