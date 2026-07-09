package av

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Media packet framing (DESIGN.md §9.7). Every media payload — a binary
// message on /ws/call or the payload of an ASSP stream frame — begins with:
//
//	u8 kind | u8 source | u16 seq   (big-endian)
//
// source is the participant id within the call (group-readiness: a 1:1 call
// uses 0 and 1). seq is a per-source, per-media wrapping counter incremented
// only for transmitted packets, so a receiver can distinguish "nothing
// changed" (no packet, seq contiguous) from "a relay shed a late frame"
// (seq gap).

// Kind identifies a media packet's payload type.
type Kind uint8

const (
	KindVideoKey   Kind = 0x01 // full frame: u16 w | u16 h | PackBits(bitmap)
	KindVideoDelta Kind = 0x02 // XOR delta:  u16 w | u16 h | PackBits(prev^cur)
	KindAudio      Kind = 0x03 // i16 predictor | u8 step index | u8 reserved | ADPCM codes
)

// PacketHeaderSize is the fixed media packet header length.
const PacketHeaderSize = 4

// MaxPacketSize bounds a whole media packet. A worst-case 1024×1024 keyframe
// RLEs to ~132KB; anything larger is not a packet this system produces.
const MaxPacketSize = 256 << 10

var errPacketShort = errors.New("av: packet too short")

// Packet is a parsed media packet. Payload aliases the input buffer.
type Packet struct {
	Kind    Kind
	Source  uint8
	Seq     uint16
	Payload []byte
}

// ParsePacket splits a raw media packet into header and payload.
func ParsePacket(b []byte) (Packet, error) {
	if len(b) < PacketHeaderSize {
		return Packet{}, errPacketShort
	}
	if len(b) > MaxPacketSize {
		return Packet{}, fmt.Errorf("av: packet of %d bytes exceeds maximum", len(b))
	}
	k := Kind(b[0])
	if k != KindVideoKey && k != KindVideoDelta && k != KindAudio {
		return Packet{}, fmt.Errorf("av: unknown packet kind 0x%02x", b[0])
	}
	return Packet{
		Kind:    k,
		Source:  b[1],
		Seq:     binary.BigEndian.Uint16(b[2:4]),
		Payload: b[4:],
	}, nil
}

// appendPacketHeader appends the 4-byte header to dst.
func appendPacketHeader(dst []byte, k Kind, source uint8, seq uint16) []byte {
	return append(dst, byte(k), source, byte(seq>>8), byte(seq))
}
