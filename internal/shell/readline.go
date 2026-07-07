package shell

import (
	"fmt"
	"io"
)

// Readline provides basic line editing for raw terminal sessions.
// It supports: printable ASCII input, backspace, left/right arrows,
// home/end, Ctrl+A/E/K/U, up/down history, Ctrl+C, Ctrl+D.
//
// Multi-row handling (when width > 0):
//
//   rl.cur tracks the cursor's absolute column offset from the start of the
//   prompt output. Most of the time the physical row is (rl.cur-1)/width and
//   rowsBack uses (rl.cur-1)/width so that the deferred-wrap state (cursor
//   parked at the last column, rl.cur == width) correctly maps to row 0.
//
//   The one exception is when we emit \r\n ourselves (wrapIfNeeded) to fix the
//   visual glitch where the cursor overlaps the last character on a full row.
//   After that emission the cursor IS physically on the next row, so we set
//   atBoundary=true and use rl.cur/width for rowsBack instead.
type Readline struct {
	r          io.Reader
	w          io.Writer
	history    []string
	histPos    int
	width      int  // terminal width; 0 = single-line mode (no wrap handling)
	cur        int  // cursor column offset from start of prompt output
	atBoundary bool // true only after wrapIfNeeded moved cursor to next row via \r\n
}

func NewReadline(r io.Reader, w io.Writer) *Readline {
	return &Readline{r: r, w: w}
}

// ReadLine displays prompt and reads a line of input.
// Returns io.EOF on Ctrl+D with an empty buffer (signals logout).
// Returns ("", nil) on Ctrl+C (clear line, try again).
func (rl *Readline) ReadLine(prompt string) (string, error) {
	rl.w.Write([]byte(prompt))
	promptLen := len([]rune(prompt))
	rl.cur = promptLen
	rl.atBoundary = false

	var buf []rune
	pos := 0
	rl.histPos = len(rl.history)

	b := make([]byte, 1)
	for {
		_, err := rl.r.Read(b)
		if err != nil {
			return "", err
		}

		switch b[0] {
		case '\r', '\n':
			rl.w.Write([]byte("\r\n"))
			line := string(buf)
			if line != "" {
				rl.history = append(rl.history, line)
				rl.histPos = len(rl.history)
			}
			return line, nil

		case 0x7f, 0x08: // DEL / Backspace
			if pos > 0 {
				buf = append(buf[:pos-1], buf[pos:]...)
				pos--
				rl.redraw(prompt, promptLen, buf, pos)
			}

		case 0x03: // Ctrl+C
			rl.w.Write([]byte("^C\r\n"))
			return "", nil

		case 0x04: // Ctrl+D
			if len(buf) == 0 {
				rl.w.Write([]byte("\r\n"))
				return "", io.EOF
			}

		case 0x01: // Ctrl+A — beginning of line
			pos = 0
			rl.redraw(prompt, promptLen, buf, pos)

		case 0x05: // Ctrl+E — end of line
			pos = len(buf)
			rl.redraw(prompt, promptLen, buf, pos)

		case 0x0b: // Ctrl+K — kill to end
			buf = buf[:pos]
			rl.redraw(prompt, promptLen, buf, pos)

		case 0x15: // Ctrl+U — kill to beginning
			buf = buf[pos:]
			pos = 0
			rl.redraw(prompt, promptLen, buf, pos)

		case 0x1b: // Escape sequence
			seq := make([]byte, 2)
			rl.r.Read(seq)
			if seq[0] != '[' {
				break
			}
			switch seq[1] {
			case 'A': // Up — history prev
				if rl.histPos > 0 {
					rl.histPos--
					buf = []rune(rl.history[rl.histPos])
					pos = len(buf)
					rl.redraw(prompt, promptLen, buf, pos)
				}
			case 'B': // Down — history next
				if rl.histPos < len(rl.history)-1 {
					rl.histPos++
					buf = []rune(rl.history[rl.histPos])
					pos = len(buf)
					rl.redraw(prompt, promptLen, buf, pos)
				} else {
					rl.histPos = len(rl.history)
					buf = nil
					pos = 0
					rl.redraw(prompt, promptLen, buf, pos)
				}
			case 'C': // Right
				if pos < len(buf) {
					pos++
					rl.cur++
					rl.atBoundary = false
					rl.w.Write([]byte("\x1b[C"))
				}
			case 'D': // Left
				if pos > 0 {
					pos--
					rl.cur--
					rl.atBoundary = false
					rl.w.Write([]byte("\x1b[D"))
				}
			case 'H': // Home
				pos = 0
				rl.redraw(prompt, promptLen, buf, pos)
			case 'F': // End
				pos = len(buf)
				rl.redraw(prompt, promptLen, buf, pos)
			}

		default:
			if b[0] >= 0x20 && b[0] < 0x7f {
				r := rune(b[0])
				buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
				pos++
				if pos == len(buf) {
					rl.w.Write([]byte{b[0]})
					rl.cur++
					rl.wrapIfNeeded()
				} else {
					rl.redraw(prompt, promptLen, buf, pos)
				}
			}
		}
	}
}

// redraw redraws the entire line from the start of the prompt.
func (rl *Readline) redraw(prompt string, promptLen int, buf []rune, pos int) {
	// Compute how many rows below the prompt row the cursor currently sits.
	// Two cases because atBoundary disambiguates the deferred-wrap state:
	//   - atBoundary=true:  cursor was moved to col 0 of the next row via \r\n,
	//                       so rl.cur is an exact multiple of width → use cur/width.
	//   - atBoundary=false: cursor may be in deferred-wrap (cur==width, still row 0),
	//                       so use (cur-1)/width which correctly gives 0 for that case.
	rowsBack := 0
	if rl.width > 0 && rl.cur > 0 {
		if rl.atBoundary {
			rowsBack = rl.cur / rl.width
		} else if rl.cur > rl.width {
			rowsBack = (rl.cur - 1) / rl.width
		}
	}

	if rowsBack > 0 {
		fmt.Fprintf(rl.w, "\x1b[%dA", rowsBack)
		rl.w.Write([]byte("\r\x1b[J")) // CR + erase to end of screen
	} else {
		rl.w.Write([]byte("\r\x1b[K")) // CR + erase to end of line
	}

	rl.w.Write([]byte(prompt))
	rl.w.Write([]byte(string(buf)))
	rl.cur = promptLen + len(buf)
	rl.atBoundary = false

	if pos < len(buf) {
		moveBack := len(buf) - pos
		fmt.Fprintf(rl.w, "\x1b[%dD", moveBack)
		rl.cur -= moveBack
	} else {
		rl.wrapIfNeeded()
	}
}

// wrapIfNeeded emits \r\n when the cursor is exactly at a terminal column
// boundary after filling the last column. This moves the cursor visibly to
// col 0 of the next row (fixing the overlap glitch) and sets atBoundary=true
// so the next redraw uses the correct rowsBack formula.
func (rl *Readline) wrapIfNeeded() {
	if rl.width > 0 && rl.cur > 0 && rl.cur%rl.width == 0 {
		rl.w.Write([]byte("\r\n"))
		rl.atBoundary = true
	}
}
