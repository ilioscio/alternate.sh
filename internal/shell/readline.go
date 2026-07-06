package shell

import (
	"fmt"
	"io"
)

// Readline provides basic line editing for raw terminal sessions.
// It supports: printable ASCII input, backspace, left/right arrows,
// home/end, Ctrl+A/E/K/U, up/down history, Ctrl+C, Ctrl+D.
//
// When width > 0, redraw correctly handles lines that wrap across
// multiple terminal rows. After filling the last column, we emit \r\n
// to force the cursor to the start of the next row. This means rl.cur
// that is a nonzero multiple of width always maps to (row=cur/width, col=0),
// making the rowsBack formula unambiguous: rowsBack = cur / width.
type Readline struct {
	r       io.Reader
	w       io.Writer
	history []string
	histPos int
	width   int // terminal width; 0 = single-line mode (no wrap handling)
	cur     int // cursor column offset from start of prompt output
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
					rl.w.Write([]byte("\x1b[C"))
				}
			case 'D': // Left
				if pos > 0 {
					pos--
					rl.cur--
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
// It handles multi-row lines by tracking rl.cur (cursor column from prompt start).
func (rl *Readline) redraw(prompt string, promptLen int, buf []rune, pos int) {
	if rl.width > 0 && rl.cur >= rl.width {
		// Cursor is on a row below the prompt row; move back up.
		// Because wrapIfNeeded ensures rl.cur is a multiple of width only when the
		// cursor is physically at col 0 of the next row (not in deferred-wrap),
		// the formula rl.cur/width gives the correct row offset.
		rowsBack := rl.cur / rl.width
		fmt.Fprintf(rl.w, "\x1b[%dA", rowsBack)
		rl.w.Write([]byte("\r\x1b[J")) // CR + erase to end of screen
	} else {
		rl.w.Write([]byte("\r\x1b[K")) // CR + erase to end of line
	}

	rl.w.Write([]byte(prompt))
	rl.w.Write([]byte(string(buf)))
	rl.cur = promptLen + len(buf)

	if pos < len(buf) {
		moveBack := len(buf) - pos
		fmt.Fprintf(rl.w, "\x1b[%dD", moveBack)
		rl.cur -= moveBack
	} else {
		// Cursor is at end of buffer; force past deferred-wrap if at a row boundary.
		rl.wrapIfNeeded()
	}
}

// wrapIfNeeded emits \r\n when the cursor is exactly at a terminal column
// boundary (deferred-wrap state). This moves the cursor to col 0 of the next
// row so rl.cur unambiguously maps to that physical position.
func (rl *Readline) wrapIfNeeded() {
	if rl.width > 0 && rl.cur > 0 && rl.cur%rl.width == 0 {
		rl.w.Write([]byte("\r\n"))
	}
}
