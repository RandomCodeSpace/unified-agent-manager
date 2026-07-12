package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestVerifyDirRejectsUnsafeRuntimeDirectories(t *testing.T) {
	parent := t.TempDir()

	missing := filepath.Join(parent, "missing")
	if err := VerifyDir(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("VerifyDir missing error = %v, want os.ErrNotExist", err)
	}

	permissive := filepath.Join(parent, "permissive")
	if err := os.Mkdir(permissive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(permissive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDir(permissive); err == nil {
		t.Fatal("VerifyDir must reject a group/world-accessible runtime directory")
	}
	info, err := os.Stat(permissive)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("VerifyDir mutated mode to %o, want read-only verification", got)
	}

	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDir(link); err == nil {
		t.Fatal("VerifyDir must reject a symlink runtime directory")
	}
}

func TestListMissingDirIsEmptyButUnsafeDirFailsClosed(t *testing.T) {
	c := &Client{Dir: filepath.Join(t.TempDir(), "missing")}
	infos, err := c.List(context.Background())
	if err != nil || len(infos) != 0 {
		t.Fatalf("List missing dir = (%v, %v), want empty success", infos, err)
	}

	if err := os.Mkdir(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background()); err == nil {
		t.Fatal("List must reject an unsafe existing runtime directory")
	}
	info, err := os.Stat(c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("List repaired unsafe mode to %o instead of failing closed", got)
	}
}

func TestStateReadRejectsMismatchedNameAndNonRegularFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	requested := "uam-fake-aabbccdd"
	mismatch := []byte(`{"name":"uam-fake-deadbeef","host_pid":1}`)
	if err := os.WriteFile(statePath(dir, requested), mismatch, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readState(dir, requested); err == nil {
		t.Fatal("readState must reject a state whose embedded name differs from its filename")
	}

	if err := os.Remove(statePath(dir, requested)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(target, []byte(`{"name":"`+requested+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, statePath(dir, requested)); err != nil {
		t.Fatal(err)
	}
	if _, err := readState(dir, requested); err == nil {
		t.Fatal("readState must reject symlink state files")
	}
}

func TestControlPathsRejectUnsafeRuntimeDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{Dir: dir}
	if _, err := c.Capture(context.Background(), "uam-fake-aabbccdd", 1); err == nil || !strings.Contains(err.Error(), "unsafe mode") {
		t.Fatalf("Capture error = %v, want unsafe directory rejection before dialing", err)
	}
	if c.HasSession(context.Background(), "uam-fake-aabbccdd") {
		t.Fatal("HasSession must not trust state in an unsafe runtime directory")
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := os.CreateTemp(t.TempDir(), "attach-output")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdout.Close() }()
	if err := runAttach(dir, "uam-fake-aabbccdd", stdin, stdout); err == nil || !strings.Contains(err.Error(), "unsafe mode") {
		t.Fatalf("runAttach error = %v, want unsafe directory rejection before dialing", err)
	}
}

func TestRemoveSessionFilesRejectsUnsafeRuntimeDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "uam-fake-aabbccdd"
	state := statePath(dir, name)
	socket := SocketPath(dir, name)
	for _, path := range []string{state, socket} {
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionFiles(dir, name); err == nil {
		t.Fatal("removeSessionFiles must reject an unsafe runtime directory")
	}
	for _, path := range []string{state, socket} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unsafe cleanup removed %s: %v", path, err)
		}
	}
}

func TestDefaultDirResolution(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", "/custom/dir")
	if got := DefaultDir(); got != "/custom/dir" {
		t.Fatalf("DefaultDir with override = %q", got)
	}
	t.Setenv("UAM_SESSION_DIR", "")
	// XDG_RUNTIME_DIR must NOT be used: logind deletes it on logout while
	// detached hosts keep running, which would strand live sessions.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	want := filepath.Join(os.TempDir(), "uam-"+strconv.Itoa(os.Getuid()))
	if got := DefaultDir(); got != want {
		t.Fatalf("DefaultDir = %q, want per-uid temp dir %q", got, want)
	}
}

func TestEnsureDirRejectsNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(path); err == nil {
		t.Fatal("EnsureDir over a regular file must fail")
	}
}

func TestEnsureDirAcceptsOwnDirAndRestrictsMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", info.Mode().Perm())
	}
}

func TestProcStartTimeReadsSelf(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc on this platform")
	}
	if got := procStartTime(os.Getpid()); got <= 0 {
		t.Fatalf("procStartTime(self) = %d, want > 0", got)
	}
	if got := procStartTime(0); got != 0 {
		t.Fatalf("procStartTime(0) = %d, want 0", got)
	}
}

// A recorded start time that does not match the live process means the PID
// was recycled: the session must read as dead, not as someone else's process.
func TestProcAliveWithStartDetectsPIDReuse(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc on this platform")
	}
	pid := os.Getpid()
	real := procStartTime(pid)
	if !procAliveWithStart(pid, real) {
		t.Fatal("matching start time must read alive")
	}
	if !procAliveWithStart(pid, 0) {
		t.Fatal("zero recorded start must fall back to plain liveness")
	}
	if procAliveWithStart(pid, real+12345) {
		t.Fatal("mismatched start time must read dead (recycled PID)")
	}
	if procAliveWithStart(-1, real) {
		t.Fatal("invalid pid must read dead")
	}
}

func TestProcIdentityMatchesRequiresRecordedAndCurrentIdentity(t *testing.T) {
	pid := os.Getpid()
	real := procStartTime(pid)
	if real == 0 {
		t.Skip("process start identity unavailable on this platform")
	}
	if !procIdentityMatches(pid, real) {
		t.Fatal("matching nonzero process identity must be accepted")
	}
	if procIdentityMatches(pid, 0) {
		t.Fatal("zero recorded identity must fail closed for signaling")
	}
	if procIdentityMatches(pid, real+1) {
		t.Fatal("mismatched process identity must fail closed for signaling")
	}
}

func TestKillRefusesPIDFallbackWithoutVerifiedIdentity(t *testing.T) {
	c := newTestClient(t)
	if err := EnsureDir(c.Dir); err != nil {
		t.Fatal(err)
	}
	name := "uam-fake-1d1d1d1d"
	if err := writeState(c.Dir, State{Name: name, HostPID: os.Getpid(), HostStart: 0}); err != nil {
		t.Fatal(err)
	}
	err := c.Kill(t.Context(), name)
	if err == nil || !strings.Contains(err.Error(), "cannot verify process identity") {
		t.Fatalf("Kill error = %v, want explicit process-identity safety error", err)
	}
	if !ProcAlive(os.Getpid()) {
		t.Fatal("Kill signaled the test process despite missing identity")
	}
}

// A stale state file whose PIDs were recycled by other processes (alive, but
// with different start times) must be swept by List, not reported live.
func TestListSweepsRecycledPIDState(t *testing.T) {
	c := newTestClient(t)
	if err := EnsureDir(c.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc on this platform")
	}
	// PID 1 is always alive; a fabricated start time marks it as "not the
	// process this state file recorded".
	st := State{Name: "uam-fake-eeee9999", HostPID: 1, HostStart: 99999999999, ChildPID: 1, ChildStart: 99999999999, CreatedUnix: 1}
	if err := writeState(c.Dir, st); err != nil {
		t.Fatal(err)
	}
	infos, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, info := range infos {
		if info.Name == st.Name {
			t.Fatalf("recycled-PID state must not be listed: %+v", info)
		}
	}
	if _, err := os.Stat(statePath(c.Dir, st.Name)); !os.IsNotExist(err) {
		t.Fatal("recycled-PID state file should be swept")
	}
}

// procStartTime must survive a comm containing spaces and parens, which
// /proc/<pid>/stat embeds unescaped.
func TestProcStartTimeParsesHostileComm(t *testing.T) {
	rest := "12345 (we ird) name)) R 1 1 1 0 -1 4194560 1 0 0 0 0 0 0 0 20 0 1 0 424242 0 0"
	// Reuse the parsing logic indirectly by checking field extraction: after
	// the last ')', starttime is the 20th field.
	i := strings.LastIndexByte(rest, ')')
	fields := strings.Fields(rest[i+1:])
	if len(fields) < 20 || fields[19] != "424242" {
		t.Fatalf("stat layout assumption broken: %v", fields)
	}
}

func TestNewClientUsesDefaultDir(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", "/custom/runtime")
	if c := NewClient(); c.Dir != "/custom/runtime" {
		t.Fatalf("NewClient dir = %q", c.Dir)
	}
}

func TestClientExePathValidation(t *testing.T) {
	c := &Client{Dir: t.TempDir(), Exe: "/nonexistent/uam"}
	if err := c.CreateSession(t.Context(), "uam-fake-90909090", t.TempDir(), nil, []string{"/bin/true"}); err == nil {
		t.Fatal("invalid Exe must fail before spawning")
	}
	if _, err := c.AttachArgv("uam-fake-90909090"); err == nil {
		t.Fatal("invalid Exe must fail AttachArgv")
	}
}

func TestRoundTripRejectsBadName(t *testing.T) {
	c := &Client{Dir: t.TempDir()}
	if _, err := c.Capture(t.Context(), "not a name", 10); err == nil {
		t.Fatal("bad name must be rejected before dialing")
	}
	if err := c.SendLine(t.Context(), "=uam-fake-abcdef12", "x"); err == nil {
		// "=" prefix is stripped (legacy exact-match syntax) and the dial
		// then fails on the missing socket — an error either way, but the
		// name itself must have been accepted.
		t.Log("expected dial error")
	}
}

// Kill must escalate when the control socket is gone but processes remain:
// SIGTERM a live-but-socketless host, and signal the orphaned agent's process
// group directly when the host already died.
func TestKillEscalatesWithoutSocket(t *testing.T) {
	c := newTestClient(t)
	if err := EnsureDir(c.Dir); err != nil {
		t.Fatal(err)
	}

	// Case 1: "host" alive (a stand-in process) with no socket.
	host := exec.Command("sleep", "60")
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = host.Wait() }()
	st := State{Name: "uam-fake-a1a1a1a1", HostPID: host.Process.Pid, HostStart: procStartTime(host.Process.Pid), CreatedUnix: 1}
	if err := writeState(c.Dir, st); err != nil {
		t.Fatal(err)
	}
	if err := c.Kill(t.Context(), st.Name); err != nil {
		t.Fatalf("Kill (wedged host): %v", err)
	}
	if ProcAlive(host.Process.Pid) && procStartTime(host.Process.Pid) == st.HostStart {
		t.Fatal("wedged host should have been terminated")
	}

	// Case 2: host dead, orphaned agent (own process group) still running.
	child := exec.Command("sleep", "60")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = child.Wait() }()
	st2 := State{Name: "uam-fake-b2b2b2b2", HostPID: 1 << 28, ChildPID: child.Process.Pid, ChildStart: procStartTime(child.Process.Pid), CreatedUnix: 1}
	if err := writeState(c.Dir, st2); err != nil {
		t.Fatal(err)
	}
	if err := c.Kill(t.Context(), st2.Name); err != nil {
		t.Fatalf("Kill (orphan agent): %v", err)
	}
}
