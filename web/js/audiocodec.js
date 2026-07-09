// IMA ADPCM audio codec (DESIGN.md §9.3, §9.7). Twin of internal/av/adpcm.go.
//
// 4-bit codes, two per byte, low nibble first. Every packet snapshots the
// encoder state (predictor + step index) it started from, so any chunk is
// independently decodable — a dropped chunk costs 20ms, never desync.

import { KIND_AUDIO, PACKET_HEADER_SIZE, writePacketHeader } from "./packets.js";

export const SAMPLE_RATE = 8000;
export const CHUNK_SAMPLES = 160; // 20ms at 8kHz; must be even

const AUDIO_HEADER_SIZE = 4; // i16 predictor | u8 step index | u8 reserved
const MAX_AUDIO_CODE_BYTES = SAMPLE_RATE / 2; // one second

const INDEX_TABLE = [-1, -1, -1, -1, 2, 4, 6, 8];

const STEP_TABLE = [
  7, 8, 9, 10, 11, 12, 13, 14, 16, 17,
  19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
  50, 55, 60, 66, 73, 80, 88, 97, 107, 118,
  130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
  337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
  876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
  2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358,
  5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
  15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767,
];

function clamp16(v) {
  return v > 32767 ? 32767 : v < -32768 ? -32768 : v;
}

function clampIndex(v) {
  return v < 0 ? 0 : v > 88 ? 88 : v;
}

export class AudioEncoder {
  constructor({ source = 0 } = {}) {
    this.source = source;
    this.predictor = 0;
    this.stepIndex = 0;
    this.seq = 0;
  }

  encodeSample(s) {
    const step = STEP_TABLE[this.stepIndex];
    let diff = s - this.predictor;
    let nib = 0;
    if (diff < 0) {
      nib = 8;
      diff = -diff;
    }
    // Quantize to 3 magnitude bits, accumulating the reconstructed
    // difference exactly as the decoder will.
    let diffq = step >> 3;
    if (diff >= step) {
      nib |= 4;
      diff -= step;
      diffq += step;
    }
    if (diff >= step >> 1) {
      nib |= 2;
      diff -= step >> 1;
      diffq += step >> 1;
    }
    if (diff >= step >> 2) {
      nib |= 1;
      diffq += step >> 2;
    }
    this.predictor = clamp16(nib & 8 ? this.predictor - diffq : this.predictor + diffq);
    this.stepIndex = clampIndex(this.stepIndex + INDEX_TABLE[nib & 7]);
    return nib;
  }

  // encodeChunk encodes an even number of PCM samples (Int16Array) into a
  // complete audio media packet.
  encodeChunk(pcm) {
    if (pcm.length === 0 || pcm.length % 2 !== 0) {
      throw new Error(`av: audio chunk must be a positive even sample count, got ${pcm.length}`);
    }
    if (pcm.length / 2 > MAX_AUDIO_CODE_BYTES) {
      throw new Error(`av: audio chunk of ${pcm.length} samples exceeds maximum`);
    }
    const buf = new Uint8Array(PACKET_HEADER_SIZE + AUDIO_HEADER_SIZE + pcm.length / 2);
    writePacketHeader(buf, KIND_AUDIO, this.source, this.seq);
    this.seq = (this.seq + 1) & 0xffff;

    // State snapshot: where the decoder must start for this chunk.
    const p16 = this.predictor & 0xffff;
    buf[4] = (p16 >> 8) & 0xff;
    buf[5] = p16 & 0xff;
    buf[6] = this.stepIndex;
    buf[7] = 0;

    for (let i = 0; i < pcm.length; i += 2) {
      const lo = this.encodeSample(pcm[i]);
      const hi = this.encodeSample(pcm[i + 1]);
      buf[8 + i / 2] = lo | (hi << 4);
    }
    return buf;
  }
}

function decodeSample(nib, st) {
  const step = STEP_TABLE[st.stepIndex];
  let diffq = step >> 3;
  if (nib & 4) diffq += step;
  if (nib & 2) diffq += step >> 1;
  if (nib & 1) diffq += step >> 2;
  st.predictor = clamp16(nib & 8 ? st.predictor - diffq : st.predictor + diffq);
  st.stepIndex = clampIndex(st.stepIndex + INDEX_TABLE[nib & 7]);
  return st.predictor;
}

// decodeAudio decodes a parsed KIND_AUDIO packet to an Int16Array.
// Stateless: the payload's own snapshot seeds the decoder.
export function decodeAudio(p) {
  if (p.kind !== KIND_AUDIO) throw new Error("av: decodeAudio on non-audio packet");
  if (p.payload.length < AUDIO_HEADER_SIZE) throw new Error("av: audio payload too short");
  const codes = p.payload.subarray(AUDIO_HEADER_SIZE);
  if (codes.length === 0 || codes.length > MAX_AUDIO_CODE_BYTES) {
    throw new Error(`av: audio chunk of ${codes.length} code bytes out of range`);
  }
  let predictor = (p.payload[0] << 8) | p.payload[1];
  if (predictor & 0x8000) predictor -= 0x10000;
  const stepIndex = p.payload[2];
  if (stepIndex > 88) throw new Error(`av: audio step index ${stepIndex} out of range`);

  const st = { predictor, stepIndex };
  const pcm = new Int16Array(codes.length * 2);
  for (let i = 0; i < codes.length; i++) {
    pcm[2 * i] = decodeSample(codes[i] & 0x0f, st);
    pcm[2 * i + 1] = decodeSample(codes[i] >> 4, st);
  }
  return pcm;
}
