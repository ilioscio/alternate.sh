package av

import "testing"

func litFraction(b *Bitmap) float64 {
	lit := 0
	for y := 0; y < b.H; y++ {
		for x := 0; x < b.W; x++ {
			if b.Get(x, y) {
				lit++
			}
		}
	}
	return float64(lit) / float64(b.W*b.H)
}

func flat(w, h int, v byte) []byte {
	g := make([]byte, w*h)
	for i := range g {
		g[i] = v
	}
	return g
}

func TestDitherBlueNoiseTonalResponse(t *testing.T) {
	const w, h = 128, 96
	if !DitherBlueNoise(flat(w, h, 0), w, h).AllZero() {
		t.Error("black input lit pixels")
	}
	if f := litFraction(DitherBlueNoise(flat(w, h, 255), w, h)); f < 0.99 {
		t.Errorf("white input lit fraction = %.3f, want >= 0.99", f)
	}
	// Mid-gray should light about half the pixels — the mask's threshold
	// distribution is uniform by construction.
	if f := litFraction(DitherBlueNoise(flat(w, h, 128), w, h)); f < 0.45 || f > 0.55 {
		t.Errorf("mid-gray lit fraction = %.3f, want ~0.5", f)
	}
	// Monotonic: brighter input never lights fewer pixels.
	prev := -1.0
	for v := 0; v <= 255; v += 15 {
		f := litFraction(DitherBlueNoise(flat(w, h, byte(v)), w, h))
		if f < prev {
			t.Fatalf("lit fraction fell from %.3f to %.3f at gray %d", prev, f, v)
		}
		prev = f
	}
}

// TestDitherBlueNoiseTemporalStability is the property the whole codec
// design leans on: identical input regions produce identical bits.
func TestDitherBlueNoiseTemporalStability(t *testing.T) {
	const w, h = 128, 96
	gray := gradientFrame(w, h, 30, 30)
	a := DitherBlueNoise(gray, w, h)
	b := DitherBlueNoise(gray, w, h)
	if !a.Equal(b) {
		t.Fatal("same input dithered to different bits")
	}
}

func TestDitherFloydSteinberg(t *testing.T) {
	const w, h = 128, 96
	if !DitherFloydSteinberg(flat(w, h, 0), w, h).AllZero() {
		t.Error("black input lit pixels")
	}
	if f := litFraction(DitherFloydSteinberg(flat(w, h, 255), w, h)); f != 1.0 {
		t.Errorf("white input lit fraction = %.3f, want 1.0", f)
	}
	if f := litFraction(DitherFloydSteinberg(flat(w, h, 128), w, h)); f < 0.45 || f > 0.56 {
		t.Errorf("mid-gray lit fraction = %.3f, want ~0.5", f)
	}
}

func TestBlueNoiseMaskDistribution(t *testing.T) {
	// Void-and-cluster assigns exactly 16 pixels to each of the 256 levels.
	var counts [256]int
	for _, v := range blueNoiseMask {
		counts[v]++
	}
	for level, c := range counts {
		if c != 16 {
			t.Fatalf("mask level %d has %d pixels, want 16", level, c)
		}
	}
}
