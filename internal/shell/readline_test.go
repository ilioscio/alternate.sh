package shell

import (
	"strings"
	"testing"
)

// vterm is a minimal terminal emulator implementing exactly the behaviors
// readline output depends on: printable chars with xterm-style deferred
// autowrap at the real width, CR, LF, and CSI A/B/G/K/J. It lets tests
// verify what actually ends up on screen — including that content above
// the prompt is never destroyed.
type vterm struct {
	width       int // the terminal's real width
	rows        [][]rune
	r, c        int
	wrapPending bool // deferred-wrap state: cursor parked past last column
}

func newVterm(width int) *vterm {
	return &vterm{width: width, rows: [][]rune{nil}}
}

func (v *vterm) put(ch rune) {
	if v.wrapPending {
		v.r++
		v.c = 0
		v.wrapPending = false
	}
	for len(v.rows) <= v.r {
		v.rows = append(v.rows, nil)
	}
	row := v.rows[v.r]
	for len(row) <= v.c {
		row = append(row, ' ')
	}
	row[v.c] = ch
	v.rows[v.r] = row
	if v.c == v.width-1 {
		v.wrapPending = true
	} else {
		v.c++
	}
}

func (v *vterm) Write(p []byte) (int, error) {
	i := 0
	for i < len(p) {
		b := p[i]
		switch {
		case b == '\r':
			v.c = 0
			v.wrapPending = false
			i++
		case b == '\n':
			v.r++
			v.wrapPending = false
			for len(v.rows) <= v.r {
				v.rows = append(v.rows, nil)
			}
			i++
		case b == 0x1b && i+1 < len(p) && p[i+1] == '[':
			j := i + 2
			n := 0
			hasNum := false
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				n = n*10 + int(p[j]-'0')
				j++
				hasNum = true
			}
			if !hasNum {
				n = 1
			}
			switch p[j] {
			case 'A':
				v.r -= n
				if v.r < 0 {
					v.r = 0 // terminals clamp at top; overshoot = destroyed lines
				}
			case 'B':
				v.r += n
				for len(v.rows) <= v.r {
					v.rows = append(v.rows, nil)
				}
			case 'G':
				v.c = n - 1
			case 'K':
				if v.r < len(v.rows) && v.c < len(v.rows[v.r]) {
					v.rows[v.r] = v.rows[v.r][:v.c]
				}
			case 'J':
				if v.r < len(v.rows) {
					if v.c < len(v.rows[v.r]) {
						v.rows[v.r] = v.rows[v.r][:v.c]
					}
					v.rows = v.rows[:v.r+1]
				}
			}
			v.wrapPending = false
			i = j + 1
		default:
			v.put(rune(b))
			i++
		}
	}
	return len(p), nil
}

func (v *vterm) line(i int) string {
	if i >= len(v.rows) {
		return ""
	}
	return strings.TrimRight(string(v.rows[i]), " ")
}

const testPrompt = "ilios@alternate.sh:~$ " // 22 chars, like production

// runReadline writes a sentinel line above the prompt, then runs one
// ReadLine over the scripted input. realWidth is the terminal's true width;
// toldWidth is what widthFn reports (they can differ, as happens live when
// a resize hasn't been observed yet).
func runReadline(t *testing.T, realWidth, toldWidth int, input string) (*vterm, string) {
	t.Helper()
	v := newVterm(realWidth)
	v.Write([]byte("SENTINEL\r\n"))
	rl := &Readline{
		r:       strings.NewReader(input),
		w:       v,
		widthFn: func() int { return toldWidth },
	}
	line, err := rl.ReadLine(testPrompt)
	if err != nil {
		t.Fatalf("ReadLine error: %v", err)
	}
	if got := v.line(0); got != "SENTINEL" {
		t.Fatalf("content above the prompt was destroyed: row 0 = %q", got)
	}
	return v, line
}

func TestWrapThenBackspace(t *testing.T) {
	// Type 60 chars after a 22-char prompt on an 80-col terminal: wraps to
	// row 2. Backspace 3 times (crossing back over the wrap), then Enter.
	input := strings.Repeat("a", 60) + "\x7f\x7f\x7f\r"
	v, line := runReadline(t, 80, 80, input)

	want := strings.Repeat("a", 57)
	if line != want {
		t.Errorf("returned line = %q, want 57 a's", line)
	}
	if got := v.line(1); got != testPrompt+want {
		t.Errorf("prompt row = %q, want prompt+57 a's", got)
	}
	if got := v.line(2); got != "" {
		t.Errorf("leftover text below prompt: %q", got)
	}
}

func TestWrapThenBackspaceWidthMismatch(t *testing.T) {
	// The reported bug: readline believes width 80 but the terminal is
	// really 168 wide (browser resized, snapshot stale). Backspacing across
	// the wrap must not eat lines above the prompt.
	input := strings.Repeat("a", 60) + "\x7f\x7f\x7f\r"
	v, line := runReadline(t, 168, 80, input)

	want := strings.Repeat("a", 57)
	if line != want {
		t.Errorf("returned line = %q, want 57 a's", line)
	}
	if got := v.line(1); got != testPrompt+want {
		t.Errorf("prompt row = %q, want prompt+57 a's", got)
	}
}

func TestExactBoundary(t *testing.T) {
	// Fill the row exactly (22+58=80), cross the boundary, retreat back
	// over it, then advance again. Every transition over the exact-width
	// state is exercised.
	input := strings.Repeat("a", 58) + // exactly fills row 0
		"bb" + // onto row 1
		"\x7f\x7f\x7f" + // back to 57: retreat over the boundary
		"cc" + // forward over it again: 59 chars
		"\r"
	v, line := runReadline(t, 80, 80, input)

	want := strings.Repeat("a", 57) + "cc"
	if line != want {
		t.Errorf("returned line = %q, want %q", line, want)
	}
	if got := v.line(1); got != (testPrompt + want)[:80] {
		t.Errorf("prompt row = %q", got)
	}
	if got := v.line(2); got != "c" {
		t.Errorf("wrap row = %q, want %q", got, "c")
	}
}

func TestMidLineEditing(t *testing.T) {
	// Insert and delete in the middle of the buffer using arrow keys.
	input := "aaaaaaaaaa" + // 10 a's
		"\x1b[D\x1b[D\x1b[D" + // left x3 → pos 7
		"\x7f\x7f" + // delete 2 → 8 chars, pos 5
		"X" + // insert → aaaaaXaaa
		"\r"
	_, line := runReadline(t, 80, 80, input)

	if line != "aaaaaXaaa" {
		t.Errorf("returned line = %q, want %q", line, "aaaaaXaaa")
	}
}

func TestMidLineEditingAcrossWrap(t *testing.T) {
	// Cursor sits on the wrapped row while edits redraw both rows.
	input := strings.Repeat("a", 70) + // 92 total → wraps
		"\x1b[D\x1b[D" + // left x2
		"XY" + // insert before last two chars
		"\r"
	v, line := runReadline(t, 80, 80, input)

	want := strings.Repeat("a", 68) + "XYaa"
	if line != want {
		t.Errorf("returned line = %q, want %q", line, want)
	}
	full := testPrompt + want
	if got := v.line(1); got != full[:80] {
		t.Errorf("row 1 = %q", got)
	}
	if got := v.line(2); got != full[80:] {
		t.Errorf("row 2 = %q, want %q", got, full[80:])
	}
}

func TestEnterMidLine(t *testing.T) {
	// Pressing Enter with the cursor mid-line must not print the \r\n in
	// the middle of the wrapped text.
	input := strings.Repeat("a", 70) +
		"\x1b[H" + // Home: cursor back on the prompt row
		"\r"
	v, line := runReadline(t, 80, 80, input)

	if line != strings.Repeat("a", 70) {
		t.Errorf("returned line = %q", line)
	}
	full := testPrompt + strings.Repeat("a", 70)
	if got := v.line(2); got != full[80:] {
		t.Errorf("wrapped row damaged by Enter: %q, want %q", got, full[80:])
	}
	// Cursor must have ended up below all text.
	if v.r != 3 {
		t.Errorf("cursor row after Enter = %d, want 3", v.r)
	}
}

func TestHistoryRecallShrinksRows(t *testing.T) {
	// Recall a short entry over a long wrapped line: stale rows must be erased.
	rlInput := strings.Repeat("b", 70) + "\r" // first line (goes to history)
	v := newVterm(80)
	v.Write([]byte("SENTINEL\r\n"))
	rl := &Readline{
		r:       strings.NewReader(rlInput + strings.Repeat("c", 70) + "\x1b[A\r"),
		w:       v,
		widthFn: func() int { return 80 },
	}
	if _, err := rl.ReadLine(testPrompt); err != nil {
		t.Fatal(err)
	}
	line, err := rl.ReadLine(testPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if line != strings.Repeat("b", 70) {
		t.Errorf("history recall = %q, want 70 b's", line)
	}
	if got := v.line(0); got != "SENTINEL" {
		t.Errorf("content above prompt destroyed: %q", got)
	}
}

func TestNoWidthFallback(t *testing.T) {
	// With no width information the readline must never move the cursor up.
	input := strings.Repeat("a", 60) + "\x7f\x7f\x7f\r"
	v := newVterm(80)
	v.Write([]byte("SENTINEL\r\n"))
	rl := NewReadline(strings.NewReader(input), v)
	line, err := rl.ReadLine(testPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if line != strings.Repeat("a", 57) {
		t.Errorf("returned line = %q", line)
	}
	if got := v.line(0); got != "SENTINEL" {
		t.Errorf("content above prompt destroyed: %q", got)
	}
}
