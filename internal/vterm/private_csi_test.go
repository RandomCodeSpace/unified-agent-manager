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

// The kitty keyboard protocol is a per-screen stack of flag bytes. The attach
// client pops it on detach, so Redraw must re-push every entry — as pushes, not
// one set, so the agent's later pops land on the values it expects — before any
// DEC private mode that can generate input.
func TestRedrawReplaysKittyPushes(t *testing.T) {
	// Given
	term := New(20, 3, 0)
	feed(t, term, "\x1b[>1u\x1b[?1h\x1b[>15uHI")

	// When
	out := string(term.Redraw())

	// Then
	pushes := strings.Index(out, "\x1b[>1u\x1b[>15u")
	if pushes < 0 {
		t.Fatalf("redraw = %q, want the kitty stack replayed bottom to top", out)
	}
	if mode := strings.Index(out, "\x1b[?1h"); mode < pushes {
		t.Fatalf("redraw = %q, want kitty pushes before the DEC private modes", out)
	}
	if strings.Contains(out, "\x1b[=") {
		t.Fatalf("redraw = %q, want no base set when only pushes were made", out)
	}
}

// Popping more entries than exist is how a terminal empties the stack; the
// spec says an emptying pop resets all flags, base included.
func TestKittyPopPastEmptyResetsBase(t *testing.T) {
	// Given
	term := New(20, 3, 0)
	feed(t, term, "\x1b[=1;1u\x1b[>2u\x1b[>4u")

	// When
	feed(t, term, "\x1b[<5u")

	// Then
	if out := string(term.Redraw()); strings.Contains(out, "u") {
		t.Fatalf("redraw = %q, want no kitty state after an emptying pop", out)
	}
	// A partial pop leaves the rest in place.
	feed(t, term, "\x1b[>1u\x1b[>2u\x1b[<u")
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[>1u") || strings.Contains(out, "\x1b[>2u") {
		t.Fatalf("redraw = %q, want only the surviving entry replayed", out)
	}
}

func TestKittyPopPreservesBaseUntilPopped(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[=1;1u\x1b[>2u\x1b[<u")
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[=1;1u") || strings.Contains(out, "\x1b[>2u") {
		t.Fatalf("redraw = %q, want the base restored after popping the pushed flags", out)
	}
	feed(t, term, "\x1b[<u")
	if out := string(term.Redraw()); strings.Contains(out, "\x1b[=") || strings.Contains(out, "\x1b[>") {
		t.Fatalf("redraw = %q, want no flags after popping the base", out)
	}
}

// CSI = flags ; mode u rewrites the top entry, or the base when nothing is
// pushed; mode 2 ors bits in and mode 3 clears them.
func TestKittySetRewritesTopOrBase(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want string
	}{
		{name: "set with empty stack replays as base", seq: "\x1b[=1;1u", want: "\x1b[=1;1u"},
		{name: "mode defaults to 1", seq: "\x1b[=3u", want: "\x1b[=3;1u"},
		{name: "set replaces top", seq: "\x1b[>1u\x1b[=4;1u", want: "\x1b[>4u"},
		{name: "mode 2 ors into top", seq: "\x1b[>1u\x1b[=2;2u", want: "\x1b[>3u"},
		{name: "mode 3 clears from top", seq: "\x1b[>15u\x1b[=1;3u", want: "\x1b[>14u"},
		{name: "base then push replays both", seq: "\x1b[=1;1u\x1b[>2u", want: "\x1b[=1;1u\x1b[>2u"},
		{name: "undefined bits are masked", seq: "\x1b[>255u", want: "\x1b[>31u"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			term := New(20, 3, 0)

			// When
			feed(t, term, test.seq)

			// Then
			if out := string(term.Redraw()); !strings.Contains(out, test.want) {
				t.Fatalf("redraw = %q, want %q", out, test.want)
			}
		})
	}
}

// Stacks are per screen: a TUI that pushes after entering its alternate screen
// must not change what Redraw replays once the agent is back on the main
// screen, and vice versa.
func TestKittyStacksArePerScreen(t *testing.T) {
	// Given
	term := New(20, 3, 0)
	feed(t, term, "\x1b[>1u")

	// When
	feed(t, term, "\x1b[?1049h")

	// Then
	if out := string(term.Redraw()); strings.Contains(out, "\x1b[>") {
		t.Fatalf("redraw = %q, want the main screen's pushes hidden on the alt screen", out)
	}
	feed(t, term, "\x1b[>15u")
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[>15u") || strings.Contains(out, "\x1b[>1u") {
		t.Fatalf("redraw = %q, want only the alt screen's push", out)
	}
	feed(t, term, "\x1b[?1049l")
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[>1u") || strings.Contains(out, "\x1b[>15u") {
		t.Fatalf("redraw = %q, want the main screen's stack back after leaving the alt screen", out)
	}
}

// kitty keeps eight slots including the base; a push past that evicts the
// oldest entry, so tracking more would replay pushes the terminal dropped.
func TestKittyPushCapEvictsOldest(t *testing.T) {
	// Given
	term := New(20, 3, 0)

	// When
	feed(t, term, "\x1b[>1u\x1b[>2u\x1b[>3u\x1b[>4u\x1b[>5u\x1b[>6u\x1b[>7u\x1b[>8u")

	// Then
	out := string(term.Redraw())
	if strings.Contains(out, "\x1b[>1u") {
		t.Fatalf("redraw = %q, want the oldest push evicted", out)
	}
	if !strings.Contains(out, "\x1b[>2u\x1b[>3u\x1b[>4u\x1b[>5u\x1b[>6u\x1b[>7u\x1b[>8u") {
		t.Fatalf("redraw = %q, want the seven newest pushes", out)
	}
}

func TestKittyPushOverflowPromotesOldestPushToBase(t *testing.T) {
	term := New(20, 3, 0)
	feed(t, term, "\x1b[=16;1u\x1b[>1u\x1b[>2u\x1b[>3u\x1b[>4u\x1b[>5u\x1b[>6u\x1b[>7u\x1b[>8u")
	const want = "\x1b[=1;1u\x1b[>2u\x1b[>3u\x1b[>4u\x1b[>5u\x1b[>6u\x1b[>7u\x1b[>8u"
	if out := string(term.Redraw()); !strings.Contains(out, want) {
		t.Fatalf("redraw = %q, want overflow to evict the old base: %q", out, want)
	}
	feed(t, term, "\x1b[<7u")
	if out := string(term.Redraw()); !strings.Contains(out, "\x1b[=1;1u") || strings.Contains(out, "\x1b[>") {
		t.Fatalf("redraw = %q, want promoted base after popping all seven pushed entries", out)
	}
}

func TestResetClearsKittyStacks(t *testing.T) {
	// Given
	term := New(20, 3, 0)
	feed(t, term, "\x1b[=1;1u\x1b[>2u\x1b[?1049h\x1b[>4u")

	// When
	feed(t, term, "\x1bc") // RIS

	// Then
	if out := string(term.Redraw()); strings.Contains(out, "u") {
		t.Fatalf("redraw = %q, want RIS to clear both kitty stacks", out)
	}
	feed(t, term, "\x1b[?1049h")
	if out := string(term.Redraw()); strings.Contains(out, "u") {
		t.Fatalf("redraw = %q, want RIS to clear the alt screen's kitty stack too", out)
	}
}
