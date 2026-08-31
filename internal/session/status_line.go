package session

import (
	"strconv"

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
