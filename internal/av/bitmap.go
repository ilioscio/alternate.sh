package av

import "fmt"

// Bitmap is a packed 1-bit-per-pixel monochrome frame. Rows pack MSB-first:
// bit 7 of Pix[0] is pixel (0,0). Width must be a positive multiple of 8;
// a set bit is a lit (foreground) pixel.
type Bitmap struct {
	W, H int
	Pix  []byte // W/8 × H bytes, row-major
}

// NewBitmap allocates an all-dark bitmap. It panics on an invalid size:
// dimensions are validated at the packet boundary (parseVideoPayload), so a
// bad size reaching here is a programming error, not bad input.
func NewBitmap(w, h int) *Bitmap {
	if w <= 0 || h <= 0 || w%8 != 0 {
		panic(fmt.Sprintf("av: invalid bitmap size %dx%d (width must be a positive multiple of 8)", w, h))
	}
	return &Bitmap{W: w, H: h, Pix: make([]byte, w/8*h)}
}

// Get reports whether pixel (x, y) is lit.
func (b *Bitmap) Get(x, y int) bool {
	return b.Pix[y*(b.W/8)+x/8]&(0x80>>(x%8)) != 0
}

// Set lights or darkens pixel (x, y).
func (b *Bitmap) Set(x, y int, lit bool) {
	i := y*(b.W/8) + x/8
	mask := byte(0x80 >> (x % 8))
	if lit {
		b.Pix[i] |= mask
	} else {
		b.Pix[i] &^= mask
	}
}

// Clone returns an independent copy.
func (b *Bitmap) Clone() *Bitmap {
	pix := make([]byte, len(b.Pix))
	copy(pix, b.Pix)
	return &Bitmap{W: b.W, H: b.H, Pix: pix}
}

// XORed returns a new bitmap holding b XOR other. Sizes must match.
func (b *Bitmap) XORed(other *Bitmap) *Bitmap {
	if b.W != other.W || b.H != other.H {
		panic(fmt.Sprintf("av: bitmap size mismatch %dx%d vs %dx%d", b.W, b.H, other.W, other.H))
	}
	out := &Bitmap{W: b.W, H: b.H, Pix: make([]byte, len(b.Pix))}
	for i := range b.Pix {
		out.Pix[i] = b.Pix[i] ^ other.Pix[i]
	}
	return out
}

// XORInPlace applies other into b (b ^= other). Sizes must match.
func (b *Bitmap) XORInPlace(other *Bitmap) {
	if b.W != other.W || b.H != other.H {
		panic(fmt.Sprintf("av: bitmap size mismatch %dx%d vs %dx%d", b.W, b.H, other.W, other.H))
	}
	for i := range b.Pix {
		b.Pix[i] ^= other.Pix[i]
	}
}

// AllZero reports whether no pixel is lit.
func (b *Bitmap) AllZero() bool {
	for _, p := range b.Pix {
		if p != 0 {
			return false
		}
	}
	return true
}

// Equal reports whether two bitmaps have identical size and content.
func (b *Bitmap) Equal(other *Bitmap) bool {
	if b.W != other.W || b.H != other.H {
		return false
	}
	for i := range b.Pix {
		if b.Pix[i] != other.Pix[i] {
			return false
		}
	}
	return true
}
