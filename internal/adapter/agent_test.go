package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func newLifecycleAgent(t *testing.T) (*Agent, *adaptertest.Backend) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	be := &adaptertest.Backend{
		Sessions:    []session.Info{{Name: "uam-fake-abc12345", CreatedUnix: 1710000000, ChildPID: 1, Cwd: "/tmp/repo", Alive: true}},
		CaptureText: "Thinking...\ncreated https://github.com/o/r/pull/7\n",
	}
	return NewAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, be), be
}

func TestAgentLifecycle(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	if ok, reason := ag.Available(); !ok || reason != "" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	if ag.Name() != "fake" || ag.DisplayName() != "Fake Agent" {
		t.Fatalf("names wrong")
	}
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.AgentType != "fake" || sess.State != Active || sess.SessionName == "" {
		t.Fatalf("bad session: %+v", sess)
	}
	creates := be.CallsOf("create")
	if len(creates) != 1 || !strings.Contains(be.CommandLog(), "fakeagent --yolo") {
		t.Fatalf("bad create calls: %+v", creates)
	}
	if creates[0].Env["UAM_AGENT"] != "fake" || creates[0].Env["UAM_ID"] != sess.ID {
		t.Fatalf("create env missing UAM_AGENT/UAM_ID: %+v", creates[0].Env)
	}
	if creates[0].ProviderIdentity != "fake" {
		t.Fatalf("create provider identity = %q, want fake", creates[0].ProviderIdentity)
	}
	sends := be.CallsOf("send")
	if len(sends) != 1 || sends[0].Text != "hello" {
		t.Fatalf("dispatch should send the prompt once: %+v", sends)
	}
	list, err := ag.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("List len=%d err=%v", len(list), err)
	}
	if list[0].PR == nil || list[0].PR.Number != 7 {
		t.Fatalf("bad classified list: %+v", list[0])
	}
	if list[0].State != Active || list[0].ProcAlive != Alive || list[0].Cwd != "/tmp/repo" {
		t.Fatalf("bad list session: %+v", list[0])
	}
	peek, err := ag.Peek(context.Background(), "abc12345")
	if err != nil || !strings.Contains(peek.TailText, "Thinking") {
		t.Fatalf("Peek: %+v %v", peek, err)
	}
	if err := ag.Reply(context.Background(), "abc12345", "ok"); err != nil {
		t.Fatal(err)
	}
	if spec, err := ag.Attach("abc12345"); err != nil || len(spec.Argv) == 0 {
		t.Fatalf("Attach: %+v %v", spec, err)
	}
	if err := ag.Stop(context.Background(), "abc12345"); err != nil {
		t.Fatal(err)
	}
	if kills := be.CallsOf("kill"); len(kills) != 1 || kills[0].Name != "uam-fake-abc12345" {
		t.Fatalf("Stop should kill the exact session: %+v", kills)
	}
}

// A dispatched session must get a user-facing label "<name> · <agent>" so
// attached terminals' titles show the user's name, not uam-<agent>-<id>. The
// persisted Session.DisplayName stays the bare name.
func TestDispatchSetsSessionLabel(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Name: "bugfix", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != "bugfix" {
		t.Fatalf("DisplayName = %q, want bare name", sess.DisplayName)
	}
	labels := be.CallsOf("label")
	if len(labels) != 1 || labels[0].Label != "bugfix · fake" {
		t.Fatalf("label calls = %+v, want one 'bugfix · fake'", labels)
	}
	if labels[0].Name != sess.SessionName {
		t.Fatalf("label target %q != session %q", labels[0].Name, sess.SessionName)
	}
}

func TestDispatchSanitizesLabelButPreservesPrompt(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	name := "bug\x1b]52;c;YQ==\x07fix\nnow"
	prompt := "keep \x1b[31mraw\x1b[0m\nexactly"
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Name: name, Prompt: prompt, Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != name || sess.Prompt != prompt {
		t.Fatalf("persisted metadata changed: name=%q prompt=%q", sess.DisplayName, sess.Prompt)
	}
	labels := be.CallsOf("label")
	if len(labels) != 1 || labels[0].Label != "bugfix now · fake" {
		t.Fatalf("label calls = %+v", labels)
	}
	sends := be.CallsOf("send")
	if len(sends) != 1 || sends[0].Text != prompt {
		t.Fatalf("prompt delivery changed: %+v", sends)
	}
}

// F52 — a per-session capture failure during the PR scrape is non-fatal: the
// session stays in the List result with PR nil.
func TestListKeepsSessionWhenCaptureFails(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	be.CaptureErr = errors.New("capture exploded")
	list, err := ag.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].PR != nil {
		t.Fatalf("capture failure should keep session with nil PR: %+v", list)
	}
}

// Resume must reuse the persisted session name (so the restored session keeps
// its identity) instead of minting a fresh one.
func TestAgentResumeUsesPersistedMetadata(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	sess, err := ag.Resume(context.Background(), ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Name: "named", Prompt: "p", Cwd: "/tmp", Mode: "yolo", SessionName: "uam-fake-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sess.SessionName != "uam-fake-abc12345" {
		t.Fatalf("SessionName = %q, want persisted name", sess.SessionName)
	}
	creates := be.CallsOf("create")
	if len(creates) != 1 || creates[0].Name != "uam-fake-abc12345" {
		t.Fatalf("create should target persisted name: %+v", creates)
	}
}

func TestPrepareLaunchRunsAfterIdentityResolutionAndMergesWithoutMutation(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	extra := []string{"--prepared"}
	preparedEnv := map[string]string{"EXTRA": "value", "UAM_AGENT": "override"}
	ag.PrepareLaunch = func(_ Context, req ResumeRequest, activity, sessionName, cwd string) (LaunchPreparation, error) {
		if activity != "resumed" || sessionName != "uam-fake-abc12345" || cwd != "/tmp" || req.ID == "" {
			t.Fatalf("preparation inputs: activity=%q name=%q cwd=%q req=%+v", activity, sessionName, cwd, req)
		}
		return LaunchPreparation{ExtraArgs: extra, Env: preparedEnv, ProviderSessionID: "provider-live"}, nil
	}
	ag.SessionArgs = func(ResumeRequest, string) []string { return []string{"--legacy"} }
	sess, err := ag.Resume(context.Background(), ResumeRequest{ID: "abc12345", Cwd: "/tmp", Mode: "safe", SessionName: "uam-fake-abc12345"})
	if err != nil {
		t.Fatal(err)
	}
	create := be.CallsOf("create")[0]
	if got := strings.Join(create.Command, " "); !strings.HasSuffix(got, "--prepared --legacy") {
		t.Fatalf("hook precedence argv=%q", got)
	}
	if create.Env["EXTRA"] != "value" || create.Env["UAM_AGENT"] != "fake" || create.Env["UAM_ID"] != "abc12345" {
		t.Fatalf("merged env=%v", create.Env)
	}
	if sess.ProviderSessionID != "provider-live" {
		t.Fatalf("provider id=%q", sess.ProviderSessionID)
	}
	extra[0], preparedEnv["EXTRA"] = "mutated", "mutated"
	if create.Command[len(create.Command)-2] != "--prepared" || create.Env["EXTRA"] != "value" {
		t.Fatalf("backend inputs alias hook-owned data: %+v", create)
	}
}

func TestPrepareLaunchFailureCreatesNoSession(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	ag.PrepareLaunch = func(Context, ResumeRequest, string, string, string) (LaunchPreparation, error) {
		return LaunchPreparation{}, errors.New("prepare exploded")
	}
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "prepare exploded") {
		t.Fatalf("Dispatch error=%v", err)
	}
	if got := be.CallsOf("create"); len(got) != 0 {
		t.Fatalf("preparation failure created session: %+v", got)
	}
}

type commandCaptureBackend struct {
	*adaptertest.Backend
	command []string
}

func (b *commandCaptureBackend) CreateProviderSession(_ context.Context, spec session.CreateSpec) error {
	b.command = spec.Command
	return nil
}

func TestLaunchPreparationCommandSelection(t *testing.T) {
	tests := []struct {
		name            string
		preparedCommand func(sessionName string) []string
		wantCommand     func(sessionName string) []string
	}{
		{
			name: "prepared override",
			preparedCommand: func(sessionName string) []string {
				return []string{"/trusted/uam", "__opencode", "--name", sessionName}
			},
			wantCommand: func(sessionName string) []string {
				return []string{"/trusted/uam", "__opencode", "--name", sessionName}
			},
		},
		{
			name:            "nil override",
			preparedCommand: func(string) []string { return nil },
			wantCommand: func(string) []string {
				return []string{"fakeagent", "--yolo", "--prepared", "--session", "session-id"}
			},
		},
		{
			name:            "empty override",
			preparedCommand: func(string) []string { return []string{} },
			wantCommand: func(string) []string {
				return []string{"fakeagent", "--yolo", "--prepared", "--session", "session-id"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag, _ := newLifecycleAgent(t)
			backend := &commandCaptureBackend{Backend: &adaptertest.Backend{}}
			ag.Backend = backend
			ag.SessionArgs = func(ResumeRequest, string) []string {
				return []string{"--session", "session-id"}
			}
			var preparedCommand []string
			ag.PrepareLaunch = func(_ Context, _ ResumeRequest, _, sessionName, _ string) (LaunchPreparation, error) {
				preparedCommand = tt.preparedCommand(sessionName)
				return LaunchPreparation{Command: preparedCommand, ExtraArgs: []string{"--prepared"}}, nil
			}

			sess, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			want := tt.wantCommand(sess.SessionName)
			if !reflect.DeepEqual(backend.command, want) {
				t.Fatalf("CreateSession command = %#v, want %#v", backend.command, want)
			}

			if len(preparedCommand) > 0 {
				preparedCommand[0] = "mutated"
				if !reflect.DeepEqual(backend.command, want) {
					t.Fatalf("CreateSession command aliases preparation slice: got %#v, want %#v", backend.command, want)
				}
			}
		})
	}
}

func TestLaunchPreparationCommandRejectsInvalidAliasBeforeHook(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	prepareCalled := false
	ag.PrepareLaunch = func(Context, ResumeRequest, string, string, string) (LaunchPreparation, error) {
		prepareCalled = true
		return LaunchPreparation{Command: []string{"/trusted/uam", "__opencode"}}, nil
	}

	_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "bad alias", Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "invalid command alias") {
		t.Fatalf("Dispatch error = %v, want invalid command alias", err)
	}
	if prepareCalled {
		t.Fatal("invalid command alias reached PrepareLaunch")
	}
	if got := be.CallsOf("create"); len(got) != 0 {
		t.Fatalf("invalid command alias created session: %+v", got)
	}
}

func TestListFromSnapshotUsesLiveProviderIdentity(t *testing.T) {
	ag, _ := newLifecycleAgent(t)
	ag.LiveProviderSessionID = func(name string) (string, error) {
		if name != "uam-fake-abc12345" {
			t.Fatalf("name=%q", name)
		}
		return "ses_live", nil
	}
	got, err := ag.ListFromSnapshot(context.Background(), []session.Info{{Name: "uam-fake-abc12345", Cwd: "/tmp", Alive: true}})
	if err != nil || len(got) != 1 || got[0].ProviderSessionID != "ses_live" {
		t.Fatalf("ListFromSnapshot=%+v, %v", got, err)
	}
}

// An alias resolvable via LookPath replaces the default command argv[0] with
// the alias's absolute path; mode args still apply.
func TestAgentCommandAliasOnPathReplacesDefaultCommand(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	dir := t.TempDir()
	aliasPath := filepath.Join(dir, "myclaude")
	writeExecutable(t, aliasPath, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "myclaude", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logText := be.CommandLog()
	if !strings.HasPrefix(logText, aliasPath) || !strings.Contains(logText, "--yolo") {
		t.Fatalf("alias dispatch argv = %q, want resolved alias path with mode args", logText)
	}
}

// An alias not on PATH falls back to a `$SHELL -ic` probe: when the
// interactive shell knows it, the session command becomes the shell
// invocation.
func TestAgentCommandAliasFallsBackToInteractiveShell(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "fakeshell")
	writeExecutable(t, shellPath, "#!/bin/sh\nexit 0\n")
	t.Setenv("SHELL", shellPath)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "onlyinshell", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logText := be.CommandLog()
	if !strings.Contains(logText, shellPath+" -ic") || !strings.Contains(logText, "onlyinshell --yolo") {
		t.Fatalf("alias shell fallback argv = %q", logText)
	}
}

// An alias the interactive shell does not know must fail BEFORE any session is
// created.
func TestAgentCommandAliasMissingFromShellFailsBeforeCreate(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "fakeshell")
	writeExecutable(t, shellPath, "#!/bin/sh\nexit 1\n")
	t.Setenv("SHELL", shellPath)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: "ghostalias", Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "ghostalias") {
		t.Fatalf("want alias-not-found error, got %v", err)
	}
	if len(be.CallsOf("create")) != 0 {
		t.Fatal("no session may be created for an unresolvable alias")
	}
}

func TestAgentRejectsUnsafeCommandAlias(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	for _, alias := range []string{"bad alias", "a;b", "$(boom)", "-flag", "a/b"} {
		_, err := ag.Dispatch(context.Background(), DispatchRequest{CommandAlias: alias, Cwd: "/tmp", Mode: "yolo"})
		if err == nil {
			t.Fatalf("alias %q must be rejected", alias)
		}
	}
	if len(be.CallsOf("create")) != 0 {
		t.Fatal("no session may be created for an unsafe alias")
	}
}

type errReader struct{}

func (r errReader) Read([]byte) (int, error) { return 0, errors.New("entropy down") }

func TestDispatchReturnsRandomIDError(t *testing.T) {
	ag, _ := newLifecycleAgent(t)
	ag.randomReader = errReader{}
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "generate session id") {
		t.Fatalf("want id generation error, got %v", err)
	}
}

func TestResumeReturnsRandomIDErrorWhenIDMissing(t *testing.T) {
	ag, _ := newLifecycleAgent(t)
	ag.randomReader = errReader{}
	_, err := ag.Resume(context.Background(), ResumeRequest{Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "generate session id") {
		t.Fatalf("want id generation error, got %v", err)
	}
}

func TestNewIDKeepsUUIDFormat(t *testing.T) {
	id, err := newID(nil)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[4]) != 12 {
		t.Fatalf("id %q is not UUID-shaped", id)
	}
	if id[14] != '4' {
		t.Fatalf("id %q missing version nibble", id)
	}
}

// A session whose prompt cannot be delivered must be rolled back (killed), or
// it lingers as an orphan the store records as Exited/closed.
func TestStartSessionRollsBackOnSendLineFailure(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	be.SendErr = errors.New("send broke")
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "send prompt") {
		t.Fatalf("want send prompt error, got %v", err)
	}
	if len(be.CallsOf("kill")) != 1 {
		t.Fatal("failed prompt delivery must kill the just-created session")
	}
}

func TestStartSessionReturnsSendLineErrorWhenRollbackKillAlsoFails(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	be.SendErr = errors.New("send broke")
	be.KillErr = errors.New("kill broke too")
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "send broke") {
		t.Fatalf("caller must see the SendLine error, got %v", err)
	}
}

func TestAgentDispatchWithoutPromptSkipsSend(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	if _, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "   ", Cwd: "/tmp", Mode: "yolo"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(be.CallsOf("send")) != 0 {
		t.Fatal("blank prompt must not be sent")
	}
}

// F32 — internal targeting is always the exact canonical name, never a prefix
// that could hit a longer neighbour.
func TestTargetUsesExactCanonicalName(t *testing.T) {
	ag, _ := newLifecycleAgent(t)
	if got := ag.target("abc12345-dead-beef-cafe-0123456789ab"); got != "uam-fake-abc12345" {
		t.Fatalf("target(uuid) = %q", got)
	}
	if got := ag.target("uam-fake-abc12345"); got != "uam-fake-abc12345" {
		t.Fatalf("target(canonical) = %q", got)
	}
	if got := ag.target("abc"); got != "uam-fake-abc" {
		t.Fatalf("target(short) = %q", got)
	}
}

func TestStartSessionWrapsCreateSessionError(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	be.CreateErr = errors.New("spawn failed")
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), "create session") || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("want wrapped create error, got %v", err)
	}
}

func TestListWrapsBackendListError(t *testing.T) {
	ag, be := newLifecycleAgent(t)
	be.ListErr = errors.New("scan failed")
	_, err := ag.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list fake sessions") {
		t.Fatalf("want wrapped list error, got %v", err)
	}
}

func TestDisplayNameFromDir(t *testing.T) {
	if got := displayNameFromDir("/home/dev/projects/uam"); got != "uam" {
		t.Fatalf("displayNameFromDir = %q", got)
	}
	if got := displayNameFromDir("/"); got != "untitled" {
		t.Fatalf("displayNameFromDir(/) = %q", got)
	}
}

func TestAgentUnavailable(t *testing.T) {
	be := &adaptertest.Backend{}
	ag := NewAgent("ghost", "Ghost", []CommandCandidate{{Display: "ghostcli", Args: []string{"definitely-not-installed-cli"}}}, nil, be)
	ok, reason := ag.Available()
	if ok || !strings.Contains(reason, "ghostcli") {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	none := NewAgent("none", "None", nil, nil, be)
	if ok, reason := none.Available(); ok || reason != "no command configured" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
