// Grayscale→1-bit dithering (DESIGN.md §9.2). Twin of internal/av/dither.go.
//
// Bitmaps are packed 1 bit per pixel, rows MSB-first: bit 7 of byte 0 is
// pixel (0,0). Width must be a multiple of 8. A set bit is a lit pixel.

import { BLUE_NOISE_MASK, BLUE_NOISE_SIZE } from "./bluenoise.js";

const M = BLUE_NOISE_SIZE - 1; // tiling mask; size is a power of two
const SHIFT = 6;               // log2(BLUE_NOISE_SIZE)

// ditherBlueNoise thresholds gray (Uint8Array, w*h, 0=black) against the
// tiled blue-noise mask: a pixel lights when gray > mask. Static regions
// produce identical bits every frame — the property the delta codec needs.
export function ditherBlueNoise(gray, w, h) {
  const stride = w >> 3;
  const pix = new Uint8Array(stride * h);
  for (let y = 0; y < h; y++) {
    const row = y * w;
    const mrow = (y & M) << SHIFT;
    for (let bx = 0; bx < stride; bx++) {
      let b = 0;
      const x0 = bx << 3;
      for (let k = 0; k < 8; k++) {
        const x = x0 + k;
        if (gray[row + x] > BLUE_NOISE_MASK[mrow | (x & M)]) b |= 0x80 >> k;
      }
      pix[y * stride + bx] = b;
    }
  }
  return pix;
}

// ditherFloydSteinberg is the optional "shimmer" style: classic error
// diffusion (7/16 right, 3/16 down-left, 5/16 down, 1/16 down-right).
// Error weights use arithmetic shifts (>>4) so JS and Go agree exactly
// on negative values.
export function ditherFloydSteinberg(gray, w, h) {
  const stride = w >> 3;
  const pix = new Uint8Array(stride * h);
  const errBuf = new Int32Array(w * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * w + x;
      const v = gray[i] + errBuf[i];
      let diff;
      if (v > 127) {
        pix[y * stride + (x >> 3)] |= 0x80 >> (x & 7);
        diff = v - 255;
      } else {
        diff = v;
      }
      if (x + 1 < w) errBuf[i + 1] += (diff * 7) >> 4;
      if (y + 1 < h) {
        if (x > 0) errBuf[i + w - 1] += (diff * 3) >> 4;
        errBuf[i + w] += (diff * 5) >> 4;
        if (x + 1 < w) errBuf[i + w + 1] += diff >> 4;
      }
    }
  }
  return pix;
}
