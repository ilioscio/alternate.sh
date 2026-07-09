package trade

import "testing"

func TestPriceCurve(t *testing.T) {
	for c := 0; c < 3; c++ {
		// Monotonic: more stock never raises the price.
		prev := price(c, -50)
		for s := 0; s <= 1100; s += 50 {
			p := price(c, s)
			if p > prev {
				t.Fatalf("%s: price rose with stock (%d)", names[c], s)
			}
			prev = p
		}
		// Bounds: 0.8×–1.2× base.
		if hi := price(c, 0); hi != basePrice[c]*1200/1000 {
			t.Errorf("%s: scarce price = %d", names[c], hi)
		}
		if lo := price(c, 1000); lo != basePrice[c]*800/1000 {
			t.Errorf("%s: glut price = %d", names[c], lo)
		}
		if price(c, 999999) < 1 {
			t.Errorf("%s: price fell below 1", names[c])
		}
	}
	// The spread is what makes hauling profitable: buying at glut must be
	// cheaper than selling at scarcity.
	if price(0, 1000) >= price(0, 0) {
		t.Fatal("no profitable spread")
	}
}

func TestParseCommodity(t *testing.T) {
	good := map[string]int{"ore": 0, "org": 1, "organics": 1, "eq": 2, "equipment": 2}
	for in, want := range good {
		if got, ok := parseCommodity(in); !ok || got != want {
			t.Errorf("parseCommodity(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"o", "x", "", "e", "gold"} {
		if _, ok := parseCommodity(bad); ok {
			t.Errorf("parseCommodity(%q) accepted", bad)
		}
	}
}
