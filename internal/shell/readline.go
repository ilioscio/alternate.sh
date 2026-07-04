package shell

import (
	"fmt"
	"io"
)

// Readline provides basic line editing for raw terminal sessions.
// It supports: printable ASCII input, backspace, left/right arrows,
// home/end, Ctrl+A/E/K/U, up/down history, Ctrl+C, Ctrl+D.
type Readline struct {
	r       io.Reader
	w       io.Writer
	history []string
	histPos int
}

func NewReadline(r io.Reader, w io.Writer) *Readline {
	return &Readline{r: r, w: w}
}

// ReadLine displays prompt and reads a line of input.
// Returns io.EOF on Ctrl+D with an empty buffer (signals logout).
// Returns ("", nil) on Ctrl+C (clear line, try again).
func (rl *Readline) ReadLine(prompt string) (string, error) {
	rl.w.Write([]byte(prompt))

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
				rl.redraw(prompt, buf, pos)
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
			rl.moveCursor(prompt, buf, pos)

		case 0x05: // Ctrl+E — end of line
			pos = len(buf)
			rl.moveCursor(prompt, buf, pos)

		case 0x0b: // Ctrl+K — kill to end
			buf = buf[:pos]
			rl.redraw(prompt, buf, pos)

		case 0x15: // Ctrl+U — kill to beginning
			buf = buf[pos:]
			pos = 0
			rl.redraw(prompt, buf, pos)

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
					rl.redraw(prompt, buf, pos)
				}
			case 'B': // Down — history next
				if rl.histPos < len(rl.history)-1 {
					rl.histPos++
					buf = []rune(rl.history[rl.histPos])
					pos = len(buf)
					rl.redraw(prompt, buf, pos)
				} else {
					rl.histPos = len(rl.history)
					buf = nil
					pos = 0
					rl.redraw(prompt, buf, pos)
				}
			case 'C': // Right
				if pos < len(buf) {
					pos++
					rl.w.Write([]byte("\x1b[C"))
				}
			case 'D': // Left
				if pos > 0 {
					pos--
					rl.w.Write([]byte("\x1b[D"))
				}
			case 'H': // Home
				pos = 0
				rl.moveCursor(prompt, buf, pos)
			case 'F': // End
				pos = len(buf)
				rl.moveCursor(prompt, buf, pos)
			}

		default:
			if b[0] >= 0x20 && b[0] < 0x7f {
				r := rune(b[0])
				// Insert at pos
				buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
				pos++
				if pos == len(buf) {
					rl.w.Write([]byte{b[0]}) // simple echo
				} else {
					rl.redraw(prompt, buf, pos)
				}
			}
		}
	}
}

// redraw redraws the entire line and repositions the cursor.
func (rl *Readline) redraw(prompt string, buf []rune, pos int) {
	// CR, erase to end of line, reprint prompt+buffer, reposition cursor.
	rl.w.Write([]byte("\r\x1b[K"))
	rl.w.Write([]byte(prompt))
	rl.w.Write([]byte(string(buf)))
	if pos < len(buf) {
		fmt.Fprintf(rl.w, "\x1b[%dD", len(buf)-pos)
	}
}

// moveCursor moves the cursor to pos without redrawing buffer content.
func (rl *Readline) moveCursor(prompt string, buf []rune, pos int) {
	// Simplest: full redraw.
	rl.redraw(prompt, buf, pos)
}
