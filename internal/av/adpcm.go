package av

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// IMA ADPCM, the classic 4-bit differential codec (Interactive Multimedia
// Association, 1992 — period-correct for our purposes and ~4KB/s at 8kHz).
// Each audio packet carries the decoder state (predictor + step index) it
// starts from, so any chunk is independently decodable: a dropped chunk
// costs 20ms of silence, never desync. Two 4-bit codes pack per byte,
// low nibble first (earlier sample in the low nibble).

var imaIndexTable = [8]int{-1, -1, -1, -1, 2, 4, 6, 8}

var imaStepTable = [89]int{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17,
	19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118,
	130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
	337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
	876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
	2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358,
	5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
	15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767,
}

// audioHeaderSize is the state snapshot preceding the ADPCM codes:
// i16 predictor | u8 step index | u8 reserved.
const audioHeaderSize = 4

// maxAudioCodeBytes caps a single chunk at one second of audio.
const maxAudioCodeBytes = SampleRate / 2

var errAudioShort = errors.New("av: audio payload too short")

func clamp16(v int) int {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return v
}

func clampIndex(v int) int {
	if v < 0 {
		return 0
	}
	if v > 88 {
		return 88
	}
	return v
}

// AudioEncoder carries codec state across chunks so the quantizer adapts
// continuously; each emitted packet still snapshots that state for resync.
type AudioEncoder struct {
	Source    uint8
	predictor int
	stepIndex int
	seq       uint16
}

// encodeSample quantizes one sample against the current state and advances it.
func (e *AudioEncoder) encodeSample(s int16) byte {
	step := imaStepTable[e.stepIndex]
	diff := int(s) - e.predictor

	var nib int
	if diff < 0 {
		nib = 8
		diff = -diff
	}

	// Quantize to 3 magnitude bits, accumulating the reconstructed
	// difference exactly as the decoder will.
	diffq := step >> 3
	if diff >= step {
		nib |= 4
		diff -= step
		diffq += step
	}
	if diff >= step>>1 {
		nib |= 2
		diff -= step >> 1
		diffq += step >> 1
	}
	if diff >= step>>2 {
		nib |= 1
		diffq += step >> 2
	}

	if nib&8 != 0 {
		e.predictor = clamp16(e.predictor - diffq)
	} else {
		e.predictor = clamp16(e.predictor + diffq)
	}
	e.stepIndex = clampIndex(e.stepIndex + imaIndexTable[nib&7])
	return byte(nib)
}

// EncodeChunk encodes an even number of PCM samples into a complete audio
// media packet (header + state snapshot + codes).
func (e *AudioEncoder) EncodeChunk(pcm []int16) ([]byte, error) {
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, fmt.Errorf("av: audio chunk must be a positive even sample count, got %d", len(pcm))
	}
	if len(pcm)/2 > maxAudioCodeBytes {
		return nil, fmt.Errorf("av: audio chunk of %d samples exceeds maximum", len(pcm))
	}

	buf := make([]byte, 0, PacketHeaderSize+audioHeaderSize+len(pcm)/2)
	buf = appendPacketHeader(buf, KindAudio, e.Source, e.seq)
	e.seq++

	// State snapshot: where the decoder must start for this chunk.
	buf = append(buf, byte(uint16(e.predictor)>>8), byte(uint16(e.predictor)), byte(e.stepIndex), 0)

	for i := 0; i < len(pcm); i += 2 {
		lo := e.encodeSample(pcm[i])
		hi := e.encodeSample(pcm[i+1])
		buf = append(buf, lo|hi<<4)
	}
	return buf, nil
}

// decodeSample reconstructs one sample from a 4-bit code, advancing state.
func decodeSample(nib int, predictor, stepIndex *int) int16 {
	step := imaStepTable[*stepIndex]
	diffq := step >> 3
	if nib&4 != 0 {
		diffq += step
	}
	if nib&2 != 0 {
		diffq += step >> 1
	}
	if nib&1 != 0 {
		diffq += step >> 2
	}
	if nib&8 != 0 {
		*predictor = clamp16(*predictor - diffq)
	} else {
		*predictor = clamp16(*predictor + diffq)
	}
	*stepIndex = clampIndex(*stepIndex + imaIndexTable[nib&7])
	return int16(*predictor)
}

// DecodeAudio decodes a KindAudio packet's payload to PCM. Stateless by
// design: the payload's own snapshot seeds the decoder.
func DecodeAudio(p Packet) ([]int16, error) {
	if p.Kind != KindAudio {
		return nil, fmt.Errorf("av: DecodeAudio on kind 0x%02x", uint8(p.Kind))
	}
	if len(p.Payload) < audioHeaderSize {
		return nil, errAudioShort
	}
	codes := p.Payload[audioHeaderSize:]
	if len(codes) == 0 || len(codes) > maxAudioCodeBytes {
		return nil, fmt.Errorf("av: audio chunk of %d code bytes out of range", len(codes))
	}

	predictor := int(int16(binary.BigEndian.Uint16(p.Payload[0:2])))
	stepIndex := int(p.Payload[2])
	if stepIndex > 88 {
		return nil, fmt.Errorf("av: audio step index %d out of range", stepIndex)
	}

	pcm := make([]int16, 0, len(codes)*2)
	for _, b := range codes {
		pcm = append(pcm, decodeSample(int(b&0x0f), &predictor, &stepIndex))
		pcm = append(pcm, decodeSample(int(b>>4), &predictor, &stepIndex))
	}
	return pcm, nil
}
