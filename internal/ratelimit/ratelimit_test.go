package ratelimit

import (
	"testing"
	"time"
)

func TestAllowWithinLimit(t *testing.T) {
	l := New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("event %d should be allowed", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Error("4th event should be rejected")
	}
	// A different key is independent.
	if !l.Allow("5.6.7.8") {
		t.Error("different key should be allowed")
	}
}

func TestWindowExpiry(t *testing.T) {
	l := New(1, 50*time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("first allowed")
	}
	if l.Allow("k") {
		t.Fatal("second within window should be rejected")
	}
	time.Sleep(60 * time.Millisecond)
	if !l.Allow("k") {
		t.Error("should be allowed after window elapses")
	}
}

func TestReset(t *testing.T) {
	l := New(1, time.Minute)
	l.Allow("k")
	if l.Allow("k") {
		t.Fatal("second should be rejected")
	}
	l.Reset("k")
	if !l.Allow("k") {
		t.Error("should be allowed after reset")
	}
}
