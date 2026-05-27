package ipcclient

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// EnsureDaemon checks for a live daemon at socketPath; if missing,
// fork-execs `uam daemon start --detach`. Returns nil when the
// supervisor is reachable.
func EnsureDaemon(socketPath string) error {
	if conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil
	}
	pidPath := filepath.Join(filepath.Dir(socketPath), "uam.pid")
	if pid, err := readPid(pidPath); err == nil && isAlive(pid) {
		// A process owns the pidfile but its socket is not yet up; wait briefly.
		if waitSocket(socketPath, 2*time.Second) {
			return nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("ipcclient os.Executable: %w", err)
	}
	if err := validateAutostartBinary(exe); err != nil {
		return err
	}
	cmd := buildAutostartCmd(exe)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ipcclient autostart daemon: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("ipcclient daemon release: %w", err)
	}
	if !waitSocket(socketPath, 5*time.Second) {
		return fmt.Errorf("ipcclient: daemon did not become reachable within 5s")
	}
	return nil
}

func waitSocket(path string, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func readPid(path string) (int, error) {
	// #nosec G304 -- path is constructed from the supervisor socket dir.
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	return strconv.Atoi(s)
}

func isAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0 doesn't deliver but kernel still checks pid existence + perms.
	return proc.Signal(syscall.Signal(0)) == nil
}

// validateAutostartBinary refuses to autostart unless the running binary
// is `uam` (or the import-path-derived `unified-agent-manager`). Under
// `go test`, os.Executable() resolves to the package's *.test binary —
// fork-execing it would re-run the test suite recursively, detached and
// inheriting tty stdio, effectively a slow fork bomb. Failing fast lets
// callers fall back to tmux instead of burning the host.
func validateAutostartBinary(exe string) error {
	base := filepath.Base(exe)
	if base != "uam" && base != "unified-agent-manager" {
		return fmt.Errorf("ipcclient autostart: running binary %q is not uam; refusing fork-exec", base)
	}
	return nil
}

// buildAutostartCmd constructs the detached `uam daemon start --detach`
// invocation. Setsid + nil stdio belt-and-braces the basename guard so a
// stray non-uam binary that ever sneaks past the check cannot dump
// output onto the user's terminals. doubleForkDaemon inside the real
// `uam` binary repeats this for the eventual long-lived child; here we
// just need the immediate fork-exec to be detached.
func buildAutostartCmd(exe string) *exec.Cmd {
	// #nosec G204 -- exe is the absolute path of the binary running this
	// code; not user input.
	cmd := exec.Command(exe, "daemon", "start", "--detach")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd
}
