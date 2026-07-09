package av

import "errors"

// PackBits run-length encoding, the classic byte-oriented RLE (TIFF/MacPaint
// flavor). A control byte c means:
//
//	c in [0,127]:   copy the next c+1 bytes literally
//	c == 128:       no-op (never emitted by this encoder, tolerated on decode)
//	c in [129,255]: repeat the next byte 257-c times (a run of 2..128)
//
// It is chosen for legibility and because the payloads it sees are extreme:
// XOR deltas of temporally-stable dithered video are dominated by long zero
// runs, which cost 2 bytes per 128. This encoder emits runs only when they
// are 3+ bytes long; shorter repeats ride inside literals.

var (
	errRLETruncated = errors.New("av: rle: truncated input")
	errRLETooLarge  = errors.New("av: rle: output exceeds limit")
)

// runLen returns the length of the run of identical bytes at src[i:],
// capped at 128 (the longest a single PackBits token can express).
func runLen(src []byte, i int) int {
	j := i + 1
	for j < len(src) && j-i < 128 && src[j] == src[i] {
		j++
	}
	return j - i
}

// PackBits compresses src. The output is deterministic: the JS twin must
// produce identical bytes for identical input.
func PackBits(src []byte) []byte {
	dst := make([]byte, 0, len(src)/8+16)
	i := 0
	for i < len(src) {
		run := runLen(src, i)
		if run >= 3 {
			dst = append(dst, byte(257-run), src[i])
			i += run
			continue
		}
		// Literal segment: extend until a 3+ run starts or 128 bytes.
		start := i
		i += run
		for i < len(src) && i-start < 128 {
			r := runLen(src, i)
			if r >= 3 {
				break
			}
			i += r
		}
		if i-start > 128 {
			i = start + 128
		}
		dst = append(dst, byte(i-start-1))
		dst = append(dst, src[start:i]...)
	}
	return dst
}

// UnpackBits decompresses src, refusing to produce more than maxOut bytes
// (frames arrive from the network; the cap is the decoder's memory-safety
// boundary).
func UnpackBits(src []byte, maxOut int) ([]byte, error) {
	dst := make([]byte, 0, maxOut)
	i := 0
	for i < len(src) {
		c := src[i]
		i++
		switch {
		case c < 128: // literal of c+1 bytes
			n := int(c) + 1
			if i+n > len(src) {
				return nil, errRLETruncated
			}
			if len(dst)+n > maxOut {
				return nil, errRLETooLarge
			}
			dst = append(dst, src[i:i+n]...)
			i += n
		case c == 128: // no-op
		default: // run of 257-c copies
			n := 257 - int(c)
			if i >= len(src) {
				return nil, errRLETruncated
			}
			if len(dst)+n > maxOut {
				return nil, errRLETooLarge
			}
			for k := 0; k < n; k++ {
				dst = append(dst, src[i])
			}
			i++
		}
	}
	return dst, nil
}
