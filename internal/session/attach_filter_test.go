package session

import (
	"bytes"
	"testing"
)

func runFilter(t *testing.T, f *stdinFilter, chunks ...string) (string, bool) {
	t.Helper()
	var out bytes.Buffer
	for i, c := range chunks {
		got, detach := f.filter([]byte(c))
		out.Write(got)
		if detach {
			if i != len(chunks)-1 {
				t.Fatalf("detached early on chunk %d", i)
			}
			return out.String(), true
		}
	}
	return out.String(), false
}

func TestLeftArrowDetachesWhenNothingTyped(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x1b[D")
	if !detach || out != "" {
		t.Fatalf("fresh left arrow should detach cleanly, out=%q detach=%v", out, detach)
	}
}

func TestSS3LeftArrowAlsoDetaches(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "\x1bOD"); !detach {
		t.Fatal("application-cursor-mode left arrow should detach")
	}
}

func TestLeftArrowInsideDraftMovesCursor(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "abc", "\x1b[D")
	if detach {
		t.Fatal("left arrow inside a typed draft must not detach")
	}
	if out != "abc\x1b[D" {
		t.Fatalf("draft cursor movement must pass through, out=%q", out)
	}
}

func TestEnterReArmsQuickDetach(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "fix the bug\r"); detach {
		t.Fatal("typing must not detach")
	}
	if _, detach := runFilter(t, f, "\x1b[D"); !detach {
		t.Fatal("left arrow right after Enter should detach")
	}
}

// History/menu navigation can leave text in the agent's input box that uam
// cannot see; any forwarded escape sequence must disarm the quick detach
// until the next submit/clear.
func TestNavigationDisarmsUntilClear(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x1b[A") // up arrow: may recall history
	if detach || out != "\x1b[A" {
		t.Fatalf("up arrow must pass through, out=%q detach=%v", out, detach)
	}
	if _, detach := runFilter(t, f, "\x1b[D"); detach {
		t.Fatal("left arrow after navigation must not detach")
	}
	// Bare Esc clears the input box (Claude Code semantics), is forwarded
	// immediately, and re-arms the quick detach.
	if out, detach := runFilter(t, f, "\x1b"); detach || out != "\x1b" {
		t.Fatalf("bare Esc must pass through without detaching, out=%q detach=%v", out, detach)
	}
	if _, detach := runFilter(t, f, "\x1b[D"); !detach {
		t.Fatal("left arrow after bare Esc should detach")
	}
}

func TestCtrlCAndCtrlUReArm(t *testing.T) {
	for _, clear := range []string{"\x03", "\x15"} {
		f := &stdinFilter{backDetach: true}
		if _, detach := runFilter(t, f, "draft"+clear, "\x1b[D"); !detach {
			t.Fatalf("left arrow after %q should detach", clear)
		}
	}
}

func TestModifiedLeftArrowPassesThrough(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x1b[1;2D") // shift-left
	if detach || out != "\x1b[1;2D" {
		t.Fatalf("modified arrow must pass through, out=%q detach=%v", out, detach)
	}
}

func TestQuickDetachDisabledPassesArrowThrough(t *testing.T) {
	f := &stdinFilter{backDetach: false}
	out, detach := runFilter(t, f, "\x1b[D")
	if detach || out != "\x1b[D" {
		t.Fatalf("disabled quick detach must forward the arrow, out=%q detach=%v", out, detach)
	}
}

func TestSequenceSplitAcrossReadsStillDetaches(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "\x1b[", "D"); !detach {
		t.Fatal("left arrow split across reads should still detach")
	}
}

func TestChordStillDetachesWhenDirty(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "draft", "\x02d")
	if !detach || out != "draft" {
		t.Fatalf("Ctrl+B d must always detach, out=%q detach=%v", out, detach)
	}
}

func TestChordDoubledSendsLiteralPrefix(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x02\x02")
	if detach || out != "\x02" {
		t.Fatalf("Ctrl+B Ctrl+B should forward one literal prefix, out=%q", out)
	}
}

func TestCtrlZSwallowed(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "a\x1ab")
	if detach || out != "ab" {
		t.Fatalf("Ctrl+Z must be swallowed, out=%q", out)
	}
}

// Deleting everything you typed re-arms the quick detach: the filter tracks
// an approximate rune count, so a backspaced-empty input box behaves like an
// untouched one.
func TestBackspacedEmptyDraftReArmsQuickDetach(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "ab\x7f\x7f"); detach {
		t.Fatal("typing and deleting must not detach by itself")
	}
	if _, detach := runFilter(t, f, "\x1b[D"); !detach {
		t.Fatal("left arrow after deleting the whole draft should detach")
	}
}

func TestPartialDeleteStaysDisarmed(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "ab\x7f", "\x1b[D"); detach {
		t.Fatal("left arrow with a char still in the box must not detach")
	}
}

func TestExtraBackspacesAtEmptyStayArmed(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	// Backspace on an empty box is a no-op; more deletes than chars typed
	// must not wedge the estimate below zero.
	if _, detach := runFilter(t, f, "\x7f\x7fa\x7f\x7f\x7f", "\x1b[D"); !detach {
		t.Fatal("left arrow after over-deleting should still detach")
	}
}

func TestMultibyteRuneDeletesWithOneBackspace(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	// é is two bytes but one rune: a single backspace empties the box.
	if _, detach := runFilter(t, f, "é\x7f", "\x1b[D"); !detach {
		t.Fatal("left arrow after deleting a multibyte rune should detach")
	}
}

func TestCtrlHBackspaceAlsoDeletes(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "a\x08", "\x1b[D"); !detach {
		t.Fatal("Ctrl+H backspace should re-arm like DEL")
	}
}

// Tab completion can insert text uam cannot count; backspaces afterwards must
// not re-arm — only a submit/clear does.
func TestTabDisarmsUntilClear(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	if _, detach := runFilter(t, f, "a\t\x7f\x7f", "\x1b[D"); detach {
		t.Fatal("backspaces after a tab must not re-arm the quick detach")
	}
	if _, detach := runFilter(t, f, "\x15", "\x1b[D"); !detach {
		t.Fatal("Ctrl+U after a tab should re-arm")
	}
}

// Agents query the terminal (Ink-based ones request the cursor position on
// every render) and the replies arrive on stdin mixed with real keystrokes.
// Replies never reach the agent's input box, so they must pass through
// without disarming the left-arrow quick detach.
func TestTerminalRepliesDoNotDisarm(t *testing.T) {
	replies := []string{
		"\x1b[24;80R",                   // CPR: cursor position report
		"\x1b[?64;1;2;6;9;15;18;21;22c", // DA1: primary device attributes
		"\x1b[?1u",                      // kitty keyboard flags report
		"\x1b[0n",                       // DSR: terminal OK
		"\x1b[?2004;1$y",                // DECRPM: mode report
	}
	for _, reply := range replies {
		f := &stdinFilter{backDetach: true}
		out, detach := runFilter(t, f, reply)
		if detach || out != reply {
			t.Fatalf("reply %q must pass through untouched, out=%q detach=%v", reply, out, detach)
		}
		if _, detach := runFilter(t, f, "\x1b[D"); !detach {
			t.Fatalf("left arrow after reply %q should still detach", reply)
		}
	}
}

// OSC and DCS replies (color queries, XTGETTCAP) carry free-form payloads;
// counting those bytes as typed runes would wedge the quick detach.
func TestStringRepliesDoNotDisarm(t *testing.T) {
	replies := []string{
		"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\", // OSC color reply, ST-terminated
		"\x1b]10;rgb:ffff/ffff/ffff\x07",   // OSC color reply, BEL-terminated
		"\x1bP1+r524742=38\x1b\\",          // DCS XTGETTCAP reply
	}
	for _, reply := range replies {
		f := &stdinFilter{backDetach: true}
		out, detach := runFilter(t, f, reply)
		if detach || out != reply {
			t.Fatalf("string reply %q must pass through untouched, out=%q detach=%v", reply, out, detach)
		}
		if _, detach := runFilter(t, f, "\x1b[D"); !detach {
			t.Fatalf("left arrow after string reply %q should still detach", reply)
		}
	}
}

func TestStringReplySplitAcrossReads(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x1b]11;rgb:1e", "1e/1e1e/1e1e\x1b", "\\", "\x1b[D")
	if !detach {
		t.Fatal("left arrow after a split string reply should still detach")
	}
	if out != "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\" {
		t.Fatalf("split reply must be forwarded intact, out=%q", out)
	}
}

func TestMouseEventsDoNotDisarm(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	wheel := "\x1b[<64;10;20M\x1b[<65;10;20m" // SGR wheel press + release
	out, detach := runFilter(t, f, wheel)
	if detach || out != wheel {
		t.Fatalf("mouse events must pass through untouched, out=%q detach=%v", out, detach)
	}
	if _, detach := runFilter(t, f, "\x1b[D"); !detach {
		t.Fatal("left arrow after mouse events should still detach")
	}
}

func TestFocusEventsDoNotDisarm(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x1b[I\x1b[O") // focus in + out
	if detach || out != "\x1b[I\x1b[O" {
		t.Fatalf("focus events must pass through untouched, out=%q detach=%v", out, detach)
	}
	if _, detach := runFilter(t, f, "\x1b[D"); !detach {
		t.Fatal("left arrow after focus events should still detach")
	}
}

// Real navigation keys still poison the estimate even with replies neutral:
// only terminal-generated traffic is exempt.
func TestArrowAndFunctionKeysStillDisarm(t *testing.T) {
	for _, key := range []string{"\x1b[A", "\x1b[B", "\x1b[Z", "\x1bOP", "\x1bf"} {
		f := &stdinFilter{backDetach: true}
		if _, detach := runFilter(t, f, key, "\x1b[D"); detach {
			t.Fatalf("left arrow after key %q must not detach", key)
		}
	}
}

// Deliberate trade-off, pinned: xterm's modified F3 (CSI 1;2R) shares its
// grammar with a cursor position report at row 1 col 2, and no parameter
// heuristic separates them without misreading common cursor positions. The
// filter sides with CPR (constant Ink traffic) over modified F3 (bound to
// text entry by no supported agent) — see seqPoisons.
func TestModifiedF3ReadsAsCursorReply(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x1b[1;2R")
	if detach || out != "\x1b[1;2R" {
		t.Fatalf("CSI 1;2R must pass through untouched, out=%q detach=%v", out, detach)
	}
	if _, detach := runFilter(t, f, "\x1b[D"); !detach {
		t.Fatal("left arrow after a CPR-shaped sequence should still detach")
	}
}
