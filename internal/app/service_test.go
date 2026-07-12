package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/claude"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/codex"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/omp"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type svcFakeAdapter struct {
	name       string
	sessions   []adapter.Session
	available  bool
	stopped    bool
	stoppedID  string
	peekedID   string
	attachedID string
	replied    string
	resumed    *adapter.ResumeRequest
	// F04: simulate a failed kill (stopErr) and a still-live pane (alive). The
	// fake implements adapter.HasSessionAdapter, returning alive from HasSession.
	stopErr error
	alive   bool
	// F12: simulate a per-adapter List failure so liveSessions can be tested for
	// logging-then-continue (one bad adapter must not blank the dashboard).
	listErr error
	// stopRemoves makes Stop drop the live sessions, mirroring the real
	// backend where Kill returns only after the host is fully gone — so a
	// restart's resume step sees the session as dead.
	stopRemoves bool
	resumeKind  adapter.ResumeKind
	resumeHook  func(adapter.ResumeRequest)
	resumeFunc  func(adapter.ResumeRequest) (adapter.Session, error)
}

func (f *svcFakeAdapter) ResumeKind(adapter.ResumeRequest) adapter.ResumeKind {
	if f.resumeKind == "" {
		return adapter.ResumeHeuristic
	}
	return f.resumeKind
}

func (f *svcFakeAdapter) Name() string        { return f.name }
func (f *svcFakeAdapter) DisplayName() string { return f.name }
func (f *svcFakeAdapter) Available() (bool, string) {
	if f.available {
		return true, ""
	}
	return false, "missing"
}
func (f *svcFakeAdapter) Dispatch(ctx adapter.Context, req adapter.DispatchRequest) (adapter.Session, error) {
	if req.Prompt == "fail" {
		return adapter.Session{}, errors.New("fail")
	}
	return adapter.Session{ID: "12345678", AgentType: f.name, CommandAlias: req.CommandAlias, DisplayName: firstNonEmpty(req.Name, req.Prompt, "untitled"), Prompt: req.Prompt, Cwd: firstNonEmpty(req.Cwd, "/tmp"), SessionName: "uam-" + f.name + "-12345678", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}, nil
}
func (f *svcFakeAdapter) Resume(ctx adapter.Context, req adapter.ResumeRequest) (adapter.Session, error) {
	f.resumed = &req
	if f.resumeHook != nil {
		f.resumeHook(req)
	}
	if f.resumeFunc != nil {
		return f.resumeFunc(req)
	}
	return adapter.Session{ID: req.ID, AgentType: f.name, CommandAlias: req.CommandAlias, DisplayName: req.Name, Prompt: req.Prompt, Cwd: req.Cwd, SessionName: req.SessionName, State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}, nil
}
func (f *svcFakeAdapter) List(ctx adapter.Context) ([]adapter.Session, error) {
	return f.sessions, f.listErr
}
func (f *svcFakeAdapter) Peek(ctx adapter.Context, id string) (adapter.PeekResult, error) {
	f.peekedID = id
	return adapter.PeekResult{TailText: "tail"}, nil
}
func (f *svcFakeAdapter) Reply(ctx adapter.Context, id, text string) error {
	f.replied = text
	return nil
}
func (f *svcFakeAdapter) Attach(id string) (adapter.AttachSpec, error) {
	f.attachedID = id
	return adapter.AttachSpec{Argv: []string{"echo", id}}, nil
}
func (f *svcFakeAdapter) Stop(ctx adapter.Context, id string) error {
	f.stopped = true
	f.stoppedID = id
	if f.stopRemoves {
		f.sessions = nil
	}
	return f.stopErr
}

func TestProviderExactServiceActionsDoNotCrossDuplicateIDs(t *testing.T) {
	ctx := context.Background()
	id := "same0001"
	claude := &svcFakeAdapter{name: "claude", available: true, sessions: []adapter.Session{{ID: id, AgentType: "claude", SessionName: "uam-claude-same0001", ProcAlive: adapter.Alive}}}
	codex := &svcFakeAdapter{name: "codex", available: true, sessions: []adapter.Session{{ID: id, AgentType: "codex", SessionName: "uam-codex-same0001", ProcAlive: adapter.Alive}}}
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("claude", id)] = RecordFromSession(claude.sessions[0], store.ModeYolo)
		cfg.Sessions[store.Key("codex", id)] = RecordFromSession(codex.sessions[0], store.ModeYolo)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{claude, codex}))

	found, _, err := svc.FindExact(ctx, "codex", id)
	if err != nil || found.AgentType != "codex" {
		t.Fatalf("FindExact = %+v, %v", found, err)
	}
	if err := svc.RenameExact(ctx, "codex", id, "selected codex"); err != nil {
		t.Fatal(err)
	}
	if err := svc.TogglePinExact(ctx, "codex", id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PeekExact(ctx, "codex", id); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReplyExact(ctx, "codex", id, "hello codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AttachSpecExact(ctx, "codex", id); err != nil {
		t.Fatal(err)
	}
	if err := svc.StopExact(ctx, "codex", id, false); err != nil {
		t.Fatal(err)
	}
	if claude.stopped || claude.peekedID != "" || claude.replied != "" || claude.attachedID != "" {
		t.Fatalf("claude adapter was targeted: %+v", claude)
	}
	if codex.stoppedID != id || codex.peekedID != id || codex.replied != "hello codex" || codex.attachedID != id {
		t.Fatalf("codex adapter did not receive exact actions: %+v", codex)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sessions[store.Key("claude", id)]; got.Name != id || got.Pinned || got.Status == store.StatusClosedByUser {
		t.Fatalf("claude record changed: %+v", got)
	}
	if got := cfg.Sessions[store.Key("codex", id)]; got.Name != "selected codex" || !got.Pinned || got.Status != store.StatusClosedByUser {
		t.Fatalf("codex record not changed exactly: %+v", got)
	}
}

func TestProviderExactResumeAndRestartUseSelectedAdapter(t *testing.T) {
	for _, action := range []string{"resume", "restart"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			id := "same0002"
			claude := &svcFakeAdapter{name: "claude", available: true, resumeKind: adapter.ResumeExact}
			codexSession := adapter.Session{ID: id, AgentType: "codex", SessionName: "uam-codex-same0002", Cwd: t.TempDir(), ProcAlive: adapter.Exited}
			codex := &svcFakeAdapter{name: "codex", available: true, resumeKind: adapter.ResumeExact}
			st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Update(func(cfg *store.Config) error {
				cfg.Sessions[store.Key("claude", id)] = store.SessionRecord{ID: id, Agent: "claude", SessionName: "uam-claude-same0002", Workdir: t.TempDir(), Mode: store.ModeYolo}
				cfg.Sessions[store.Key("codex", id)] = RecordFromSession(codexSession, store.ModeYolo)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{claude, codex}))
			if action == "resume" {
				err = svc.ResumeBackgroundExact(ctx, "codex", id)
			} else {
				err = svc.RestartExact(ctx, "codex", id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if claude.resumed != nil || codex.resumed == nil || codex.resumed.ID != id {
				t.Fatalf("resume crossed provider: claude=%+v codex=%+v", claude.resumed, codex.resumed)
			}
		})
	}
}
func (f *svcFakeAdapter) HasSession(ctx adapter.Context, id string) bool { return f.alive }

func TestServiceWorkflow(t *testing.T) {
	svc, fake := newWorkflowService(t)
	sess := assertWorkflowDispatch(t, svc)
	list := assertWorkflowLoadAndFind(t, svc, sess.ID)
	assertWorkflowMetadataMutations(t, svc, list)
	assertWorkflowAdapterActions(t, svc, fake)
}

func newWorkflowService(t *testing.T) (*Service, *svcFakeAdapter) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "live0001", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", SessionName: "uam-fake-live0001", State: adapter.Active, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/1", Number: 1, Status: adapter.PROpen}}}}
	return NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake})), fake
}

func assertWorkflowDispatch(t *testing.T, svc *Service) adapter.Session {
	t.Helper()
	sess, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" {
		t.Fatal("empty id")
	}
	return sess
}

func assertWorkflowLoadAndFind(t *testing.T, svc *Service, idPrefix string) []adapter.Session {
	t.Helper()
	list, _, err := svc.LoadSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 {
		t.Fatalf("list=%+v", list)
	}
	found, _, err := svc.Find(context.Background(), idPrefix[:4])
	if err != nil || found.DisplayName != "hello" {
		t.Fatalf("found=%+v err=%v", found, err)
	}
	return list
}

func TestServiceFindRejectsAmbiguousPrefix(t *testing.T) {
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{
		{ID: "abc12345", AgentType: "fake", DisplayName: "one", SessionName: "uam-fake-abc12345", State: adapter.Active, CreatedAt: time.Now()},
		{ID: "abc67890", AgentType: "fake", DisplayName: "two", SessionName: "uam-fake-abc67890", State: adapter.Active, CreatedAt: time.Now()},
	}}
	svc := NewService(nil, adapter.NewRegistry([]adapter.AgentAdapter{fake}))

	_, _, err := svc.Find(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "abc") {
		t.Fatalf("Find ambiguous prefix error = %v", err)
	}
}

func TestServiceFindExactMatchWinsOverAmbiguousPrefix(t *testing.T) {
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{
		{ID: "abc", AgentType: "fake", DisplayName: "exact-id", SessionName: "uam-fake-exact", State: adapter.Active, CreatedAt: time.Now()},
		{ID: "abc67890", AgentType: "fake", DisplayName: "prefix-id", SessionName: "uam-fake-prefix", State: adapter.Active, CreatedAt: time.Now()},
		{ID: "def12345", AgentType: "fake", DisplayName: "exact-tmux", SessionName: "uam-fake-abc", State: adapter.Active, CreatedAt: time.Now()},
		{ID: "def67890", AgentType: "fake", DisplayName: "prefix-tmux", SessionName: "uam-fake-abc-extra", State: adapter.Active, CreatedAt: time.Now()},
	}}
	svc := NewService(nil, adapter.NewRegistry([]adapter.AgentAdapter{fake}))

	found, _, err := svc.Find(context.Background(), "abc")
	if err != nil || found.DisplayName != "exact-id" {
		t.Fatalf("Find exact ID = %+v err=%v", found, err)
	}
	found, _, err = svc.Find(context.Background(), "uam-fake-abc")
	if err != nil || found.DisplayName != "exact-tmux" {
		t.Fatalf("Find exact tmux session = %+v err=%v", found, err)
	}
}

func assertWorkflowMetadataMutations(t *testing.T, svc *Service, list []adapter.Session) {
	t.Helper()
	if err := svc.Rename(context.Background(), "1234", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := svc.TogglePin(context.Background(), "1234"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUI(func(ui *store.UISettings) { ui.GroupByDir = true }); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateSortOrder(list); err != nil {
		t.Fatal(err)
	}
}

func assertWorkflowAdapterActions(t *testing.T, svc *Service, fake *svcFakeAdapter) {
	t.Helper()
	if p, err := svc.Peek(context.Background(), "live"); err != nil || p.TailText != "tail" {
		t.Fatalf("peek=%+v err=%v", p, err)
	}
	if err := svc.Reply(context.Background(), "live", "yes"); err != nil || fake.replied != "yes" {
		t.Fatalf("reply %q %v", fake.replied, err)
	}
	if spec, err := svc.AttachSpec(context.Background(), "live"); err != nil || len(spec.Argv) == 0 {
		t.Fatalf("attach=%+v err=%v", spec, err)
	}
	if err := svc.Stop(context.Background(), "live", true); err != nil || !fake.stopped {
		t.Fatalf("stop %v stopped=%v", err, fake.stopped)
	}
}

func TestServicePrintListAndErrors(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	unsafeName := "safe\x1b]52;c;YQ==\x07name"
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{
		ID: "live0001", AgentType: "fake", DisplayName: unsafeName, Cwd: "/tmp/evil\x1b[2Jrepo",
		SessionName: "uam-fake-live0001", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(),
	}}}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "missing", "", "x", "", ""); err == nil {
		t.Fatal("want missing agent error")
	}
	if _, _, err := svc.Find(context.Background(), "missing"); err == nil {
		t.Fatal("want find error")
	}
	out := captureStdout(t, func() {
		if err := svc.PrintList(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "[") || !strings.Contains(out, `\u001b]52`) {
		t.Fatalf("json out=%q", out)
	}
	out = captureStdout(t, func() {
		if err := svc.PrintList(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "\x1b") || !strings.Contains(out, "safename") || !strings.Contains(out, "/tmp/evilrepo") {
		t.Fatalf("plain text output was not sanitized: %q", out)
	}
}

func TestServicePersistsPromptAndReportsDeadTmuxRecord(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "bugfix", "fix parser", "/tmp/project", ""); err != nil {
		t.Fatal(err)
	}
	sessions, _, err := svc.LoadSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions=%+v", sessions)
	}
	if sessions[0].DisplayName != "bugfix" || sessions[0].Prompt != "fix parser" || sessions[0].ProcAlive != adapter.Exited {
		t.Fatalf("stored dead session = %+v", sessions[0])
	}
}

func TestServicePersistsAndMergesCommandAlias(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "12345678", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", SessionName: "uam-fake-12345678", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}}}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamedWithAlias(context.Background(), "fake", "ghcp", "bugfix", "fix parser", "/tmp/project", "yolo"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.Load()
	if got := cfg.Sessions[store.Key("fake", "12345678")].CommandAlias; got != "ghcp" {
		t.Fatalf("persisted alias = %q, want ghcp", got)
	}
	sessions, _, err := svc.LoadSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].CommandAlias != "ghcp" || sessions[0].AgentType != "fake" {
		t.Fatalf("merged session alias/type = %+v", sessions)
	}
}

func TestAttachSpecResumesDeadSessionFromMetadata(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	created := time.Now().Add(-time.Hour)
	if err := st.Save(store.Config{
		SchemaVersion: store.CurrentSchemaVersion,
		DefaultAgent:  "fake",
		Sessions: map[string]store.SessionRecord{
			"fake:abc12345": {ID: "abc12345-dead-beef-cafe-0123456789ab", Agent: "fake", CommandAlias: "ghcp", Name: "bugfix", Prompt: "fix parser", Mode: store.ModeYolo, Workdir: "/tmp/project", SessionName: "uam-fake-abc12345", CreatedAt: created},
		},
	}); err != nil {
		t.Fatal(err)
	}

	spec, err := svc.AttachSpec(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("AttachSpec: %v", err)
	}
	if len(spec.Argv) == 0 {
		t.Fatal("empty attach spec")
	}
	if fake.resumed == nil {
		t.Fatal("dead metadata-backed session should be resumed before attach")
	}
	if fake.resumed.ID != "abc12345-dead-beef-cafe-0123456789ab" || fake.resumed.Name != "bugfix" || fake.resumed.CommandAlias != "ghcp" || fake.resumed.Prompt != "fix parser" || fake.resumed.Cwd != "/tmp/project" || fake.resumed.Mode != "yolo" || fake.resumed.SessionName != "uam-fake-abc12345" {
		t.Fatalf("resume metadata = %+v", fake.resumed)
	}
}

func TestSortSessionsAndRecord(t *testing.T) {
	now := time.Now()
	sessions := []adapter.Session{{ID: "dead", ProcAlive: adapter.Exited, CreatedAt: now}, {ID: "live", ProcAlive: adapter.Alive, CreatedAt: now}, {ID: "p", ProcAlive: adapter.Exited, Pinned: true, CreatedAt: now}}
	SortSessions(sessions)
	if sessions[0].ID != "p" || sessions[1].ID != "live" {
		t.Fatalf("order=%+v", sessions)
	}
	rec := RecordFromSession(adapter.Session{ID: "id", AgentType: "fake", CommandAlias: "ghcp", Prompt: "do work", Cwd: "/tmp", SessionName: "tm", CreatedAt: now}, "")
	if rec.Mode != store.ModeYolo || rec.Name != "id" || rec.CommandAlias != "ghcp" || rec.Prompt != "do work" {
		t.Fatalf("rec=%+v", rec)
	}
	if rec.Status != store.StatusActive {
		t.Fatalf("new records should default to StatusActive, got %q", rec.Status)
	}
}

func TestSortSessionsPushesClosedToBottom(t *testing.T) {
	now := time.Now()
	// Closed sessions belong below everything else, even pinned ones, so the
	// Active group renders without interruption at the top.
	sessions := []adapter.Session{
		{ID: "closed-pinned", Pinned: true, Closed: true, ProcAlive: adapter.Exited, CreatedAt: now},
		{ID: "live", ProcAlive: adapter.Alive, CreatedAt: now},
		{ID: "stopped-active", ProcAlive: adapter.Exited, CreatedAt: now},
	}
	SortSessions(sessions)
	if sessions[0].ID != "live" || sessions[1].ID != "stopped-active" || sessions[2].ID != "closed-pinned" {
		t.Fatalf("order=%+v", sessions)
	}
}

func TestSortSessionsIsDeterministicForTiedRows(t *testing.T) {
	now := time.Now()
	// Three live rows that tie on every field except agent+id (same creation
	// second is the common real-world case, since tmux reports whole seconds).
	// The sort must produce the same order regardless of input order, otherwise
	// map-iteration order leaks through the stable sort and rows flap on every
	// refresh tick.
	mk := func() []adapter.Session {
		return []adapter.Session{
			{ID: "c", AgentType: "codex", ProcAlive: adapter.Alive, CreatedAt: now},
			{ID: "a", AgentType: "claude", ProcAlive: adapter.Alive, CreatedAt: now},
			{ID: "b", AgentType: "claude", ProcAlive: adapter.Alive, CreatedAt: now},
		}
	}
	want := []string{"claude/a", "claude/b", "codex/c"}
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}} {
		in := mk()
		shuffled := []adapter.Session{in[order[0]], in[order[1]], in[order[2]]}
		SortSessions(shuffled)
		for i, w := range want {
			if got := shuffled[i].AgentType + "/" + shuffled[i].ID; got != w {
				t.Fatalf("input order %v: position %d = %q, want %q", order, i, got, w)
			}
		}
	}
}

func TestStopSoftCloseFlagsRecordClosedByUser(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	// Soft-close (remove=false) must keep the record and flag it as closed.
	if err := svc.Stop(context.Background(), "12345678", false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !fake.stopped {
		t.Fatal("adapter Stop was not invoked")
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", "12345678")]
	if !ok {
		t.Fatalf("record removed unexpectedly on soft close: %+v", cfg.Sessions)
	}
	if rec.Status != store.StatusClosedByUser {
		t.Fatalf("status = %q, want %q", rec.Status, store.StatusClosedByUser)
	}
}

func TestStopRemoveDeletesRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), "12345678", true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sessions[store.Key("fake", "12345678")]; ok {
		t.Fatalf("record should be gone after Stop(remove=true): %+v", cfg.Sessions)
	}
}

// F04 — a failed adapter kill on a still-live pane must NOT delete/flag the
// record: the session is alive and the record is the only handle to kill it.
func TestStopRemoveKeepsRecordWhenKillFailsAndPaneAlive(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, stopErr: errors.New("kill boom"), alive: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	err = svc.Stop(context.Background(), "12345678", true)
	if err == nil {
		t.Fatal("Stop should surface the kill failure when the pane is still alive")
	}
	cfg, _ := st.Load()
	if _, ok := cfg.Sessions[store.Key("fake", "12345678")]; !ok {
		t.Fatalf("record must survive when kill fails and pane is alive: %+v", cfg.Sessions)
	}
}

func TestStopSoftCloseKeepsRecordActiveWhenKillFailsAndPaneAlive(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, stopErr: errors.New("kill boom"), alive: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), "12345678", false); err == nil {
		t.Fatal("soft Stop should surface the kill failure when the pane is still alive")
	}
	cfg, _ := st.Load()
	rec := cfg.Sessions[store.Key("fake", "12345678")]
	if rec.Status == store.StatusClosedByUser {
		t.Fatalf("record must not be flagged closed when the live pane survived the failed kill: %+v", rec)
	}
}

// F04 trap — if the kill fails but the pane is already GONE, Stop must still
// clean the record up (preserves `uam rm` cleanup of already-dead sessions).
func TestStopRemoveDeletesRecordWhenKillFailsButPaneGone(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, stopErr: errors.New("no such session"), alive: false}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), "12345678", true); err != nil {
		t.Fatalf("Stop should succeed when the pane is already gone: %v", err)
	}
	cfg, _ := st.Load()
	if _, ok := cfg.Sessions[store.Key("fake", "12345678")]; ok {
		t.Fatalf("record should be removed when pane is gone: %+v", cfg.Sessions)
	}
}

func TestStopSoftCloseFlagsRecordWhenKillFailsButPaneGone(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, stopErr: errors.New("no such session"), alive: false}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), "12345678", false); err != nil {
		t.Fatalf("soft Stop should succeed when the pane is already gone: %v", err)
	}
	cfg, _ := st.Load()
	if cfg.Sessions[store.Key("fake", "12345678")].Status != store.StatusClosedByUser {
		t.Fatalf("record should be flagged closed when pane is gone: %+v", cfg.Sessions[store.Key("fake", "12345678")])
	}
}

func TestNotifyClosedMarksMatchingRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}

	if err := svc.NotifyClosed("uam-fake-12345678"); err != nil {
		t.Fatalf("NotifyClosed: %v", err)
	}
	cfg, _ := st.Load()
	if cfg.Sessions[store.Key("fake", "12345678")].Status != store.StatusClosedByUser {
		t.Fatalf("status = %q", cfg.Sessions[store.Key("fake", "12345678")].Status)
	}

	// Idempotent: calling again leaves the record untouched.
	if err := svc.NotifyClosed("uam-fake-12345678"); err != nil {
		t.Fatalf("NotifyClosed (second call): %v", err)
	}

	// No-op when the tmux name does not match any record (e.g., race with rm).
	if err := svc.NotifyClosed("uam-fake-unknown"); err != nil {
		t.Fatalf("NotifyClosed unknown: %v", err)
	}

	// No-op when the tmux name is empty (e.g., tmux substitution failure).
	if err := svc.NotifyClosed(""); err != nil {
		t.Fatalf("NotifyClosed empty: %v", err)
	}
}

// F03 — when persisting a freshly dispatched session fails, the live session
// must still be returned (advisory error), and the adapter must NOT be told to
// Stop it (the agent is running; killing it on a persist hiccup loses work).
func TestDispatchReturnsLiveSessionOnPersistFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only dir is not enforced for root")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	// First dispatch creates the lock + config files.
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "warmup", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	// Make the config dir read-only so the next store write (the tmp-file create)
	// fails inside Store.Update.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	sess, err := svc.DispatchNamed(context.Background(), "fake", "second", "do work", "/tmp", "yolo")
	if err == nil {
		t.Fatal("expected an advisory persist error")
	}
	if !strings.Contains(err.Error(), sess.ID) {
		t.Fatalf("advisory error should reference the live session id %q: %v", sess.ID, err)
	}
	if sess.ID == "" || sess.SessionName == "" {
		t.Fatalf("live session must be returned despite persist failure: %+v", sess)
	}
	if fake.stopped {
		t.Fatal("adapter.Stop must NOT be called on a persist failure — the session is live")
	}
}

// F03 characterization — the dispatch mode is honoured (not silently forced to
// yolo) when persisting the record.
func TestDispatchPersistsRequestedMode(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "do work", "/tmp", "safe"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.Load()
	if got := cfg.Sessions[store.Key("fake", "12345678")].Mode; got != store.ModeSafe {
		t.Fatalf("persisted mode = %q, want %q (dispatch must not hardcode yolo)", got, store.ModeSafe)
	}
}

// C1-2 — renaming a live session whose store record is missing at mutation time
// must backfill a full record (real ID + tmux name) before applying the field
// change, not write a zero-value phantom that can never be killed once the pane
// dies. We drive the no-record path directly through renameRecord so the test is
// independent of the refresh backfill (which would otherwise mask the bug).
func TestRenameLiveSessionWithoutStoreRecordBackfillsRecord(t *testing.T) {
	live := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "A", Cwd: "/tmp", SessionName: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	// Empty store: no record for the live session.
	if err := st.Save(store.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if err := svc.applyRecordMutation(live, func(rec *store.SessionRecord) { rec.Name = "renamed" }); err != nil {
		t.Fatalf("rename mutation: %v", err)
	}
	cfg, _ := st.Load()
	rec := cfg.Sessions[store.Key("fake", "aaaa1111")]
	if rec.ID != "aaaa1111" || rec.SessionName != "uam-fake-aaaa1111" {
		t.Fatalf("rename must backfill a full record, got %+v", rec)
	}
	if rec.Name != "renamed" {
		t.Fatalf("rename did not apply, got name %q", rec.Name)
	}
}

func TestTogglePinLiveSessionWithoutStoreRecordBackfillsRecord(t *testing.T) {
	live := adapter.Session{ID: "bbbb2222", AgentType: "fake", DisplayName: "B", Cwd: "/tmp", SessionName: "uam-fake-bbbb2222", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	if err := st.Save(store.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if err := svc.applyRecordMutation(live, func(rec *store.SessionRecord) { rec.Pinned = !rec.Pinned }); err != nil {
		t.Fatalf("toggle pin mutation: %v", err)
	}
	cfg, _ := st.Load()
	rec := cfg.Sessions[store.Key("fake", "bbbb2222")]
	if rec.ID != "bbbb2222" || rec.SessionName != "uam-fake-bbbb2222" {
		t.Fatalf("toggle pin must backfill a full record, got %+v", rec)
	}
	if !rec.Pinned {
		t.Fatalf("toggle pin did not apply, got %+v", rec)
	}
}

func TestResumeBackgroundClearsClosedStatus(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	// User closes the session (soft close), then resumes it.
	if err := svc.Stop(context.Background(), "12345678", false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Drop the live session so Find sees an exited pane, forcing resume.
	fake.sessions = nil
	if err := svc.ResumeBackground(context.Background(), "12345678"); err != nil {
		t.Fatalf("ResumeBackground: %v", err)
	}
	cfg, _ := st.Load()
	if cfg.Sessions[store.Key("fake", "12345678")].Status != store.StatusActive {
		t.Fatalf("status after resume = %q, want %q", cfg.Sessions[store.Key("fake", "12345678")].Status, store.StatusActive)
	}
	if cfg.Sessions[store.Key("fake", "12345678")].LastExitCode != nil {
		t.Fatal("successful resume must clear the previous exit code")
	}
}

func TestResumeBackgroundDoesNotEraseImmediateReplacementExit(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	oldExit, replacementExit := 2, 17
	const sessionName = "uam-fake-12345678"
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession(store.Key("fake", "12345678"), store.SessionRecord{
			ID: "12345678", Agent: "fake", Workdir: dir, SessionName: sessionName,
			Status: store.StatusActive, LastExitCode: &oldExit,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, resumeKind: adapter.ResumeExact}
	fake.resumeHook = func(adapter.ResumeRequest) {
		matched, err := st.TryRecordSessionExit(store.SessionExit{SessionName: sessionName, ExitCode: replacementExit})
		if err != nil || !matched {
			t.Fatalf("record immediate replacement exit: matched=%v err=%v", matched, err)
		}
	}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))

	if err := svc.ResumeBackground(context.Background(), "12345678"); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", "12345678")]
	if rec.LastExitCode == nil || *rec.LastExitCode != replacementExit {
		t.Fatalf("exit code after immediate replacement failure=%v, want %d", rec.LastExitCode, replacementExit)
	}
	if got := deadSessionFromRecord(rec, time.Now()).State; got != adapter.Failed {
		t.Fatalf("dead replacement state=%q, want %q", got, adapter.Failed)
	}
}

func TestFailedConcurrentResumeCannotRollbackSuccessfulResume(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	oldExit := 9
	const sessionName = "uam-fake-12345678"
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession(store.Key("fake", "12345678"), store.SessionRecord{
			ID: "12345678", Agent: "fake", Workdir: dir, SessionName: sessionName,
			Status: store.StatusClosedByUser, LastExitCode: &oldExit,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	calls := 0
	var callsMu sync.Mutex
	fake := &svcFakeAdapter{name: "fake", available: true, resumeKind: adapter.ResumeExact}
	fake.resumeFunc = func(req adapter.ResumeRequest) (adapter.Session, error) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(firstEntered)
			<-releaseFirst
			return adapter.Session{}, errors.New("first launch failed")
		}
		return adapter.Session{ID: req.ID, AgentType: "fake", Cwd: req.Cwd, SessionName: req.SessionName, State: adapter.Active, ProcAlive: adapter.Alive}, nil
	}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))

	firstDone := make(chan error, 1)
	go func() { firstDone <- svc.ResumeBackground(context.Background(), "12345678") }()
	<-firstEntered
	if err := svc.ResumeBackground(context.Background(), "12345678"); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	close(releaseFirst)
	if err := <-firstDone; err == nil {
		t.Fatal("first resume unexpectedly succeeded")
	}

	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", "12345678")]
	if rec.Status != store.StatusActive || rec.LastExitCode != nil {
		t.Fatalf("winning resume lifecycle overwritten: status=%q exit=%v", rec.Status, rec.LastExitCode)
	}
}

func TestHeuristicResumeRejectsAmbiguousWorkspaceUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if err := st.Update(func(cfg *store.Config) error {
		for _, id := range []string{"11111111", "22222222"} {
			cfg.PutSession(store.Key("fake", id), store.SessionRecord{ID: id, Agent: "fake", Name: id, Workdir: "/tmp/project", SessionName: "uam-fake-" + id, Status: store.StatusActive})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeBackground(context.Background(), "11111111"); !errors.Is(err, ErrAmbiguousResume) {
		t.Fatalf("resume error=%v, want ErrAmbiguousResume", err)
	}
	if fake.resumed != nil {
		t.Fatal("ambiguous heuristic resume must fail before provider launch")
	}
	if err := svc.ResumeBackgroundWithOptions(context.Background(), "11111111", ResumeOptions{AllowLatest: true}); err != nil {
		t.Fatalf("allow-latest resume: %v", err)
	}
	if fake.resumed == nil {
		t.Fatal("allow-latest must launch heuristic resume")
	}
}

func TestProductionProviderResumeKindMatrixThroughAmbiguityGuard(t *testing.T) {
	tests := []struct {
		name, provider, providerID string
		newAdapter                 func() adapter.AgentAdapter
		isolatedOMP                bool
		wantAmbiguous              bool
	}{
		{"opencode exact", "opencode", "ses_known123", func() adapter.AgentAdapter { return opencode.New(nil) }, false, false},
		{"opencode fallback", "opencode", "", func() adapter.AgentAdapter { return opencode.New(nil) }, false, true},
		{"claude exact", "claude", "abc12345-dead-beef-cafe-0123456789ab", func() adapter.AgentAdapter { return claude.New(nil) }, false, false},
		{"claude fallback", "claude", "", func() adapter.AgentAdapter { return claude.New(nil) }, false, true},
		{"omp isolated", "omp", "", func() adapter.AgentAdapter { return omp.New(nil) }, true, false},
		{"omp legacy", "omp", "", func() adapter.AgentAdapter { return omp.New(nil) }, false, true},
		{"codex remains heuristic", "codex", "legacy-value", func() adapter.AgentAdapter { return codex.New(nil) }, false, true},
		{"copilot derives exact UAM name", "copilot", "", func() adapter.AgentAdapter { return copilot.New(nil) }, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			id, other := "11111111-dead-beef-cafe-0123456789ab", "22222222-dead-beef-cafe-0123456789ab"
			if tt.isolatedOMP {
				dir := filepath.Join(state, "uam", "providers", "omp", id)
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				for path := filepath.Join(state, "uam"); path != dir; {
					if err := os.Chmod(path, 0o700); err != nil {
						t.Fatal(err)
					}
					next := filepath.Join(path, strings.Split(strings.TrimPrefix(dir, path+string(os.PathSeparator)), string(os.PathSeparator))[0])
					path = next
				}
			}
			cfg := store.DefaultConfig()
			for _, candidate := range []string{id, other} {
				providerID := ""
				if candidate == id {
					providerID = tt.providerID
				}
				cfg.PutSession(store.Key(tt.provider, candidate), store.SessionRecord{ID: candidate, Agent: tt.provider, Workdir: "/tmp/project", SessionName: "uam-" + tt.provider + "-" + candidate[:8], ProviderSessionID: providerID, Status: store.StatusActive})
			}
			svc := NewService(nil, adapter.NewRegistry([]adapter.AgentAdapter{tt.newAdapter()}))
			_, _, _, err := svc.prepareResume(adapter.Session{ID: id, AgentType: tt.provider}, cfg, ResumeOptions{})
			if got := errors.Is(err, ErrAmbiguousResume); got != tt.wantAmbiguous {
				t.Fatalf("error=%v ambiguous=%v want=%v", err, got, tt.wantAmbiguous)
			}
		})
	}
}

func TestUniqueHeuristicResumeStillWorks(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession(store.Key("fake", "11111111"), store.SessionRecord{ID: "11111111", Agent: "fake", Workdir: "/tmp/project", SessionName: "uam-fake-11111111", Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeBackground(context.Background(), "11111111"); err != nil {
		t.Fatal(err)
	}
	if fake.resumed == nil {
		t.Fatal("unique heuristic resume did not launch")
	}
}

func TestExactResumeIgnoresOtherSessionsInWorkspace(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, resumeKind: adapter.ResumeExact}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if err := st.Update(func(cfg *store.Config) error {
		for _, id := range []string{"11111111", "22222222"} {
			cfg.PutSession(store.Key("fake", id), store.SessionRecord{ID: id, Agent: "fake", Workdir: "/tmp/project", SessionName: "uam-fake-" + id, ProviderSessionID: "provider-" + id, Status: store.StatusActive})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeBackground(context.Background(), "11111111"); err != nil {
		t.Fatal(err)
	}
	if fake.resumed == nil {
		t.Fatal("exact resume did not launch")
	}
}

func TestAmbiguousRestartDoesNotStopLiveSession(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	live := adapter.Session{ID: "11111111", AgentType: "fake", Cwd: "/tmp/project", SessionName: "uam-fake-11111111", ProcAlive: adapter.Alive, State: adapter.Active}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{live}, stopRemoves: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if err := st.Update(func(cfg *store.Config) error {
		for _, id := range []string{"11111111", "22222222"} {
			cfg.PutSession(store.Key("fake", id), store.SessionRecord{ID: id, Agent: "fake", Workdir: "/tmp/project", SessionName: "uam-fake-" + id, Status: store.StatusActive})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Restart(context.Background(), "11111111"); !errors.Is(err, ErrAmbiguousResume) {
		t.Fatalf("restart error=%v, want ErrAmbiguousResume", err)
	}
	if fake.stopped {
		t.Fatal("ambiguous restart must fail before stopping the live session")
	}
}

func TestDeadSessionStateReflectsNaturalExitCode(t *testing.T) {
	zero, crash := 0, 7
	if got := deadSessionFromRecord(store.SessionRecord{LastExitCode: &zero}, time.Now()).State; got != adapter.Completed {
		t.Fatalf("natural exit 0 state=%q, want Completed", got)
	}
	if got := deadSessionFromRecord(store.SessionRecord{LastExitCode: &crash}, time.Now()).State; got != adapter.Failed {
		t.Fatalf("natural crash state=%q, want Failed", got)
	}
}

// Restart replaces a live session's agent process in place: stop the backend
// session, then resume it under the same identity (id, session name, record)
// with the provider's resume args.
func TestRestartStopsThenResumesLiveSession(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, stopRemoves: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	live, err := svc.DispatchNamed(context.Background(), "fake", "tracker", "hello", "/tmp", "yolo")
	if err != nil {
		t.Fatal(err)
	}
	fake.sessions = []adapter.Session{live}
	if err := svc.Restart(context.Background(), "12345678"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !fake.stopped {
		t.Fatal("restart must stop the live backend session first")
	}
	if fake.resumed == nil {
		t.Fatal("restart must resume the session after stopping it")
	}
	if fake.resumed.ID != "12345678" || fake.resumed.SessionName != "uam-fake-12345678" {
		t.Fatalf("restart must keep the session identity, resumed %+v", fake.resumed)
	}
	cfg, _ := st.Load()
	if cfg.Sessions[store.Key("fake", "12345678")].Status != store.StatusActive {
		t.Fatalf("record must stay active after restart, got %q", cfg.Sessions[store.Key("fake", "12345678")].Status)
	}
}

// Restarting a session that is already stopped skips the stop and just
// resumes it — an idempotent restart.
func TestRestartOfStoppedSessionJustResumes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if _, err := svc.DispatchNamed(context.Background(), "fake", "tracker", "hello", "/tmp", "yolo"); err != nil {
		t.Fatal(err)
	}
	// No live session listed: the agent already exited.
	if err := svc.Restart(context.Background(), "12345678"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if fake.stopped {
		t.Fatal("restart of a stopped session must not call Stop")
	}
	if fake.resumed == nil {
		t.Fatal("restart of a stopped session must resume it")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		_ = w.Close()
		os.Stdout = old
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}
