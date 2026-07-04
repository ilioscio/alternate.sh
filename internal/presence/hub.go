package presence

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type WriteNotice struct {
	From    string
	Message string
}

type Entry struct {
	SessionID string
	Username  string
	TTY       string
	FromAddr  string
	LoginAt   time.Time
	State     string // what the user is currently doing, shown in 'w'
	MesgOn    bool
	WriteCh   chan WriteNotice
}

type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*Entry // session ID -> entry
	ttySeq   atomic.Int64
}

func NewHub() *Hub {
	return &Hub{sessions: make(map[string]*Entry)}
}

func (h *Hub) Register(e *Entry) {
	h.mu.Lock()
	h.sessions[e.SessionID] = e
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

// Send delivers a WriteNotice to every active session for username.
// Returns the number of sessions reached.
func (h *Hub) Send(username string, notice WriteNotice) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, e := range h.sessions {
		if e.Username == username && e.MesgOn {
			select {
			case e.WriteCh <- notice:
				n++
			default:
				// channel full, drop
			}
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
