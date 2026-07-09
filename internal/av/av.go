// Package av implements the alternate.sh media codecs: 1-bit dithered
// monochrome video (keyframe + XOR-delta, PackBits RLE) and lofi narrowband
// audio (IMA ADPCM, 8kHz mono), plus the 4-byte media packet framing they
// share. See DESIGN.md §9.
//
// This is the Go twin of the canonical browser implementation in web/js/.
// The two are cross-validated byte-for-byte against the shared test vectors
// in testdata/, so any behavioral change here must be mirrored there (and
// vice versa) or the vector tests fail. The server itself never encodes or
// decodes media in production — it relays opaque packets — but this package
// lets integration tests prove that real, decodable media flowed end-to-end.
package av

//go:generate go run ./gen

// Media pipeline constants. These are the negotiated defaults; CALL_OPEN may
// clamp video dimensions and fps downward, never upward.
const (
	// DefaultWidth and DefaultHeight are the target video frame size.
	// Width must always be a multiple of 8 (rows pack MSB-first into bytes).
	DefaultWidth  = 128
	DefaultHeight = 96

	// DefaultFPS is the target video frame rate.
	DefaultFPS = 24

	// SampleRate is the audio sample rate in Hz. Narrowband on purpose:
	// the telephone band lives comfortably inside 8kHz.
	SampleRate = 8000

	// ChunkSamples is the number of PCM samples per audio packet:
	// 20ms at 8kHz. Must be even (two 4-bit codes pack per byte).
	ChunkSamples = 160
)
