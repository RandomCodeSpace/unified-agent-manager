package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type svcFakeAdapter struct {
	name      string
	sessions  []adapter.Session
	available bool
	stopped   bool
	replied   string
	resumed   *adapter.ResumeRequest
	// F04: simulate a failed kill (stopErr) and a still-live pane (alive). The
	// fake implements adapter.HasSessionAdapter, returning alive from HasSession.
	stopErr error
	alive   bool
	// F12: simulate a per-adapter List failure so liveSessions can be tested for
	// logging-then-continue (one bad adapter must not blank the dashboard).
	listErr error
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
	return adapter.Session{ID: req.ID, AgentType: f.name, CommandAlias: req.CommandAlias, DisplayName: req.Name, Prompt: req.Prompt, Cwd: req.Cwd, SessionName: req.SessionName, State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}, nil
}
func (f *svcFakeAdapter) List(ctx adapter.Context) ([]adapter.Session, error) {
	return f.sessions, f.listErr
}
func (f *svcFakeAdapter) Peek(ctx adapter.Context, id string) (adapter.PeekResult, error) {
	return adapter.PeekResult{TailText: "tail"}, nil
}
func (f *svcFakeAdapter) Reply(ctx adapter.Context, id, text string) error {
	f.replied = text
	return nil
}
func (f *svcFakeAdapter) Attach(id string) (adapter.AttachSpec, error) {
	return adapter.AttachSpec{Argv: []string{"echo", id}}, nil
}
func (f *svcFakeAdapter) Stop(ctx adapter.Context, id string) error {
	f.stopped = true
	return f.stopErr
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
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
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
	if !strings.Contains(out, "[") {
		t.Fatalf("json out=%q", out)
	}
	out = captureStdout(t, func() {
		if err := svc.PrintList(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	})
	_ = out
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
