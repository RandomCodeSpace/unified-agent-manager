package session

import (
	"bytes"
	"errors"
	"io"
	"strings"
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

func TestAttachMousePolicy(t *testing.T) {
	tests := []struct {
		name, value, sshConnection, sshTTY string
		want                               bool
	}{
		{"unset local", "", "", "", true}, {"auto local", "auto", "", "", true},
		{"unset ssh connection", "", "client", "", false}, {"auto ssh tty", "auto", "", "/dev/pts/1", false},
		{"on ssh", "on", "client", "", true}, {"off local", "off", "", "", false},
		{"invalid local", "maybe", "", "", true}, {"invalid ssh", "maybe", "client", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{AttachMouseEnv: tt.value, "SSH_CONNECTION": tt.sshConnection, "SSH_TTY": tt.sshTTY}
			if got := attachMouseEnabled(func(key string) string { return env[key] }); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func filterHostOutput(t *testing.T, mouse bool, chunks ...[]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	f := newAttachOutputFilter(&out, mouse)
	for _, chunk := range chunks {
		if n, err := f.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(chunk))
		}
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestAttachOutputFilterOwnedModes(t *testing.T) {
	for _, mouse := range []bool{true, false} {
		for _, mode := range []string{"47", "1047", "1049"} {
			for _, final := range []string{"h", "l"} {
				if got := string(filterHostOutput(t, mouse, []byte("before\x1b[?"+mode+final+"after"))); got != "beforeafter" {
					t.Fatalf("mouse=%v mode=%s%s got %q", mouse, mode, final, got)
				}
			}
		}
	}
	input := []byte("部署\x1b[31m\x1b[?1;1000;2004;1006;1049hX\x1b[?1004l\x1b]0;title\a")
	if got, want := string(filterHostOutput(t, false, input)), "部署\x1b[31m\x1b[?1;2004hX\x1b[?1004l\x1b]0;title\a"; got != want {
		t.Fatalf("mouse off = %q, want %q", got, want)
	}
	if got, want := string(filterHostOutput(t, true, input)), "部署\x1b[31m\x1b[?1;1000;2004;1006hX\x1b[?1004l\x1b]0;title\a"; got != want {
		t.Fatalf("mouse on = %q, want %q", got, want)
	}
}

func TestAttachOutputFilterSplitAndFlush(t *testing.T) {
	input := []byte("a\x1b[?1;1000;2004;1049hb")
	for split := 0; split <= len(input); split++ {
		if got := string(filterHostOutput(t, false, input[:split], input[split:])); got != "a\x1b[?1;2004hb" {
			t.Fatalf("split %d = %q", split, got)
		}
	}
	for _, partial := range []string{"\x1b", "\x1b[", "\x1b[?1000"} {
		if got := string(filterHostOutput(t, false, []byte(partial))); got != partial {
			t.Fatalf("partial %q flushed as %q", partial, got)
		}
	}
	long := "\x1b[?" + strings.Repeat("1", 4097)
	if got := string(filterHostOutput(t, false, []byte(long))); got != long {
		t.Fatal("over-cap CSI changed")
	}
}

func TestAttachOutputFilterRestartsAfterAbortedCSI(t *testing.T) {
	for _, mode := range []string{"47", "1047", "1049"} {
		for _, final := range []string{"h", "l"} {
			input := []byte("\x1b[12\x1b[?" + mode + final)
			want := []byte("\x1b[12\x18") // CAN terminates the forwarded incomplete CSI.
			for split := 0; split <= len(input); split++ {
				if got := filterHostOutput(t, true, input[:split], input[split:]); !bytes.Equal(got, want) {
					t.Fatalf("mode=%s%s split=%d got %q, want %q", mode, final, split, got, want)
				}
			}
		}
	}
}

func TestAttachOutputFilterAbortedCSIMultiChunkAndEOF(t *testing.T) {
	input := []byte("\x1b[12\x1b[?1049l")
	chunks := make([][]byte, 0, len(input))
	for i := range input {
		chunks = append(chunks, input[i:i+1])
	}
	if got, want := filterHostOutput(t, true, chunks...), []byte("\x1b[12\x18"); !bytes.Equal(got, want) {
		t.Fatalf("byte chunks = %q, want %q", got, want)
	}
	for _, incomplete := range []string{"\x1b[12\x1b", "\x1b[12\x1b[", "\x1b[12\x1b[?1049"} {
		if got := string(filterHostOutput(t, false, []byte(incomplete))); got != incomplete {
			t.Fatalf("EOF changed incomplete %q to %q", incomplete, got)
		}
	}
	nonOwned := "\x1b[12\x1b[?2004h"
	if got := string(filterHostOutput(t, true, []byte(nonOwned))); got != nonOwned {
		t.Fatalf("non-owned abort sequence changed to %q", got)
	}
}

func TestAttachOutputFilterRestartsAtCapEdge(t *testing.T) {
	for _, prefixLen := range []int{maxAttachCSI, maxAttachCSI + 1} {
		prefix := "\x1b[" + strings.Repeat("1", prefixLen-2)
		input := []byte(prefix + "\x1b[?1049l")
		want := []byte(prefix + "\x18")
		for split := 0; split <= len(input); split++ {
			if got := filterHostOutput(t, false, input[:split], input[split:]); !bytes.Equal(got, want) {
				t.Fatalf("prefix=%d split=%d tail=%q, want CAN-terminated prefix", prefixLen, split, got[len(got)-min(16, len(got)):])
			}
		}
	}
}

func TestAttachOutputFilterPreservesMalformedPrivateModes(t *testing.T) {
	for _, input := range []string{
		"\x1b[?1:2;1049h",
		"\x1b[?abc;1049h",
		"\x1b[?1;;1049h",
		"\x1b[?;1049h",
		"\x1b[?1;1049$h",
		"\x1b[?1.2;1049l",
	} {
		for split := 0; split <= len(input); split++ {
			if got := string(filterHostOutput(t, false, []byte(input[:split]), []byte(input[split:]))); got != input {
				t.Fatalf("input=%q split=%d changed to %q", input, split, got)
			}
		}
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestAttachOutputFilterPropagatesDownstreamError(t *testing.T) {
	want := errors.New("write failed")
	if _, err := io.WriteString(newAttachOutputFilter(failingWriter{want}, false), "plain"); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

type chunkWriter struct {
	bytes.Buffer
	max int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.Buffer.Write(p)
}

func TestAttachOutputFilterHandlesPartialDownstreamWrites(t *testing.T) {
	var dst chunkWriter
	dst.max = 2
	f := newAttachOutputFilter(&dst, false)
	input := []byte("部署\x1b[?1;1000;2004;1049h tail")
	if n, err := f.Write(input); err != nil || n != len(input) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := dst.String(), "部署\x1b[?1;2004h tail"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBracketedPasteBypassesAttachBindings(t *testing.T) {
	paste := []byte("\x1b[200~部署 café 🚀\r\n\x02d\x03\x1a\x1b[D\x1b[201~")
	for split := 0; split <= len(paste); split++ {
		f := &stdinFilter{backDetach: true}
		var got []byte
		for _, chunk := range [][]byte{paste[:split], paste[split:]} {
			out, detach := f.filter(chunk)
			if detach {
				t.Fatalf("split %d detached", split)
			}
			got = append(got, out...)
		}
		if !bytes.Equal(got, paste) {
			t.Fatalf("split %d = %q, want %q", split, got, paste)
		}
		if _, detach := f.filter([]byte{detachPrefix, 'd'}); !detach {
			t.Fatalf("split %d detach did not resume", split)
		}
	}
}

func TestBracketMarkerInsideTerminalReplyDoesNotEnterPaste(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	reply := "\x1b]0;literal \x1b[200~ marker\a"
	if out, detach := runFilter(t, f, reply); detach || out != reply {
		t.Fatalf("reply = %q detach=%v", out, detach)
	}
	if _, detach := runFilter(t, f, string([]byte{detachPrefix, 'd'})); !detach {
		t.Fatal("marker inside OSC incorrectly entered paste mode")
	}
}

func TestRawUTF8AndCRLFAreByteExact(t *testing.T) {
	input := []byte("部署 café 🚀\r\n")
	for split := 0; split <= len(input); split++ {
		f := &stdinFilter{backDetach: true}
		var got []byte
		for _, chunk := range [][]byte{input[:split], input[split:]} {
			out, detach := f.filter(chunk)
			if detach {
				t.Fatalf("split %d detached", split)
			}
			got = append(got, out...)
		}
		if !bytes.Equal(got, input) {
			t.Fatalf("split %d = %q, want %q", split, got, input)
		}
	}
}

func TestAttachOutputFilterPreservesUnrelatedControls(t *testing.T) {
	for _, input := range [][]byte{
		[]byte("\x1b[31mred\x1b[0m"), []byte("\x1b[?1004h\x1b[?1007l\x1b[?2004h"),
		[]byte("\x1b[?1000$p\x1b[?1000$y"), []byte("\x1b]0;title\a"),
		[]byte("\x1bP$qm\x1b\\"), []byte("部署 café 🚀؛\r\n"),
		{0x9b, '?', '1', '0', '4', '9', 'l'},
	} {
		if got := filterHostOutput(t, false, input); !bytes.Equal(got, input) {
			t.Fatalf("%q changed to %q", input, got)
		}
	}
}

func FuzzAttachFiltering(f *testing.F) {
	for _, seed := range []string{"plain text", "世界🚀", "\x02d", "\x1b[D", "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"} {
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, input string, backDetach bool) {
		filter := &stdinFilter{backDetach: backDetach}
		out, _ := filter.filter([]byte(input))
		if len(out) > 2*len(input)+2 {
			t.Fatalf("filter expanded %d input bytes to %d bytes", len(input), len(out))
		}
	})
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

func TestCtrlCSwallowedForTerminalCopy(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x03")
	if detach || out != "" {
		t.Fatalf("plain Ctrl+C must not reach the agent, out=%q detach=%v", out, detach)
	}
}

func TestChordCSendsLiteralCtrlC(t *testing.T) {
	f := &stdinFilter{backDetach: true}
	out, detach := runFilter(t, f, "\x02c")
	if detach || out != "\x03" {
		t.Fatalf("Ctrl+B c should forward a literal Ctrl+C, out=%q detach=%v", out, detach)
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
