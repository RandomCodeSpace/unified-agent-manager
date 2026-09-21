package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
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
	if !m.confirmLatest || !strings.Contains(strings.ToLower(m.View().Content), "several retained conversations") || !strings.Contains(m.View().Content, "fake") || !strings.Contains(m.View().Content, "chosen") {
		t.Fatalf("missing provider/session-specific confirmation: %s", m.View().Content)
	}
	declined, cmd := m.handleKey(keyMsg("esc"))
	if cmd != nil || declined.(Model).confirmLatest || fake.resumed != nil {
		t.Fatal("declining ambiguity confirmation mutated provider")
	}

	m, fake = ambiguousTUIModel(t, false)
	model, _ = m.Update(m.handleSpaceKey(" ")())
	m = model.(Model)
	// A refresh/reorder replaces the row under the cursor while the modal is open.
	m.sessions = []adapter.Session{{ID: "22222222", AgentType: "fake", DisplayName: "other", ProcAlive: adapter.Exited}, {ID: "11111111", AgentType: "fake", DisplayName: "chosen", ProcAlive: adapter.Exited}}
	m.selected = 0
	accepted, follow := m.handleKey(keyMsg("y"))
	m, retry := settleConfirmation(t, accepted.(Model), follow)
	if retry == nil || m.confirmLatest {
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
				_, model, restartFollow := m.handleModalKey(keyMsg("r"), "r")
				m, cmd = settleConfirmation(t, model.(Model), restartFollow)
			}
			model, _ := m.Update(cmd())
			m = model.(Model)
			if !m.confirmLatest {
				t.Fatalf("%s did not prompt: message=%q", action, m.message)
			}
			if action == "restart" && (fake.stopped || fake.resumed != nil) {
				t.Fatal("ambiguous restart preflight was destructive")
			}
			model, follow := m.handleKey(keyMsg("y"))
			m, retry := settleConfirmation(t, model.(Model), follow)
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

func TestSortSessionsKeepsManualOrderAcrossLifecycle(t *testing.T) {
	// A session keeps its door when it stops: liveness is displayed on the
	// stamp, never encoded in the order.
	sessions := lifecycleFixtures()
	SortSessions(sessions)
	if got := strings.Join(sessionIDs(sessions), ","); got != "clean,crash,signal,explicit,run" {
		t.Fatalf("manual order = %v", got)
	}
}

func TestRunningStoppedLabelsAcrossResponsiveAndGroupedRenderers(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		for _, size := range []struct{ width, height int }{{120, 40}, {80, 30}, {44, 20}} {
			t.Run(strings.Join([]string{boolName(grouped), string(rune(size.width))}, "/"), func(t *testing.T) {
				m := Model{width: size.width, height: size.height, sizeKnown: true, sessions: lifecycleFixtures(), groupByDir: grouped}
				SortSessions(m.sessions)
				out := m.View().Content
				if strings.Contains(out, "ACTIVE") || strings.Contains(out, "CLOSED") || strings.Contains(strings.ToLower(out), "closed") {
					t.Fatalf("legacy lifecycle wording remains: %s", out)
				}
				// Every geometry keeps literal lifecycle words.
				for _, word := range []string{"Running", "Stopped", "Failed"} {
					if !strings.Contains(out, word) {
						t.Fatalf("dashboard at %dx%d missing status word %q: %s", size.width, size.height, word, out)
					}
				}
			})
		}
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
		{name: "clean", sess: adapter.Session{DisplayName: "clean", ProcAlive: adapter.Exited, ExitCode: exitCode(0)}, glyph: "○", forbid: "Failed"},
		{name: "crash", sess: adapter.Session{DisplayName: "crash", ProcAlive: adapter.Exited, ExitCode: exitCode(23)}, glyph: "✕", detail: "Failed 23"},
		{name: "signal", sess: adapter.Session{DisplayName: "signal", ProcAlive: adapter.Exited, ExitCode: exitCode(-1)}, glyph: "✕", detail: "Failed signal"},
		{name: "explicit", sess: adapter.Session{DisplayName: "explicit", ProcAlive: adapter.Exited, ExitCode: exitCode(-1), Closed: true}, glyph: "○", forbid: "signal"},
	}
	var m Model
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, _ := m.stampLines(stamp{sess: tc.sess}, stampMinWidth)
			plain := ansi.Strip(line)
			if !strings.Contains(plain, tc.glyph) || (tc.detail != "" && !strings.Contains(plain, tc.detail)) || (tc.forbid != "" && strings.Contains(plain, tc.forbid)) {
				t.Fatalf("stamp = %q", plain)
			}
			if ansi.StringWidth(plain) != stampMinWidth {
				t.Fatalf("stamp is %d cells, want %d: %q", ansi.StringWidth(plain), stampMinWidth, plain)
			}
		})
	}
	long := adapter.Session{DisplayName: strings.Repeat("界", 40), ProcAlive: adapter.Exited, ExitCode: exitCode(123456)}
	line, _ := m.stampLines(stamp{sess: long}, stampMinWidth)
	plain := ansi.Strip(line)
	if ansi.StringWidth(plain) != stampMinWidth || !strings.Contains(plain, "Failed 123456") {
		t.Fatalf("bounded failure detail lost: width=%d stamp=%q", ansi.StringWidth(plain), plain)
	}
}

func TestFailureDetailRidesTheStatusWord(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		status string
	}{
		{name: "crash", code: 17, status: "Failed 17"},
		{name: "signal", code: -1, status: "Failed signal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := adapter.Session{ID: tc.name, DisplayName: "provider session", Prompt: "preserve this task", ProcAlive: adapter.Exited, ExitCode: exitCode(tc.code)}
			if got := stampStatus(sess); got != tc.status {
				t.Fatalf("stampStatus = %q, want %q", got, tc.status)
			}
			var stampModel Model
			line, _ := stampModel.stampLines(stamp{sess: sess}, stampMinWidth)
			if plain := ansi.Strip(line); strings.Count(plain, tc.status) != 1 || strings.Contains(plain, sess.Prompt) {
				t.Fatalf("stamp = %q, want one %q and no prompt", plain, tc.status)
			}
			m := Model{width: 100, height: 20, sizeKnown: true, sessions: []adapter.Session{sess}}
			view := m.View().Content
			if !strings.Contains(view, tc.status) || strings.Contains(view, "exit 17") {
				t.Fatalf("dashboard must spell the failure once as a status word: %q", view)
			}
			assertViewGeometry(t, view, 100, 20)
		})
	}
}

func TestReorderAcrossRunningStoppedSwapsAndPersists(t *testing.T) {
	m := Model{sessions: []adapter.Session{
		{ID: "run", AgentType: "fake", ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "stop", AgentType: "fake", ProcAlive: adapter.Exited, SortIndex: 1},
	}, selected: 0}
	if cmd := m.moveSession(1); cmd == nil {
		t.Fatal("cross-lifecycle reorder must schedule persistence: doors do not depend on liveness")
	}
	if m.selected != 1 || !m.reorderPending || sessionIDs(m.sessions)[0] != "stop" {
		t.Fatalf("move did not swap: %+v", sessionIDs(m.sessions))
	}
}
