package calls

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func params() Params { return Params{Width: 128, Height: 96, FPS: 24} }

func TestCallLifecycle(t *testing.T) {
	m := NewManager()
	c, err := m.Offer("ilios", "nova", MediaAV, params())
	if err != nil {
		t.Fatal(err)
	}
	if m.Get(c.ID) != c {
		t.Fatal("Get did not find the live call")
	}
	if m.PendingFor("nova", "ilios") != c {
		t.Fatal("PendingFor did not find the ringing call")
	}
	if m.PendingFor("ilios", "nova") != nil {
		t.Fatal("PendingFor matched with roles reversed")
	}
	if c.Active() {
		t.Fatal("call active before accept")
	}

	if !c.Accept() {
		t.Fatal("Accept failed on a ringing call")
	}
	select {
	case <-c.Accepted():
	default:
		t.Fatal("Accepted channel not closed")
	}
	if !c.Active() {
		t.Fatal("call not active after accept")
	}
	if m.PendingFor("nova", "ilios") != nil {
		t.Fatal("accepted call still shows as pending")
	}

	c.End("hung up")
	select {
	case <-c.Ended():
	default:
		t.Fatal("Ended channel not closed")
	}
	if c.EndReason() != "hung up" {
		t.Fatalf("EndReason = %q", c.EndReason())
	}
	if m.Get(c.ID) != nil {
		t.Fatal("ended call still registered")
	}
	// End is idempotent; a second reason does not overwrite.
	c.End("again")
	if c.EndReason() != "hung up" {
		t.Fatal("second End overwrote the reason")
	}
	if c.Accept() {
		t.Fatal("Accept succeeded on an ended call")
	}
}

func TestBusy(t *testing.T) {
	m := NewManager()
	if _, err := m.Offer("ilios", "nova", MediaAV, params()); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"ilios", "kay"}, // caller busy
		{"kay", "ilios"}, // callee busy (as caller of first call)
		{"kay", "nova"},  // callee busy (as callee of first call)
	} {
		if _, err := m.Offer(pair[0], pair[1], MediaAV, params()); !errors.Is(err, ErrBusy) {
			t.Errorf("Offer(%s→%s) err = %v, want ErrBusy", pair[0], pair[1], err)
		}
	}
}

func TestRingTimeout(t *testing.T) {
	m := NewManager()
	m.RingTimeout = 30 * time.Millisecond
	c, err := m.Offer("ilios", "nova", MediaAV, params())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Ended():
	case <-time.After(2 * time.Second):
		t.Fatal("ring never timed out")
	}
	if c.EndReason() != "no answer" {
		t.Fatalf("EndReason = %q, want %q", c.EndReason(), "no answer")
	}
	if c.Accept() {
		t.Fatal("Accept succeeded after ring timeout")
	}
}

func TestRateLimit(t *testing.T) {
	m := NewManager()
	var last error
	for i := 0; i < 10; i++ {
		c, err := m.Offer("ilios", fmt.Sprintf("user%d", i), MediaAV, params())
		if err != nil {
			last = err
			break
		}
		c.End("test")
	}
	if !errors.Is(last, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit within 10 rapid offers, got %v", last)
	}
}

func TestBadMedia(t *testing.T) {
	m := NewManager()
	if _, err := m.Offer("a", "b", "smellovision", params()); err == nil {
		t.Fatal("unknown media accepted")
	}
}

func TestParamsClamp(t *testing.T) {
	cases := []struct {
		in, max, want Params
	}{
		// In-range passes through.
		{Params{128, 96, 24}, Params{128, 96, 24}, Params{128, 96, 24}},
		// Ceiling clamps down.
		{Params{640, 480, 60}, Params{128, 96, 24}, Params{128, 96, 24}},
		// Floors hold even against a lower ceiling.
		{Params{16, 16, 1}, Params{128, 96, 24}, Params{96, 72, 6}},
		// Width snaps down to a multiple of 8.
		{Params{125, 96, 24}, Params{160, 120, 24}, Params{120, 96, 24}},
	}
	for i, c := range cases {
		got := c.in.Clamp(c.max.Width, c.max.Height, c.max.FPS)
		if got != c.want {
			t.Errorf("case %d: Clamp = %+v, want %+v", i, got, c.want)
		}
	}
}
