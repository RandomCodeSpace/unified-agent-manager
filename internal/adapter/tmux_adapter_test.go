package adapter

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func TestTmuxAgentLifecycleWithFakeTmux(t *testing.T) {
	ag, logPath := setupLifecycleAgent(t)
	assertAgentAvailable(t, ag)
	assertAgentDispatchAndList(t, ag)
	assertAgentInteractions(t, ag)
	assertTmuxLifecycleLog(t, logPath)
}

func setupLifecycleAgent(t *testing.T) (*TmuxAgent, string) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-fake-abc12345|1710000000|0|1|/tmp/repo|fakeagent" ;;
  *"capture-pane"*) printf 'Thinking...\ncreated https://github.com/o/r/pull/7\n' ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	return NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client), logPath
}

func assertAgentAvailable(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	if ok, reason := ag.Available(); !ok || reason != "" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	if ag.Name() != "fake" || ag.DisplayName() != "Fake Agent" {
		t.Fatalf("names wrong")
	}
}

func assertAgentDispatchAndList(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.AgentType != "fake" || sess.State != Active || sess.TmuxSession == "" {
		t.Fatalf("bad session: %+v", sess)
	}
	list, err := ag.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("List len=%d err=%v", len(list), err)
	}
	if list[0].PR == nil || list[0].PR.Number != 7 {
		t.Fatalf("bad classified list: %+v", list[0])
	}
}

func assertAgentInteractions(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	peek, err := ag.Peek(context.Background(), "abc12345")
	if err != nil || !strings.Contains(peek.TailText, "Thinking") {
		t.Fatalf("Peek: %+v %v", peek, err)
	}
	for _, action := range []func() error{
		func() error { return ag.Reply(context.Background(), "abc12345", "ok") },
		func() error { _, err := ag.Attach("abc12345"); return err },
		func() error { return ag.Stop(context.Background(), "abc12345") },
	} {
		if err := action(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertTmuxLifecycleLog(t *testing.T, logPath string) {
	t.Helper()
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	for _, want := range []string{"set-option", "bind-key", "new-session", "send-keys", "kill-session"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %s: %s", want, logData)
		}
	}
	if strings.Contains(logText, "exec bash") {
		t.Fatalf("agent exit should terminate tmux session, log should not keep a fallback shell: %s", logData)
	}
}

// A dispatched session must get a user-facing label: @uam_label (for the tmux
// status line / title) set to "<name> · <agent>", and its window renamed to
// the short name — so tmux shows the user's name, not uam-<agent>-<id>. The
// persisted Session.DisplayName stays the bare name.
func TestDispatchSetsSessionLabel(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_LOG\"\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, nil, client)

	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp", Mode: "yolo", Name: "tracker"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != "tracker" {
		t.Fatalf("DisplayName = %q, want tracker", sess.DisplayName)
	}
	data, _ := os.ReadFile(logPath)
	logText := string(data)
	if !strings.Contains(logText, "@uam_label tracker · fake") {
		t.Fatalf("expected @uam_label 'tracker · fake': %s", logText)
	}
	if !strings.Contains(logText, "rename-window -t "+sess.TmuxSession+" tracker") {
		t.Fatalf("expected window rename to tracker: %s", logText)
	}
}

// F52 — a PR-scrape capture-pane failure must be logged (debug) and stay
// per-session non-fatal: List still returns the session, just without a PR.
func TestListLogsCaptureFailureButKeepsSession(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	// list-sessions succeeds; capture-pane fails for the PR scrape.
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-fake-abc12345|1710000000|0|1|/tmp/repo|fakeagent" ;;
  *"capture-pane"*) echo "capture boom" >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", filepath.Join(dir, "tmux.log"))
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)

	var buf bytes.Buffer
	prev := log.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer log.SetLogger(prev)

	list, err := ag.List(context.Background())
	if err != nil {
		t.Fatalf("a per-session capture failure must not fail List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("session should still be listed despite capture failure, got %d", len(list))
	}
	if list[0].PR != nil {
		t.Fatalf("PR should be nil when capture failed, got %+v", list[0].PR)
	}
	if !strings.Contains(buf.String(), "capture") {
		t.Fatalf("capture failure should be logged, got: %q", buf.String())
	}
}

func TestTmuxAgentResumeUsesPersistedMetadata(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	sess, err := ag.Resume(context.Background(), ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Name: "bugfix", Prompt: "fix parser", Cwd: "/tmp/project", Mode: "yolo", TmuxSession: "uam-fake-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sess.ID != "abc12345-dead-beef-cafe-0123456789ab" || sess.DisplayName != "bugfix" || sess.Prompt != "fix parser" || sess.Cwd != "/tmp/project" || sess.TmuxSession != "uam-fake-abc12345" || sess.ProcAlive != Alive {
		t.Fatalf("resumed session did not preserve metadata: %+v", sess)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	for _, want := range []string{"new-session", "uam-fake-abc12345", "/tmp/project", "fakeagent --yolo", "send-keys", "fix parser"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("resume log missing %q: %s", want, logText)
		}
	}
}

func TestTmuxAgentCommandAliasOnPathReplacesDefaultCommand(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ghcp"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	ag.SessionArgs = func(req ResumeRequest, activity string) []string { return []string{"--session", req.ID} }

	sess, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "ghcp", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.AgentType != "fake" || sess.CommandAlias != "ghcp" {
		t.Fatalf("alias dispatch should keep agent type and alias, got %+v", sess)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, filepath.Join(dir, "ghcp")+" --yolo --session "+sess.ID) {
		t.Fatalf("alias on PATH should launch resolved alias command with yolo/session args: %s", logText)
	}
	if strings.Contains(logText, "fakeagent") {
		t.Fatalf("alias should replace the default candidate, got: %s", logText)
	}
}

func TestTmuxAgentCommandAliasFallsBackToInteractiveShell(t *testing.T) {
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "shell")
	writeExecutable(t, shellPath, `#!/bin/sh
printf '%s\n' "$*" >> "$SHELL_LOG"
case "$*" in
  *"type ghcp"*) exit 0 ;;
esac
exit 1
`)
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", shellPath)
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	t.Setenv("SHELL_LOG", filepath.Join(dir, "shell.log"))
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", nil, []string{"--yolo"}, client)
	ag.SessionArgs = func(req ResumeRequest, activity string) []string { return []string{"two words", "semi;colon"} }

	if _, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "ghcp", Cwd: "/tmp", Mode: "yolo"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, shellPath+" -ic") || !strings.Contains(logText, "ghcp --yolo") {
		t.Fatalf("missing interactive shell fallback: %s", logText)
	}
	if !strings.Contains(logText, "'two words'") || !strings.Contains(logText, "'semi;colon'") {
		t.Fatalf("fallback must shell-quote non-alias args: %s", logText)
	}
	shellData, _ := os.ReadFile(filepath.Join(dir, "shell.log"))
	if !strings.Contains(string(shellData), "type ghcp") {
		t.Fatalf("fallback should preflight alias in interactive shell: %s", shellData)
	}
}

func TestTmuxAgentCommandAliasMissingFromShellFailsBeforeCreate(t *testing.T) {
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "shell")
	writeExecutable(t, shellPath, "#!/bin/sh\nexit 1\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_LOG\"\nexit 0\n")
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", shellPath)
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", nil, nil, client)

	_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "ghcp", Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "not found on PATH or in interactive shell") {
		t.Fatalf("Dispatch error = %v, want missing alias", err)
	}
	if data, _ := os.ReadFile(logPath); len(data) != 0 {
		t.Fatalf("tmux session should not be created for missing alias: %s", data)
	}
}

func TestTmuxAgentRejectsUnsafeCommandAlias(t *testing.T) {
	ag := NewTmuxAgent("fake", "Fake Agent", nil, nil, nil)
	for _, alias := range []string{"-ghcp", "gh/cp", "gh cp", "gh;cp", "gh$cp", "gh`cp`"} {
		t.Run(alias, func(t *testing.T) {
			_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: alias})
			if err == nil || !strings.Contains(err.Error(), "invalid command alias") {
				t.Fatalf("Dispatch error = %v, want invalid alias", err)
			}
		})
	}
}

func TestDispatchReturnsRandomIDError(t *testing.T) {
	wantErr := errors.New("random unavailable")
	ag := NewTmuxAgent("fake", "Fake Agent", nil, nil, nil)
	ag.randomReader = errReader{err: wantErr}

	_, err := ag.Dispatch(context.Background(), DispatchRequest{})
	if err == nil {
		t.Fatal("expected dispatch error when random ID generation fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("dispatch error should wrap random reader error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "generate session id") {
		t.Fatalf("dispatch error should include ID generation context, got: %v", err)
	}
}

func TestResumeReturnsRandomIDErrorWhenIDMissing(t *testing.T) {
	wantErr := errors.New("random unavailable")
	ag := NewTmuxAgent("fake", "Fake Agent", nil, nil, nil)
	ag.randomReader = errReader{err: wantErr}

	_, err := ag.Resume(context.Background(), ResumeRequest{})
	if err == nil {
		t.Fatal("expected resume error when random ID generation fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("resume error should wrap random reader error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "generate session id") {
		t.Fatalf("resume error should include ID generation context, got: %v", err)
	}
}

func TestNewIDKeepsUUIDFormat(t *testing.T) {
	id, err := newID(bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}))
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	if id != "00010203-0405-4607-8809-0a0b0c0d0e0f" {
		t.Fatalf("id = %q, want UUID v4 format with version/variant bits", id)
	}
}

// F19 — a resume/dispatch that creates the tmux session but then fails to send
// the prompt must roll back the live (prompt-less) session, otherwise it lingers
// as an orphan the store records as Exited/closed.
func TestStartSessionRollsBackTmuxOnSendLineFailure(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	// send-keys fails; everything else (new-session, kill-session) succeeds.
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"send-keys"*) echo "boom" >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err == nil {
		t.Fatal("expected dispatch error when send-keys fails")
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, "new-session") {
		t.Fatalf("session should have been created: %s", logText)
	}
	if !strings.Contains(logText, "kill-session") {
		t.Fatalf("send-keys failure must roll back the created session via kill-session: %s", logText)
	}
}

// F19 trap — if the rollback Kill itself fails, the caller must still see the
// original SendLine error (not the kill error).
func TestStartSessionReturnsSendLineErrorWhenRollbackKillAlsoFails(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"send-keys"*) echo "sendboom" >&2; exit 1 ;;
  *"kill-session"*) echo "killboom" >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err == nil {
		t.Fatal("expected dispatch error")
	}
	if !strings.Contains(err.Error(), "sendboom") {
		t.Fatalf("error should surface the original send-keys failure, got: %v", err)
	}
}

// F25 — startSession must create the tmux session BEFORE applying server
// config. On first dispatch the server doesn't exist yet, so configuring it
// first fails and (pre-fix) latched the failure forever. Assert new-session
// precedes set-option in the recorded command log.
func TestStartSessionConfiguresServerAfterCreate(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	if _, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logText := func() string { b, _ := os.ReadFile(logPath); return string(b) }()
	nsIdx := strings.Index(logText, "new-session")
	soIdx := strings.Index(logText, "set-option")
	if nsIdx < 0 || soIdx < 0 {
		t.Fatalf("expected both new-session and set-option in log: %s", logText)
	}
	if nsIdx > soIdx {
		t.Fatalf("CreateSession must precede server config so the server exists: %s", logText)
	}
}

func TestTmuxAgentDispatchWithoutPromptSkipsSendKeys(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != "tmp" {
		t.Fatalf("DisplayName=%q, want dir-derived name", sess.DisplayName)
	}
	logData, _ := os.ReadFile(logPath)
	// `send-keys -t` is the prompt-injection form; the mouse copy/paste config
	// bindings legitimately contain `send-keys -X`/`-M`, so match the targeted
	// form rather than the bare substring.
	if strings.Contains(string(logData), "send-keys -t") {
		t.Fatalf("empty prompt should not be sent: %s", logData)
	}
}

// F32 — target() must use tmux exact-match (`=` prefix) so a neighbour session
// whose name shares the truncated prefix is never hit by `-t`. Drive Stop/Peek
// through a fake tmux and assert the recorded `-t` token is exact-anchored.
func newTargetingAgent(t *testing.T) (*TmuxAgent, string) {
	t.Helper()
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"capture-pane"*) printf 'tail\n' ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	return NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client), logPath
}

func TestTargetUsesExactMatchForFullUUID(t *testing.T) {
	ag, logPath := newTargetingAgent(t)
	// A full-UUID id whose first 8 chars name the session.
	if err := ag.Stop(context.Background(), "abc12345-dead-beef-cafe-0123456789ab"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, "-t =uam-fake-abc12345") {
		t.Fatalf("kill must target the exact session, got: %s", logText)
	}
}

func TestTargetUsesExactMatchForCanonicalName(t *testing.T) {
	ag, logPath := newTargetingAgent(t)
	// A canonical (already uam-prefixed) name must also be exact-anchored so a
	// longer neighbour ("uam-fake-abc123450" etc.) is never matched by prefix.
	if _, err := ag.Peek(context.Background(), "uam-fake-abc12345"); err != nil {
		t.Fatalf("Peek: %v", err)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, "-t =uam-fake-abc12345") {
		t.Fatalf("capture must target the exact session, got: %s", logText)
	}
}

// F57 — startSession must wrap a CreateSession failure with the tmux session
// name (boundary context) while preserving the underlying error for errors.Is.
func TestStartSessionWrapsCreateSessionError(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"new-session"*) echo "create boom" >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", filepath.Join(dir, "tmux.log"))
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp", Mode: "yolo"})
	if err == nil {
		t.Fatal("expected dispatch error when new-session fails")
	}
	if !strings.Contains(err.Error(), "uam-fake-") {
		t.Fatalf("CreateSession failure must be wrapped with the tmux session name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "create boom") {
		t.Fatalf("wrapped error must preserve the underlying cause, got: %v", err)
	}
}

// F57 — List must wrap a tmux list failure with the agent name so a caller
// (and the log) can tell which provider's scan failed.
func TestListWrapsTmuxListError(t *testing.T) {
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
case "$*" in
  *"list-sessions"*) echo "protocol mismatch" >&2; exit 1 ;;
esac
exit 0
`)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	_, err := ag.List(context.Background())
	if err == nil {
		t.Fatal("expected List error on a genuine list-sessions failure")
	}
	if !strings.Contains(err.Error(), "fake") {
		t.Fatalf("List failure must be wrapped with the agent name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "protocol mismatch") {
		t.Fatalf("wrapped error must preserve the underlying cause, got: %v", err)
	}
}

func TestDisplayNameFromDir(t *testing.T) {
	if got := displayNameFromDir("/home/dev/projects/uam"); got != "uam" {
		t.Fatalf("dir name = %q, want uam", got)
	}
	if got := displayNameFromDir("/"); got != "untitled" {
		t.Fatalf("root dir name = %q, want untitled", got)
	}
	cwd, _ := os.Getwd()
	if got := displayNameFromDir("."); got != filepath.Base(cwd) {
		t.Fatalf("relative dir name = %q, want %q", got, filepath.Base(cwd))
	}
}

func TestTmuxAgentUnavailable(t *testing.T) {
	ag := NewTmuxAgent("missing", "Missing", []CommandCandidate{{Display: "definitely-missing", Args: []string{"definitely-missing-uam-test"}}}, nil, nil)
	if ok, reason := ag.Available(); ok || reason == "" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	if _, err := ag.Dispatch(context.Background(), DispatchRequest{}); err == nil {
		t.Fatal("expected dispatch error")
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}
