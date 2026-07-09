package assp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []Frame{
		{Channel: 0, Type: TypeRequest, Flags: 0, Payload: []byte("hello")},
		{Channel: 7, Type: TypeStreamData, Flags: FlagDroppable, Payload: []byte{0x00, 0xff, 0x10}},
		{Channel: 65535, Type: TypeStreamClose, Flags: 0, Payload: nil}, // empty payload
		{Channel: 1, Type: TypeStreamData, Flags: 0, Payload: bytes.Repeat([]byte{0xab}, 4096)},
	}
	var buf bytes.Buffer
	for _, f := range cases {
		buf.Reset()
		if err := WriteFrame(&buf, f.Channel, f.Type, f.Flags, f.Payload); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if got.Channel != f.Channel || got.Type != f.Type || got.Flags != f.Flags {
			t.Errorf("header mismatch: got %v want %v", got, f)
		}
		if !bytes.Equal(got.Payload, f.Payload) {
			t.Errorf("payload mismatch for %v", f)
		}
	}
}

func TestDroppableFlag(t *testing.T) {
	f := Frame{Flags: FlagDroppable}
	if !f.Droppable() {
		t.Error("Droppable() should be true")
	}
	if (Frame{Flags: 0}).Droppable() {
		t.Error("Droppable() should be false")
	}
}

func TestMultipleFramesStream(t *testing.T) {
	// Frames on different channels written back-to-back must decode in order
	// with correct boundaries — this is the muxing invariant.
	var buf bytes.Buffer
	WriteFrame(&buf, 0, TypeRequest, 0, []byte("ctrl"))
	WriteFrame(&buf, 3, TypeStreamData, FlagDroppable, []byte("video-frame"))
	WriteFrame(&buf, 0, TypeResponse, 0, []byte("ok"))

	want := []struct {
		ch   uint16
		typ  uint8
		data string
	}{
		{0, TypeRequest, "ctrl"},
		{3, TypeStreamData, "video-frame"},
		{0, TypeResponse, "ok"},
	}
	for i, w := range want {
		f, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if f.Channel != w.ch || f.Type != w.typ || string(f.Payload) != w.data {
			t.Errorf("frame %d: got %v %q, want ch=%d typ=%d %q", i, f, f.Payload, w.ch, w.typ, w.data)
		}
	}
	if _, err := ReadFrame(&buf); err != io.EOF {
		t.Errorf("expected clean EOF after last frame, got %v", err)
	}
}

func TestReadCleanEOF(t *testing.T) {
	// Nothing buffered → EOF exactly on a frame boundary.
	if _, err := ReadFrame(bytes.NewReader(nil)); err != io.EOF {
		t.Errorf("want io.EOF, got %v", err)
	}
}

func TestReadTruncatedHeaderAndBody(t *testing.T) {
	// Partial header → unexpected EOF.
	if _, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x00})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated header: want ErrUnexpectedEOF, got %v", err)
	}
	// Full header promising 10 bytes, but only 3 present → unexpected EOF.
	var hdr [HeaderSize]byte
	encodeHeader(hdr[:], 1, TypeStreamData, 0, 10)
	b := append(hdr[:], []byte("abc")...)
	if _, err := ReadFrame(bytes.NewReader(b)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated body: want ErrUnexpectedEOF, got %v", err)
	}
}

func TestWriteRejectsOversize(t *testing.T) {
	big := make([]byte, MaxPayload+1)
	if err := WriteFrame(io.Discard, 0, TypeStreamData, 0, big); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("write oversize: want ErrPayloadTooLarge, got %v", err)
	}
}

func TestReadRejectsOversizeHeader(t *testing.T) {
	// A hostile peer claims a huge length; we must reject before allocating.
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], MaxPayload+1)
	if _, err := ReadFrame(bytes.NewReader(hdr[:])); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("read oversize: want ErrPayloadTooLarge, got %v", err)
	}
}

func TestConnConcurrentWrites(t *testing.T) {
	// Many goroutines writing different channels must never corrupt framing:
	// every frame that arrives is well-formed and its payload matches its
	// channel-derived expected content.
	client, server := net.Pipe()
	cc := NewConn(client)
	sc := NewConn(server)

	const writers = 16
	const perWriter = 50

	payloadFor := func(ch uint16, i int) []byte {
		p := make([]byte, 32)
		for j := range p {
			p[j] = byte(ch) ^ byte(i)
		}
		return p
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for ch := 1; ch <= writers; ch++ {
			wg.Add(1)
			go func(ch uint16) {
				defer wg.Done()
				for i := 0; i < perWriter; i++ {
					if err := cc.Write(ch, TypeStreamData, 0, payloadFor(ch, i)); err != nil {
						t.Errorf("write ch %d: %v", ch, err)
						return
					}
				}
			}(uint16(ch))
		}
		wg.Wait()
		cc.Close()
	}()

	// Reader: validate each frame's payload matches what its channel/counter
	// should produce. Track a per-channel counter.
	counters := map[uint16]int{}
	for {
		f, err := sc.ReadFrame()
		if err != nil {
			break
		}
		i := counters[f.Channel]
		counters[f.Channel]++
		if !bytes.Equal(f.Payload, payloadFor(f.Channel, i)) {
			t.Fatalf("corrupted/interleaved frame on ch %d seq %d", f.Channel, i)
		}
	}
	<-done
	for ch := uint16(1); ch <= writers; ch++ {
		if counters[ch] != perWriter {
			t.Errorf("channel %d: got %d frames, want %d", ch, counters[ch], perWriter)
		}
	}
}
