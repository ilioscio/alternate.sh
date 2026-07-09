// PackBits run-length encoding. Twin of internal/av/rle.go — the two must
// produce identical bytes for identical input (vector-enforced).
//
// Control byte c: [0,127] copy next c+1 literally; 128 no-op (tolerated on
// decode, never emitted); [129,255] repeat next byte 257-c times.

function runLen(src, i) {
  let j = i + 1;
  while (j < src.length && j - i < 128 && src[j] === src[i]) j++;
  return j - i;
}

export function packBits(src) {
  const dst = [];
  let i = 0;
  while (i < src.length) {
    const run = runLen(src, i);
    if (run >= 3) {
      dst.push(257 - run, src[i]);
      i += run;
      continue;
    }
    // Literal segment: extend until a 3+ run starts or 128 bytes.
    const start = i;
    i += run;
    while (i < src.length && i - start < 128) {
      const r = runLen(src, i);
      if (r >= 3) break;
      i += r;
    }
    if (i - start > 128) i = start + 128;
    dst.push(i - start - 1);
    for (let k = start; k < i; k++) dst.push(src[k]);
  }
  return Uint8Array.from(dst);
}

export function unpackBits(src, maxOut) {
  const dst = new Uint8Array(maxOut);
  let n = 0;
  let i = 0;
  while (i < src.length) {
    const c = src[i++];
    if (c < 128) {
      const len = c + 1;
      if (i + len > src.length) throw new Error("av: rle: truncated input");
      if (n + len > maxOut) throw new Error("av: rle: output exceeds limit");
      dst.set(src.subarray(i, i + len), n);
      n += len;
      i += len;
    } else if (c === 128) {
      // no-op
    } else {
      const len = 257 - c;
      if (i >= src.length) throw new Error("av: rle: truncated input");
      if (n + len > maxOut) throw new Error("av: rle: output exceeds limit");
      dst.fill(src[i], n, n + len);
      n += len;
      i++;
    }
  }
  return dst.subarray(0, n);
}
