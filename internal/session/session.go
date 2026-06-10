// Package session is uam's native session backend. It replaces the private
// tmux server: every managed agent runs under a small detached "host" process
// (`uam __host`, see host.go) that owns the agent's PTY, renders its output
// through an in-process terminal emulator, and serves peek / reply / attach /
// kill over a per-session Unix socket. Hosts outlive the uam process that
// started them, so sessions keep running when the TUI exits — the same
// lifetime contract the tmux server provided — without requiring tmux to be
// installed.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

// ErrInvalidSessionName is returned when a session name fails the allow-list.
var ErrInvalidSessionName = fmt.Errorf("session name failed allow-list")

// NameRE is the allow-list for session names uam may create. It matches the
// canonical shape minted by adapter.startSession ("uam-<provider>-<id>"): a
// lowercase-alphanumeric provider segment and a hex id segment. Names that
// pass are safe to embed in file paths (no separators, no dots).
var NameRE = regexp.MustCompile(`^uam-[a-z0-9]+-[0-9a-f]{1,16}$`)

// ValidateName rejects session names outside the canonical allow-list.
func ValidateName(name string) error {
	if !NameRE.MatchString(name) {
		return fmt.Errorf("invalid session name %q: %w", name, ErrInvalidSessionName)
	}
	return nil
}

// DefaultDir returns the runtime directory holding per-session sockets and
// state files. Resolution order: $UAM_SESSION_DIR, $XDG_RUNTIME_DIR/uam, then
// a per-UID directory under the system temp dir. Unix socket paths must stay
// short (the sockaddr_un limit is ~104 bytes), which rules out deep home
// paths.
func DefaultDir() string {
	if v := os.Getenv("UAM_SESSION_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "uam")
	}
	return filepath.Join(os.TempDir(), "uam-"+strconv.Itoa(os.Getuid()))
}

// EnsureDir creates the runtime directory owner-only. The 0700 mode is the
// security boundary: sockets and state files inside inherit protection from
// it, so another local user can neither attach to a session nor inject input.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session dir %s: %w", dir, err)
	}
	// MkdirAll is a no-op on an existing directory; re-assert the mode so a
	// pre-existing world-readable dir cannot silently expose sockets. 0700 is
	// required (not 0600): the owner must traverse the directory.
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- directory needs the execute bit; owner-only.
		return fmt.Errorf("restrict session dir %s: %w", dir, err)
	}
	return nil
}

// State is the on-disk record a host writes next to its socket. It is the
// native replacement for `tmux list-sessions` output: List scans these files
// to enumerate live sessions without dialing every socket.
type State struct {
	Name        string   `json:"name"`
	HostPID     int      `json:"host_pid"`
	ChildPID    int      `json:"child_pid"`
	CreatedUnix int64    `json:"created_unix"`
	Cwd         string   `json:"cwd"`
	Label       string   `json:"label,omitempty"`
	Command     []string `json:"command"`
}

// Info is one live session as reported by List.
type Info struct {
	Name        string
	CreatedUnix int64
	ChildPID    int
	// Cwd is the agent process's current working directory (live from /proc
	// when available, else the directory the session started in).
	Cwd string
	// Alive reports whether the agent process itself is still running. The
	// host lingers briefly after the child exits, so this is the liveness
	// signal the dashboard's Active/Failed classification keys on.
	Alive bool
}

func statePath(dir, name string) string { return filepath.Join(dir, name+".json") }

// SocketPath returns the control socket path for a session.
func SocketPath(dir, name string) string { return filepath.Join(dir, name+".sock") }

func writeState(dir string, st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	path := statePath(dir, st.Name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readState(dir, name string) (State, error) {
	data, err := os.ReadFile(statePath(dir, name))
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("parse session state %s: %w", name, err)
	}
	return st, nil
}

func removeSessionFiles(dir, name string) {
	_ = os.Remove(statePath(dir, name))
	_ = os.Remove(SocketPath(dir, name))
}

// ProcAlive reports whether pid is a live process (signal-0 probe). It is the
// native equivalent of the old tmux.PaneAlive.
func ProcAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// procCwd returns the live working directory of pid via /proc (Linux). On
// platforms or failures where that is unavailable it returns "".
func procCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return cwd
}
