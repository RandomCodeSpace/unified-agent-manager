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
	if !strings.HasPrefix(out, "\x1b[2J\x1b[H") {
		t.Fatalf("redraw must clear first: %q", out)
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
