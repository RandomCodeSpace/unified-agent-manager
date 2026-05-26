package ipcclient

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	// #nosec G204 -- exe is the absolute path of the binary running this
	// code; not user input.
	cmd := exec.Command(exe, "daemon", "start", "--detach")
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
	return proc.Signal(nil) == nil
}
