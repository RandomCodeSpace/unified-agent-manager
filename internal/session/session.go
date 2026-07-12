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
// state files: $UAM_SESSION_DIR if set, else a per-UID directory under the
// system temp dir (like tmux's /tmp/tmux-<uid>).
//
// $XDG_RUNTIME_DIR is deliberately NOT used: systemd-logind deletes it when
// the user's last login session ends, not only on reboot — which would strand
// still-running detached hosts (they survive logout) with no socket or state
// file, and a later "resume" would spawn duplicates. The temp dir survives
// logout and is cleared on reboot, matching the hosts' actual lifetime. Unix
// socket paths must also stay short (the sockaddr_un limit is ~104 bytes),
// which rules out deep home paths.
func DefaultDir() string {
	if v := os.Getenv("UAM_SESSION_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "uam-"+strconv.Itoa(os.Getuid()))
}

// EnsureDir creates the runtime directory owner-only. The 0700 mode is the
// security boundary: sockets and state files inside inherit protection from
// it, so another local user can neither attach to a session nor inject input.
// Because the default parent is the sticky shared temp dir, the directory is
// also verified to be a real directory (not a symlink) owned by the current
// user — a foreign pre-created /tmp/uam-<uid> is refused, like tmux refuses a
// foreign /tmp/tmux-<uid>.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session dir %s: %w", dir, err)
	}
	if err := verifyDirIdentity(dir); err != nil {
		return err
	}
	// MkdirAll is a no-op on an existing directory. Creation paths retain the
	// historical repair behavior for a directory owned by this user; read and
	// control paths use VerifyDir and fail closed instead.
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- directory needs the execute bit; owner-only.
		return fmt.Errorf("restrict session dir %s: %w", dir, err)
	}
	return VerifyDir(dir)
}

// VerifyDir validates an existing runtime directory without changing it.
// The directory is the local authorization boundary around session sockets
// and state, so every read/control path must call this before trusting files
// beneath it.
func VerifyDir(dir string) error {
	if err := verifyDirIdentity(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat session dir %s: %w", dir, err)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("session dir %s has unsafe mode %04o; want 0700", dir, info.Mode().Perm())
	}
	return nil
}

func verifyDirIdentity(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat session dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("session dir %s is not a directory", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("session dir %s is owned by uid %d, not the current user", dir, st.Uid)
	}
	return nil
}

// State is the on-disk record a host writes next to its socket. It is the
// native replacement for `tmux list-sessions` output: List scans these files
// to enumerate live sessions without dialing every socket.
type State struct {
	Name    string `json:"name"`
	HostPID int    `json:"host_pid"`
	// HostStart / ChildStart are platform-specific stable process identities
	// derived from kernel start times (0 where unavailable). They disambiguate
	// a recycled PID from the original
	// process, so a stale state file can never make uam treat — or worse,
	// signal — an unrelated process as a session.
	HostStart   int64    `json:"host_start,omitempty"`
	ChildPID    int      `json:"child_pid"`
	ChildStart  int64    `json:"child_start,omitempty"`
	CreatedUnix int64    `json:"created_unix"`
	Cwd         string   `json:"cwd"`
	Label       string   `json:"label,omitempty"`
	Command     []string `json:"command"`
}

// hostAlive / childAlive are the start-time-verified liveness probes for a
// persisted state record.
func (st State) hostAlive() bool  { return procAliveWithStart(st.HostPID, st.HostStart) }
func (st State) childAlive() bool { return procAliveWithStart(st.ChildPID, st.ChildStart) }

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
	if err := VerifyDir(dir); err != nil {
		return err
	}
	if err := ValidateName(st.Name); err != nil {
		return err
	}
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
	if err := ValidateName(name); err != nil {
		return State{}, err
	}
	if err := VerifyDir(dir); err != nil {
		return State{}, err
	}
	path := statePath(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return State{}, err
	}
	if !info.Mode().IsRegular() {
		return State{}, fmt.Errorf("session state %s is not a regular file", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("parse session state %s: %w", name, err)
	}
	if st.Name != name {
		return State{}, fmt.Errorf("session state name %q does not match file name %q", st.Name, name)
	}
	return st, nil
}

func removeSessionFiles(dir, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := VerifyDir(dir); err != nil {
		return err
	}
	for _, path := range []string{statePath(dir, name), SocketPath(dir, name)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ProcAlive reports whether pid is a live process (signal-0 probe). It is the
// native equivalent of the old tmux.PaneAlive.
func ProcAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// procAliveWithStart is ProcAlive hardened against PID reuse: when a start
// identity was recorded AND the live process's identity is readable, they
// must match. This intentionally remains permissive for display compatibility:
// missing identity degrades to the plain signal-0 probe. Signaling paths use
// procIdentityMatches instead.
func procAliveWithStart(pid int, start int64) bool {
	if !ProcAlive(pid) {
		return false
	}
	if start == 0 {
		return true
	}
	current := procStartTime(pid)
	return current == 0 || current == start
}

// procIdentityMatches is the fail-closed process identity check used before
// PID-based fallback signaling. Both recorded and live identities must be
// available and equal; liveness alone is never authorization to signal.
func procIdentityMatches(pid int, recorded int64) bool {
	if pid <= 0 || recorded == 0 || !ProcAlive(pid) {
		return false
	}
	current := procStartTime(pid)
	return current != 0 && current == recorded
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
