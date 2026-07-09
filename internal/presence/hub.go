package presence

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ilioscio/alternate.sh/internal/calls"
)

// NoticeKind distinguishes the notification types delivered to terminals.
type NoticeKind string

const (
	NoticeWrite NoticeKind = "write" // write — respects mesg
	NoticeWall  NoticeKind = "wall"  // admin broadcast — always delivered
	NoticeBiff  NoticeKind = "biff"  // new mail alert — respects biff setting
	NoticeTalk  NoticeKind = "talk"  // talk/ytalk invitation — respects mesg
	NoticeCall  NoticeKind = "call"  // incoming A/V call — respects mesg
)

type WriteNotice struct {
	Kind    NoticeKind
	From    string
	Message string
}

type Entry struct {
	SessionID    string
	Username     string
	TTY          string
	FromAddr     string
	LoginAt      time.Time
	LastActivity time.Time
	State        string // what the user is currently doing, shown in 'w'
	MesgOn       bool
	BiffOn       bool
	WriteCh      chan WriteNotice
}

type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*Entry // session ID -> entry
	ttySeq   atomic.Int64

	// Rooms carries the real-time talk/ytalk/call byte streams.
	Rooms *RoomBroker

	// Calls is the call-signaling exchange (offers, rings, hangups).
	Calls *calls.Manager

	// incomingTalk marks pending cross-node talk invitations, keyed by
	// (localUser, remoteUser@remoteNode). Set by the federation server when a
	// peer opens a talk; consulted by the talk command so that a user running
	// `talk remote@node` joins the already-bridged relay room instead of
	// dialing back out.
	itMu         sync.Mutex
	incomingTalk map[string]bool
}

func NewHub() *Hub {
	return &Hub{
		sessions:     make(map[string]*Entry),
		Rooms:        NewRoomBroker(),
		Calls:        calls.NewManager(),
		incomingTalk: make(map[string]bool),
	}
}

func talkKey(localUser, remote string) string { return localUser + "\x00" + remote }

// AddIncomingTalk records a pending inbound talk invitation.
func (h *Hub) AddIncomingTalk(localUser, remote string) {
	h.itMu.Lock()
	h.incomingTalk[talkKey(localUser, remote)] = true
	h.itMu.Unlock()
}

// HasIncomingTalk reports whether a pending inbound talk exists.
func (h *Hub) HasIncomingTalk(localUser, remote string) bool {
	h.itMu.Lock()
	defer h.itMu.Unlock()
	return h.incomingTalk[talkKey(localUser, remote)]
}

// RemoveIncomingTalk clears a pending inbound talk (on accept or teardown).
func (h *Hub) RemoveIncomingTalk(localUser, remote string) {
	h.itMu.Lock()
	delete(h.incomingTalk, talkKey(localUser, remote))
	h.itMu.Unlock()
}

func (h *Hub) Register(e *Entry) {
	if e.LastActivity.IsZero() {
		e.LastActivity = e.LoginAt
	}
	h.mu.Lock()
	h.sessions[e.SessionID] = e
	h.mu.Unlock()
}

// Touch records activity for a session; drives the IDLE column in w/finger.
func (h *Hub) Touch(sessionID string) {
	h.mu.Lock()
	if e, ok := h.sessions[sessionID]; ok {
		e.LastActivity = time.Now()
	}
	h.mu.Unlock()
}

// SetBiff updates the live biff (new-mail alert) flag for a session.
func (h *Hub) SetBiff(sessionID string, on bool) {
	h.mu.Lock()
	if e, ok := h.sessions[sessionID]; ok {
		e.BiffOn = on
	}
	h.mu.Unlock()
}

func (h *Hub) Unregister(sessionID string) {
	h.mu.Lock()
	delete(h.sessions, sessionID)
	h.mu.Unlock()
}

func (h *Hub) AllocateTTY() string {
	return fmt.Sprintf("pts/%d", h.ttySeq.Add(1)-1)
}

func (h *Hub) List() []*Entry {
	h.mu.RLock()
	entries := make([]*Entry, 0, len(h.sessions))
	for _, e := range h.sessions {
		cp := *e
		entries = append(entries, &cp)
	}
	h.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LoginAt.Before(entries[j].LoginAt)
	})
	return entries
}

func (h *Hub) FindByUsername(username string) []*Entry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []*Entry
	for _, e := range h.sessions {
		if e.Username == username {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

func (h *Hub) SetState(sessionID, state string) {
	h.mu.Lock()
	if e, ok := h.sessions[sessionID]; ok {
		e.State = state
	}
	h.mu.Unlock()
}

func (h *Hub) SetMesg(sessionID string, on bool) {
	h.mu.Lock()
	if e, ok := h.sessions[sessionID]; ok {
		e.MesgOn = on
	}
	h.mu.Unlock()
}

// Send delivers a WriteNotice to every active session for username,
// filtered by the per-kind opt-out: mesg gates write/talk, biff gates
// mail alerts, wall is always delivered. Returns sessions reached.
func (h *Hub) Send(username string, notice WriteNotice) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, e := range h.sessions {
		if e.Username != username {
			continue
		}
		switch notice.Kind {
		case NoticeBiff:
			if !e.BiffOn {
				continue
			}
		case NoticeWall:
			// always delivered
		default: // write, talk
			if !e.MesgOn {
				continue
			}
		}
		select {
		case e.WriteCh <- notice:
			n++
		default:
			// channel full, drop
		}
	}
	return n
}

func (h *Hub) Count() int {
	h.mu.RLock()
	n := len(h.sessions)
	h.mu.RUnlock()
	return n
}
