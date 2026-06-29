package cli

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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type cliFakeAdapter struct {
	sessions []adapter.Session
	stopped  bool
	resumed  bool
	attached []string
}

func (f *cliFakeAdapter) Name() string        { return "fake" }
func (f *cliFakeAdapter) DisplayName() string { return "fake" }
func (f *cliFakeAdapter) Available() (bool, string) {
	return true, ""
}
func (f *cliFakeAdapter) Dispatch(ctx adapter.Context, req adapter.DispatchRequest) (adapter.Session, error) {
	if req.Prompt == "fail" {
		return adapter.Session{}, errors.New("fail")
	}
	sess := adapter.Session{ID: "abc12345", AgentType: "fake", CommandAlias: req.CommandAlias, DisplayName: firstNonEmpty(req.Name, req.Prompt, "untitled"), Prompt: req.Prompt, Cwd: req.Cwd, SessionName: "uam-fake-abc12345", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	f.sessions = append(f.sessions, sess)
	return sess, nil
}
func (f *cliFakeAdapter) List(ctx adapter.Context) ([]adapter.Session, error) { return f.sessions, nil }
func (f *cliFakeAdapter) Peek(ctx adapter.Context, id string) (adapter.PeekResult, error) {
	return adapter.PeekResult{TailText: "tail for " + id}, nil
}
func (f *cliFakeAdapter) Reply(ctx adapter.Context, id, text string) error { return nil }
func (f *cliFakeAdapter) Attach(id string) (adapter.AttachSpec, error) {
	f.attached = append(f.attached, id)
	return adapter.AttachSpec{Argv: []string{"echo", id}}, nil
}

// Stop drops the live sessions, mirroring the real backend where Kill
// returns only after the host is fully gone.
func (f *cliFakeAdapter) Stop(ctx adapter.Context, id string) error {
	f.stopped = true
	f.sessions = nil
	return nil
}

func (f *cliFakeAdapter) Resume(ctx adapter.Context, req adapter.ResumeRequest) (adapter.Session, error) {
	f.resumed = true
	sess := adapter.Session{ID: req.ID, AgentType: "fake", DisplayName: req.Name, Cwd: req.Cwd, SessionName: req.SessionName, State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	f.sessions = append(f.sessions, sess)
	return sess, nil
}

func noopRunTUI(context.Context, tea.Model) error { return nil }

func TestRunDispatchListPeekAndStop(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"--cwd", "/tmp", "fake", "#bugfix", "fix", "thing"})
	if id != "abc12345" {
		t.Fatalf("id=%q", id)
	}
	if out := captureCLIStdout(t, func() { must(t, runList(context.Background(), svc, []string{"--json"})) }); !strings.Contains(out, "bugfix") {
		t.Fatalf("list=%q", out)
	}
	if out := captureCLIStdout(t, func() { must(t, runPeek(context.Background(), svc, []string{id})) }); !strings.Contains(out, "tail for") {
		t.Fatalf("peek=%q", out)
	}
	must(t, runStop(context.Background(), svc, "stop", []string{id}))
	if !fake.stopped {
		t.Fatal("fake adapter was not stopped")
	}
}

// restart stops the live agent process and resumes the session in place.
func TestRunRestart(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"--cwd", "/tmp", "fake", "#bugfix", "fix", "thing"})
	must(t, runRestart(context.Background(), svc, []string{id}))
	if !fake.stopped || !fake.resumed {
		t.Fatalf("restart must stop then resume: stopped=%v resumed=%v", fake.stopped, fake.resumed)
	}
	if err := runRestart(context.Background(), svc, nil); err == nil {
		t.Fatal("restart without id should fail")
	}
}

func TestCLIArgumentValidationAndParsing(t *testing.T) {
	svc, _ := newCLITestService(t)
	if err := RunDispatch(context.Background(), svc, nil); err == nil {
		t.Fatal("dispatch without agent should fail")
	}
	if _, err := requireArg(nil, "missing"); err == nil {
		t.Fatal("empty args should fail")
	}
	name, prompt := parseNameAndPrompt([]string{"#name", "do", "work"})
	if name != "name" || prompt != "do work" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
	name, prompt = parseNameAndPrompt([]string{"do", "work"})
	if name != "" || prompt != "do work" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
	name, prompt = parseNameAndPrompt([]string{"#name", "do   work", "\twith tabs"})
	if name != "name" || prompt != "do   work \twith tabs" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
	name, prompt = parseNameAndPrompt([]string{"do   work", "\twith tabs"})
	if name != "" || prompt != "do   work \twith tabs" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
}

func TestRunWithTUIHelpVersionAndDefault(t *testing.T) {
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	called := false
	runTUI := func(ctx context.Context, model tea.Model) error {
		called = true
		return nil
	}
	if err := RunWithTUI(context.Background(), nil, runTUI); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("default command should run TUI")
	}
	if out := captureCLIStderr(t, Usage); !strings.Contains(out, "uam") {
		t.Fatalf("usage=%q", out)
	}
	versionOut := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"version"}, runTUI)) })
	if strings.TrimSpace(versionOut) == "" {
		t.Fatal("version output should not be empty")
	}
	if err := RunWithTUI(context.Background(), []string{"unknown"}, runTUI); err == nil {
		t.Fatal("unknown command should fail")
	}
}

func TestRunCommandAttachLastAndNew(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"fake", "attachable"})
	var tuiCalls int
	runTUI := func(ctx context.Context, model tea.Model) error {
		tuiCalls++
		return nil
	}
	if err := runCommand(context.Background(), svc, []string{"attach", id}, runTUI); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(context.Background(), svc, []string{"last"}, runTUI); err != nil {
		t.Fatal(err)
	}
	if tuiCalls != 2 {
		t.Fatalf("TUI calls=%d", tuiCalls)
	}
	out := captureCLIStdout(t, func() {
		withCLIStdin(t, "fake\n\n/tmp\n#from-new prompt\n", func() { must(t, runNew(context.Background(), svc, runTUI)) })
	})
	if !strings.Contains(out, "abc12345") {
		t.Fatalf("new should attach the created session, output=%q", out)
	}
	if tuiCalls != 3 {
		t.Fatalf("TUI calls=%d, want 3", tuiCalls)
	}
	if len(fake.attached) == 0 || fake.attached[len(fake.attached)-1] != "abc12345" {
		t.Fatalf("new did not attach created session, attached=%v", fake.attached)
	}
}

func TestRunLastWithoutSessionsFails(t *testing.T) {
	svc, _ := newCLITestService(t)
	if err := runLast(context.Background(), svc, func(context.Context, tea.Model) error { return nil }); err == nil {
		t.Fatal("last without sessions should fail")
	}
}

func TestRunNotifyClosedFlagsRecord(t *testing.T) {
	svc, _ := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"fake", "to-close"})
	if id == "" {
		t.Fatal("dispatch did not return an id")
	}
	// The notify payload is the backend session name, not the agent id.
	if err := runNotifyClosed(svc, []string{"uam-fake-" + id}); err != nil {
		t.Fatalf("notify-closed: %v", err)
	}
	cfg, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", id)]
	if !ok {
		t.Fatalf("record missing: %+v", cfg.Sessions)
	}
	if rec.Status != store.StatusClosedByUser {
		t.Fatalf("status = %q, want %q", rec.Status, store.StatusClosedByUser)
	}
}

func TestRunNotifyClosedRequiresName(t *testing.T) {
	svc, _ := newCLITestService(t)
	if err := runNotifyClosed(svc, nil); err == nil {
		t.Fatal("notify-closed without a session name should fail")
	}
}

func TestNewServiceRegistersHermesWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	writeCLIExecutable(t, filepath.Join(dir, "hermes"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)
	if a, ok := svc.Registry.Get("hermes"); !ok || a.DisplayName() != "Hermes Agent" {
		t.Fatalf("hermes adapter missing: %v %v", a, ok)
	}
}

func newCLITestService(t *testing.T) (*app.Service, *cliFakeAdapter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &cliFakeAdapter{}
	return app.NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake})), fake
}

func dispatchAndCaptureID(t *testing.T, svc *app.Service, args []string) string {
	t.Helper()
	out := captureCLIStdout(t, func() { must(t, RunDispatch(context.Background(), svc, args)) })
	return strings.TrimSpace(out)
}

func captureCLIStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureCLIFile(t, &os.Stdout, fn)
}

func captureCLIStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureCLIFile(t, &os.Stderr, fn)
}

func captureCLIFile(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*target = w
	defer func() {
		_ = w.Close()
		*target = old
	}()
	fn()
	_ = w.Close()
	*target = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func withCLIStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(input)
	_ = w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	fn()
}

func writeCLIExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// The internal __host/__attach subcommands are routed through runCommand;
// invalid input must surface as errors rather than silently doing nothing.
func TestRunCommandInternalSubcommands(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", t.TempDir())
	svc, _ := newCLITestService(t)
	noTUI := func(context.Context, tea.Model) error { return nil }
	if err := runCommand(context.Background(), svc, []string{"__host", "--name", "bad name", "--", "/bin/true"}, noTUI); err == nil {
		t.Fatal("__host with an invalid name must fail")
	}
	if err := runCommand(context.Background(), svc, []string{"__attach"}, noTUI); err == nil {
		t.Fatal("__attach without a session must fail")
	}
}
