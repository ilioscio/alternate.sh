package av

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestBlueNoiseMaskParity asserts the JS mask (web/js/bluenoise.js) is
// byte-identical to the Go mask. Both files are generated together by
// internal/av/gen; this catches one being regenerated without the other,
// or hand edits to either.
func TestBlueNoiseMaskParity(t *testing.T) {
	path := filepath.Join("..", "..", "web", "js", "bluenoise.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	body := regexp.MustCompile(`(?s)new Uint8Array\(\[(.*?)\]\)`).FindSubmatch(data)
	if body == nil {
		t.Fatal("could not locate Uint8Array literal in bluenoise.js")
	}
	nums := regexp.MustCompile(`\d+`).FindAllString(string(body[1]), -1)
	if len(nums) != len(blueNoiseMask) {
		t.Fatalf("JS mask has %d entries, Go mask has %d", len(nums), len(blueNoiseMask))
	}
	for i, s := range nums {
		v, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		if uint8(v) != blueNoiseMask[i] {
			t.Fatalf("mask differs at index %d: JS %d, Go %d", i, v, blueNoiseMask[i])
		}
	}
}
