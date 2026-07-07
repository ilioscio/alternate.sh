package shell

import (
	"fmt"
	"io"
	"strings"
)

// Readline provides basic line editing for raw terminal sessions.
// It supports: printable ASCII input, backspace, left/right arrows,
// home/end, Ctrl+A/E/K/U, up/down history, Ctrl+C, Ctrl+D.
//
// Multi-row model (when widthFn reports a positive width):
//
// The width is queried live on every operation, so terminal resizes between
// keystrokes are picked up — a stale width is what makes wrapped-line editing
// destroy screen content.
//
// All wraps are committed explicitly: whenever rendered text fills a row
// exactly, the readline itself emits \r\n rather than relying on the
// terminal's auto-wrap. From a terminal's deferred-wrap state this commits
// the pending wrap (no blank line); on a terminal wider than the width we
// were told, it forces the wrap at our width. Either way the physical row
// layout always matches the tracked model, so cursor-up movements during
// redraw are exact and can never overshoot into scrollback content above
// the prompt.
//
// Tracked state: curRow/curCol are the cursor position relative to the start
// of the prompt (row 0 = prompt row); endRow is the last row occupied by
// rendered text. Repositioning uses relative vertical moves plus absolute
// column addressing (CSI G), which works across wrapped rows.
type Readline struct {
	r       io.Reader
	w       io.Writer
	history []string
	histPos int

	// widthFn returns the current terminal width in columns. nil (or a
	// non-positive result) disables multi-row handling; the readline then
	// falls back to single-row redraws that never move the cursor up.
	widthFn func() int

	curRow int // cursor row, relative to prompt row
	curCol int // cursor column, 0-based
	endRow int // last row occupied by prompt+buffer text
}

func NewReadline(r io.Reader, w io.Writer) *Readline {
	return &Readline{r: r, w: w}
}

func (rl *Readline) width() int {
	if rl.widthFn == nil {
		return 0
	}
	if w := rl.widthFn(); w > 0 {
		return w
	}
	return 0
}

// ReadLine displays prompt and reads a line of input.
// Returns io.EOF on Ctrl+D with an empty buffer (signals logout).
// Returns ("", nil) on Ctrl+C (clear line, try again).
func (rl *Readline) ReadLine(prompt string) (string, error) {
	promptRunes := []rune(prompt)
	var buf []rune
	pos := 0
	rl.histPos = len(rl.history)
	rl.curRow, rl.curCol, rl.endRow = 0, 0, 0
	rl.render(promptRunes, buf, pos)

	b := make([]byte, 1)
	for {
		if _, err := rl.r.Read(b); err != nil {
			return "", err
		}

		switch b[0] {
		case '\r', '\n':
			rl.moveTo(promptRunes, len(buf))
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
				rl.render(promptRunes, buf, pos)
			}

		case 0x03: // Ctrl+C
			rl.moveTo(promptRunes, len(buf))
			rl.w.Write([]byte("^C\r\n"))
			return "", nil

		case 0x04: // Ctrl+D
			if len(buf) == 0 {
				rl.w.Write([]byte("\r\n"))
				return "", io.EOF
			}

		case 0x01: // Ctrl+A — beginning of line
			pos = 0
			rl.moveTo(promptRunes, pos)

		case 0x05: // Ctrl+E — end of line
			pos = len(buf)
			rl.moveTo(promptRunes, pos)

		case 0x0b: // Ctrl+K — kill to end
			buf = buf[:pos]
			rl.render(promptRunes, buf, pos)

		case 0x15: // Ctrl+U — kill to beginning
			buf = buf[pos:]
			pos = 0
			rl.render(promptRunes, buf, pos)

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
					rl.render(promptRunes, buf, pos)
				}
			case 'B': // Down — history next
				if rl.histPos < len(rl.history)-1 {
					rl.histPos++
					buf = []rune(rl.history[rl.histPos])
				} else {
					rl.histPos = len(rl.history)
					buf = nil
				}
				pos = len(buf)
				rl.render(promptRunes, buf, pos)
			case 'C': // Right
				if pos < len(buf) {
					pos++
					rl.moveTo(promptRunes, pos)
				}
			case 'D': // Left
				if pos > 0 {
					pos--
					rl.moveTo(promptRunes, pos)
				}
			case 'H': // Home
				pos = 0
				rl.moveTo(promptRunes, pos)
			case 'F': // End
				pos = len(buf)
				rl.moveTo(promptRunes, pos)
			}

		default:
			if b[0] >= 0x20 && b[0] < 0x7f {
				r := rune(b[0])
				buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
				pos++
				if pos == len(buf) {
					rl.echo(b[0])
				} else {
					rl.render(promptRunes, buf, pos)
				}
			}
		}
	}
}

// echo appends one character at the end of the line, committing the wrap
// explicitly when the last column fills so the tracked position stays exact.
func (rl *Readline) echo(c byte) {
	rl.w.Write([]byte{c})
	rl.curCol++
	if w := rl.width(); w > 0 && rl.curCol >= w {
		rl.w.Write([]byte("\r\n"))
		rl.curRow++
		rl.curCol = 0
	}
	if rl.curRow > rl.endRow {
		rl.endRow = rl.curRow
	}
}

// render redraws the whole line (prompt + buffer) starting from the prompt
// row, erases any leftover rows below, and leaves the cursor at pos.
func (rl *Readline) render(prompt, buf []rune, pos int) {
	w := rl.width()
	var sb strings.Builder

	// Move to the prompt row. curRow counts rows we created ourselves via
	// explicit \r\n, so this can never overshoot above the prompt.
	if rl.curRow > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", rl.curRow)
	}
	sb.WriteString("\r")
	if rl.endRow > 0 {
		sb.WriteString("\x1b[J") // erase this row and all rows below
	} else {
		sb.WriteString("\x1b[K") // single row: erase to end of line
	}

	line := append(append([]rune{}, prompt...), buf...)
	if w > 0 {
		for i := 0; i < len(line); i += w {
			end := i + w
			if end > len(line) {
				end = len(line)
			}
			sb.WriteString(string(line[i:end]))
			if end-i == w {
				sb.WriteString("\r\n") // commit the wrap at our width
			}
		}
		rl.endRow = len(line) / w
		rl.curRow = rl.endRow
		rl.curCol = len(line) % w
	} else {
		sb.WriteString(string(line))
		rl.endRow, rl.curRow = 0, 0
		rl.curCol = len(line)
	}

	rl.w.Write([]byte(sb.String()))
	rl.moveTo(prompt, pos)
}

// moveTo positions the cursor at buffer index pos without redrawing text,
// using relative vertical movement and absolute column addressing.
func (rl *Readline) moveTo(prompt []rune, pos int) {
	off := len(prompt) + pos
	w := rl.width()
	if w <= 0 {
		fmt.Fprintf(rl.w, "\x1b[%dG", off+1)
		rl.curCol = off
		return
	}

	tRow, tCol := off/w, off%w
	var sb strings.Builder
	if d := tRow - rl.curRow; d > 0 {
		fmt.Fprintf(&sb, "\x1b[%dB", d)
	} else if d < 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", -d)
	}
	fmt.Fprintf(&sb, "\x1b[%dG", tCol+1)
	rl.w.Write([]byte(sb.String()))
	rl.curRow, rl.curCol = tRow, tCol
}
