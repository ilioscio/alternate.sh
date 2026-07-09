// Package assp implements the Alternate Shell Server Protocol, a small
// multiplexed binary protocol for server-to-server federation: presence,
// finger, mail/news sync, and real-time stream relay (talk today; lofi audio
// and dithered-mono video later).
//
// Wire format — every frame is an 8-byte fixed header followed by a payload:
//
//	 0      4        6      7      8
//	+------+--------+------+------+----------------+
//	| len  | chan   | type | flag | payload (len)  |
//	+------+--------+------+------+----------------+
//	  u32     u16     u8     u8
//
//   - len:     payload length in bytes (big-endian), not counting the header
//   - chan:    multiplex channel. 0 is control (request/response); 1+ are
//              independent stream channels (one per talk/audio/video session)
//   - type:    frame type (see constants)
//   - flag:    bit flags (see constants); notably FlagDroppable
//
// The header is deliberately tiny so that high-rate, small-payload media
// streams (e.g. 24fps dithered video) carry negligible overhead, and the
// channel field lets a single TCP+TLS connection carry presence, a talk
// session, and a call simultaneously without extra sockets.
package assp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// HeaderSize is the fixed frame header length in bytes.
	HeaderSize = 8

	// MaxPayload bounds a single frame's payload to protect against a peer
	// claiming an enormous length. Media frames stay well under this; control
	// messages are tiny. 8 MiB is generous headroom.
	MaxPayload = 8 << 20

	// ControlChannel carries request/response control messages.
	ControlChannel uint16 = 0
)

// Frame types.
const (
	TypeRequest  uint8 = 0x01 // control: a request (payload begins with a request id)
	TypeResponse uint8 = 0x02 // control: a response to a request
	TypeError    uint8 = 0x03 // control: an error response

	TypeStreamOpen  uint8 = 0x10 // open a stream on this channel
	TypeStreamData  uint8 = 0x11 // stream payload bytes
	TypeStreamClose uint8 = 0x12 // close the stream on this channel

	TypeHello uint8 = 0x20 // handshake: node identity + nonce
	TypeAuth  uint8 = 0x21 // handshake: HMAC proof of the shared secret
)

// Frame flags (bitfield).
const (
	// FlagDroppable marks a frame the sender considers safe to drop under
	// pressure (e.g. a stale video frame). The transport is still reliable;
	// this is a hint to relays and the receiving application, and lets media
	// senders shed load by never queuing late frames in the first place.
	FlagDroppable uint8 = 1 << 0
)

var (
	// ErrPayloadTooLarge is returned when a frame's declared length exceeds
	// MaxPayload.
	ErrPayloadTooLarge = errors.New("assp: frame payload exceeds maximum")
)

// Frame is a single decoded protocol frame. Payload is owned by the Frame; the
// reader returns a fresh slice per frame so callers may retain it.
type Frame struct {
	Channel uint16
	Type    uint8
	Flags   uint8
	Payload []byte
}

// Droppable reports whether the FlagDroppable bit is set.
func (f Frame) Droppable() bool { return f.Flags&FlagDroppable != 0 }

func (f Frame) String() string {
	return fmt.Sprintf("Frame{chan=%d type=0x%02x flags=0x%02x len=%d}",
		f.Channel, f.Type, f.Flags, len(f.Payload))
}

// encodeHeader writes the 8-byte header for a payload of the given length.
func encodeHeader(dst []byte, ch uint16, typ, flags uint8, payloadLen int) {
	binary.BigEndian.PutUint32(dst[0:4], uint32(payloadLen))
	binary.BigEndian.PutUint16(dst[4:6], ch)
	dst[6] = typ
	dst[7] = flags
}

// WriteFrame encodes and writes a single frame to w. It performs the header
// and payload in as few writes as practical. It is not safe for concurrent
// use on the same writer; use a Conn for synchronized access.
func WriteFrame(w io.Writer, ch uint16, typ, flags uint8, payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrPayloadTooLarge
	}
	var hdr [HeaderSize]byte
	encodeHeader(hdr[:], ch, typ, flags, len(payload))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrame reads a single frame from r. It returns io.EOF only when the
// stream ends cleanly on a frame boundary; a truncated frame yields
// io.ErrUnexpectedEOF.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		// A clean EOF before any header bytes is a real EOF; mid-header is not.
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(hdr[0:4])
	if length > MaxPayload {
		return Frame{}, ErrPayloadTooLarge
	}
	f := Frame{
		Channel: binary.BigEndian.Uint16(hdr[4:6]),
		Type:    hdr[6],
		Flags:   hdr[7],
	}
	if length > 0 {
		f.Payload = make([]byte, length)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return Frame{}, err
		}
	}
	return f, nil
}
