package av

import (
	"encoding/binary"
	"fmt"
)

// The video codec (DESIGN.md §9.5, §9.7). A frame is a 1-bit bitmap.
// Keyframes carry the full bitmap; delta frames carry the XOR against the
// previous frame; both are PackBits-compressed. With blue-noise dithering a
// still scene produces all-zero deltas, which are simply not sent.

// DefaultKeyInterval is the keyframe cadence in input frames (~2s at 24fps).
// Periodic keyframes bound how long a receiver that lost a delta stays frozen.
const DefaultKeyInterval = 48

// maxVideoDim bounds parsed frame dimensions; real calls use ≤160×120.
const maxVideoDim = 1024

// videoHeaderSize is the payload prefix: u16 width | u16 height.
const videoHeaderSize = 4

// VideoEncoder turns a sequence of same-sized bitmaps into media packets.
type VideoEncoder struct {
	Source      uint8
	KeyInterval int // frames between keyframes; DefaultKeyInterval if 0

	prev     *Bitmap
	seq      uint16
	sinceKey int
}

// Encode consumes the next frame and returns a complete media packet, or nil
// when the frame is identical to the previous one and no keyframe is due —
// the "still scenes are nearly free" property. The rules are deterministic
// (and vector-locked against the JS twin):
//
//   - keyframe when: first frame, KeyInterval frames have passed since the
//     last keyframe, the frame size changed, or the delta would encode no
//     smaller than the keyframe (a scene cut).
//   - otherwise: XOR delta, skipped entirely if empty.
func (e *VideoEncoder) Encode(frame *Bitmap) []byte {
	ki := e.KeyInterval
	if ki <= 0 {
		ki = DefaultKeyInterval
	}

	// sinceKey counts every input frame since the last keyframe, including
	// this one — so a keyframe goes out every ki frames of wall-clock time
	// even when everything in between was skipped as static.
	e.sinceKey++
	forceKey := e.prev == nil || e.sinceKey >= ki ||
		e.prev.W != frame.W || e.prev.H != frame.H

	kind := KindVideoKey
	var rle []byte
	if forceKey {
		rle = PackBits(frame.Pix)
	} else {
		delta := frame.XORed(e.prev)
		if delta.AllZero() {
			return nil
		}
		deltaRLE := PackBits(delta.Pix)
		keyRLE := PackBits(frame.Pix)
		if len(keyRLE) <= len(deltaRLE) {
			rle = keyRLE
		} else {
			kind = KindVideoDelta
			rle = deltaRLE
		}
	}

	if kind == KindVideoKey {
		e.sinceKey = 0
	}
	e.prev = frame.Clone()

	buf := make([]byte, 0, PacketHeaderSize+videoHeaderSize+len(rle))
	buf = appendPacketHeader(buf, kind, e.Source, e.seq)
	e.seq++
	buf = append(buf, byte(frame.W>>8), byte(frame.W), byte(frame.H>>8), byte(frame.H))
	buf = append(buf, rle...)
	return buf
}

// VideoDecoder reconstructs frames from one source's packet stream.
type VideoDecoder struct {
	prev    *Bitmap
	lastSeq uint16
	waiting bool // lost a delta; ignoring deltas until the next keyframe
}

// parseVideoPayload validates and splits a video payload.
func parseVideoPayload(payload []byte) (w, h int, rle []byte, err error) {
	if len(payload) < videoHeaderSize {
		return 0, 0, nil, fmt.Errorf("av: video payload too short")
	}
	w = int(binary.BigEndian.Uint16(payload[0:2]))
	h = int(binary.BigEndian.Uint16(payload[2:4]))
	if w <= 0 || h <= 0 || w%8 != 0 || w > maxVideoDim || h > maxVideoDim {
		return 0, 0, nil, fmt.Errorf("av: invalid video dimensions %dx%d", w, h)
	}
	return w, h, payload[videoHeaderSize:], nil
}

// Decode consumes one video packet and returns the current frame, or
// (nil, nil) when the packet is legitimately unusable — a delta following a
// dropped frame — in which case the caller keeps showing the last frame and
// waits for the next keyframe. The returned bitmap is the decoder's internal
// state: valid until the next Decode call.
func (d *VideoDecoder) Decode(p Packet) (*Bitmap, error) {
	w, h, rle, err := parseVideoPayload(p.Payload)
	if err != nil {
		return nil, err
	}
	want := w / 8 * h
	pix, err := UnpackBits(rle, want)
	if err != nil {
		return nil, err
	}
	if len(pix) != want {
		return nil, fmt.Errorf("av: video frame decoded to %d bytes, want %d", len(pix), want)
	}

	switch p.Kind {
	case KindVideoKey:
		d.prev = &Bitmap{W: w, H: h, Pix: pix}
		d.lastSeq = p.Seq
		d.waiting = false
		return d.prev, nil

	case KindVideoDelta:
		if d.prev == nil || d.waiting {
			return nil, nil
		}
		if p.Seq != d.lastSeq+1 {
			// A relay shed a frame; deltas no longer apply cleanly.
			d.waiting = true
			return nil, nil
		}
		if d.prev.W != w || d.prev.H != h {
			return nil, fmt.Errorf("av: delta size %dx%d does not match frame %dx%d", w, h, d.prev.W, d.prev.H)
		}
		d.prev.XORInPlace(&Bitmap{W: w, H: h, Pix: pix})
		d.lastSeq = p.Seq
		return d.prev, nil

	default:
		return nil, fmt.Errorf("av: Decode on non-video kind 0x%02x", uint8(p.Kind))
	}
}
