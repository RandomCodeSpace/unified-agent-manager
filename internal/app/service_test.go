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

	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
	"github.com/randomcodespace/unified-agent-manager/internal/store"
)

type svcFakeAdapter struct {
	name      string
	sessions  []adapter.Session
	available bool
	stopped   bool
	replied   string
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
	return adapter.Session{ID: "12345678", AgentType: f.name, DisplayName: req.Prompt, Cwd: firstNonEmpty(req.Cwd, "/tmp"), TmuxSession: "uam-" + f.name + "-12345678", State: adapter.Working, CreatedAt: time.Now()}, nil
}
func (f *svcFakeAdapter) List(ctx adapter.Context) ([]adapter.Session, error) { return f.sessions, nil }
func (f *svcFakeAdapter) Peek(ctx adapter.Context, id string) (adapter.PeekResult, error) {
	return adapter.PeekResult{TailText: "tail", Summary: "sum"}, nil
}
func (f *svcFakeAdapter) Reply(ctx adapter.Context, id, text string) error {
	f.replied = text
	return nil
}
func (f *svcFakeAdapter) Attach(id string) (adapter.AttachSpec, error) {
	return adapter.AttachSpec{Argv: []string{"echo", id}}, nil
}
func (f *svcFakeAdapter) Stop(ctx adapter.Context, id string) error            { f.stopped = true; return nil }
func (f *svcFakeAdapter) Rename(ctx adapter.Context, id, newName string) error { return nil }
func (f *svcFakeAdapter) Subscribe(ctx adapter.Context) (<-chan adapter.SessionEvent, error) {
	return nil, nil
}

func TestServiceWorkflow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "live0001", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", TmuxSession: "uam-fake-live0001", State: adapter.Completed, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/1", Number: 1, Status: adapter.PROpen}}}}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	sess, err := svc.Dispatch(context.Background(), "fake", "hello", "/tmp", "yolo")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" {
		t.Fatal("empty id")
	}
	list, _, err := svc.LoadSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 {
		t.Fatalf("list=%+v", list)
	}
	found, _, err := svc.Find(context.Background(), "1234")
	if err != nil || found.DisplayName != "hello" {
		t.Fatalf("found=%+v err=%v", found, err)
	}
	if err := svc.Rename(context.Background(), "1234", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := svc.TogglePin(context.Background(), "1234"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUI(func(ui *store.UISettings) { ui.GroupByDir = true }); err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Peek(context.Background(), "live"); err != nil || p.TailText != "tail" {
		t.Fatalf("peek=%+v err=%v", p, err)
	}
	if err := svc.Reply(context.Background(), "live", "yes"); err != nil || fake.replied != "yes" {
		t.Fatalf("reply %q %v", fake.replied, err)
	}
	if spec, err := svc.AttachSpec(context.Background(), "live"); err != nil || len(spec.Argv) == 0 {
		t.Fatalf("attach=%+v err=%v", spec, err)
	}
	if err := svc.UpdateSortOrder(list); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), "live", true); err != nil || !fake.stopped {
		t.Fatalf("stop %v stopped=%v", err, fake.stopped)
	}
}

func TestServicePrintListAndErrors(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
	if _, err := svc.Dispatch(context.Background(), "missing", "x", "", ""); err == nil {
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

func TestSortSessionsAndRecord(t *testing.T) {
	now := time.Now()
	sessions := []adapter.Session{{ID: "c", State: adapter.Completed, CreatedAt: now}, {ID: "n", State: adapter.NeedsInput, CreatedAt: now}, {ID: "p", State: adapter.Completed, Pinned: true, CreatedAt: now}}
	SortSessions(sessions)
	if sessions[0].ID != "p" || sessions[1].ID != "n" {
		t.Fatalf("order=%+v", sessions)
	}
	rec := RecordFromSession(adapter.Session{ID: "id", AgentType: "fake", Cwd: "/tmp", TmuxSession: "tm", CreatedAt: now}, "")
	if rec.Mode != store.ModeYolo || rec.Name != "id" {
		t.Fatalf("rec=%+v", rec)
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
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}
