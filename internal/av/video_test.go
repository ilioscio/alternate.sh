package av

import (
	"testing"
)

// gradientFrame builds a horizontal-gradient grayscale image with an optional
// white box at (bx, by) — a synthetic "talking head with motion".
func gradientFrame(w, h, bx, by int) []byte {
	gray := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gray[y*w+x] = byte(x * 255 / (w - 1))
		}
	}
	if bx >= 0 {
		for y := by; y < by+20 && y < h; y++ {
			for x := bx; x < bx+20 && x < w; x++ {
				gray[y*w+x] = 255
			}
		}
	}
	return gray
}

func noiseFrame(w, h int, seed uint32) []byte {
	rng := xorshift32(seed)
	return rng.bytes(w * h)
}

func mustDecode(t *testing.T, d *VideoDecoder, raw []byte) *Bitmap {
	t.Helper()
	p, err := ParsePacket(raw)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	bm, err := d.Decode(p)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if bm == nil {
		t.Fatal("Decode returned nil frame for an in-order packet")
	}
	return bm
}

func kindOf(t *testing.T, raw []byte) Kind {
	t.Helper()
	p, err := ParsePacket(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p.Kind
}

func TestVideoRoundTrip(t *testing.T) {
	const w, h = 128, 96
	enc := &VideoEncoder{Source: 2}
	dec := &VideoDecoder{}

	for i := 0; i < 12; i++ {
		frame := DitherBlueNoise(gradientFrame(w, h, 10+4*i, 30), w, h)
		raw := enc.Encode(frame)
		if raw == nil {
			t.Fatalf("frame %d: moving scene produced no packet", i)
		}
		wantKind := KindVideoDelta
		if i == 0 {
			wantKind = KindVideoKey
		}
		if k := kindOf(t, raw); k != wantKind {
			t.Fatalf("frame %d: kind = 0x%02x, want 0x%02x", i, k, wantKind)
		}
		got := mustDecode(t, dec, raw)
		if !got.Equal(frame) {
			t.Fatalf("frame %d: decoded bitmap differs from encoded", i)
		}
	}
}

func TestVideoStaticSceneSkips(t *testing.T) {
	const w, h = 128, 96
	enc := &VideoEncoder{KeyInterval: 6}
	frame := DitherBlueNoise(gradientFrame(w, h, 40, 40), w, h)

	if enc.Encode(frame) == nil {
		t.Fatal("first frame must be a keyframe")
	}
	// Frames 1..5: identical, all skipped.
	for i := 1; i <= 5; i++ {
		if raw := enc.Encode(frame); raw != nil {
			t.Fatalf("static frame %d: got a packet, want skip", i)
		}
	}
	// Frame 6: cadence forces a keyframe even though nothing changed.
	raw := enc.Encode(frame)
	if raw == nil {
		t.Fatal("keyframe cadence did not fire")
	}
	if k := kindOf(t, raw); k != KindVideoKey {
		t.Fatalf("cadence frame kind = 0x%02x, want keyframe", k)
	}
}

func TestVideoSceneCutPrefersKeyframe(t *testing.T) {
	const w, h = 128, 96
	enc := &VideoEncoder{}
	enc.Encode(DitherBlueNoise(gradientFrame(w, h, -1, 0), w, h))

	// A noise frame's XOR against the gradient is also noise; the delta
	// can't beat the keyframe, so the encoder must send a keyframe.
	raw := enc.Encode(DitherBlueNoise(noiseFrame(w, h, 7), w, h))
	if raw == nil {
		t.Fatal("scene cut produced no packet")
	}
	if k := kindOf(t, raw); k != KindVideoKey {
		t.Fatalf("scene cut kind = 0x%02x, want keyframe", k)
	}
}

func TestVideoDeltaIsSmall(t *testing.T) {
	const w, h = 128, 96
	enc := &VideoEncoder{}
	key := enc.Encode(DitherBlueNoise(gradientFrame(w, h, 20, 30), w, h))
	delta := enc.Encode(DitherBlueNoise(gradientFrame(w, h, 24, 30), w, h))
	if delta == nil {
		t.Fatal("moving box produced no delta")
	}
	if len(delta)*3 > len(key) {
		t.Errorf("delta = %d bytes vs keyframe %d; expected far smaller", len(delta), len(key))
	}
}

func TestVideoDropRecovery(t *testing.T) {
	const w, h = 128, 96
	enc := &VideoEncoder{KeyInterval: 5}
	dec := &VideoDecoder{}

	frames := make([]*Bitmap, 12)
	packets := make([][]byte, 12)
	for i := range frames {
		frames[i] = DitherBlueNoise(gradientFrame(w, h, 8+4*i, 20+2*i), w, h)
		packets[i] = enc.Encode(frames[i])
		if packets[i] == nil {
			t.Fatalf("frame %d unexpectedly skipped", i)
		}
	}

	mustDecode(t, dec, packets[0]) // keyframe
	mustDecode(t, dec, packets[1])
	// packets[2] is lost in the relay. packet 3 arrives: seq gap.
	p3, _ := ParsePacket(packets[3])
	bm, err := dec.Decode(p3)
	if err != nil {
		t.Fatalf("post-gap delta: %v", err)
	}
	if bm != nil {
		t.Fatal("post-gap delta was applied; decoder must freeze")
	}
	// Deltas keep arriving; still frozen.
	p4, _ := ParsePacket(packets[4])
	if bm, _ := dec.Decode(p4); bm != nil {
		t.Fatal("decoder unfroze without a keyframe")
	}
	// KeyInterval=5 means packets[5] is a keyframe: recovery.
	if k := kindOf(t, packets[5]); k != KindVideoKey {
		t.Fatalf("expected packets[5] to be the cadence keyframe, got 0x%02x", k)
	}
	got := mustDecode(t, dec, packets[5])
	if !got.Equal(frames[5]) {
		t.Fatal("recovered frame differs from source")
	}
	// And deltas apply cleanly again.
	got = mustDecode(t, dec, packets[6])
	if !got.Equal(frames[6]) {
		t.Fatal("post-recovery delta differs from source")
	}
}

func TestVideoDecoderRejectsGarbage(t *testing.T) {
	dec := &VideoDecoder{}
	cases := map[string]Packet{
		"short payload": {Kind: KindVideoKey, Payload: []byte{0, 128}},
		"width not x8":  {Kind: KindVideoKey, Payload: []byte{0, 100, 0, 96}},
		"zero height":   {Kind: KindVideoKey, Payload: []byte{0, 128, 0, 0}},
		"huge":          {Kind: KindVideoKey, Payload: []byte{8, 0, 8, 0}},
		"bad rle":       {Kind: KindVideoKey, Payload: []byte{0, 8, 0, 1, 200}},
		"wrong length":  {Kind: KindVideoKey, Payload: []byte{0, 8, 0, 2, 0, 0xAA}},
	}
	for name, p := range cases {
		if _, err := dec.Decode(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	// A delta before any keyframe is silently unusable, not an error.
	frame := NewBitmap(8, 2)
	rle := PackBits(frame.Pix)
	payload := append([]byte{0, 8, 0, 2}, rle...)
	bm, err := (&VideoDecoder{}).Decode(Packet{Kind: KindVideoDelta, Seq: 5, Payload: payload})
	if err != nil || bm != nil {
		t.Errorf("delta before keyframe: got (%v, %v), want (nil, nil)", bm, err)
	}
}

func TestVideoIdleBitrate(t *testing.T) {
	// The §9.5 claim: a still scene idles at a few hundred bytes/second.
	// A full-frame dithered gradient is the worst case (its keyframes RLE
	// terribly), so this bounds the *ceiling*: even incompressible content
	// idles under 1KB/s on periodic keyframes alone.
	const w, h = 128, 96
	enc := &VideoEncoder{} // default 48-frame cadence
	worst := DitherBlueNoise(gradientFrame(w, h, 50, 40), w, h)

	total := 0
	for i := 0; i < DefaultFPS*10; i++ { // 10 seconds
		if raw := enc.Encode(worst); raw != nil {
			total += len(raw)
		}
	}
	if perSecond := total / 10; perSecond > 1000 {
		t.Errorf("static worst-case scene costs %d B/s, want <= 1000", perSecond)
	}

	// Typical content — a mostly dark scene — should be far cheaper.
	enc = &VideoEncoder{}
	dark := DitherBlueNoise(gradientFrame(w, h, 50, 40), w, h)
	for i := range dark.Pix { // keep only the box region's rows
		if i/16 < 30 || i/16 > 60 {
			dark.Pix[i] = 0
		}
	}
	total = 0
	for i := 0; i < DefaultFPS*10; i++ {
		if raw := enc.Encode(dark); raw != nil {
			total += len(raw)
		}
	}
	if perSecond := total / 10; perSecond > 400 {
		t.Errorf("static dark scene costs %d B/s, want <= 400", perSecond)
	}
}
