package vterm

import (
	"strings"
	"testing"
)

// CSI sequences with a '<', '=' or '>' prefix are protocol negotiation, not
// drawing. They used to lose their prefix before dispatch and land on SCORC and
// SGR, so an agent pushing kitty keyboard flags teleported the cursor and
// corrupted every row painted afterwards.
func TestPrivatePrefixedCSIDoesNotDraw(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		{name: "kitty push", seq: "\x1b[>1u"},
		{name: "kitty pop", seq: "\x1b[<u"},
		{name: "kitty set", seq: "\x1b[=0;1u"},
		{name: "kitty query", seq: "\x1b[?u"},
		{name: "modify other keys", seq: "\x1b[>4;2m"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			term := New(20, 3, 100)
			feed(t, term, "line1\r\nline2\r\n")

			// When
			feed(t, term, test.seq)
			feed(t, term, "AFTER")

			// Then
			if got, want := term.Capture(10), "line1\nline2\nAFTER\n"; got != want {
				t.Fatalf("capture = %q, want %q", got, want)
			}
			if out := string(term.Redraw()); strings.Contains(out, "\x1b[0;2;4m") {
				t.Fatalf("redraw carries a modifyOtherKeys parameter as SGR: %q", out)
			}
		})
	}
}

// XTSAVE (CSI ? Pm s) must not clobber the cursor an agent saved with SCOSC.
func TestPrivateSaveModeKeepsSavedCursor(t *testing.T) {
	// Given
	term := New(20, 3, 0)
	feed(t, term, "\x1b[2;3H\x1b[s")

	// When
	feed(t, term, "\x1b[?1007s\x1b[1;1H\x1b[u")

	// Then
	if out := string(term.Redraw()); !strings.HasSuffix(out, "\x1b[2;3H") {
		t.Fatalf("redraw cursor = %q, want the SCOSC position restored", out)
	}
}

// DECAWM off pins the cursor in the last column; wrapping anyway scrolled a row
// the real terminal keeps on screen into scrollback.
func TestAutowrapOffDoesNotScroll(t *testing.T) {
	// Given
	term := New(5, 3, 100)
	feed(t, term, "\x1b[3;1H")

	// When
	feed(t, term, "\x1b[?7lABCDEFGH")

	// Then
	if got, want := term.Capture(10), "\n\nABCDH\n"; got != want {
		t.Fatalf("capture = %q, want %q", got, want)
	}
	// Autowrap back on resumes wrapping.
	feed(t, term, "\x1b[?7h\x1b[1;1H\x1b[2JIJKLMN")
	if got, want := term.Capture(10), "IJKLMN\n"; got != want {
		t.Fatalf("capture after re-enabling autowrap = %q, want %q", got, want)
	}
}

// The DEC special graphics set draws box borders; discarding the designation
// captured raw letters and repainted them as letters after re-attach.
func TestSpecialGraphicsCharsetTranslates(t *testing.T) {
	// Given
	term := New(20, 3, 0)

	// When
	feed(t, term, "\x1b(0lqqk\x1b(Bplain")

	// Then
	if got, want := term.Capture(10), "┌──┐plain\n"; got != want {
		t.Fatalf("capture = %q, want %q", got, want)
	}
}

// Redraw has to hand the client the scroll region, the agent's saved cursor and
// the live pen; without them the agent keeps drawing into a terminal whose
// state silently differs from the one it left.
func TestRedrawReplaysRegionSavedCursorAndPen(t *testing.T) {
	// Given
	term := New(20, 6, 100)
	feed(t, term, "\x1b[2;5r\x1b[3;1Hbody\x1b7\x1b[31m")

	// When
	out := string(term.Redraw())

	// Then
	region := strings.Index(out, "\x1b[2;5r")
	if region < 0 {
		t.Fatalf("redraw = %q, want the scroll region replayed", out)
	}
	if body := strings.Index(out, "body"); body > region {
		t.Fatalf("redraw = %q, want the region set after the grid paint", out)
	}
	if !strings.Contains(out, "\x1b7") {
		t.Fatalf("redraw = %q, want the agent's saved cursor replayed", out)
	}
	if !strings.HasSuffix(out, "\x1b[0;31m") {
		t.Fatalf("redraw = %q, want the live SGR pen restored last", out)
	}
}

func TestFocusReportingTracksMode1004(t *testing.T) {
	term := New(80, 24, 100)
	if term.FocusReporting() {
		t.Fatal("focus reporting should be off by default")
	}
	_, _ = term.Write([]byte("\x1b[?1004h"))
	if !term.FocusReporting() {
		t.Fatal("focus reporting should be on after ?1004h")
	}
	_, _ = term.Write([]byte("\x1b[?1004l"))
	if term.FocusReporting() {
		t.Fatal("focus reporting should be off after ?1004l")
	}
}
