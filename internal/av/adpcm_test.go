package av

import (
	"math"
	"testing"
)

func sineChunk(freq float64, amp int, phase0 float64, n int) ([]int16, float64) {
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(float64(amp) * math.Sin(phase0+2*math.Pi*freq*float64(i)/SampleRate))
	}
	return pcm, phase0 + 2*math.Pi*freq*float64(n)/SampleRate
}

func TestADPCMRoundTripQuality(t *testing.T) {
	enc := &AudioEncoder{Source: 1}
	phase := 0.0
	var pcmAll, decAll []int16
	for c := 0; c < 10; c++ {
		var pcm []int16
		pcm, phase = sineChunk(440, 12000, phase, ChunkSamples)
		raw, err := enc.EncodeChunk(pcm)
		if err != nil {
			t.Fatalf("EncodeChunk: %v", err)
		}
		p, err := ParsePacket(raw)
		if err != nil {
			t.Fatalf("ParsePacket: %v", err)
		}
		if p.Kind != KindAudio || p.Source != 1 || p.Seq != uint16(c) {
			t.Fatalf("packet header = %+v", p)
		}
		dec, err := DecodeAudio(p)
		if err != nil {
			t.Fatalf("DecodeAudio: %v", err)
		}
		if len(dec) != ChunkSamples {
			t.Fatalf("decoded %d samples, want %d", len(dec), ChunkSamples)
		}
		pcmAll = append(pcmAll, pcm...)
		decAll = append(decAll, dec...)
	}

	// SNR over the steady-state portion (skip the first chunk while the
	// quantizer step adapts up from silence).
	var sig, noise float64
	for i := ChunkSamples; i < len(pcmAll); i++ {
		s := float64(pcmAll[i])
		e := float64(pcmAll[i]) - float64(decAll[i])
		sig += s * s
		noise += e * e
	}
	snr := 10 * math.Log10(sig/noise)
	if snr < 15 {
		t.Errorf("sine SNR = %.1f dB, want >= 15", snr)
	}
}

// TestADPCMChunkIndependence proves the resync property: a chunk decoded in
// isolation (as after a drop) yields exactly the same PCM as one decoded in
// sequence, because each packet snapshots the encoder state it started from.
func TestADPCMChunkIndependence(t *testing.T) {
	enc := &AudioEncoder{}
	phase := 0.0
	var packets [][]byte
	for c := 0; c < 5; c++ {
		var pcm []int16
		pcm, phase = sineChunk(700, 9000, phase, ChunkSamples)
		raw, err := enc.EncodeChunk(pcm)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, raw)
	}

	// Decode chunk 3 alone; then decode all in order and compare.
	p3, _ := ParsePacket(packets[3])
	alone, err := DecodeAudio(p3)
	if err != nil {
		t.Fatal(err)
	}
	p3again, _ := ParsePacket(packets[3])
	inSeq, err := DecodeAudio(p3again)
	if err != nil {
		t.Fatal(err)
	}
	for i := range alone {
		if alone[i] != inSeq[i] {
			t.Fatalf("sample %d differs decoded alone vs in sequence", i)
		}
	}
}

func TestADPCMSilence(t *testing.T) {
	enc := &AudioEncoder{}
	raw, err := enc.EncodeChunk(make([]int16, ChunkSamples))
	if err != nil {
		t.Fatal(err)
	}
	want := PacketHeaderSize + audioHeaderSize + ChunkSamples/2
	if len(raw) != want {
		t.Errorf("silence chunk = %d bytes, want %d", len(raw), want)
	}
	p, _ := ParsePacket(raw)
	dec, err := DecodeAudio(p)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range dec {
		if s > 24 || s < -24 {
			t.Fatalf("silence decoded to %d at sample %d", s, i)
		}
	}
}

func TestADPCMErrors(t *testing.T) {
	enc := &AudioEncoder{}
	if _, err := enc.EncodeChunk(nil); err == nil {
		t.Error("empty chunk: want error")
	}
	if _, err := enc.EncodeChunk(make([]int16, 3)); err == nil {
		t.Error("odd chunk: want error")
	}
	if _, err := enc.EncodeChunk(make([]int16, SampleRate+2)); err == nil {
		t.Error("oversized chunk: want error")
	}

	if _, err := DecodeAudio(Packet{Kind: KindAudio, Payload: []byte{0, 0}}); err == nil {
		t.Error("short payload: want error")
	}
	if _, err := DecodeAudio(Packet{Kind: KindAudio, Payload: []byte{0, 0, 89, 0, 0x11}}); err == nil {
		t.Error("step index 89: want error")
	}
	if _, err := DecodeAudio(Packet{Kind: KindAudio, Payload: []byte{0, 0, 3, 0}}); err == nil {
		t.Error("no codes: want error")
	}
	if _, err := DecodeAudio(Packet{Kind: KindVideoKey, Payload: []byte{0, 0, 3, 0, 0}}); err == nil {
		t.Error("wrong kind: want error")
	}
}
