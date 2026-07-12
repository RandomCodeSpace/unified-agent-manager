package vterm

import (
	"strings"
	"testing"
)

func feed(t *testing.T, term *Terminal, s string) {
	t.Helper()
	if _, err := term.Write([]byte(s)); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestPlainLinesCapture(t *testing.T) {
	term := New(80, 24, 100)
	feed(t, term, "hello\r\nworld\r\n")
	if got, want := term.Capture(200), "hello\nworld\n"; got != want {
		t.Fatalf("Capture = %q, want %q", got, want)
	}
}

func TestCaptureTailLimitsLines(t *testing.T) {
	term := New(80, 4, 100)
	feed(t, term, "a\r\nb\r\nc\r\nd\r\ne\r\n")
	if got, want := term.Capture(2), "d\ne\n"; got != want {
		t.Fatalf("Capture(2) = %q, want %q", got, want)
	}
}

func TestScrolledLinesEnterHistory(t *testing.T) {
	term := New(80, 3, 100)
	feed(t, term, "one\r\ntwo\r\nthree\r\nfour\r\nfive")
	got := term.Capture(200)
	want := "one\ntwo\nthree\nfour\nfive\n"
	if got != want {
		t.Fatalf("Capture = %q, want %q", got, want)
	}
}

func TestHistoryIsCapped(t *testing.T) {
	term := New(80, 2, 3)
	for i := 0; i < 20; i++ {
		feed(t, term, "line\r\n")
	}
	lines := strings.Count(term.Capture(0), "\n")
	// 3 history lines + at most 2 screen rows.
	if lines > 5 {
		t.Fatalf("history not capped: %d lines", lines)
	}
}

func TestCarriageReturnOverwrites(t *testing.T) {
	term := New(80, 24, 0)
	feed(t, term, "Progress 10%\rProgress 99%")
	if got := term.Capture(200); got != "Progress 99%\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestSGRAndOSCAreStripped(t *testing.T) {
	term := New(80, 24, 0)
	feed(t, term, "\x1b]0;window title\x07\x1b[1;32mgreen\x1b[0m plain")
	if got := term.Capture(200); got != "green plain\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestCursorAddressingAndEraseLine(t *testing.T) {
	term := New(20, 5, 0)
	feed(t, term, "aaaaa\r\nbbbbb\r\nccccc")
	// Move to row 2 col 3, erase to end of line, write.
	feed(t, term, "\x1b[2;3HXX\x1b[K")
	got := term.Capture(200)
	want := "aaaaa\nbbXX\nccccc\n"
	if got != want {
		t.Fatalf("Capture = %q, want %q", got, want)
	}
}

func TestEraseDisplayClears(t *testing.T) {
	term := New(20, 5, 0)
	feed(t, term, "junk junk junk")
	feed(t, term, "\x1b[2J\x1b[Hfresh")
	if got := term.Capture(200); got != "fresh\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestSoftWrapJoinsOnCapture(t *testing.T) {
	term := New(10, 4, 10)
	feed(t, term, strings.Repeat("x", 25))
	// 25 x's wrap onto three rows; capture joins them back into one line
	// (the capture-pane -J contract).
	if got := term.Capture(200); got != strings.Repeat("x", 25)+"\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestAlternateScreenSwitchAndRestore(t *testing.T) {
	term := New(40, 5, 50)
	feed(t, term, "main screen line\r\n")
	feed(t, term, "\x1b[?1049h") // enter alt
	feed(t, term, "TUI CONTENT")
	if got := term.Capture(200); !strings.Contains(got, "TUI CONTENT") || strings.Contains(got, "main screen") {
		t.Fatalf("alt capture = %q", got)
	}
	feed(t, term, "\x1b[?1049l") // leave alt
	if got := term.Capture(200); !strings.Contains(got, "main screen line") || strings.Contains(got, "TUI CONTENT") {
		t.Fatalf("main capture after alt = %q", got)
	}
}

func TestScrollRegionScrollsOnlyRegion(t *testing.T) {
	term := New(20, 4, 10)
	feed(t, term, "top\r\nA\r\nB\r\nbottom")
	// Region rows 2-3; cursor to region bottom; LF scrolls only the region.
	feed(t, term, "\x1b[2;3r\x1b[3;1H\nC")
	got := term.Capture(200)
	if !strings.Contains(got, "top") || !strings.Contains(got, "bottom") {
		t.Fatalf("rows outside region must not scroll: %q", got)
	}
	if !strings.Contains(got, "B") || !strings.Contains(got, "C") || strings.Contains(got, "A\n") {
		t.Fatalf("region should have scrolled A out: %q", got)
	}
}

func TestWideRunes(t *testing.T) {
	term := New(10, 3, 0)
	feed(t, term, "日本語")
	if got := term.Capture(200); got != "日本語\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestUTF8SplitAcrossWrites(t *testing.T) {
	term := New(10, 3, 0)
	b := []byte("héllo")
	for _, c := range b {
		feed(t, term, string([]byte{c}))
	}
	if got := term.Capture(200); got != "héllo\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestEscapeSequenceSplitAcrossWrites(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "red:\x1b[3")
	feed(t, term, "1mX\x1b[0m")
	if got := term.Capture(200); got != "red:X\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestInsertDeleteLinesAndChars(t *testing.T) {
	term := New(20, 4, 0)
	feed(t, term, "one\r\ntwo\r\nthree")
	feed(t, term, "\x1b[1;1H\x1b[1L") // insert a line at top
	feed(t, term, "zero")
	if got := term.Capture(200); got != "zero\none\ntwo\nthree\n" {
		t.Fatalf("after IL: %q", got)
	}
	feed(t, term, "\x1b[1;1H\x1b[1M") // delete top line again
	if got := term.Capture(200); got != "one\ntwo\nthree\n" {
		t.Fatalf("after DL: %q", got)
	}
	feed(t, term, "\x1b[1;1H\x1b[2P") // delete "on" from "one"
	if got := term.Capture(200); got != "e\ntwo\nthree\n" {
		t.Fatalf("after DCH: %q", got)
	}
}

func TestResizePreservesContent(t *testing.T) {
	term := New(40, 10, 50)
	feed(t, term, "keep me\r\nand me")
	term.Resize(20, 5)
	if got := term.Capture(200); got != "keep me\nand me\n" {
		t.Fatalf("after shrink: %q", got)
	}
	term.Resize(80, 30)
	feed(t, term, " still works")
	if got := term.Capture(200); got != "keep me\nand me still works\n" {
		t.Fatalf("after grow: %q", got)
	}
}

func TestRowsDroppedByResizeEnterHistory(t *testing.T) {
	term := New(20, 5, 50)
	feed(t, term, "a\r\nb\r\nc\r\nd\r\ne")
	term.Resize(20, 2)
	got := term.Capture(200)
	if got != "a\nb\nc\nd\ne\n" {
		t.Fatalf("resize must not lose lines: %q", got)
	}
}

func TestRedrawPaintsScreenAndCursor(t *testing.T) {
	term := New(20, 5, 0)
	feed(t, term, "row1\r\nrow2")
	out := string(term.Redraw())
	if !strings.HasPrefix(out, "\x1b[0m\x1b[2J\x1b[H") {
		t.Fatalf("redraw must reset attributes and clear first: %q", out)
	}
	if !strings.Contains(out, "row1\r\nrow2") {
		t.Fatalf("redraw missing content: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[2;5H") {
		t.Fatalf("redraw must park cursor after row2: %q", out)
	}
}

func TestCaptureDefaultsAndEmpty(t *testing.T) {
	term := New(80, 24, 10)
	if got := term.Capture(0); got != "" {
		t.Fatalf("empty terminal capture = %q", got)
	}
}

func TestBackspaceAndTab(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "ab\bC\tD")
	// "ab", BS over b, C, tab to col 8, D.
	if got := term.Capture(200); got != "aC      D\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestNewClampsDegenerateSizes(t *testing.T) {
	term := New(0, -1, -5)
	cols, rows := term.Size()
	if cols != 1 || rows != 1 {
		t.Fatalf("Size = %d,%d, want clamped 1,1", cols, rows)
	}
	feed(t, term, "x")
	if got := term.Capture(10); got != "x\n" {
		t.Fatalf("Capture = %q", got)
	}
}

func TestCursorSaveRestoreAndIndexEscapes(t *testing.T) {
	term := New(20, 5, 0)
	feed(t, term, "abc\x1b7XY\x1b8Z") // save at col 4, write XY, restore, Z overwrites X
	if got := term.Capture(10); got != "abcZY\n" {
		t.Fatalf("ESC 7/8: %q", got)
	}
	term = New(20, 5, 0)
	feed(t, term, "top\x1bEnext")
	if got := term.Capture(10); got != "top\nnext\n" {
		t.Fatalf("ESC E: %q", got)
	}
	// ESC M at the top scrolls the region down (reverse index).
	term = New(20, 3, 0)
	feed(t, term, "one\r\ntwo\x1b[1;1H\x1bMzero")
	if got := term.Capture(10); got != "zero\none\ntwo\n" {
		t.Fatalf("ESC M: %q", got)
	}
	// CSI S / T scroll the screen up and down.
	term = New(20, 3, 10)
	feed(t, term, "a\r\nb\r\nc\x1b[1S\x1b[1T")
	if got := term.Capture(10); !strings.Contains(got, "b\nc\n") {
		t.Fatalf("CSI S/T: %q", got)
	}
}

func TestResetClearsEverything(t *testing.T) {
	term := New(20, 3, 10)
	feed(t, term, "junk\r\nmore\x1bcfresh")
	if got := term.Capture(10); !strings.HasSuffix(got, "fresh\n") {
		t.Fatalf("ESC c: %q", got)
	}
}

func TestRelativeCursorMoves(t *testing.T) {
	term := New(20, 5, 0)
	// Write, move up/left/down/right and overwrite deterministically.
	feed(t, term, "aaaa\r\nbbbb\x1b[1A\x1b[2DX\x1b[1B\x1b[1CY")
	got := term.Capture(10)
	if !strings.Contains(got, "aX") || !strings.Contains(got, "Y") {
		t.Fatalf("relative moves: %q", got)
	}
	// CSI E/F: next/previous line to column 1.
	term = New(20, 5, 0)
	feed(t, term, "one\x1b[1Etwo\x1b[1Fzero")
	if got := term.Capture(10); got != "zero\ntwo\n" {
		t.Fatalf("CSI E/F: %q", got)
	}
}

func TestInsertCharsAndEraseVariants(t *testing.T) {
	term := New(20, 4, 0)
	feed(t, term, "abcdef\x1b[1;3H\x1b[2@") // insert 2 blanks at col 3
	if got := term.Capture(10); got != "ab  cdef\n" {
		t.Fatalf("ICH: %q", got)
	}
	feed(t, term, "\x1b[1;3H\x1b[2X") // erase 2 chars in place
	if got := term.Capture(10); got != "ab  cdef\n" {
		t.Fatalf("ECH: %q", got)
	}
	// EL mode 1: erase from start of line through cursor.
	term = New(20, 4, 0)
	feed(t, term, "abcdef\x1b[1;3H\x1b[1K")
	if got := term.Capture(10); got != "   def\n" {
		t.Fatalf("EL1: %q", got)
	}
	// ED mode 1: erase from start of display through cursor.
	term = New(20, 4, 0)
	feed(t, term, "top\r\nmid\r\nbot\x1b[2;2H\x1b[1J")
	if got := term.Capture(10); !strings.Contains(got, "bot") || strings.Contains(got, "top") {
		t.Fatalf("ED1: %q", got)
	}
}

func TestLegacyAltScreenAndDCS(t *testing.T) {
	term := New(20, 4, 10)
	feed(t, term, "main\x1b[?47halt47\x1b[?47l")
	if got := term.Capture(10); !strings.Contains(got, "main") || strings.Contains(got, "alt47") {
		t.Fatalf("?47 alt screen: %q", got)
	}
	// DCS payloads are consumed without touching the grid; ESC \ terminates.
	feed(t, term, "\x1bPsome dcs payload\x1b\\after")
	if got := term.Capture(10); !strings.Contains(got, "after") || strings.Contains(got, "payload") {
		t.Fatalf("DCS: %q", got)
	}
	// OSC terminated by ST (ESC \) instead of BEL.
	feed(t, term, "\x1b]0;title\x1b\\!")
	if got := term.Capture(10); !strings.Contains(got, "after!") {
		t.Fatalf("OSC ST: %q", got)
	}
}

func TestMalformedCSIRecovers(t *testing.T) {
	term := New(20, 3, 0)
	// An oversized parameter string overflows the buffer and is abandoned;
	// an ESC inside a CSI restarts sequence parsing.
	feed(t, term, "\x1b["+strings.Repeat("1;", 40)+"mok")
	feed(t, term, "\x1b[12\x1b[31mred")
	if got := term.Capture(10); !strings.Contains(got, "ok") || !strings.Contains(got, "red") {
		t.Fatalf("malformed CSI: %q", got)
	}
}

func TestHugeCSICountsAreClampedToTheGrid(t *testing.T) {
	const huge = "999999999999999999999"
	for _, tc := range []struct {
		name, setup, hugeOp, boundedOp string
	}{
		{"IL", "one\r\ntwo\r\nthree\r\nfour\x1b[1;1H", "\x1b[" + huge + "L", "\x1b[4L"},
		{"DL", "one\r\ntwo\r\nthree\r\nfour\x1b[1;1H", "\x1b[" + huge + "M", "\x1b[4M"},
		{"ICH", "abcdefgh\x1b[1;1H", "\x1b[" + huge + "@", "\x1b[8@"},
		{"DCH", "abcdefgh\x1b[1;1H", "\x1b[" + huge + "P", "\x1b[8P"},
		{"ECH", "abcdefgh\x1b[1;1H", "\x1b[" + huge + "X", "\x1b[8X"},
		{"SU", "one\r\ntwo\r\nthree\r\nfour", "\x1b[" + huge + "S", "\x1b[4S"},
		{"SD", "one\r\ntwo\r\nthree\r\nfour", "\x1b[" + huge + "T", "\x1b[4T"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hugeTerm, boundedTerm := New(8, 4, 0), New(8, 4, 0)
			feed(t, hugeTerm, tc.setup+tc.hugeOp)
			feed(t, boundedTerm, tc.setup+tc.boundedOp)
			if got, want := string(hugeTerm.Redraw()), string(boundedTerm.Redraw()); got != want {
				t.Fatalf("huge count differs from grid-sized count:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func BenchmarkHostileCSICounts(b *testing.B) {
	seq := []byte("\x1b[999999999999999999999S\x1b[999999999999999999999L\x1b[999999999999999999999P")
	for b.Loop() {
		term := New(120, 40, 200)
		_, _ = term.Write(seq)
	}
}

func TestScrollRegionReverseWrapAndKeypadIgnored(t *testing.T) {
	term := New(20, 4, 0)
	// Keypad mode escapes and charset designators are consumed silently.
	feed(t, term, "\x1b=\x1b>\x1b(Bvisible")
	if got := term.Capture(10); got != "visible\n" {
		t.Fatalf("ignored escapes: %q", got)
	}
}

// Re-attach repaints the screen from the emulator grid, so the grid must
// remember SGR attributes and Redraw must re-emit them — otherwise every
// re-attach turns the session black and white until the agent happens to
// repaint. Capture stays plain text by contract.
func TestRedrawReplaysColors(t *testing.T) {
	term := New(60, 5, 0)
	feed(t, term, "\x1b[1;32mgreen\x1b[0m plain \x1b[38;5;196mred256\x1b[0m \x1b[48;2;10;20;30mtruebg\x1b[0m")
	out := string(term.Redraw())
	for _, want := range []string{
		"\x1b[0;1;32mgreen",           // bold + 16-color fg
		"\x1b[0m plain ",              // explicit reset between styled runs
		"\x1b[0;38;5;196mred256",      // 256-color fg
		"\x1b[0;48;2;10;20;30mtruebg", // truecolor bg
		"truebg\x1b[0m\x1b[",          // attributes reset before the cursor park
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("redraw missing %q: %q", want, out)
		}
	}
	if got := term.Capture(10); strings.Contains(got, "\x1b") {
		t.Fatalf("capture must stay plain text: %q", got)
	}
}

// Bright (90–107) and basic background colors round-trip through the grid.
func TestRedrawReplaysBrightAndBackgroundColors(t *testing.T) {
	term := New(40, 3, 0)
	feed(t, term, "\x1b[93;44mwarn\x1b[0m")
	out := string(term.Redraw())
	if !strings.Contains(out, "\x1b[0;93;44mwarn") {
		t.Fatalf("bright fg + bg lost: %q", out)
	}
}

// SGR attribute-off codes clear only their attribute.
func TestSGRAttributeOffCodes(t *testing.T) {
	term := New(40, 3, 0)
	feed(t, term, "\x1b[1;4;31mab\x1b[22;24mcd")
	out := string(term.Redraw())
	if !strings.Contains(out, "\x1b[0;1;4;31mab") {
		t.Fatalf("bold+underline+red lost: %q", out)
	}
	if !strings.Contains(out, "\x1b[0;31mcd") {
		t.Fatalf("22/24 must clear bold/underline but keep color: %q", out)
	}
}

// Colon-separated SGR sub-parameters (kitty/newer emitters) parse the same as
// the semicolon forms.
func TestSGRColonSubparameters(t *testing.T) {
	term := New(40, 3, 0)
	feed(t, term, "\x1b[38:5:99mX\x1b[0m \x1b[38:2::1:2:3mY\x1b[0m \x1b[4:0mZ")
	out := string(term.Redraw())
	if !strings.Contains(out, "\x1b[0;38;5;99mX") {
		t.Fatalf("colon 256-color form lost: %q", out)
	}
	if !strings.Contains(out, "\x1b[0;38;2;1;2;3mY") {
		t.Fatalf("colon truecolor form lost: %q", out)
	}
	if strings.Contains(out, "\x1b[0;4mZ") {
		t.Fatalf("4:0 must mean underline off: %q", out)
	}
}

// Erases fill with the current background (BCE), so a TUI that paints colored
// bars via SGR-bg + erase replays with its background intact.
func TestRedrawPreservesBackgroundColorErase(t *testing.T) {
	term := New(10, 3, 0)
	feed(t, term, "\x1b[44m\x1b[2J\x1b[Hab")
	out := string(term.Redraw())
	if !strings.Contains(out, "\x1b[0;44mab") {
		t.Fatalf("text on erased bg lost its background: %q", out)
	}
	// The erased remainder of the row paints as spaces in the same bg run
	// (no attribute switch), and bg-only rows below are not trimmed.
	if !strings.Contains(out, "ab"+strings.Repeat(" ", 8)+"\r\n") {
		t.Fatalf("erased cells must replay as bg-colored spaces: %q", out)
	}
	if rows := strings.Count(out, "\r\n") + 1; rows != 3 {
		t.Fatalf("bg-colored blank rows must not be trimmed, got %d rows: %q", rows, out)
	}
	if got := term.Capture(10); strings.Contains(got, "\x1b") {
		t.Fatalf("capture must stay plain text: %q", got)
	}
}

// EL with a background color extends that background to the end of the line.
func TestEraseLineUsesCurrentBackground(t *testing.T) {
	term := New(8, 2, 0)
	feed(t, term, "xy\x1b[42m\x1b[K")
	out := string(term.Redraw())
	if !strings.Contains(out, "xy\x1b[0;42m"+strings.Repeat(" ", 6)) {
		t.Fatalf("EL must fill with current bg: %q", out)
	}
}

// A re-attaching client gets a fresh Redraw; the agent's input-affecting DEC
// private modes are set live only on the first attach, so Redraw must replay
// the ones still active or arrows (application cursor keys) and wheel scroll
// (mouse reporting) break on every re-attach.
func TestRedrawReplaysApplicationCursorKeys(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?1h") // DECCKM on
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[?1h") {
		t.Fatalf("Redraw must replay application cursor keys: %q", out)
	}
}

func TestRedrawReplaysMouseModes(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?1000;1002;1003;1006h") // tracking + SGR encoding
	out := string(term.Redraw())
	for _, want := range []string{"\x1b[?1000h", "\x1b[?1002h", "\x1b[?1003h", "\x1b[?1006h"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Redraw must replay mouse mode %q: %q", want, out)
		}
	}
}

func TestRedrawReplaysBracketedPasteAndFocus(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?2004h\x1b[?1004h")
	out := string(term.Redraw())
	if !strings.Contains(out, "\x1b[?2004h") || !strings.Contains(out, "\x1b[?1004h") {
		t.Fatalf("Redraw must replay bracketed paste and focus reporting: %q", out)
	}
}

func TestRedrawReplaysHiddenCursor(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?25l") // cursor hidden by the agent
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[?25l") {
		t.Fatalf("Redraw must replay a hidden cursor: %q", out)
	}
	// A visible cursor is the terminal default; do not emit a redundant ?25h.
	vis := New(20, 3, 0)
	feed(t, vis, "\x1b[?25l\x1b[?25h")
	if out := string(vis.Redraw()); strings.Contains(out, "\x1b[?25") {
		t.Fatalf("Redraw must not emit cursor-visibility at the default: %q", out)
	}
}

func TestRedrawReplaysApplicationKeypad(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b=") // DECKPAM
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b=") {
		t.Fatalf("Redraw must replay application keypad: %q", out)
	}
	num := New(20, 3, 0)
	feed(t, num, "\x1b=\x1b>") // app then back to numeric
	if out := string(num.Redraw()); strings.Contains(out, "\x1b=") {
		t.Fatalf("Redraw must not replay keypad at the numeric default: %q", out)
	}
}

func TestRedrawOmitsModesAtTheirDefault(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "hello")
	out := string(term.Redraw())
	for _, none := range []string{"\x1b[?1h", "\x1b[?1000h", "\x1b[?1006h", "\x1b[?2004h", "\x1b[?1004h", "\x1b[?25l", "\x1b="} {
		if strings.Contains(out, none) {
			t.Fatalf("fresh terminal must not replay default-off mode %q: %q", none, out)
		}
	}
}

func TestRedrawDropsModesTurnedBackOff(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?1h\x1b[?1000h\x1b[?1l\x1b[?1000l") // on then off
	out := string(term.Redraw())
	if strings.Contains(out, "\x1b[?1h") || strings.Contains(out, "\x1b[?1000h") {
		t.Fatalf("Redraw must not replay modes the agent turned back off: %q", out)
	}
}

func TestResetClearsReplayedModes(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?1h\x1b[?1000h\x1b=")
	feed(t, term, "\x1bc") // RIS full reset
	out := string(term.Redraw())
	if strings.Contains(out, "\x1b[?1h") || strings.Contains(out, "\x1b[?1000h") || strings.Contains(out, "\x1b=") {
		t.Fatalf("RIS must clear tracked modes: %q", out)
	}
}

// Modes are global to the terminal, not per-screen: a full-screen TUI sets
// DECCKM after entering its alternate screen, and Redraw must still replay it
// once the agent is back (or while it is on either screen).
func TestRedrawReplaysModesSetOnAltScreen(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?1049h\x1b[?1h\x1b[?1000h") // enter alt, then set modes
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[?1h") || !strings.Contains(out, "\x1b[?1000h") {
		t.Fatalf("modes set on the alt screen must still replay: %q", out)
	}
}

// The replayed modes must precede the grid content so the terminal is in the
// right state before the cursor is parked and the agent's next output lands.
func TestRedrawModesPrecedeContent(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[?1hHI")
	out := string(term.Redraw())
	mode := strings.Index(out, "\x1b[?1h")
	content := strings.Index(out, "HI")
	if mode < 0 || content < 0 || mode > content {
		t.Fatalf("mode replay must come before content (mode=%d content=%d): %q", mode, content, out)
	}
}
