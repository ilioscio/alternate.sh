package av

import (
	"bytes"
	"testing"
)

// xorshift32 is a tiny deterministic PRNG for test inputs, self-contained so
// test data never depends on math/rand's algorithm.
type xorshift32 uint32

func (x *xorshift32) next() uint32 {
	v := uint32(*x)
	v ^= v << 13
	v ^= v >> 17
	v ^= v << 5
	*x = xorshift32(v)
	return v
}

func (x *xorshift32) bytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(x.next())
	}
	return out
}

func roundTrip(t *testing.T, name string, raw []byte) []byte {
	t.Helper()
	packed := PackBits(raw)
	got, err := UnpackBits(packed, len(raw))
	if err != nil {
		t.Fatalf("%s: UnpackBits: %v", name, err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("%s: round trip mismatch: %d bytes in, %d out", name, len(raw), len(got))
	}
	return packed
}

func TestPackBitsRoundTrip(t *testing.T) {
	rng := xorshift32(1)
	cases := map[string][]byte{
		"empty":          {},
		"one":            {0x42},
		"two-same":       {7, 7},
		"three-same":     {7, 7, 7},
		"zeros-128":      make([]byte, 128),
		"zeros-129":      make([]byte, 129),
		"zeros-1536":     make([]byte, 1536), // an empty 128×96 delta
		"literal-128":    rng.bytes(128),
		"literal-200":    rng.bytes(200),
		"random-4k":      rng.bytes(4096),
		"alternating":    bytes.Repeat([]byte{0xAA, 0x55}, 100),
		"run-then-lit":   append(make([]byte, 300), rng.bytes(50)...),
		"lit-then-run":   append(rng.bytes(50), bytes.Repeat([]byte{0xFF}, 300)...),
		"short-runs-2":   bytes.Repeat([]byte{1, 1, 2, 2, 3, 3}, 40),
		"ones-4k":        bytes.Repeat([]byte{0xFF}, 4096),
		"single-run-max": bytes.Repeat([]byte{9}, 128),
	}
	for name, raw := range cases {
		packed := roundTrip(t, name, raw)
		if name == "zeros-1536" && len(packed) > 24 {
			t.Errorf("empty delta packed to %d bytes, want <= 24", len(packed))
		}
	}
}

func TestPackBitsRandomSweep(t *testing.T) {
	// Sweep run-heavy inputs at varying densities: flip random bits in a zero
	// buffer so run/literal boundaries land everywhere.
	rng := xorshift32(99)
	for density := 1; density <= 512; density *= 2 {
		raw := make([]byte, 2048)
		for i := 0; i < density; i++ {
			raw[rng.next()%2048] = byte(rng.next())
		}
		roundTrip(t, "sweep", raw)
	}
}

func TestUnpackBitsErrors(t *testing.T) {
	if _, err := UnpackBits([]byte{5, 1, 2}, 100); err == nil {
		t.Error("truncated literal: want error")
	}
	if _, err := UnpackBits([]byte{200}, 100); err == nil {
		t.Error("truncated run: want error")
	}
	if _, err := UnpackBits([]byte{200, 0xFF}, 10); err == nil {
		t.Error("run past maxOut: want error")
	}
	if _, err := UnpackBits([]byte{63, 0}, 10); err == nil {
		t.Error("literal past maxOut: want error")
	}
	// The 128 no-op control byte is tolerated.
	got, err := UnpackBits([]byte{128, 0, 7}, 10)
	if err != nil || !bytes.Equal(got, []byte{7}) {
		t.Errorf("no-op control: got %v, %v", got, err)
	}
}
