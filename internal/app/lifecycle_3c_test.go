package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func exitCode(n int) *int { return &n }

func lifecycleFixtures() []adapter.Session {
	return []adapter.Session{
		{ID: "run", AgentType: "fake", DisplayName: "running", Cwd: "/tmp/a", ProcAlive: adapter.Alive, SortIndex: 9},
		{ID: "clean", AgentType: "fake", DisplayName: "clean", Cwd: "/tmp/a", ProcAlive: adapter.Exited, ExitCode: exitCode(0), SortIndex: 0},
		{ID: "crash", AgentType: "fake", DisplayName: "crashed", Cwd: "/tmp/a", ProcAlive: adapter.Exited, ExitCode: exitCode(17), SortIndex: 1},
		{ID: "signal", AgentType: "fake", DisplayName: "signaled", Cwd: "/tmp/a", ProcAlive: adapter.Exited, ExitCode: exitCode(-1), SortIndex: 2},
		{ID: "explicit", AgentType: "fake", DisplayName: "explicit", Cwd: "/tmp/a", ProcAlive: adapter.Exited, ExitCode: exitCode(-1), Closed: true, SortIndex: 3},
	}
}

func ambiguousTUIModel(t *testing.T, alive bool) (Model, *svcFakeAdapter) {
	t.Helper()
	id := "11111111"
	proc := adapter.Exited
	if alive {
		proc = adapter.Alive
	}
	fake := &svcFakeAdapter{name: "fake", available: true, resumeKind: adapter.ResumeHeuristic, stopRemoves: true,
		sessions: []adapter.Session{{ID: id, AgentType: "fake", DisplayName: "chosen", SessionName: "uam-fake-11111111", Cwd: "/tmp/shared", ProcAlive: proc}}}
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", id)] = store.SessionRecord{ID: id, Agent: "fake", Name: "chosen", SessionName: "uam-fake-11111111", Workdir: "/tmp/shared", Status: store.StatusActive}
		cfg.Sessions[store.Key("fake", "22222222")] = store.SessionRecord{ID: "22222222", Agent: "fake", Name: "other", SessionName: "uam-fake-22222222", Workdir: "/tmp/shared", Status: store.StatusActive}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = append([]adapter.Session(nil), fake.sessions...)
	return m, fake
}

func TestAmbiguousSpaceConfirmationDeclineAndSnapshotAccept(t *testing.T) {
	m, fake := ambiguousTUIModel(t, false)
	msg := m.handleSpaceKey(" ")()
	model, _ := m.Update(msg)
	m = model.(Model)
	if !m.confirmLatest || !strings.Contains(strings.ToLower(m.View()), "several retained conversations") || !strings.Contains(m.View(), "fake") || !strings.Contains(m.View(), "chosen") {
		t.Fatalf("missing provider/session-specific confirmation: %s", m.View())
	}
	declined, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || declined.(Model).confirmLatest || fake.resumed != nil {
		t.Fatal("declining ambiguity confirmation mutated provider")
	}

	m, fake = ambiguousTUIModel(t, false)
	model, _ = m.Update(m.handleSpaceKey(" ")())
	m = model.(Model)
	// A refresh/reorder replaces the row under the cursor while the modal is open.
	m.sessions = []adapter.Session{{ID: "22222222", AgentType: "fake", DisplayName: "other", ProcAlive: adapter.Exited}, {ID: "11111111", AgentType: "fake", DisplayName: "chosen", ProcAlive: adapter.Exited}}
	m.selected = 0
	accepted, retry := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if retry == nil || accepted.(Model).confirmLatest {
		t.Fatal("accept did not close modal and retry")
	}
	_ = retry()
	if fake.resumed == nil || fake.resumed.ID != "11111111" {
		t.Fatalf("accept retargeted resume: %+v", fake.resumed)
	}
}

func TestAmbiguousAttachAndRestartConfirmation(t *testing.T) {
	for _, action := range []string{"attach", "restart"} {
		t.Run(action, func(t *testing.T) {
			m, fake := ambiguousTUIModel(t, action == "restart")
			var cmd tea.Cmd
			if action == "attach" {
				cmd = m.handleEnterKey()
			} else {
				m.confirmStop, m.confirmStopAgent, m.confirmStopID = true, "fake", "11111111"
				_, model, restartCmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}, "r")
				m, cmd = model.(Model), restartCmd
			}
			model, _ := m.Update(cmd())
			m = model.(Model)
			if !m.confirmLatest {
				t.Fatalf("%s did not prompt: message=%q", action, m.message)
			}
			if action == "restart" && (fake.stopped || fake.resumed != nil) {
				t.Fatal("ambiguous restart preflight was destructive")
			}
			model, retry := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			m = model.(Model)
			result := retry()
			if action == "attach" {
				attach, ok := result.(attachSpecMsg)
				if !ok || attach.err != nil {
					t.Fatalf("attach retry = %#v", result)
				}
			} else if loaded, ok := result.(sessionsLoadedMsg); !ok || loaded.err != nil {
				t.Fatalf("restart retry = %#v", result)
			}
			if fake.resumed == nil || fake.resumed.ID != "11111111" {
				t.Fatalf("%s retry target=%+v", action, fake.resumed)
			}
		})
	}
}

func TestExactAndUniqueResumeDoNotPrompt(t *testing.T) {
	for _, kind := range []adapter.ResumeKind{adapter.ResumeExact, adapter.ResumeHeuristic} {
		m, fake := ambiguousTUIModel(t, false)
		fake.resumeKind = kind
		if kind == adapter.ResumeHeuristic {
			if err := m.service.Store.Update(func(cfg *store.Config) error { delete(cfg.Sessions, store.Key("fake", "22222222")); return nil }); err != nil {
				t.Fatal(err)
			}
		}
		msg := m.handleSpaceKey(" ")()
		if loaded, ok := msg.(sessionsLoadedMsg); !ok || loaded.err != nil || errors.Is(loaded.err, ErrAmbiguousResume) {
			t.Fatalf("one-step resume result = %#v", msg)
		}
		if fake.resumed == nil {
			t.Fatal("one-step resume did not run")
		}
	}
}

func TestSortSessionsPartitionsByRunningBeforeStoppedRegardlessOfClosed(t *testing.T) {
	sessions := lifecycleFixtures()
	SortSessions(sessions)
	if sessions[0].ID != "run" {
		t.Fatalf("first session = %q, want running session; order=%v", sessions[0].ID, sessionIDs(sessions))
	}
	if got := sessionIDs(sessions[1:]); strings.Join(got, ",") != "clean,crash,signal,explicit" {
		t.Fatalf("stopped manual order = %v", got)
	}
}

func TestRunningStoppedLabelsAcrossResponsiveAndGroupedRenderers(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		for _, size := range []struct{ width, height int }{{120, 40}, {80, 30}, {44, 20}} {
			t.Run(strings.Join([]string{boolName(grouped), string(rune(size.width))}, "/"), func(t *testing.T) {
				m := Model{width: size.width, height: size.height, sizeKnown: true, sessions: lifecycleFixtures(), groupByDir: grouped}
				SortSessions(m.sessions)
				out := m.View()
				if strings.Contains(out, "ACTIVE") || strings.Contains(out, "CLOSED") || strings.Contains(strings.ToLower(out), "closed") {
					t.Fatalf("legacy lifecycle wording remains: %s", out)
				}
				if size.width == 44 {
					if !strings.Contains(strings.ToLower(out), "stopped") {
						t.Fatalf("compact view must expose stopped count: %s", out)
					}
				} else if !strings.Contains(out, "RUNNING") || !strings.Contains(out, "STOPPED") {
					t.Fatalf("responsive view missing lifecycle groups: %s", out)
				}
			})
		}
	}

	m := Model{width: 100, sessions: lifecycleFixtures()}
	SortSessions(m.sessions)
	out := m.renderTable()
	if !strings.Contains(out, "RUNNING") || !strings.Contains(out, "STOPPED") || strings.Contains(out, "ACTIVE") || strings.Contains(out, "CLOSED") {
		t.Fatalf("unbounded table lifecycle labels: %s", out)
	}
}

func TestGroupedLifecycleHeadingsDoNotRepeatAcrossPinPartitions(t *testing.T) {
	m := Model{width: 80, height: 30, sizeKnown: true, groupByDir: true, sessions: []adapter.Session{
		{ID: "rp", AgentType: "fake", Cwd: "/tmp/p", ProcAlive: adapter.Alive, Pinned: true},
		{ID: "ru", AgentType: "fake", Cwd: "/tmp/u", ProcAlive: adapter.Alive},
		{ID: "sp", AgentType: "fake", Cwd: "/tmp/p", ProcAlive: adapter.Exited, Pinned: true},
		{ID: "su", AgentType: "fake", Cwd: "/tmp/u", ProcAlive: adapter.Exited},
	}}
	SortSessions(m.sessions)
	out := strings.Join(m.groupedSessionListLines(80, 30, LayoutStandard), "\n")
	if got := strings.Count(out, "RUNNING"); got != 1 {
		t.Fatalf("RUNNING heading count=%d, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "STOPPED"); got != 1 {
		t.Fatalf("STOPPED heading count=%d, want 1:\n%s", got, out)
	}
}

func boolName(v bool) string {
	if v {
		return "grouped"
	}
	return "plain"
}

func TestStoppedExitPresentationDistinguishesFailureAndExplicitStop(t *testing.T) {
	cases := []struct {
		name   string
		sess   adapter.Session
		glyph  string
		detail string
		forbid string
	}{
		{name: "clean", sess: adapter.Session{DisplayName: "clean", ProcAlive: adapter.Exited, ExitCode: exitCode(0)}, glyph: "◦", forbid: "exit 0"},
		{name: "crash", sess: adapter.Session{DisplayName: "crash", ProcAlive: adapter.Exited, ExitCode: exitCode(23)}, glyph: "!", detail: "exit 23"},
		{name: "signal", sess: adapter.Session{DisplayName: "signal", ProcAlive: adapter.Exited, ExitCode: exitCode(-1)}, glyph: "!", detail: "signal"},
		{name: "explicit", sess: adapter.Session{DisplayName: "explicit", ProcAlive: adapter.Exited, ExitCode: exitCode(-1), Closed: true}, glyph: "◦", forbid: "signal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := renderRow(tc.sess, false, 14, 22, true)
			if !strings.Contains(row, tc.glyph) || (tc.detail != "" && !strings.Contains(row, tc.detail)) || (tc.forbid != "" && strings.Contains(row, tc.forbid)) {
				t.Fatalf("row = %q", row)
			}
			if ansi.StringWidth(ansi.Truncate(row, 44, "…")) > 44 {
				t.Fatalf("row wider than 44: %q", row)
			}
		})
	}
	long := adapter.Session{DisplayName: strings.Repeat("界", 40), ProcAlive: adapter.Exited, ExitCode: exitCode(123456)}
	row := ansi.Truncate(renderRow(long, false, 20, 17, true), 44, "…")
	if ansi.StringWidth(row) > 44 || !strings.Contains(row, "exit 123456") {
		t.Fatalf("bounded failure detail lost: width=%d row=%q", ansi.StringWidth(row), row)
	}
	compact := renderRow(long, false, 39, 0, false)
	if ansi.StringWidth(compact) > 44 || !strings.Contains(compact, "exit 123456") {
		t.Fatalf("compact failure detail lost: width=%d row=%q", ansi.StringWidth(compact), compact)
	}
}

func TestFailureDetailAppendsToPromptWithoutReplacingOrDuplicatingIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		detail string
	}{
		{name: "crash", code: 17, detail: "exit 17"},
		{name: "signal", code: -1, detail: "signal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := adapter.Session{ID: tc.name, DisplayName: "provider session", Prompt: "preserve this task", ProcAlive: adapter.Exited, ExitCode: exitCode(tc.code)}
			row := renderRow(sess, false, 14, 44, true)
			want := "preserve this task · " + tc.detail
			if !strings.Contains(row, want) || strings.Count(row, tc.detail) != 1 {
				t.Fatalf("task-column failure summary = %q, want one %q", row, want)
			}

			m := Model{width: 44, height: 20, sizeKnown: true, sessions: []adapter.Session{sess}}
			summary := strings.Join(m.selectedSummaryLines(44, LayoutCompact), "\n")
			if !strings.Contains(summary, want) || strings.Count(summary, tc.detail) != 1 {
				t.Fatalf("selected failure summary = %q, want one %q", summary, want)
			}

			compactRow := renderRow(sess, false, 39, 0, false)
			if strings.Contains(compactRow, sess.Prompt) || strings.Count(compactRow, tc.detail) != 1 {
				t.Fatalf("compact name-only row should show detail once, got %q", compactRow)
			}

			sess.Prompt = strings.Repeat("long task ", 20)
			boundedRow := renderRow(sess, false, 14, 28, true)
			if !strings.Contains(boundedRow, " · "+tc.detail) || ansi.StringWidth(boundedRow) > 50 {
				t.Fatalf("bounded task column lost failure suffix: width=%d row=%q", ansi.StringWidth(boundedRow), boundedRow)
			}
			m.sessions[0] = sess
			boundedSummary := strings.Join(m.selectedSummaryLines(44, LayoutCompact), "\n")
			if !strings.Contains(boundedSummary, " · "+tc.detail) || ansi.StringWidth(strings.Split(boundedSummary, "\n")[1]) > 44 {
				t.Fatalf("bounded selected summary lost failure suffix: %q", boundedSummary)
			}

			sess.Prompt = "already recorded · " + tc.detail
			if got := boundedTaskSummary(sess, 44); strings.Count(got, tc.detail) != 1 {
				t.Fatalf("pre-suffixed prompt duplicated failure detail: %q", got)
			}
		})
	}
}

func TestReorderRejectsAcrossRunningStoppedWithoutSideEffects(t *testing.T) {
	m := Model{sessions: []adapter.Session{
		{ID: "run", AgentType: "fake", ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "stop", AgentType: "fake", ProcAlive: adapter.Exited, SortIndex: 1},
	}, selected: 0, peekOpen: true, peekTargetID: "run", peekText: "tail"}
	if cmd := m.moveSession(1); cmd != nil {
		t.Fatal("cross-lifecycle reorder scheduled persistence")
	}
	if m.selected != 0 || m.reorderPending || m.reorderSeq != 0 || m.peekTargetID != "run" || m.peekText != "tail" || sessionIDs(m.sessions)[0] != "run" {
		t.Fatalf("rejected move mutated state: %+v", m)
	}
}
