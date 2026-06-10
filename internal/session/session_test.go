package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	dir := filepath.Join(t.TempDir(), "run")
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

func TestAgentExitMarksStoreRecordClosed(t *testing.T) {
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
	waitFor(t, "record marked closed", func() bool {
		cfg, err := st.Load()
		if err != nil {
			return false
		}
		rec := cfg.Sessions["fake:deadbeef"]
		return rec.Status == store.StatusClosedByUser && rec.LastExitCode != nil && *rec.LastExitCode == 3
	})
	waitFor(t, "runtime files removed", func() bool {
		_, err := os.Stat(SocketPath(c.Dir, name))
		return os.IsNotExist(err)
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
	for _, name := range []string{"uam-fake-aaaa1111", "uam-fake-bbbb2222"} {
		if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "sleep 60"}); err != nil {
			t.Fatalf("CreateSession %s: %v", name, err)
		}
	}
	if err := c.KillAll(ctx); err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	infos, err := c.List(ctx)
	if err != nil || len(infos) != 0 {
		t.Fatalf("sessions remain after KillAll: %+v %v", infos, err)
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
}
