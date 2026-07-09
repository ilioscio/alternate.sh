package av

import "testing"

func TestBitmapGetSet(t *testing.T) {
	b := NewBitmap(16, 4)
	pts := [][2]int{{0, 0}, {7, 0}, {8, 0}, {15, 3}, {3, 2}}
	for _, p := range pts {
		b.Set(p[0], p[1], true)
	}
	for _, p := range pts {
		if !b.Get(p[0], p[1]) {
			t.Errorf("pixel (%d,%d) not set", p[0], p[1])
		}
	}
	lit := 0
	for y := 0; y < 4; y++ {
		for x := 0; x < 16; x++ {
			if b.Get(x, y) {
				lit++
			}
		}
	}
	if lit != len(pts) {
		t.Errorf("lit %d pixels, want %d", lit, len(pts))
	}
	b.Set(7, 0, false)
	if b.Get(7, 0) {
		t.Error("pixel (7,0) still set after clear")
	}
}

func TestBitmapXOR(t *testing.T) {
	a := NewBitmap(8, 2)
	b := NewBitmap(8, 2)
	a.Set(1, 0, true)
	a.Set(2, 1, true)
	b.Set(2, 1, true)
	b.Set(5, 1, true)

	d := a.XORed(b)
	if d.Get(2, 1) {
		t.Error("common pixel survived XOR")
	}
	if !d.Get(1, 0) || !d.Get(5, 1) {
		t.Error("differing pixels lost in XOR")
	}

	// Applying the delta to a reconstructs b.
	a.XORInPlace(d)
	if !a.Equal(b) {
		t.Error("a XOR delta != b")
	}
	if !NewBitmap(8, 2).AllZero() {
		t.Error("fresh bitmap not AllZero")
	}
	if d.AllZero() {
		t.Error("nonzero delta reported AllZero")
	}
}
