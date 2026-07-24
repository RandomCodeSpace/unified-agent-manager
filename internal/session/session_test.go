package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// TestMain doubles as the host/attach entry point: Client spawns
// os.Executable() with the internal __host/__attach argv, and under `go test`
// that executable is this test binary. Routing here exercises the real
// detached host end to end.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__host" {
		if err := RunHost(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "uam-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Dir: dir, Exe: exe}
	t.Cleanup(func() { _ = c.KillAll(context.Background()) })
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"uam-claude-abc12345", "uam-omp-0", "uam-x9-deadbeefcafe0123"} {
		if err := ValidateName(ok); err != nil {
			t.Fatalf("ValidateName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "uam-claude-", "uam-claude-XYZ", "uam--abc", "evil/../path", "uam-a-abc; rm -rf"} {
		if err := ValidateName(bad); err == nil {
			t.Fatalf("ValidateName(%q) should fail", bad)
		}
	}
}

// Killing a session and immediately recreating it under the same name (the
// restart flow) must leave the replacement's socket intact: the old host's
// deferred listener Close used to unlink the socket path when its process
// exited ~50ms AFTER Kill had already returned — deleting the socket the
// replacement host had just created, leaving a running but unreachable host.
func TestRecreateAfterKillKeepsSocket(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-cccc1111"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	st, err := readState(c.Dir, name)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	oldHost, oldStart := st.HostPID, st.HostStart
	if err := c.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	// Wait for the OLD host process to fully exit — its deferred cleanup is
	// what used to unlink the new socket — then the socket must still exist
	// and answer.
	waitFor(t, "old host exit", func() bool { return !procAliveWithStart(oldHost, oldStart) })
	time.Sleep(20 * time.Millisecond) // let any buggy deferred unlink land
	if _, err := os.Stat(SocketPath(c.Dir, name)); err != nil {
		t.Fatalf("replacement socket gone after old host exit: %v", err)
	}
	if _, err := c.Capture(ctx, name, 5); err != nil {
		t.Fatalf("peek after recreate: %v", err)
	}
}

func TestCreateListCaptureSendKill(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-abc12345"
	err := c.CreateSession(ctx, name, t.TempDir(), map[string]string{"UAM_TEST_VAR": "v1"}, []string{"/bin/sh", "-c", `echo "var=$UAM_TEST_VAR"; while read line; do echo "got:$line"; done`})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !c.HasSession(ctx, name) {
		t.Fatal("HasSession should be true after create")
	}

	infos, err := c.List(ctx)
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	if infos[0].Name != name || !infos[0].Alive || infos[0].ChildPID <= 0 {
		t.Fatalf("bad info: %+v", infos[0])
	}

	// Environment must reach the agent.
	waitFor(t, "env line in capture", func() bool {
		out, err := c.Capture(ctx, name, 50)
		return err == nil && strings.Contains(out, "var=v1")
	})

	// SendLine delivers text plus Enter; the shell loop echoes it back.
	if err := c.SendLine(ctx, name, "ping\n"); err != nil {
		t.Fatalf("SendLine: %v", err)
	}
	waitFor(t, "reply in capture", func() bool {
		out, _ := c.Capture(ctx, name, 50)
		return strings.Contains(out, "got:ping")
	})

	if err := c.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitFor(t, "session gone", func() bool { return !c.HasSession(ctx, name) })
	if _, err := os.Stat(SocketPath(c.Dir, name)); !os.IsNotExist(err) {
		t.Fatalf("socket not cleaned up: %v", err)
	}
}

func TestSendLineNormalizesUnicodeMultilineEmptyAndTrailingNewlines(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-abc12346"
	command := `stty -echo; printf 'ready\n'; while IFS= read -r line; do printf 'record:%s\n' "$line"; done`
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "SendLine fixture readiness", func() bool {
		out, err := c.Capture(ctx, name, 100)
		return err == nil && strings.Contains(out, "ready")
	})

	tests := []struct {
		name      string
		input     string
		wantLines []string
	}{
		{name: "Unicode", input: "unicode-π-你好", wantLines: []string{"record:unicode-π-你好"}},
		{name: "multiline", input: "multi-first\nmulti-second", wantLines: []string{"record:multi-first", "record:multi-second"}},
		{name: "empty", input: "", wantLines: []string{"record:"}},
		{name: "one trailing newline", input: "one-tail\n", wantLines: []string{"record:one-tail"}},
		{name: "many trailing newlines", input: "many-tail\n\n\n", wantLines: []string{"record:many-tail"}},
	}
	for _, tt := range tests {
		if err := c.SendLine(ctx, name, tt.input); err != nil {
			t.Fatalf("SendLine(%s): %v", tt.name, err)
		}
		last := tt.wantLines[len(tt.wantLines)-1]
		waitFor(t, tt.name+" output", func() bool {
			out, err := c.Capture(ctx, name, 100)
			return err == nil && strings.Contains(out, last)
		})
	}

	out, err := c.Capture(ctx, name, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "record:"); got != 6 {
		t.Fatalf("normalized records = %d, want 6 exactly; output=%q", got, out)
	}
	for _, want := range []string{"record:unicode-π-你好", "record:multi-first", "record:multi-second", "record:one-tail", "record:many-tail"} {
		if got := strings.Count(out, want); got != 1 {
			t.Fatalf("output count for %q = %d, want 1; output=%q", want, got, out)
		}
	}
	if err := c.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
}

func TestNaturalAgentCrashRemainsResumableAndRecordsFailure(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-deadbeef"

	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession("fake:deadbeef", store.SessionRecord{ID: "deadbeef", Agent: "fake", Name: "n", SessionName: name, Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "exit 3"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "natural crash recorded", func() bool {
		cfg, err := st.Load()
		if err != nil {
			return false
		}
		rec := cfg.Sessions["fake:deadbeef"]
		return rec.Status == store.StatusActive && rec.LastExitCode != nil && *rec.LastExitCode == 3
	})
	waitFor(t, "runtime files removed", func() bool {
		_, err := os.Stat(SocketPath(c.Dir, name))
		return os.IsNotExist(err)
	})
}

func TestImmediateExitRecordsProviderIdentityHandoff(t *testing.T) {
	c := newTestClient(t)
	name := "uam-opencode-a1b2c3d4"
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession("opencode:a1b2c3d4", store.SessionRecord{ID: "a1b2c3d4", Agent: "opencode", Name: "n", SessionName: name, Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handoff, err := ProviderIdentityPath(c.Dir, name)
	if err != nil {
		t.Fatal(err)
	}
	command := `umask 077; printf '{"session_name":"` + name + `","provider_session_id":"ses_fast123"}' > "$` + ProviderIdentityFileEnv + `"; exit 0`
	if err := c.CreateSession(context.Background(), name, t.TempDir(), map[string]string{ProviderIdentityFileEnv: handoff}, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "fast identity exit persisted", func() bool {
		cfg, err := st.Load()
		if err != nil {
			return false
		}
		rec := cfg.Sessions["opencode:a1b2c3d4"]
		return rec.LastExitCode != nil && *rec.LastExitCode == 0 && rec.ProviderSessionID == "ses_fast123"
	})
	for _, path := range []string{statePath(c.Dir, name), SocketPath(c.Dir, name), handoff} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("runtime file not removed after exit: %s: %v", path, err)
		}
	}
}

func TestImmediateExitRecordsProviderIdentityHandoffWithRelativeRuntimeDir(t *testing.T) {
	root, err := os.MkdirTemp("", "uam-rel-real-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	aliasedRoot := root + "-alias"
	if err := os.Symlink(root, aliasedRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(aliasedRoot) })
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(aliasedRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalCwd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(root, "cfg"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Dir: "runtime", Exe: exe}
	t.Cleanup(func() { _ = c.KillAll(context.Background()) })

	name := "uam-opencode-a1b2c3d5"
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession("opencode:a1b2c3d5", store.SessionRecord{ID: "a1b2c3d5", Agent: "opencode", Name: "n", SessionName: name, Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	absoluteRuntimeDir := filepath.Join(aliasedRoot, c.Dir)
	handoff, err := ProviderIdentityPath(absoluteRuntimeDir, name)
	if err != nil {
		t.Fatal(err)
	}
	command := `umask 077; printf '{"session_name":"` + name + `","provider_session_id":"ses_relative123"}' > "$` + ProviderIdentityFileEnv + `"; exit 0`
	if err := c.CreateSession(context.Background(), name, t.TempDir(), map[string]string{ProviderIdentityFileEnv: handoff}, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "relative runtime identity exit persisted", func() bool {
		cfg, err := st.Load()
		if err != nil {
			return false
		}
		rec := cfg.Sessions["opencode:a1b2c3d5"]
		return rec.LastExitCode != nil && *rec.LastExitCode == 0 && rec.ProviderSessionID == "ses_relative123"
	})
	for _, path := range []string{statePath(absoluteRuntimeDir, name), SocketPath(absoluteRuntimeDir, name), handoff} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("runtime file not removed after exit: %s: %v", path, err)
		}
	}
}

func TestCreateSessionPreservesShortRelativeSocketPathInDeepWorkingDirectory(t *testing.T) {
	root, err := os.MkdirTemp("", "uam-deep-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	deepCwd := filepath.Join(root, strings.Repeat("d", 90))
	if err := os.MkdirAll(deepCwd, 0o700); err != nil {
		t.Fatal(err)
	}
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(deepCwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalCwd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(root, "cfg"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Dir: ".uam", Exe: exe}
	t.Cleanup(func() { _ = c.KillAll(context.Background()) })

	name := "uam-fake-dead0001"
	if err := c.CreateSession(context.Background(), name, root, nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("CreateSession with short relative socket path: %v", err)
	}
	if !c.HasSession(context.Background(), name) {
		t.Fatal("relative-path session should be live after create")
	}
}

func TestProviderIdentityStaleHostCleanupRemovesAllRuntimeFiles(t *testing.T) {
	c := newTestClient(t)
	name := "uam-opencode-aabbccdd"
	if err := writeState(c.Dir, State{Name: name, HostPID: 1 << 28, ChildPID: 1 << 28, CreatedUnix: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SocketPath(c.Dir, name), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteProviderIdentity(c.Dir, name, "ses_stale"); err != nil {
		t.Fatal(err)
	}
	providerPath := providerIdentityTestPath(t, c.Dir, name)

	infos, err := c.List(context.Background())
	if err != nil || len(infos) != 0 {
		t.Fatalf("List stale session = (%+v, %v), want empty success", infos, err)
	}
	for _, path := range []string{statePath(c.Dir, name), SocketPath(c.Dir, name), providerPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stale runtime file not removed: %s: %v", path, err)
		}
	}
}

func TestNaturalAgentExitBeforeRecordPersistenceEventuallyRecordsExit(t *testing.T) {
	c := newTestClient(t)
	shortDir, err := os.MkdirTemp("", "uam-exit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
	c.Dir = shortDir
	ctx := context.Background()
	name := "uam-fake-feedface"

	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "exit 3"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Waiting for runtime cleanup proves the host observed the exit before the
	// durable record existed. The host must retain responsibility for recording
	// the exit long enough for dispatch persistence to catch up.
	waitFor(t, "runtime files removed before persistence", func() bool {
		_, err := os.Stat(SocketPath(c.Dir, name))
		return os.IsNotExist(err)
	})
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession("fake:feedface", store.SessionRecord{ID: "feedface", Agent: "fake", Name: "n", SessionName: name, Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "late natural exit recorded", func() bool {
		cfg, err := st.Load()
		if err != nil {
			return false
		}
		rec := cfg.Sessions["fake:feedface"]
		return rec.Status == store.StatusActive && rec.LastExitCode != nil && *rec.LastExitCode == 3
	})
}

func TestNaturalAgentExitZeroRemainsResumable(t *testing.T) {
	c := newTestClient(t)
	name := "uam-fake-cafebabe"
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession("fake:cafebabe", store.SessionRecord{ID: "cafebabe", Agent: "fake", SessionName: name, Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateSession(context.Background(), name, t.TempDir(), nil, []string{"/bin/sh", "-c", "exit 0"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "natural success recorded", func() bool {
		cfg, err := st.Load()
		if err != nil {
			return false
		}
		rec := cfg.Sessions["fake:cafebabe"]
		return rec.Status == store.StatusActive && rec.LastExitCode != nil && *rec.LastExitCode == 0
	})
}

func TestCreateSessionReportsStartupFailure(t *testing.T) {
	c := newTestClient(t)
	err := c.CreateSession(context.Background(), "uam-fake-11112222", t.TempDir(), nil, []string{"/nonexistent/agent-binary"})
	if err == nil || !strings.Contains(err.Error(), "agent-binary") {
		t.Fatalf("want startup failure mentioning the command, got %v", err)
	}
	if c.HasSession(context.Background(), "uam-fake-11112222") {
		t.Fatal("failed create must not leave a session behind")
	}
}

func TestCreateSessionRejectsDuplicate(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-33334444"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate create should fail, got %v", err)
	}
}

func TestCreateSessionRejectsInvalidName(t *testing.T) {
	c := newTestClient(t)
	if err := c.CreateSession(context.Background(), "evil name", t.TempDir(), nil, []string{"/bin/sh"}); err == nil {
		t.Fatal("invalid name must be rejected")
	}
}

func TestKillMissingSessionErrors(t *testing.T) {
	c := newTestClient(t)
	if err := c.Kill(context.Background(), "uam-fake-99990000"); err == nil {
		t.Fatal("killing a missing session should error")
	}
	if c.HasSession(context.Background(), "uam-fake-99990000") {
		t.Fatal("HasSession on missing session")
	}
}

func TestKillAllIsIdempotent(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	if err := c.KillAll(ctx); err != nil {
		t.Fatalf("KillAll on empty dir: %v", err)
	}
	names := []string{"uam-fake-aaaa1111", "uam-fake-bbbb2222"}
	states := make(map[string]State, len(names))
	for index, name := range names {
		if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
			t.Fatalf("CreateSession %s: %v", name, err)
		}
		if err := WriteProviderIdentity(c.Dir, name, fmt.Sprintf("ses_cleanup%d", index)); err != nil {
			t.Fatalf("WriteProviderIdentity %s: %v", name, err)
		}
		state, err := readState(c.Dir, name)
		if err != nil {
			t.Fatalf("readState %s: %v", name, err)
		}
		states[name] = state
	}
	if err := c.KillAll(ctx); err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	infos, err := c.List(ctx)
	if err != nil || len(infos) != 0 {
		t.Fatalf("sessions remain after KillAll: %+v %v", infos, err)
	}
	for _, name := range names {
		state := states[name]
		waitFor(t, "KillAll process cleanup for "+name, func() bool {
			return !state.hostAlive() && !state.childAlive()
		})
		if state.hostAlive() || state.childAlive() {
			t.Fatalf("process remains after KillAll for %s: host=%d child=%d", name, state.HostPID, state.ChildPID)
		}
		providerPath, err := ProviderIdentityPath(c.Dir, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{statePath(c.Dir, name), SocketPath(c.Dir, name), providerPath} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("runtime artifact remains after KillAll: %s (%v)", path, err)
			}
		}
	}
	if err := c.KillAll(ctx); err != nil {
		t.Fatalf("KillAll repeat: %v", err)
	}
}

func TestSetSessionLabelPersistsToState(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-cccc3333"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := c.SetSessionLabel(ctx, name, "fixer · fake"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}
	waitFor(t, "label in state file", func() bool {
		st, err := readState(c.Dir, name)
		return err == nil && st.Label == "fixer · fake"
	})
}

func TestTitleSequenceSanitizesTerminalControls(t *testing.T) {
	got := titleSequence("safe\u009d0;forged\a red\nnow")
	want := "\x1b]0;safe red now\x07"
	if got != want {
		t.Fatalf("titleSequence = %q, want %q", got, want)
	}
}

func TestListSweepsStaleStateFiles(t *testing.T) {
	c := newTestClient(t)
	if err := EnsureDir(c.Dir); err != nil {
		t.Fatal(err)
	}
	// A state file whose host and child pids are both long dead.
	if err := writeState(c.Dir, State{Name: "uam-fake-dddd4444", HostPID: 1 << 28, ChildPID: 1 << 28, CreatedUnix: 1}); err != nil {
		t.Fatal(err)
	}
	infos, err := c.List(context.Background())
	if err != nil || len(infos) != 0 {
		t.Fatalf("stale state should be swept: %+v %v", infos, err)
	}
	if _, err := os.Stat(statePath(c.Dir, "uam-fake-dddd4444")); !os.IsNotExist(err) {
		t.Fatal("stale state file should be removed")
	}
}

func TestAttachArgvUsesOwnBinary(t *testing.T) {
	c := newTestClient(t)
	argv, err := c.AttachArgv("uam-fake-eeee5555")
	if err != nil {
		t.Fatalf("AttachArgv: %v", err)
	}
	if len(argv) < 3 || argv[0] != c.Exe || argv[1] != "__attach" || argv[len(argv)-1] != "uam-fake-eeee5555" {
		t.Fatalf("bad attach argv: %v", argv)
	}
}

// Attach end to end through the client side: connect, see the screen replay,
// type a line, detach with the Ctrl+B d chord.
func TestAttachStreamsAndDetaches(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-ffff6666"
	err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", `echo banner; while read line; do echo "typed:$line"; done`})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "banner", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "banner")
	})

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runAttach(c.Dir, name, stdinR, stdoutW) }()
	go func() {
		_, _ = stdinW.WriteString("hi\r")
		// Wait for the round-trip before detaching so output is deterministic.
		waitFor(t, "typed echo", func() bool {
			out, _ := c.Capture(ctx, name, 10)
			return strings.Contains(out, "typed:hi")
		})
		_, _ = stdinW.Write([]byte{0x02, 'd'}) // Ctrl+B d
	}()

	if err := <-done; err != nil {
		t.Fatalf("runAttach: %v", err)
	}
	_ = stdoutW.Close()
	buf := make([]byte, 64*1024)
	n, _ := stdoutR.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "banner") {
		t.Fatalf("attach replay missing banner: %q", out)
	}
	if !strings.Contains(out, "[uam: detached]") {
		t.Fatalf("missing detach notice: %q", out)
	}
	// Terminal-state ownership is tty-only: piped output must stay free of
	// alternate-screen switches.
	if strings.Contains(out, "\x1b[?1049") {
		t.Fatalf("non-tty attach must not emit alt-screen sequences: %q", out)
	}
}

// On a real terminal the attach client must own the screen the way
// `tmux attach` did: bridge the session inside its own alternate screen (so
// the replay's clear and live agent output never land on the user's primary
// screen — the buffer uam's TUI reveals again when it exits), and on detach
// reset the modes an agent could have leaked (mouse tracking, bracketed
// paste, kitty keyboard, hidden cursor) before handing the primary screen
// back. Regression test for the post-tmux rendering bugs: corrupted TUI after
// attach/detach and session residue left on the terminal after quitting uam.
func TestAttachOwnsTerminalStateOnTTY(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-aaaa9999"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "echo banner; sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "banner", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "banner")
	})

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()

	done := make(chan error, 1)
	go func() { done <- runAttach(c.Dir, name, tty, tty) }()

	var mu sync.Mutex
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(got)
	}

	waitFor(t, "replay banner", func() bool { return strings.Contains(snapshot(), "banner") })
	pre := snapshot()
	enter := strings.Index(pre, "\x1b[?1049h")
	if enter < 0 {
		t.Fatalf("tty attach must enter its own alternate screen: %q", pre)
	}
	if clear := strings.Index(pre, "\x1b[2J"); clear >= 0 && clear < enter {
		t.Fatalf("replay clear must land inside the alt screen, not on the primary: %q", pre)
	}
	// Alternate scroll mode (?1007) turns mouse wheel motion into arrow keys
	// on the alt screen; left enabled, scrolling types into the agent.
	if scroll := strings.Index(pre, "\x1b[?1007l"); scroll < 0 || scroll < enter {
		t.Fatalf("attach must disable alternate scroll inside its alt screen: %q", pre)
	}

	if _, err := ptmx.Write([]byte{0x02, 'd'}); err != nil { // Ctrl+B d
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runAttach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach chord did not detach")
	}
	waitFor(t, "detach notice", func() bool { return strings.Contains(snapshot(), "[uam: detached]") })
	full := snapshot()
	exit := strings.Index(full, "\x1b[?1049l")
	if exit < 0 {
		t.Fatalf("detach must leave the alternate screen: %q", full)
	}
	for _, reset := range []string{
		"\x1b[?1000;1002;1003;1004;1005;1006;1015l", // mouse tracking + focus reporting off
		"\x1b[?2004l", // bracketed paste off
		"\x1b[?25h",   // cursor visible
		"\x1b[?1007r", // alternate scroll restored to the user's saved setting
	} {
		idx := strings.Index(full, reset)
		if idx < 0 {
			t.Fatalf("detach must reset leakable terminal modes (missing %q): %q", reset, full)
		}
		if idx > exit {
			t.Fatalf("mode reset %q must precede the alt-screen exit: %q", reset, full)
		}
	}
	if note := strings.Index(full, "[uam: detached]"); note < exit {
		t.Fatalf("detach notice must print on the restored primary screen: %q", full)
	}
	if !c.HasSession(ctx, name) {
		t.Fatal("session must keep running after detach")
	}
}

func TestLegacyCodexNamePrefixPreservesPrimaryScreenScrollback(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-codex-aaaa9999"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "echo codex-inline; sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "codex inline marker", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "codex-inline")
	})

	attached := startQuietAttach(t, c.Dir, name, 80, 24)
	waitFor(t, "codex attach replay", func() bool { return strings.Contains(attached.Snapshot(), "codex-inline") })
	if output := attached.Snapshot(); strings.Contains(output, "\x1b[?1049h") {
		t.Fatalf("codex attach entered an alternate screen and hid terminal scrollback: %q", output)
	}
	attached.Detach(t)
	if output := attached.Snapshot(); strings.Contains(output, "\x1b[?1049l") {
		t.Fatalf("codex attach left an alternate screen it never entered: %q", output)
	}
}

type attachedPTY struct {
	ptmx     *os.File
	done     chan error
	snapshot func() string
}

func startQuietAttach(t *testing.T, dir, name string, cols, rows uint16) *attachedPTY {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true}) }()
	return &attachedPTY{ptmx: ptmx, done: done, snapshot: capturePTYOutput(ptmx)}
}

func capturePTYOutput(ptmx *os.File) func() string {
	var mu sync.Mutex
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(got)
	}
}

func (a *attachedPTY) Snapshot() string { return a.snapshot() }

func (a *attachedPTY) Detach(t *testing.T) {
	t.Helper()
	if _, err := a.ptmx.Write([]byte{0x02, 'd'}); err != nil { // Ctrl+B d
		t.Fatal(err)
	}
	select {
	case err := <-a.done:
		if err != nil {
			t.Fatalf("runAttach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach chord did not detach")
	}
}

func TestResizeNudgeBounds(t *testing.T) {
	cols, rows, ok := resizeNudge(80, 24)
	if !ok || cols != 80 || rows != 23 {
		t.Fatalf("row nudge = %dx%d %v, want 80x23 true", cols, rows, ok)
	}

	cols, rows, ok = resizeNudge(2, 1)
	if !ok || cols != 1 || rows != 1 {
		t.Fatalf("column nudge = %dx%d %v, want 1x1 true", cols, rows, ok)
	}

	if _, _, ok := resizeNudge(1, 1); ok {
		t.Fatal("1x1 terminal must not be nudged")
	}
	if _, _, ok := resizeNudge(0, 24); ok {
		t.Fatal("invalid terminal size must not be nudged")
	}

	h := &host{}
	h.applyResizeLocked(0, 24)
	h.applyPTYSizeLocked(80, 0)
}

func TestAttachReplayUsesCurrentTerminalSize(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-eeee9999"
	cmd := []string{"/bin/sh", "-c", "printf '\033[999;1Hedge\033[999;999H'; sleep 60"}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, cmd); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "edge", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "edge")
	})

	attached := startQuietAttach(t, c.Dir, name, 80, 24)
	waitFor(t, "initial replay cursor", func() bool {
		out := attached.Snapshot()
		return strings.Contains(out, "\x1b[24;80H") || strings.Contains(out, "\x1b[50;200H")
	})
	out := attached.Snapshot()
	if !strings.Contains(out, "edge") {
		t.Fatalf("attach replay missing edge marker: %q", out)
	}
	if !strings.Contains(out, "\x1b[24;80H") {
		t.Fatalf("attach replay must park cursor using attached terminal size: %q", out)
	}
	if strings.Contains(out, "\x1b[50;200H") {
		t.Fatalf("attach replay must not use detached terminal size: %q", out)
	}
	attached.Detach(t)
}

func TestSameSizeAttachNudgeDoesNotTruncateReplay(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-dddd9999"
	cmd := []string{"/bin/sh", "-c", `while IFS= read -r line; do [ "$line" = paint ] && printf '\033[Htop-row-safe\033[24;1Hbottom-row-guard\033[H'; done`}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, cmd); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	attachOnce := func(what string, ready func(string) bool) string {
		attached := startQuietAttach(t, c.Dir, name, 80, 24)
		waitFor(t, what, func() bool { return ready(attached.Snapshot()) })
		out := attached.Snapshot()
		attached.Detach(t)
		return out
	}

	attachOnce("initial empty replay", func(out string) bool {
		return strings.Contains(out, "\x1b[1;1H")
	})
	if err := c.SendLine(ctx, name, "paint"); err != nil {
		t.Fatalf("SendLine: %v", err)
	}
	waitFor(t, "painted screen", func() bool {
		out, _ := c.Capture(ctx, name, 30)
		return strings.Contains(out, "top-row-safe") && strings.Contains(out, "bottom-row-guard")
	})

	out := attachOnce("same-size replay", func(out string) bool {
		return strings.Contains(out, "bottom-row-guard")
	})
	if !strings.Contains(out, "top-row-safe") {
		t.Fatalf("same-size attach replay lost top-row content: %q", out)
	}
}

// Detaching while the agent is mid-burst must not scribble the primary
// screen: bytes still buffered in the host→terminal pump at detach time have
// to be drained inside the alternate screen, so nothing follows the
// alt-screen exit except the detach notice.
func TestAttachDetachDrainsPumpBeforeScreenRestore(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-bbbb0000"
	err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "i=0; while [ $i -lt 2000 ]; do echo spam$i; i=$((i+1)); done; sleep 60"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()

	done := make(chan error, 1)
	go func() { done <- runAttach(c.Dir, name, tty, tty) }()

	var mu sync.Mutex
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(got)
	}

	// Detach as soon as the burst starts flowing, while plenty of it is
	// still in flight between the host and the terminal.
	waitFor(t, "burst start", func() bool { return strings.Contains(snapshot(), "spam") })
	if _, err := ptmx.Write([]byte{0x02, 'd'}); err != nil { // Ctrl+B d
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runAttach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach chord did not detach")
	}
	waitFor(t, "detach notice", func() bool { return strings.Contains(snapshot(), "[uam: detached]") })
	full := snapshot()
	exit := strings.LastIndex(full, "\x1b[?1049l")
	if exit < 0 {
		t.Fatalf("detach must leave the alternate screen: %q", full)
	}
	// The note is written after termios are restored, so the pty line
	// discipline may ONLCR-translate its newlines — compare CR-insensitively.
	tail := strings.ReplaceAll(full[exit+len("\x1b[?1049l"):], "\r", "")
	if tail != "\n[uam: detached]\n" {
		t.Fatalf("only the detach notice may follow the alt-screen exit, got %q", tail)
	}
}

func TestAttachQuietSuppressesPrimaryScreenNotice(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-abcd2222"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "echo banner; sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "banner", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "banner")
	})

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()

	done := make(chan error, 1)
	go func() { done <- runAttachWithOptions(c.Dir, name, tty, tty, attachOptions{quiet: true}) }()

	var mu sync.Mutex
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(got)
	}

	waitFor(t, "replay banner", func() bool { return strings.Contains(snapshot(), "banner") })
	if _, err := ptmx.Write([]byte{0x02, 'd'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runAttach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach chord did not detach")
	}
	waitFor(t, "alternate screen exit", func() bool { return strings.Contains(snapshot(), "\x1b[?1049l") })

	full := snapshot()
	exit := strings.LastIndex(full, "\x1b[?1049l")
	if exit < 0 {
		t.Fatalf("detach must leave the alternate screen: %q", full)
	}
	tail := strings.ReplaceAll(full[exit+len("\x1b[?1049l"):], "\r", "")
	if strings.Contains(tail, "[uam:") {
		t.Fatalf("quiet attach must not print a primary-screen notice, tail=%q full=%q", tail, full)
	}
}

// On re-attach the agent's input-affecting private modes (application cursor
// keys, mouse tracking) must come back. The agent sets them live only on its
// first paint; the attach client resets them on detach, so the host's Redraw
// has to replay them or arrows and wheel scroll die on every re-entry.
// Regression test for the resume/re-attach mode-loss bug.
func TestReattachReplaysAgentPrivateModes(t *testing.T) {
	t.Setenv(AttachMouseEnv, "on")
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-cccc1111"
	// The agent enables application cursor keys + SGR mouse, prints a banner,
	// then idles — the modes land in the host's vterm before any attach.
	cmd := []string{"/bin/sh", "-c", "printf '\\033[?1h\\033[?1000h\\033[?1006h'; echo banner; sleep 60"}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, cmd); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	attachOnce := func() string {
		ptmx, tty, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ptmx.Close(); _ = tty.Close() }()
		done := make(chan error, 1)
		go func() { done <- runAttach(c.Dir, name, tty, tty) }()
		var mu sync.Mutex
		var got []byte
		go func() {
			buf := make([]byte, 4096)
			for {
				n, rerr := ptmx.Read(buf)
				if n > 0 {
					mu.Lock()
					got = append(got, buf[:n]...)
					mu.Unlock()
				}
				if rerr != nil {
					return
				}
			}
		}()
		snapshot := func() string {
			mu.Lock()
			defer mu.Unlock()
			return string(got)
		}
		waitFor(t, "replay banner", func() bool { return strings.Contains(snapshot(), "banner") })
		out := snapshot()
		if _, err := ptmx.Write([]byte{0x02, 'd'}); err != nil { // Ctrl+B d
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runAttach: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("detach chord did not detach")
		}
		return out
	}

	if first := attachOnce(); !strings.Contains(first, "banner") {
		t.Fatalf("first attach missing banner: %q", first)
	}
	second := attachOnce()
	for _, want := range []string{"\x1b[?1h", "\x1b[?1000h", "\x1b[?1006h"} {
		if !strings.Contains(second, want) {
			t.Fatalf("re-attach must replay agent private mode %q: %q", want, second)
		}
	}
}

func TestAttachMouseOffFiltersReplayedProviderModes(t *testing.T) {
	t.Setenv(AttachMouseEnv, "off")
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-78780001"
	cmd := []string{"/bin/sh", "-c", "printf '\033[?1;1000;2004;1006hmouse-policy-marker'; sleep 60"}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, cmd); err != nil {
		t.Fatal(err)
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()
	done := make(chan error, 1)
	go func() { done <- runAttachWithOptions(c.Dir, name, tty, tty, attachOptions{quiet: true}) }()
	var mu sync.Mutex
	var output []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				output = append(output, buf[:n]...)
				mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}()
	snapshot := func() string { mu.Lock(); defer mu.Unlock(); return string(output) }
	waitFor(t, "mouse policy marker", func() bool { return strings.Contains(snapshot(), "mouse-policy-marker") })
	beforeDetach := snapshot()
	if err := validateMouseOffAttachOutput(beforeDetach); err != nil {
		t.Fatalf("mouse-off attach output: %v: %q", err, beforeDetach)
	}
	if _, err := ptmx.Write([]byte{detachPrefix, 'd'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach timed out")
	}
}

func TestValidateMouseOffAttachOutputAcceptsGroupedDarwinModes(t *testing.T) {
	for _, darwinOutput := range []string{
		screenEnter + "\x1b[2J\x1b[H\x1b[?1;2004hmouse-policy-marker",
		screenEnter + "\x1b[?2004;1hmouse-policy-marker",
	} {
		if err := validateMouseOffAttachOutput(darwinOutput); err != nil {
			t.Fatalf("equivalent grouped private modes rejected: %v", err)
		}
	}
}

func TestValidateMouseOffAttachOutputRejectsWeakenedEvidence(t *testing.T) {
	tests := []struct{ name, output string }{
		{"missing outer screen", "\x1b[?1;2004hmouse-policy-marker"},
		{"missing cursor mode", screenEnter + "\x1b[?2004hmouse-policy-marker"},
		{"missing paste mode", screenEnter + "\x1b[?1hmouse-policy-marker"},
		{"mouse combined", screenEnter + "\x1b[?1;1000;2004hmouse-policy-marker"},
		{"provider alt enable", screenEnter + "\x1b[?1;47;2004hmouse-policy-marker"},
		{"second outer enable", screenEnter + "\x1b[?1;1049;2004hmouse-policy-marker"},
		{"provider alt disable", screenEnter + "\x1b[?1047l\x1b[?1;2004hmouse-policy-marker"},
		{"missing payload", screenEnter + "\x1b[?1;2004h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateMouseOffAttachOutput(tt.output); err == nil {
				t.Fatalf("invalid output accepted: %q", tt.output)
			}
		})
	}
}

func validateMouseOffAttachOutput(output string) error {
	if !strings.HasPrefix(output, screenEnter) {
		return fmt.Errorf("missing attach-owned outer screen")
	}
	body := output[len(screenEnter):]
	marker := strings.Index(body, "mouse-policy-marker")
	if marker < 0 {
		return fmt.Errorf("missing provider payload marker")
	}
	body = body[:marker]
	foundEnabled := map[string]bool{}
	for i := 0; i+3 < len(body); i++ {
		if body[i] != 0x1b || body[i+1] != '[' || body[i+2] != '?' {
			continue
		}
		final := i + 3
		for final < len(body) && (body[final] < 0x40 || body[final] > 0x7e) {
			final++
		}
		if final == len(body) || (body[final] != 'h' && body[final] != 'l') {
			continue
		}
		params := strings.Split(body[i+3:final], ";")
		for _, param := range params {
			switch param {
			case "47", "1047", "1049":
				return fmt.Errorf("provider alternate-screen mode %s%c leaked", param, body[final])
			case "1000", "1002", "1003", "1005", "1006", "1015":
				return fmt.Errorf("provider mouse mode %s%c leaked", param, body[final])
			}
			if body[final] == 'h' {
				foundEnabled[param] = true
			}
		}
		i = final
	}
	for _, mode := range []string{"1", "2004"} {
		if !foundEnabled[mode] {
			return fmt.Errorf("missing preserved provider mode %s", mode)
		}
	}
	return nil
}

// The left-arrow quick detach works end to end through the real attach
// client: with nothing typed since attach, a bare left arrow detaches.
func TestAttachLeftArrowDetaches(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-77778888"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "echo up; sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runAttach(c.Dir, name, stdinR, stdoutW) }()
	if _, err := stdinW.Write([]byte("\x1b[D")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runAttach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("left arrow did not detach")
	}
	_ = stdoutW.Close()
	buf := make([]byte, 64*1024)
	n, _ := stdoutR.Read(buf)
	if !strings.Contains(string(buf[:n]), "[uam: detached]") {
		t.Fatalf("missing detach notice: %q", string(buf[:n]))
	}
	if !c.HasSession(ctx, name) {
		t.Fatal("session must keep running after quick detach")
	}
}
