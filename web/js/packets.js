// Media packet framing (DESIGN.md §9.7). Twin of internal/av/packet.go.
//
// Every media payload — a binary message on /ws/call or the payload of an
// ASSP stream frame — begins with a 4-byte header:
//
//   u8 kind | u8 source | u16 seq   (big-endian)

export const KIND_VIDEO_KEY = 0x01;   // full frame: u16 w | u16 h | PackBits(bitmap)
export const KIND_VIDEO_DELTA = 0x02; // XOR delta:  u16 w | u16 h | PackBits(prev^cur)
export const KIND_AUDIO = 0x03;       // i16 predictor | u8 step index | u8 reserved | ADPCM

export const PACKET_HEADER_SIZE = 4;

// A worst-case keyframe RLEs far below this; anything larger is not ours.
export const MAX_PACKET_SIZE = 256 << 10;

// parsePacket splits a raw packet into {kind, source, seq, payload}.
// payload aliases the input buffer.
export function parsePacket(b) {
  if (b.length < PACKET_HEADER_SIZE) throw new Error("av: packet too short");
  if (b.length > MAX_PACKET_SIZE) throw new Error("av: packet exceeds maximum");
  const kind = b[0];
  if (kind !== KIND_VIDEO_KEY && kind !== KIND_VIDEO_DELTA && kind !== KIND_AUDIO) {
    throw new Error(`av: unknown packet kind 0x${kind.toString(16)}`);
  }
  return {
    kind,
    source: b[1],
    seq: (b[2] << 8) | b[3],
    payload: b.subarray(PACKET_HEADER_SIZE),
  };
}

// writePacketHeader fills the first 4 bytes of buf.
export function writePacketHeader(buf, kind, source, seq) {
  buf[0] = kind;
  buf[1] = source;
  buf[2] = (seq >> 8) & 0xff;
  buf[3] = seq & 0xff;
}
