package av

// Grayscale→1-bit dithering (DESIGN.md §9.2). Blue-noise ordered dithering is
// the default: a fixed threshold mask means a static region produces identical
// bits every frame, which is what makes the delta codec nearly free at rest.
// Floyd–Steinberg is the optional "shimmer" style — organic but temporally
// unstable, so it costs real bitrate.
//
// Both must match the JS twins bit-for-bit; the shared test vectors enforce it.

// DitherBlueNoise converts 8-bit grayscale (w×h, row-major, 0=black) to a
// 1-bit bitmap by thresholding against the tiled 64×64 blue-noise mask.
// A pixel lights when gray > mask.
func DitherBlueNoise(gray []byte, w, h int) *Bitmap {
	bm := NewBitmap(w, h)
	stride := w / 8
	for y := 0; y < h; y++ {
		row := y * w
		mrow := (y & (blueNoiseSize - 1)) << blueNoiseShift
		for bx := 0; bx < stride; bx++ {
			var b byte
			x0 := bx * 8
			for k := 0; k < 8; k++ {
				x := x0 + k
				if gray[row+x] > blueNoiseMask[mrow|(x&(blueNoiseSize-1))] {
					b |= 0x80 >> k
				}
			}
			bm.Pix[y*stride+bx] = b
		}
	}
	return bm
}

// DitherFloydSteinberg converts 8-bit grayscale to 1-bit with classic
// error diffusion (7/16 right, 3/16 down-left, 5/16 down, 1/16 down-right).
// Error weights use arithmetic shifts (>>4) so Go and JS agree exactly on
// negative values.
func DitherFloydSteinberg(gray []byte, w, h int) *Bitmap {
	bm := NewBitmap(w, h)
	errBuf := make([]int32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			v := int32(gray[i]) + errBuf[i]
			var diff int32
			if v > 127 {
				bm.Set(x, y, true)
				diff = v - 255
			} else {
				diff = v
			}
			if x+1 < w {
				errBuf[i+1] += (diff * 7) >> 4
			}
			if y+1 < h {
				if x > 0 {
					errBuf[i+w-1] += (diff * 3) >> 4
				}
				errBuf[i+w] += (diff * 5) >> 4
				if x+1 < w {
					errBuf[i+w+1] += diff >> 4
				}
			}
		}
	}
	return bm
}
