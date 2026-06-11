// Package vterm is a minimal in-process VT100/xterm screen emulator. It
// replaces tmux's pane rendering for the native session backend: the session
// host feeds raw PTY output into a Terminal, and Capture returns the rendered
// plain-text tail the way `tmux capture-pane -p -J` used to. It models only
// what peek/capture needs — a character grid, scrollback history, cursor
// motion, erase/insert/delete, scroll regions, and the alternate screen.
// Colors and attributes (SGR) are tracked per cell so Redraw can repaint a
// re-attaching client faithfully; Capture output stays plain text by
// contract, exactly like the old capture-pane path.
package vterm

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Terminal is a virtual terminal. It is not goroutine-safe; the session host
// serializes access behind its own lock.
type Terminal struct {
	cols, rows int

	main *screen
	alt  *screen
	// onAlt reports whether the alternate screen (DEC 1049/47/1047) is active.
	// Full-screen agent TUIs run on the alt screen; line-oriented output and
	// scrollback history accumulate on the main screen only.
	onAlt bool

	// history holds lines scrolled off the top of the main screen, oldest
	// first, capped at maxHistory.
	history    []bufLine
	maxHistory int

	// Parser state. Escape sequences and UTF-8 runes can split across Write
	// calls, so both the sequence buffer and a partial-rune buffer persist.
	state   parseState
	seq     []byte
	partial []byte

	// cur is the current SGR state; printed cells and BCE fills carry it.
	cur attr
}

// bufLine is one captured line: its text and whether it is the soft-wrap
// continuation of the previous line (joined by Capture, like capture-pane -J).
type bufLine struct {
	text    string
	wrapped bool
}

type parseState int

const (
	stGround  parseState = iota
	stEsc                // after ESC
	stCSI                // after ESC [
	stOSC                // after ESC ] — consumed until BEL or ST
	stDCS                // after ESC P / X / ^ / _ — consumed until ST
	stCharset            // after ESC ( ) * + — one designator byte follows
)

// New returns a Terminal with the given grid size and scrollback capacity.
func New(cols, rows, maxHistory int) *Terminal {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if maxHistory < 0 {
		maxHistory = 0
	}
	return &Terminal{
		cols:       cols,
		rows:       rows,
		main:       newScreen(cols, rows),
		alt:        newScreen(cols, rows),
		maxHistory: maxHistory,
	}
}

func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

func (t *Terminal) active() *screen {
	if t.onAlt {
		return t.alt
	}
	return t.main
}

// Write feeds raw PTY output into the emulator. It never fails; implementing
// io.Writer keeps the host's reader loop a plain io pipeline.
func (t *Terminal) Write(p []byte) (int, error) {
	data := p
	if len(t.partial) > 0 {
		data = append(t.partial, p...) //nolint:gocritic // intentional new slice
		t.partial = nil
	}
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if !utf8.FullRune(data) && len(data) < utf8.UTFMax {
				// Incomplete trailing sequence — keep it for the next Write.
				t.partial = append(t.partial, data...)
				break
			}
			// Genuinely invalid byte: drop it rather than corrupt the grid.
			data = data[1:]
			continue
		}
		t.step(r)
		data = data[size:]
	}
	return len(p), nil
}

func (t *Terminal) step(r rune) {
	switch t.state {
	case stGround:
		t.stepGround(r)
	case stEsc:
		t.stepEsc(r)
	case stCSI:
		t.stepCSI(r)
	case stOSC:
		t.stepOSC(r)
	case stDCS:
		t.stepDCS(r)
	case stCharset:
		t.state = stGround // discard the designator byte
	}
}

func (t *Terminal) stepGround(r rune) {
	s := t.active()
	switch r {
	case 0x1b:
		t.state = stEsc
	case '\r':
		s.x = 0
		s.pendingWrap = false
	case '\n', 0x0b, 0x0c:
		t.lineFeed(false)
	case '\b':
		if s.x > 0 {
			s.x--
		}
		s.pendingWrap = false
	case '\t':
		s.x = min((s.x/8+1)*8, t.cols-1)
		s.pendingWrap = false
	case 0x07, 0x00, 0x0e, 0x0f:
		// BEL and shift-in/out: ignored.
	default:
		if r >= 0x20 {
			t.print(r)
		}
	}
}

func (t *Terminal) stepEsc(r rune) {
	t.state = stGround
	s := t.active()
	switch r {
	case '[':
		t.state = stCSI
		t.seq = t.seq[:0]
	case ']':
		t.state = stOSC
		t.seq = t.seq[:0]
	case 'P', 'X', '^', '_':
		t.state = stDCS
	case '(', ')', '*', '+':
		t.state = stCharset
	case '7':
		s.savedX, s.savedY = s.x, s.y
	case '8':
		s.x, s.y = min(s.savedX, t.cols-1), min(s.savedY, t.rows-1)
		s.pendingWrap = false
	case 'D':
		t.lineFeed(false)
	case 'E':
		s.x = 0
		t.lineFeed(false)
	case 'M':
		t.reverseIndex()
	case 'c':
		t.reset()
	case '=', '>':
		// Keypad modes: ignored.
	}
}

func (t *Terminal) stepCSI(r rune) {
	// Parameter / intermediate bytes accumulate; a final byte 0x40–0x7e
	// dispatches.
	if r >= 0x40 && r <= 0x7e {
		t.state = stGround
		t.dispatchCSI(string(t.seq), byte(r))
		return
	}
	if r >= 0x20 && r <= 0x3f && len(t.seq) < 64 {
		t.seq = append(t.seq, byte(r))
		return
	}
	if r == 0x1b || r > 0x7e {
		// Malformed sequence; bail to ground (re-handle ESC).
		t.state = stGround
		if r == 0x1b {
			t.state = stEsc
		}
	}
}

func (t *Terminal) stepOSC(r rune) {
	if r == 0x07 {
		t.state = stGround
		return
	}
	if r == 0x1b {
		// Likely ST (ESC \). Consume the backslash via stEsc's default arm.
		t.state = stEsc
		return
	}
	// Title and clipboard payloads are irrelevant to capture; discard.
}

func (t *Terminal) stepDCS(r rune) {
	if r == 0x1b {
		t.state = stEsc
	}
}

func (t *Terminal) dispatchCSI(params string, final byte) {
	private := strings.HasPrefix(params, "?")
	params = strings.TrimLeft(params, "?<=>")
	// Strip intermediate bytes (e.g. the space in "CSI Ps SP q").
	if i := strings.IndexFunc(params, func(r rune) bool { return r < '0' || r > ';' }); i >= 0 {
		params = params[:i]
	}
	n := csiParams(params)
	arg := func(i, def int) int {
		if i < len(n) && n[i] > 0 {
			return n[i]
		}
		return def
	}
	s := t.active()
	switch final {
	case 'A':
		s.moveY(-arg(0, 1))
	case 'B', 'e':
		s.moveY(arg(0, 1))
	case 'C', 'a':
		s.moveX(arg(0, 1))
	case 'D':
		s.moveX(-arg(0, 1))
	case 'E':
		s.x = 0
		s.moveY(arg(0, 1))
	case 'F':
		s.x = 0
		s.moveY(-arg(0, 1))
	case 'G', '`':
		s.x = clamp(arg(0, 1)-1, 0, t.cols-1)
		s.pendingWrap = false
	case 'd':
		s.y = clamp(arg(0, 1)-1, 0, t.rows-1)
		s.pendingWrap = false
	case 'H', 'f':
		s.y = clamp(arg(0, 1)-1, 0, t.rows-1)
		s.x = clamp(arg(1, 1)-1, 0, t.cols-1)
		s.pendingWrap = false
	case 'J':
		t.eraseDisplay(arg(0, 0) /* default 0 even when params empty */)
	case 'K':
		s.eraseLine(argDefault(n, 0, 0), t.cols, t.bceFill())
	case 'L':
		s.insertLines(arg(0, 1), t.bceFill())
	case 'M':
		s.deleteLines(arg(0, 1), t.bceFill())
	case '@':
		s.insertChars(arg(0, 1), t.cols, t.bceFill())
	case 'P':
		s.deleteChars(arg(0, 1), t.cols, t.bceFill())
	case 'X':
		s.eraseChars(arg(0, 1), t.cols, t.bceFill())
	case 'S':
		for i := 0; i < arg(0, 1); i++ {
			t.scrollUp()
		}
	case 'T':
		for i := 0; i < arg(0, 1); i++ {
			t.scrollDownRegion()
		}
	case 'r':
		top := clamp(arg(0, 1)-1, 0, t.rows-1)
		bot := clamp(arg(1, t.rows)-1, 0, t.rows-1)
		if top < bot {
			s.top, s.bottom = top, bot
		} else {
			s.top, s.bottom = 0, t.rows-1
		}
		s.x, s.y = 0, s.top
		s.pendingWrap = false
	case 'h':
		if private {
			t.setPrivateMode(n, true)
		}
	case 'l':
		if private {
			t.setPrivateMode(n, false)
		}
	case 's':
		s.savedX, s.savedY = s.x, s.y
	case 'u':
		s.x, s.y = min(s.savedX, t.cols-1), min(s.savedY, t.rows-1)
		s.pendingWrap = false
	case 'm':
		if !private {
			t.applySGR(params)
		}
	case 'n', 'q', 't', 'g', 'c':
		// Reports, cursor style, window ops, tab clears: no grid effect.
	}
}

// bceFill is the cell erases reveal: blank, carrying the current background
// (xterm's back-color-erase), so colored bars and panels replay intact.
func (t *Terminal) bceFill() cell {
	return cell{a: attr{bg: t.cur.bg}}
}

// argDefault is arg() but distinguishing "no parameter" from explicit 0 is
// unnecessary for our erase handling; it simply mirrors arg with a 0 default.
func argDefault(n []int, i, def int) int {
	if i < len(n) {
		return n[i]
	}
	return def
}

func csiParams(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		v := 0
		for _, c := range p {
			if c < '0' || c > '9' || v > 1<<20 {
				break
			}
			v = v*10 + int(c-'0')
		}
		out = append(out, v)
	}
	return out
}

func (t *Terminal) setPrivateMode(params []int, on bool) {
	for _, p := range params {
		switch p {
		case 47, 1047, 1049:
			t.switchAlt(on)
		case 25, 2004, 1000, 1002, 1003, 1004, 1005, 1006, 7:
			// Cursor visibility, bracketed paste, mouse reporting, autowrap:
			// no effect on captured text.
		}
	}
}

func (t *Terminal) switchAlt(on bool) {
	if on == t.onAlt {
		return
	}
	if on {
		s := t.main
		s.savedX, s.savedY = s.x, s.y
		t.alt.clearAll(t.bceFill())
		t.alt.x, t.alt.y = 0, 0
		t.onAlt = true
		return
	}
	t.onAlt = false
	s := t.main
	s.x, s.y = min(s.savedX, t.cols-1), min(s.savedY, t.rows-1)
	s.pendingWrap = false
}

func (t *Terminal) print(r rune) {
	w := runewidth.RuneWidth(r)
	if w <= 0 {
		return // combining marks / zero-width: skip, capture is best-effort
	}
	s := t.active()
	if s.pendingWrap {
		s.x = 0
		t.lineFeed(true)
		s.pendingWrap = false
	}
	if s.x+w > t.cols {
		// A wide rune that does not fit hard-wraps early.
		s.x = 0
		t.lineFeed(true)
	}
	s.cells[s.y][s.x] = cell{r: r, a: t.cur}
	if w == 2 && s.x+1 < t.cols {
		s.cells[s.y][s.x+1] = cell{a: t.cur}
	}
	s.x += w
	if s.x >= t.cols {
		s.x = t.cols - 1
		s.pendingWrap = true
	}
}

// lineFeed moves the cursor down one row, scrolling at the bottom of the
// scroll region. wrapped marks the entered row as a soft-wrap continuation.
func (t *Terminal) lineFeed(wrapped bool) {
	s := t.active()
	if s.y == s.bottom {
		t.scrollUp()
		s.wrapped[s.y] = wrapped
		return
	}
	if s.y < t.rows-1 {
		s.y++
		s.wrapped[s.y] = wrapped
	}
}

// scrollUp shifts the scroll region up one row. On the main screen with a
// full-height region the departing top row enters scrollback history.
func (t *Terminal) scrollUp() {
	s := t.active()
	if !t.onAlt && s.top == 0 && s.bottom == t.rows-1 && t.maxHistory > 0 {
		t.pushHistory(bufLine{text: rowText(s.cells[0]), wrapped: s.wrapped[0]})
	}
	top, bot := s.top, s.bottom
	rec := s.cells[top]
	copy(s.cells[top:bot], s.cells[top+1:bot+1])
	copy(s.wrapped[top:bot], s.wrapped[top+1:bot+1])
	s.cells[bot] = rec
	clearRow(s.cells[bot], t.bceFill())
	s.wrapped[bot] = false
}

func (t *Terminal) scrollDownRegion() {
	s := t.active()
	top, bot := s.top, s.bottom
	rec := s.cells[bot]
	copy(s.cells[top+1:bot+1], s.cells[top:bot])
	copy(s.wrapped[top+1:bot+1], s.wrapped[top:bot])
	s.cells[top] = rec
	clearRow(s.cells[top], t.bceFill())
	s.wrapped[top] = false
}

func (t *Terminal) reverseIndex() {
	s := t.active()
	if s.y == s.top {
		t.scrollDownRegion()
		return
	}
	if s.y > 0 {
		s.y--
	}
}

func (t *Terminal) pushHistory(l bufLine) {
	t.history = append(t.history, l)
	if len(t.history) > t.maxHistory {
		// Trim in chunks so a busy session does not memmove on every line.
		drop := len(t.history) - t.maxHistory
		t.history = append(t.history[:0], t.history[drop:]...)
	}
}

func (t *Terminal) eraseDisplay(mode int) {
	s := t.active()
	fill := t.bceFill()
	switch mode {
	case 0:
		s.eraseLine(0, t.cols, fill)
		for y := s.y + 1; y < t.rows; y++ {
			clearRow(s.cells[y], fill)
			s.wrapped[y] = false
		}
	case 1:
		s.eraseLine(1, t.cols, fill)
		for y := 0; y < s.y; y++ {
			clearRow(s.cells[y], fill)
			s.wrapped[y] = false
		}
	case 2, 3:
		s.clearAll(fill)
	}
}

func (t *Terminal) reset() {
	t.onAlt = false
	t.main = newScreen(t.cols, t.rows)
	t.alt = newScreen(t.cols, t.rows)
	t.state = stGround
	t.cur = attr{}
}

// Resize changes the grid size, preserving as much content as fits. Scroll
// regions reset to full height (matching xterm). Rows dropped from the top of
// the main screen move into history.
func (t *Terminal) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols == t.cols && rows == t.rows {
		return
	}
	for _, s := range []*screen{t.main, t.alt} {
		s.resize(cols, rows, t.cols, t.rows, s == t.main && t.maxHistory > 0, t)
	}
	t.cols, t.rows = cols, rows
}

// Capture renders the last maxLines lines of the terminal as plain text:
// scrollback history plus the active screen, with soft-wrapped lines joined
// (the capture-pane -J contract) and trailing blank lines trimmed. The result
// ends with a newline when non-empty.
func (t *Terminal) Capture(maxLines int) string {
	if maxLines <= 0 {
		maxLines = 200
	}
	s := t.active()
	lines := make([]bufLine, 0, len(t.history)+t.rows)
	if !t.onAlt || t.maxHistory > 0 {
		lines = append(lines, t.history...)
	}
	last := t.rows - 1
	for ; last >= 0; last-- {
		if rowText(s.cells[last]) != "" {
			break
		}
	}
	for y := 0; y <= last; y++ {
		lines = append(lines, bufLine{text: rowText(s.cells[y]), wrapped: s.wrapped[y]})
	}
	joined := make([]string, 0, len(lines))
	for _, l := range lines {
		if l.wrapped && len(joined) > 0 {
			joined[len(joined)-1] += l.text
			continue
		}
		joined = append(joined, l.text)
	}
	if len(joined) > maxLines {
		joined = joined[len(joined)-maxLines:]
	}
	if len(joined) == 0 {
		return ""
	}
	return strings.Join(joined, "\n") + "\n"
}

// Redraw returns an ANSI byte sequence that repaints the current screen on a
// fresh terminal: reset attributes, clear, draw every row with the SGR state
// each cell was written with, and park the cursor. The session host sends it
// to a newly attached client so the user sees the live screen — colors
// included — immediately.
func (t *Terminal) Redraw() []byte {
	s := t.active()
	var b strings.Builder
	b.WriteString("\x1b[0m\x1b[2J\x1b[H")
	last := t.rows - 1
	for ; last >= 0; last-- {
		if !rowBlank(s.cells[last]) {
			break
		}
	}
	cur := attr{}
	for y := 0; y <= last; y++ {
		if y > 0 {
			b.WriteString("\r\n")
		}
		row := s.cells[y]
		end := len(row) - 1
		for ; end >= 0; end-- {
			if row[end].visible() {
				break
			}
		}
		for x := 0; x <= end; x++ {
			c := row[x]
			if c.r == 0 && x > 0 && runewidth.RuneWidth(row[x-1].r) == 2 {
				// Wide-rune continuation: no extra column.
				continue
			}
			if c.a != cur {
				b.WriteString(c.a.sgr())
				cur = c.a
			}
			r := c.r
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
	}
	if cur != (attr{}) {
		b.WriteString("\x1b[0m")
	}
	b.WriteString("\x1b[" + strconv.Itoa(s.y+1) + ";" + strconv.Itoa(s.x+1) + "H")
	return []byte(b.String())
}

// screen is one character grid (main or alternate).
type screen struct {
	cells   [][]cell
	wrapped []bool
	x, y    int
	// pendingWrap defers the wrap after writing the last column (DECAWM
	// semantics): the next printable wraps, but CR/cursor motion cancels it.
	pendingWrap    bool
	top, bottom    int
	savedX, savedY int
}

func newScreen(cols, rows int) *screen {
	s := &screen{cells: make([][]cell, rows), wrapped: make([]bool, rows), bottom: rows - 1}
	for i := range s.cells {
		s.cells[i] = make([]cell, cols)
	}
	return s
}

func (s *screen) clearAll(fill cell) {
	for y := range s.cells {
		clearRow(s.cells[y], fill)
		s.wrapped[y] = false
	}
}

func (s *screen) moveX(d int) {
	s.x = clamp(s.x+d, 0, len(s.cells[0])-1)
	s.pendingWrap = false
}

func (s *screen) moveY(d int) {
	s.y = clamp(s.y+d, 0, len(s.cells)-1)
	s.pendingWrap = false
}

func (s *screen) eraseLine(mode, cols int, fill cell) {
	row := s.cells[s.y]
	switch mode {
	case 0:
		for x := s.x; x < cols; x++ {
			row[x] = fill
		}
	case 1:
		for x := 0; x <= s.x && x < cols; x++ {
			row[x] = fill
		}
	case 2:
		clearRow(row, fill)
	}
}

func (s *screen) insertLines(n int, fill cell) {
	if s.y < s.top || s.y > s.bottom {
		return
	}
	for i := 0; i < n; i++ {
		rec := s.cells[s.bottom]
		copy(s.cells[s.y+1:s.bottom+1], s.cells[s.y:s.bottom])
		copy(s.wrapped[s.y+1:s.bottom+1], s.wrapped[s.y:s.bottom])
		s.cells[s.y] = rec
		clearRow(s.cells[s.y], fill)
		s.wrapped[s.y] = false
	}
}

func (s *screen) deleteLines(n int, fill cell) {
	if s.y < s.top || s.y > s.bottom {
		return
	}
	for i := 0; i < n; i++ {
		rec := s.cells[s.y]
		copy(s.cells[s.y:s.bottom], s.cells[s.y+1:s.bottom+1])
		copy(s.wrapped[s.y:s.bottom], s.wrapped[s.y+1:s.bottom+1])
		s.cells[s.bottom] = rec
		clearRow(s.cells[s.bottom], fill)
		s.wrapped[s.bottom] = false
	}
}

func (s *screen) insertChars(n, cols int, fill cell) {
	row := s.cells[s.y]
	for i := 0; i < n; i++ {
		copy(row[s.x+1:cols], row[s.x:cols-1])
		row[s.x] = fill
	}
}

func (s *screen) deleteChars(n, cols int, fill cell) {
	row := s.cells[s.y]
	for i := 0; i < n; i++ {
		copy(row[s.x:cols-1], row[s.x+1:cols])
		row[cols-1] = fill
	}
}

func (s *screen) eraseChars(n, cols int, fill cell) {
	row := s.cells[s.y]
	for x := s.x; x < s.x+n && x < cols; x++ {
		row[x] = fill
	}
}

func (s *screen) resize(cols, rows, oldCols, oldRows int, pushTop bool, t *Terminal) {
	// Shrinking rows drops from the top; the dropped main-screen rows are
	// preserved as history so capture never loses them.
	if rows < oldRows {
		drop := oldRows - rows
		// Keep the cursor visible: drop blank rows from the bottom first.
		// rowBlank (not rowText) so a BCE-colored blank row survives the
		// shrink the same way Redraw would paint it.
		for drop > 0 && oldRows-1 > s.y && rowBlank(s.cells[oldRows-1]) {
			s.cells = s.cells[:oldRows-1]
			s.wrapped = s.wrapped[:oldRows-1]
			oldRows--
			drop--
		}
		if drop > 0 {
			if pushTop {
				for i := 0; i < drop; i++ {
					t.pushHistory(bufLine{text: rowText(s.cells[i]), wrapped: s.wrapped[i]})
				}
			}
			s.cells = s.cells[drop:]
			s.wrapped = s.wrapped[drop:]
			s.y = max(0, s.y-drop)
		}
	}
	for len(s.cells) < rows {
		s.cells = append(s.cells, make([]cell, cols))
		s.wrapped = append(s.wrapped, false)
	}
	for i := range s.cells {
		row := s.cells[i]
		if len(row) < cols {
			grown := make([]cell, cols)
			copy(grown, row)
			s.cells[i] = grown
		} else if len(row) > cols {
			s.cells[i] = row[:cols]
		}
	}
	s.top, s.bottom = 0, rows-1
	s.x = clamp(s.x, 0, cols-1)
	s.y = clamp(s.y, 0, rows-1)
	s.pendingWrap = false
	s.savedX = clamp(s.savedX, 0, cols-1)
	s.savedY = clamp(s.savedY, 0, rows-1)
}

func clearRow(row []cell, fill cell) {
	for i := range row {
		row[i] = fill
	}
}

// rowText renders a grid row as plain text: zero cells inside the line become
// spaces, trailing blanks are trimmed, attributes are ignored (the Capture
// contract).
func rowText(row []cell) string {
	last := len(row) - 1
	for ; last >= 0; last-- {
		if row[last].r != 0 && row[last].r != ' ' {
			break
		}
	}
	if last < 0 {
		return ""
	}
	var b strings.Builder
	for x := 0; x <= last; x++ {
		r := row[x].r
		if r == 0 {
			// A zero cell is either an erased cell or a wide-rune
			// continuation; the continuation contributes no extra column.
			if x > 0 && runewidth.RuneWidth(row[x-1].r) == 2 {
				continue
			}
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
