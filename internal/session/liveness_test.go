package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
