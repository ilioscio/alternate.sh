// The 1-bit video codec (DESIGN.md §9.5, §9.7). Twin of internal/av/video.go;
// every rule here is vector-locked against the Go implementation.

import { packBits, unpackBits } from "./rle.js";
import {
  KIND_VIDEO_KEY,
  KIND_VIDEO_DELTA,
  PACKET_HEADER_SIZE,
  writePacketHeader,
} from "./packets.js";

// Keyframe cadence in input frames (~2s at 24fps).
export const DEFAULT_KEY_INTERVAL = 48;

const MAX_VIDEO_DIM = 1024;
const VIDEO_HEADER_SIZE = 4; // u16 width | u16 height

function xorAllZero(a, b) {
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

export class VideoEncoder {
  constructor({ source = 0, keyInterval = DEFAULT_KEY_INTERVAL } = {}) {
    this.source = source;
    this.keyInterval = keyInterval > 0 ? keyInterval : DEFAULT_KEY_INTERVAL;
    this.prev = null;
    this.prevW = 0;
    this.prevH = 0;
    this.seq = 0;
    this.sinceKey = 0;
  }

  // encode consumes the next packed frame and returns a complete media
  // packet (Uint8Array), or null when the frame is identical to the previous
  // one and no keyframe is due. Rules (identical to the Go twin):
  //   keyframe when: first frame, keyInterval frames since the last keyframe,
  //   size changed, or the delta would encode no smaller than the keyframe.
  encode(pix, w, h) {
    // sinceKey counts every input frame including this one, so keyframes fire
    // on wall-clock cadence even across skipped static frames.
    this.sinceKey++;
    const forceKey =
      this.prev === null || this.sinceKey >= this.keyInterval ||
      this.prevW !== w || this.prevH !== h;

    let kind = KIND_VIDEO_KEY;
    let rle;
    if (forceKey) {
      rle = packBits(pix);
    } else {
      if (xorAllZero(pix, this.prev)) return null;
      const delta = new Uint8Array(pix.length);
      for (let i = 0; i < pix.length; i++) delta[i] = pix[i] ^ this.prev[i];
      const deltaRLE = packBits(delta);
      const keyRLE = packBits(pix);
      if (keyRLE.length <= deltaRLE.length) {
        rle = keyRLE;
      } else {
        kind = KIND_VIDEO_DELTA;
        rle = deltaRLE;
      }
    }

    if (kind === KIND_VIDEO_KEY) this.sinceKey = 0;
    this.prev = pix.slice();
    this.prevW = w;
    this.prevH = h;

    const buf = new Uint8Array(PACKET_HEADER_SIZE + VIDEO_HEADER_SIZE + rle.length);
    writePacketHeader(buf, kind, this.source, this.seq);
    this.seq = (this.seq + 1) & 0xffff;
    buf[4] = (w >> 8) & 0xff;
    buf[5] = w & 0xff;
    buf[6] = (h >> 8) & 0xff;
    buf[7] = h & 0xff;
    buf.set(rle, 8);
    return buf;
  }
}

function parseVideoPayload(payload) {
  if (payload.length < VIDEO_HEADER_SIZE) throw new Error("av: video payload too short");
  const w = (payload[0] << 8) | payload[1];
  const h = (payload[2] << 8) | payload[3];
  if (w <= 0 || h <= 0 || w % 8 !== 0 || w > MAX_VIDEO_DIM || h > MAX_VIDEO_DIM) {
    throw new Error(`av: invalid video dimensions ${w}x${h}`);
  }
  return { w, h, rle: payload.subarray(VIDEO_HEADER_SIZE) };
}

export class VideoDecoder {
  constructor() {
    this.prev = null; // {w, h, pix}
    this.lastSeq = 0;
    this.waiting = false;
  }

  // decode consumes one parsed video packet and returns {w, h, pix} — the
  // decoder's internal state, valid until the next decode — or null when the
  // packet is legitimately unusable (a delta after a dropped frame): keep
  // showing the last frame and wait for the next keyframe. Throws on
  // malformed input.
  decode(p) {
    const { w, h, rle } = parseVideoPayload(p.payload);
    const want = (w >> 3) * h;
    const pix = unpackBits(rle, want);
    if (pix.length !== want) {
      throw new Error(`av: video frame decoded to ${pix.length} bytes, want ${want}`);
    }

    if (p.kind === KIND_VIDEO_KEY) {
      this.prev = { w, h, pix };
      this.lastSeq = p.seq;
      this.waiting = false;
      return this.prev;
    }

    // Delta.
    if (this.prev === null || this.waiting) return null;
    if (p.seq !== ((this.lastSeq + 1) & 0xffff)) {
      // A relay shed a frame; deltas no longer apply cleanly.
      this.waiting = true;
      return null;
    }
    if (this.prev.w !== w || this.prev.h !== h) {
      throw new Error(`av: delta size ${w}x${h} does not match frame ${this.prev.w}x${this.prev.h}`);
    }
    for (let i = 0; i < want; i++) this.prev.pix[i] ^= pix[i];
    this.lastSeq = p.seq;
    return this.prev;
  }
}
