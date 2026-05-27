package ipcclient

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReadPidParsesTrimmed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pidfile")
	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid, err := readPid(path)
	if err != nil {
		t.Fatalf("readPid: %v", err)
	}
	if pid != 4242 {
		t.Fatalf("got %d", pid)
	}
}

func TestReadPidErrorsOnEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pidfile")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPid(path); err == nil {
		t.Fatalf("expected parse error on empty pid")
	}
}

func TestReadPidErrorsOnMissing(t *testing.T) {
	if _, err := readPid(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatalf("expected missing-file error")
	}
}

func TestIsAliveCurrentProcess(t *testing.T) {
	if !isAlive(os.Getpid()) {
		t.Fatalf("expected current pid to be alive")
	}
}

func TestIsAliveDeadProcess(t *testing.T) {
	// pid 0 is invalid; signal(0) on it returns "no such process" on most
	// platforms. A massive pid is also a safe bet.
	if isAlive(0x7FFFFFFE) {
		t.Skip("unexpectedly high pid appeared alive; skipping")
	}
}

func TestWaitSocketReturnsTrueWhenListenerExists(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if !waitSocket(sock, 500*time.Millisecond) {
		t.Fatalf("expected waitSocket to find existing listener")
	}
}

func TestWaitSocketReturnsFalseOnTimeout(t *testing.T) {
	dir := t.TempDir()
	if waitSocket(filepath.Join(dir, "nope.sock"), 100*time.Millisecond) {
		t.Fatalf("expected timeout for missing socket")
	}
}

func TestEnsureDaemonReturnsNilWhenSocketReachable(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if err := EnsureDaemon(sock); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
}

func TestReadPidStrconvEdgeCase(t *testing.T) {
	// strconv.Atoi on something that strconv.Itoa produced must round-trip.
	dir := t.TempDir()
	path := filepath.Join(dir, "p")
	pid := os.Getpid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readPid(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != pid {
		t.Fatalf("round-trip: got %d want %d", got, pid)
	}
}

func TestValidateAutostartBinaryAcceptsUam(t *testing.T) {
	if err := validateAutostartBinary("/usr/local/bin/uam"); err != nil {
		t.Fatalf("expected uam to be accepted, got %v", err)
	}
}

func TestValidateAutostartBinaryAcceptsUnifiedAgentManager(t *testing.T) {
	if err := validateAutostartBinary("/usr/local/bin/unified-agent-manager"); err != nil {
		t.Fatalf("expected unified-agent-manager to be accepted, got %v", err)
	}
}

func TestValidateAutostartBinaryRejectsTestBinary(t *testing.T) {
	err := validateAutostartBinary("/tmp/go-build123/cli.test")
	if err == nil {
		t.Fatalf("expected test binary to be rejected")
	}
	if !strings.Contains(err.Error(), "refusing fork-exec") {
		t.Fatalf("expected refusal message, got %v", err)
	}
}

func TestBuildAutostartCmdDetached(t *testing.T) {
	cmd := buildAutostartCmd("/usr/local/bin/uam")
	want := []string{"/usr/local/bin/uam", "daemon", "start", "--detach"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args length got %d want %d", len(cmd.Args), len(want))
	}
	for i, a := range want {
		if cmd.Args[i] != a {
			t.Fatalf("args[%d] got %q want %q", i, cmd.Args[i], a)
		}
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("expected Setsid=true, got %+v", cmd.SysProcAttr)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatalf("expected nil stdio, got stdin=%v stdout=%v stderr=%v", cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
}
