// Package ratelimit provides a simple in-memory sliding-window limiter keyed
// by an arbitrary string (typically a client IP). It is process-local — fine
// for a single-instance deployment; a horizontally-scaled setup would move
// this to a shared store.
package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
	max    int
	window time.Duration
}

// New returns a limiter allowing at most max events per key within window.
func New(max int, window time.Duration) *Limiter {
	l := &Limiter{
		events: make(map[string][]time.Time),
		max:    max,
		window: window,
	}
	return l
}

// Allow records an event for key and reports whether it is within the limit.
// Timestamps outside the window are pruned on each call; empty keys are
// dropped to bound memory.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	times := l.events[key]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= l.max {
		// Store the pruned slice so it doesn't grow unbounded on rejection.
		if len(kept) == 0 {
			delete(l.events, key)
		} else {
			l.events[key] = kept
		}
		return false
	}

	l.events[key] = append(kept, now)
	return true
}

// Reset clears the record for a key (e.g. after a successful, legitimate
// action that shouldn't count against future attempts).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.events, key)
	l.mu.Unlock()
}
