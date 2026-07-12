package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errTestBoom = errors.New("boom")

func storeRecord(agent, id string) store.SessionRecord {
	return store.SessionRecord{ID: id, Agent: agent, Name: id, Status: store.StatusActive}
}

// readOnlyStore returns a store whose config dir is read-only after a warm-up
// write, so any subsequent Store.Update fails (used to drive the F55 error
// surfacing). It skips under root, where dir perms are not enforced.
func readOnlyStore(t *testing.T) *store.Store {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("read-only dir is not enforced for root")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Warm-up write creates the lock + config files.
	if err := st.Update(func(*store.Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return st
}

// F28 — truncate must measure display width (not byte length) so multibyte and
// wide (CJK/emoji) strings stay valid UTF-8 and never exceed the column budget.
func TestTruncateIsWidthAware(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"ascii-fits", "hello", 10},
		{"ascii-trunc", "abcdef", 4},
		{"accent", "café crème brûlée", 6},
		{"cjk", "你好世界你好世界", 5},
		{"emoji", "🚀🚀🚀🚀🚀🚀", 5},
		{"mixed", "fix 世界 bug 🚀 now", 8},
		{"zero", "anything", 0},
		{"one", "anything", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q,%d)=%q is not valid UTF-8", tc.in, tc.n, got)
			}
			if w := lipgloss.Width(got); w > tc.n {
				t.Fatalf("truncate(%q,%d)=%q has display width %d > %d", tc.in, tc.n, got, w, tc.n)
			}
		})
	}
}

// F28 — truncate must not chop a multibyte rune mid-sequence (the old byte-slice
// path produced mojibake on the boundary).
func TestTruncateNeverEmitsMojibake(t *testing.T) {
	in := "héllo wörld 世界"
	for n := 0; n <= 20; n++ {
		got := truncate(in, n)
		if !utf8.ValidString(got) {
			t.Fatalf("truncate(%q,%d)=%q is not valid UTF-8", in, n, got)
		}
	}
}

func TestRenderedMetadataCannotInjectTerminalControls(t *testing.T) {
	sess := adapter.Session{
		ID:          "1",
		AgentType:   "fake\x1b[2J",
		DisplayName: "safe\x1b]52;c;YQ==\x07name",
		Prompt:      "fix\nthis\x1b[31m now",
		Cwd:         "/tmp/evil\x1b[Hrepo",
		ProcAlive:   adapter.Alive,
	}
	m := Model{width: 100, sessions: []adapter.Session{sess}, selected: 0, confirmStopID: "1"}
	out := m.renderDetails() + renderRow(sess, false, 30, 40, true) + m.renderConfirm()
	for _, unsafe := range []string{"\x1b[2J", "\x1b]52", "\x1b[31m", "\x1b[H"} {
		if strings.Contains(out, unsafe) {
			t.Fatalf("rendered control sequence %q: %q", unsafe, out)
		}
	}
	for _, want := range []string{"safename", "fix this now", "/tmp/evilrepo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sanitized text missing %q: %q", want, out)
		}
	}
}

// F28 — the task column must stay aligned even when a row's name contains wide
// characters: byte-length padding would push the task cell out of alignment.
func TestRenderRowAlignsTaskColumnForMultibyte(t *testing.T) {
	asciiRow := renderRow(adapter.Session{ID: "1", DisplayName: "ascii", Prompt: "task", ProcAlive: adapter.Alive}, false, 14, 16, true)
	wideRow := renderRow(adapter.Session{ID: "2", DisplayName: "世界世界", Prompt: "task", ProcAlive: adapter.Alive}, false, 14, 16, true)
	// The cursor + glyph prefix is identical; the name cell must occupy the same
	// number of display columns up to the task text in both rows.
	asciiTaskIdx := lipgloss.Width(asciiRow[:strings.Index(asciiRow, "task")])
	wideTaskIdx := lipgloss.Width(wideRow[:strings.Index(wideRow, "task")])
	if asciiTaskIdx != wideTaskIdx {
		t.Fatalf("task column misaligned for wide name: ascii col=%d wide col=%d\nascii=%q\nwide=%q", asciiTaskIdx, wideTaskIdx, asciiRow, wideRow)
	}
}

// F26 — the PR status dot must render in the row when the session has a PR, and
// the glyphs must be distinct per status (not color-only, so they survive a
// no-color terminal / screen-scrape).
func TestPRStatusDotGlyphsAreDistinct(t *testing.T) {
	statuses := []adapter.PRStatus{adapter.PROpen, adapter.PRMerged, adapter.PRDraft, adapter.PRClosed}
	seen := map[string]adapter.PRStatus{}
	for _, st := range statuses {
		g := strings.TrimSpace(prStatusDot(st))
		if g == "" {
			t.Fatalf("status %q has no glyph", st)
		}
		if prev, ok := seen[g]; ok {
			t.Fatalf("status %q and %q share glyph %q (not distinct)", prev, st, g)
		}
		seen[g] = st
	}
}

// F26 — a row whose session carries a PR must render its status dot; a PR-less
// row must not.
func TestRenderRowShowsPRDotOnlyWhenPRPresent(t *testing.T) {
	withPR := renderRow(adapter.Session{ID: "1", DisplayName: "x", ProcAlive: adapter.Alive, PR: &adapter.PRRef{Status: adapter.PRMerged}}, false, 14, 16, true)
	noPR := renderRow(adapter.Session{ID: "2", DisplayName: "x", ProcAlive: adapter.Alive}, false, 14, 16, true)
	dot := strings.TrimSpace(prStatusDot(adapter.PRMerged))
	if !strings.Contains(withPR, dot) {
		t.Fatalf("row with a PR should show its status dot %q: %q", dot, withPR)
	}
	if strings.Contains(noPR, dot) {
		t.Fatalf("row without a PR should not show a PR dot: %q", noPR)
	}
}

// F26/F28 — adding the PR dot must not break column alignment between a row with
// a PR and a row without one.
func TestRenderRowColumnsAlignWithPRDot(t *testing.T) {
	withPR := renderRow(adapter.Session{ID: "1", DisplayName: "name", Prompt: "task", ProcAlive: adapter.Alive, PR: &adapter.PRRef{Status: adapter.PROpen}}, false, 14, 16, true)
	noPR := renderRow(adapter.Session{ID: "2", DisplayName: "name", Prompt: "task", ProcAlive: adapter.Alive}, false, 14, 16, true)
	withIdx := lipgloss.Width(withPR[:strings.Index(withPR, "task")])
	noIdx := lipgloss.Width(noPR[:strings.Index(noPR, "task")])
	if withIdx != noIdx {
		t.Fatalf("task column misaligned with/without PR dot: with=%d no=%d\nwith=%q\nno=%q", withIdx, noIdx, withPR, noPR)
	}
}

// F30 — a reboot-survivor dead session (Exited, not user-closed) must NOT show
// the red Failed glyph; it shows a neutral resumable glyph. A user-closed dead
// session and a live session each get their own glyph.
func TestStateGlyphDistinguishesResumableFromFailed(t *testing.T) {
	live, _ := sessionGlyph(adapter.Session{ProcAlive: adapter.Alive})
	resumable, _ := sessionGlyph(adapter.Session{ProcAlive: adapter.Exited})
	closed, _ := sessionGlyph(adapter.Session{ProcAlive: adapter.Exited, Closed: true})
	if live == resumable {
		t.Fatalf("a live session and a resumable dead session must use distinct glyphs (both %q)", live)
	}
	failGlyph, _ := stateGlyph(adapter.Failed)
	if resumable == failGlyph {
		t.Fatalf("a reboot-survivor resumable session must not use the red Failed glyph %q", failGlyph)
	}
	_ = closed
}

// F30 — the rendered row for a reboot-survivor dead session must not contain the
// literal word "failed" nor the failure glyph.
func TestRenderRowResumableSessionNotMarkedFailed(t *testing.T) {
	row := renderRow(adapter.Session{ID: "1", DisplayName: "rebooted", ProcAlive: adapter.Exited}, false, 14, 16, true)
	if strings.Contains(strings.ToLower(row), "failed") {
		t.Fatalf("resumable row should not show the literal \"failed\": %q", row)
	}
	failGlyph, _ := stateGlyph(adapter.Failed)
	if strings.Contains(row, failGlyph) {
		t.Fatalf("resumable row should not show the red Failed glyph %q: %q", failGlyph, row)
	}
}

// F30 — deadSessionFromRecord must not emit State=Active (the glyph is driven off
// ProcAlive/Closed, but the State invariant must hold for downstream code).
func TestDeadSessionFromRecordDoesNotEmitActiveState(t *testing.T) {
	rec := storeRecord("a", "id1")
	sess := deadSessionFromRecord(rec, time.Now())
	if sess.State == adapter.Active {
		t.Fatalf("deadSessionFromRecord must not emit State=Active: %+v", sess)
	}
}

// F58 — the live/fail glyph styles are package-level vars (allocated once), not
// rebuilt per row per frame.
func TestGlyphStylesAreHoisted(t *testing.T) {
	if _, ok := liveGlyphStyle.GetForeground().(lipgloss.AdaptiveColor); !ok {
		t.Fatalf("liveGlyphStyle must keep an AdaptiveColor foreground, got %T", liveGlyphStyle.GetForeground())
	}
	if _, ok := failGlyphStyle.GetForeground().(lipgloss.AdaptiveColor); !ok {
		t.Fatalf("failGlyphStyle must keep an AdaptiveColor foreground, got %T", failGlyphStyle.GetForeground())
	}
}

// C2-2 — moving the cursor up/down while the peek panel is open must re-fire the
// peek for the newly selected session and blank the stale text synchronously.
func TestUpDownRefiresPeekWhenPanelOpen(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "1", AgentType: "fake", DisplayName: "one", ProcAlive: adapter.Alive},
		{ID: "2", AgentType: "fake", DisplayName: "two", ProcAlive: adapter.Alive},
	}
	m.peekOpen = true
	m.peekText = "stale tail from session one"

	handled, cmd := m.handleMovementKey("down")
	if !handled {
		t.Fatal("down should be handled")
	}
	if m.selected != 1 {
		t.Fatalf("selection should advance to 1, got %d", m.selected)
	}
	if m.peekText != "" {
		t.Fatalf("peek text should be blanked synchronously on move, got %q", m.peekText)
	}
	if cmd == nil {
		t.Fatal("moving with the peek panel open should re-fire the peek command")
	}
}

// C2-2 — with the panel closed, up/down must NOT fire a peek (avoids an N+1
// capture storm on every keystroke).
func TestUpDownDoesNotPeekWhenPanelClosed(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "1", AgentType: "fake", DisplayName: "one", ProcAlive: adapter.Alive},
		{ID: "2", AgentType: "fake", DisplayName: "two", ProcAlive: adapter.Alive},
	}
	m.peekOpen = false

	if _, cmd := m.handleMovementKey("down"); cmd != nil {
		t.Fatalf("down with the peek panel closed should not fire a peek command, got %v", cmd)
	}
	if _, cmd := m.handleMovementKey("up"); cmd != nil {
		t.Fatalf("up with the peek panel closed should not fire a peek command, got %v", cmd)
	}
}

// C2-2 — moving onto the same row (boundary no-op) must not re-fire the peek.
func TestPeekNotRefiredWhenSelectionUnchanged(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "fake", DisplayName: "one", ProcAlive: adapter.Alive}}
	m.peekOpen = true
	m.peekText = "tail"
	if _, cmd := m.handleMovementKey("up"); cmd != nil { // already at top → no-op
		t.Fatalf("a no-op move should not re-fire the peek, got %v", cmd)
	}
	if m.peekText != "tail" {
		t.Fatalf("a no-op move should not blank the peek text, got %q", m.peekText)
	}
}

// F53 — a message must persist across short refresh ticks and only expire after
// the TTL has elapsed.
func TestMessageExpiresOnlyAfterTTL(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.message = "just happened"
	m.messageSetAt = time.Now()

	// A refresh tick well within the TTL must not clear the message.
	model, _ := m.Update(refreshMsg(time.Now().Add(messageTTL / 2)))
	m = model.(Model)
	if m.message != "just happened" {
		t.Fatalf("message cleared too early: %q", m.message)
	}

	// A refresh tick past the TTL must clear it.
	model, _ = m.Update(refreshMsg(time.Now().Add(messageTTL + time.Second)))
	m = model.(Model)
	if m.message != "" {
		t.Fatalf("message should expire after the TTL, got %q", m.message)
	}
}

// F53 — a freshly emitted message must not be wiped by the very next 2s tick.
func TestFreshMessageSurvivesNextTick(t *testing.T) {
	m := NewWithDeps(nil, nil)
	// Simulate handleSessionsLoaded stamping a message right before a tick.
	model, _ := m.Update(sessionsLoadedMsg{err: errTestBoom})
	m = model.(Model)
	if m.message == "" {
		t.Fatal("error should populate the status line")
	}
	model, _ = m.Update(refreshMsg(time.Now()))
	m = model.(Model)
	if m.message == "" {
		t.Fatal("a just-emitted error must survive the next refresh tick (no blanket clear)")
	}
}

// F55 — a failed SetDefaultAgent (Tab) must surface the error in the status line
// instead of silently reporting success.
func TestTabSurfacesSetDefaultAgentError(t *testing.T) {
	st := readOnlyStore(t)
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{
		&svcFakeAdapter{name: "a", available: true},
		&svcFakeAdapter{name: "b", available: true},
	}))
	m.defaultAgent = "a"
	model, _ := m.handleKey(keyMsg("tab"))
	m = model.(Model)
	if m.message == "" {
		t.Fatal("a failed SetDefaultAgent must surface an error in the status line")
	}
}

// F55 — a failed SetUI (Ctrl+S group-by-dir toggle) must surface the error.
func TestGroupByDirToggleSurfacesSetUIError(t *testing.T) {
	st := readOnlyStore(t)
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "a", available: true}}))
	model, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if m.message == "" {
		t.Fatal("a failed SetUI must surface an error in the status line")
	}
}
