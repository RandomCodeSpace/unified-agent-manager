package session

import (
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// paintStatus renders a UAM notice for a viewer's terminal.
//
// With a known geometry it draws on the last row and puts the cursor back
// where the agent left it, so a notice never scrolls the screen the agent is
// drawing on — the previous form appended two newlines, which pushed the whole
// alternate screen up with nothing to repaint it. The agent's next paint of
// that row simply overwrites the notice, which is the right lifetime for it.
//
// Without a geometry (a pipe, or a client that never reported a size) it falls
// back to the plain form, since cursor addressing would be guesswork.
func paintStatus(cols, rows int, message string) string {
	text := "[uam: " + message + "]"
	if cols <= 0 || rows <= 0 {
		return "\r\n" + text + "\r\n"
	}
	// A notice longer than the terminal is painted across as many bottom rows
	// as it needs. Cutting it instead would lose content — `prefix i` alone
	// runs past 80 columns — and letting it wrap would scroll, which is the
	// whole thing this avoids.
	lines := wrapToWidth(text, cols, max(1, rows-1))
	out := "\x1b7" // save cursor and pen
	top := rows - len(lines) + 1
	for index, line := range lines {
		out += "\x1b[" + strconv.Itoa(top+index) + ";1H" + // bottom rows
			"\x1b[0m" + // the notice is uam's, not the agent's colours
			"\x1b[2K" + // clear the row so a shorter notice cannot trail
			line
	}
	return out + "\x1b8" // restore cursor and pen
}

// clearPaintedStatus removes a notice drawn by paintStatus without moving the
// provider's cursor. Unknown geometry is never painted by the startup spinner.
func clearPaintedStatus(cols, rows int, message string) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	lines := wrapToWidth("[uam: "+message+"]", cols, max(1, rows-1))
	out := "\x1b7"
	top := rows - len(lines) + 1
	for index := range lines {
		out += "\x1b[" + strconv.Itoa(top+index) + ";1H\x1b[2K"
	}
	return out + "\x1b8"
}

// wrapToWidth splits text into at most maxLines runs of at most cols cells.
// Anything past the last line is dropped: at that point the notice is longer
// than the terminal is tall.
// pinScrollRegion confines scrolling to the provider's viewport so a linefeed
// on its last row can never push the status bar out of the terminal.
func pinScrollRegion(viewportRows int) string {
	if viewportRows <= 0 {
		return ""
	}
	return "\x1b[1;" + strconv.Itoa(viewportRows) + "r"
}

// paintStatusBar draws the persistent attach status bar on the terminal's
// last row, which the provider never owns: the host sizes the PTY to
// viewportRows, one short of the terminal. The bar is reverse video across the
// full width, truncated to one row. When pin is set the scroll region is
// re-established first (initial paint, resize, or after a provider reset that
// dropped it); repaints after a plain clear-screen leave the provider's own
// region alone.
func paintStatusBar(cols, rows, viewportRows int, text string, pin bool) string {
	if cols <= 0 || rows <= 0 || viewportRows <= 0 || viewportRows >= rows {
		return ""
	}
	line := wrapToWidth(text, cols, 1)[0]
	width := 0
	for _, r := range line {
		width += runewidth.RuneWidth(r)
	}
	if width < cols {
		line += strings.Repeat(" ", cols-width)
	}
	out := "\x1b7" // save cursor and pen
	if pin {
		out += pinScrollRegion(viewportRows)
	}
	out += "\x1b[" + strconv.Itoa(rows) + ";1H" + "\x1b[0m\x1b[7m" + line + "\x1b[0m"
	return out + "\x1b8" // restore cursor and pen
}

func wrapToWidth(text string, cols, maxLines int) []string {
	lines := make([]string, 0, 1)
	line, width := "", 0
	for _, r := range text {
		runeWidth := runewidth.RuneWidth(r)
		if width+runeWidth > cols {
			lines = append(lines, line)
			if len(lines) == maxLines {
				return lines
			}
			line, width = "", 0
		}
		line += string(r)
		width += runeWidth
	}
	return append(lines, line)
}
