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
// multiple terminal rows by tracking the cursor's absolute column
// offset from the start of the prompt.
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
					rl.w.Write([]byte{b[0]}) // simple echo
					rl.cur++
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
	if rl.width > 0 && rl.cur > rl.width {
		// Cursor has wrapped onto a row below the prompt row; move back up.
		// Use (cur-1)/width so that cur==width (deferred-wrap, still on row 0)
		// does not incorrectly add an extra row.
		rowsBack := (rl.cur - 1) / rl.width
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
	}
}
