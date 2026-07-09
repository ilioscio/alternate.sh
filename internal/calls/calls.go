// Package calls implements call signaling for the retro-futurism A/V layer
// (DESIGN.md §9.6): offers that ring until answered, declined, canceled, or
// timed out, and active calls that end when either side hangs up. Media
// never flows through here — once a call is active, media packets ride the
// room relay; this package is only the telephone exchange.
package calls

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ilioscio/alternate.sh/internal/ratelimit"
)

// Media selections for a call.
const (
	MediaAV    = "av"    // video + audio
	MediaAudio = "audio" // audio only
)

// Params are the negotiated codec parameters. The callee's node clamps a
// caller's proposal to its own limits (never upward).
type Params struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	FPS    int `json:"fps"`
}

// Clamp bounds p to the given maxima and the codec's hard floor
// (96×72 @ 6fps, width a multiple of 8).
func (p Params) Clamp(maxW, maxH, maxFPS int) Params {
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	p.Width = clamp(p.Width, 96, maxW) &^ 7 // multiple of 8
	p.Height = clamp(p.Height, 72, maxH)
	p.FPS = clamp(p.FPS, 6, maxFPS)
	return p
}

// Source ids within a call (the media packet header's source byte).
const (
	SourceCaller uint8 = 0
	SourceCallee uint8 = 1
)

// call lifecycle states.
const (
	stateRinging = iota
	stateActive
	stateEnded
)

// Call is one call's signaling state. Caller and Callee are usernames;
// a remote party is qualified ("user@host").
type Call struct {
	ID     string
	Caller string
	Callee string
	Media  string
	Params Params

	mgr *Manager

	mu        sync.Mutex
	state     int
	endReason string
	ringTimer *time.Timer
	accepted  chan struct{}
	ended     chan struct{}
}

// Accepted is closed when the callee answers.
func (c *Call) Accepted() <-chan struct{} { return c.accepted }

// Ended is closed when the call ends for any reason.
func (c *Call) Ended() <-chan struct{} { return c.ended }

// EndReason reports why the call ended ("" while live).
func (c *Call) EndReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.endReason
}

// Active reports whether the call has been accepted and not yet ended.
func (c *Call) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == stateActive
}

// Accept transitions ringing → active. It reports false if the call had
// already ended (e.g. the ring timed out in the same instant).
func (c *Call) Accept() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != stateRinging {
		return false
	}
	c.state = stateActive
	if c.ringTimer != nil {
		c.ringTimer.Stop()
	}
	close(c.accepted)
	return true
}

// End terminates the call (idempotent) and removes it from the manager.
func (c *Call) End(reason string) {
	c.mu.Lock()
	if c.state == stateEnded {
		c.mu.Unlock()
		return
	}
	c.state = stateEnded
	c.endReason = reason
	if c.ringTimer != nil {
		c.ringTimer.Stop()
	}
	close(c.ended)
	c.mu.Unlock()

	c.mgr.remove(c.ID)
}

// Involves reports whether user is a party to this call.
func (c *Call) Involves(user string) bool {
	return c.Caller == user || c.Callee == user
}

// RoomID is the presence-room key carrying this call's media. Keyed by call
// ID, not participant set: a call is a negotiated object with its own
// lifecycle, and a cross-node call has a distinct room on each node.
func (c *Call) RoomID() string { return "call:" + c.ID }

// Manager tracks all live calls on this node.
type Manager struct {
	// RingTimeout bounds how long an unanswered offer rings.
	RingTimeout time.Duration

	mu    sync.Mutex
	calls map[string]*Call

	// initiations rate-limits call placement per caller (anti-harassment,
	// mirroring the write/talk limits of §10.3).
	initiations *ratelimit.Limiter
}

func NewManager() *Manager {
	return &Manager{
		RingTimeout: 45 * time.Second,
		calls:       make(map[string]*Call),
		initiations: ratelimit.New(6, time.Minute),
	}
}

var (
	ErrBusy      = errors.New("busy")
	ErrRateLimit = errors.New("too many calls placed; wait a minute")
)

// Offer places a new call from caller to callee and starts it ringing.
// Both parties must be free; callers are rate-limited.
func (m *Manager) Offer(caller, callee, media string, p Params) (*Call, error) {
	if media != MediaAV && media != MediaAudio {
		return nil, fmt.Errorf("unknown media type %q", media)
	}
	if !m.initiations.Allow(caller) {
		return nil, ErrRateLimit
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c.Involves(caller) || c.Involves(callee) {
			return nil, ErrBusy
		}
	}

	var idb [8]byte
	rand.Read(idb[:])
	c := &Call{
		ID:       hex.EncodeToString(idb[:]),
		Caller:   caller,
		Callee:   callee,
		Media:    media,
		Params:   p,
		mgr:      m,
		state:    stateRinging,
		accepted: make(chan struct{}),
		ended:    make(chan struct{}),
	}
	// Assign under the call mutex: the timer callback reads ringTimer via
	// End the instant it fires.
	c.mu.Lock()
	c.ringTimer = time.AfterFunc(m.RingTimeout, func() { c.End("no answer") })
	c.mu.Unlock()
	m.calls[c.ID] = c
	return c, nil
}

// Get returns the call with the given ID, or nil.
func (m *Manager) Get(id string) *Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[id]
}

// PendingFor returns the ringing call from caller to callee, or nil —
// the lookup behind the symmetric answer (`call <caller>` accepts).
func (m *Manager) PendingFor(callee, caller string) *Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c.Caller == caller && c.Callee == callee {
			c.mu.Lock()
			ringing := c.state == stateRinging
			c.mu.Unlock()
			if ringing {
				return c
			}
		}
	}
	return nil
}

// ForUser returns any live call the user is a party to, or nil.
func (m *Manager) ForUser(user string) *Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c.Involves(user) {
			return c
		}
	}
	return nil
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.calls, id)
	m.mu.Unlock()
}
