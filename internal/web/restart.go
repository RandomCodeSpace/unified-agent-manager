package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	// binaryPoll is how often the service looks at its binary on disk.
	binaryPoll = 30 * time.Second
	// binaryReadGap is the least time between the looks status reads ask for.
	binaryReadGap = 5 * time.Second
	// binaryVersionTimeout bounds one `<binary> version` run.
	binaryVersionTimeout = 10 * time.Second
	// restartTick is how often a pending restart looks for idle Tasks,
	// besides each Task change.
	restartTick = 5 * time.Second
	// maxVersionBytes bounds what `<binary> version` may print.
	maxVersionBytes = 4 << 10
)

// Service restart states, as the UI sees them.
const (
	restartNone       = "none"
	restartAvailable  = "available"
	restartPending    = "pending"
	restartRestarting = "restarting"
)

// errRestart ends runDaemon for RunDaemon to run the installed binary in
// its place.
var errRestart = errors.New("restart onto the installed binary")

// execve replaces the process image; tests replace it.
var execve = syscall.Exec

// ServiceStatus is the uam version the service runs, the one installed at
// its binary's path, and the restart onto it, as GET /api/service reports
// them.
type ServiceStatus struct {
	Running string `json:"running"`
	// Installed is the version at the binary's path, read when the file
	// changed; empty while it cannot be read (Error says why).
	Installed string `json:"installed,omitempty"`
	// Restart: none, available (Installed differs from Running), pending
	// (requested, waiting for no Task to work or wait) or restarting.
	Restart string `json:"restart"`
	Error   string `json:"error,omitempty"`
}

// binaryStamp identifies the file at the binary's path: replacing it changes
// the inode, and rewriting it the size or modification time.
type binaryStamp struct {
	size, mtime int64
	dev, ino    uint64
}

func statBinary(path string) (binaryStamp, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return binaryStamp{}, err
	}
	if !fi.Mode().IsRegular() {
		return binaryStamp{}, fmt.Errorf("%s is not a regular file", path)
	}
	st := binaryStamp{size: fi.Size(), mtime: fi.ModTime().UnixNano()}
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		// Dev is an int32 on macOS.
		st.dev, st.ino = uint64(sys.Dev), sys.Ino
	}
	return st, nil
}

// binaryWatch follows the uam binary the service started from: the path is
// resolved once at start, since a replaced binary leaves the running one's
// /proc/self/exe pointing at a deleted file.
type binaryWatch struct {
	path    string
	running string
	// readVersion runs the binary's `version`; tests replace it.
	readVersion func(ctx context.Context, path string) (string, error)
	// requests asks the daemon to shut down and run the installed binary.
	requests chan struct{}
	// kick asks the loop to look for idle Tasks now.
	kick chan struct{}
	// wanted is set while a restart is requested and not yet begun.
	wanted atomic.Bool
	// looking serializes looks, so one file change runs `version` once.
	looking sync.Mutex

	mu         sync.Mutex
	stamp      binaryStamp
	lookedAt   time.Time
	installed  string
	err        string
	restarting bool
}

// newBinaryWatch follows path, which runs version running. An empty path
// means it could not be resolved; resolveErr says why.
func newBinaryWatch(path, running string, resolveErr error) *binaryWatch {
	w := &binaryWatch{
		path: path, running: running, readVersion: runVersion,
		requests: make(chan struct{}, 1), kick: make(chan struct{}, 1),
		installed: running,
	}
	if resolveErr != nil {
		w.installed, w.err = "", shortError(fmt.Errorf("locate the uam binary: %w", resolveErr))
		return w
	}
	// The file at start is the one running; only a later change is read.
	st, err := statBinary(path)
	if err != nil {
		w.installed, w.err = "", shortError(err)
		return w
	}
	w.stamp = st
	return w
}

// resolveExecutable is the real path of the running binary.
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// runVersion runs `path version` with a short timeout and a minimal
// environment and returns the version it prints.
func runVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, binaryVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version") // #nosec G204 -- the service's own binary path, resolved at start, with a fixed argument.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.WaitDelay = time.Second
	out := cappedBuffer{limit: maxVersionBytes}
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run %s version: %w", filepath.Base(path), err)
	}
	return parseVersion(out.buf.String())
}

// parseVersion takes the first line `uam version` prints: one word of
// printable characters.
func parseVersion(out string) (string, error) {
	line, _, _ := strings.Cut(out, "\n")
	v := strings.TrimSpace(line)
	if v == "" || len(v) > 128 || strings.IndexFunc(v, func(r rune) bool { return !unicode.IsPrint(r) || unicode.IsSpace(r) }) >= 0 {
		return "", fmt.Errorf("unexpected version output %q", clipRunes(v, 40))
	}
	return v, nil
}

// look stats the binary when gap has passed since the last look, and reads
// its version when the file changed. A missing file or a failed read is an
// error, and cancels a requested restart.
func (w *binaryWatch) look(ctx context.Context, now time.Time, gap time.Duration) {
	if w.path == "" {
		return
	}
	w.looking.Lock()
	defer w.looking.Unlock()
	w.mu.Lock()
	if gap > 0 && now.Sub(w.lookedAt) < gap {
		w.mu.Unlock()
		return
	}
	w.lookedAt = now
	prev := w.stamp
	w.mu.Unlock()
	st, err := statBinary(w.path)
	if err == nil && st == prev {
		return
	}
	v := ""
	if err == nil {
		v, err = w.readVersion(ctx, w.path)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stamp = st
	if err != nil {
		if msg := shortError(err); msg != w.err {
			log.Warn("read the installed uam binary failed", "path", w.path, "error", err)
			w.err = msg
		}
		w.installed = ""
		w.wanted.Store(false)
		return
	}
	if v != w.installed {
		log.Info("installed uam binary changed", "path", w.path, "version", v, "running", w.running)
	}
	w.installed, w.err = v, ""
	if v == w.running {
		w.wanted.Store(false)
	}
}

// newer reports whether a version other than the running one is installed.
// The caller holds w.mu.
func (w *binaryWatch) newerLocked() bool {
	return w.err == "" && w.installed != "" && w.installed != w.running
}

func (w *binaryWatch) status() ServiceStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := ServiceStatus{Running: w.running, Installed: w.installed, Error: w.err, Restart: restartNone}
	switch {
	case w.restarting:
		st.Restart = restartRestarting
	case w.wanted.Load():
		st.Restart = restartPending
	case w.newerLocked():
		st.Restart = restartAvailable
	}
	return st
}

// request asks for a restart onto the installed binary.
func (w *binaryWatch) request() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case w.restarting:
	case w.err != "":
		return newError(http.StatusConflict, "cannot restart: %s", w.err)
	case !w.newerLocked():
		return newError(http.StatusConflict, "uam %s is the version installed; there is nothing to restart onto", w.running)
	default:
		w.wanted.Store(true)
	}
	return nil
}

// begin turns a wanted restart into one under way and asks the daemon for
// it. It reports false when none is wanted, or the file no longer holds a
// newer version.
func (w *binaryWatch) begin() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.wanted.Load() || w.restarting {
		return false
	}
	if !w.newerLocked() {
		w.wanted.Store(false)
		return false
	}
	w.wanted.Store(false)
	w.restarting = true
	select {
	case w.requests <- struct{}{}:
	default:
	}
	return true
}

// postpone puts a restart the daemon could not begin back to pending.
func (w *binaryWatch) postpone() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.restarting = false
	w.wanted.Store(true)
}

// nudge asks the loop to look for idle Tasks while a restart is pending. It
// never blocks, so it runs with Manager.mu held.
func (w *binaryWatch) nudge() {
	if !w.wanted.Load() {
		return
	}
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// busyTasksLocked counts the Tasks that work or wait for anything. The
// caller holds mu.
func (m *Manager) busyTasksLocked() int {
	n := 0
	for _, s := range m.sessions {
		if s.runsOrWaitsLocked() {
			n++
		}
	}
	return n
}

// binaryLoop looks at the binary every binaryPoll and, while a restart is
// pending, begins it once no Task works or waits: on each Task change and
// every restartTick.
func (m *Manager) binaryLoop() {
	defer m.wg.Done()
	w := m.binary
	poll := time.NewTicker(binaryPoll)
	defer poll.Stop()
	tick := time.NewTicker(restartTick)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-poll.C:
			w.look(m.ctx, m.now(), 0)
		case <-tick.C:
		case <-w.kick:
		}
		m.tryRestart()
	}
}

// tryRestart begins a pending restart when no Task works or waits. The
// daemon checks again under mu as it shuts down.
func (m *Manager) tryRestart() {
	w := m.binary
	if !w.wanted.Load() {
		return
	}
	m.mu.Lock()
	busy := m.busyTasksLocked()
	m.mu.Unlock()
	if busy > 0 {
		return
	}
	// The file may have changed again since its version was read.
	w.look(m.ctx, m.now(), 0)
	if w.begin() {
		log.Info("uam web restart begins", "version", w.status().Installed)
	}
}

// restartRequests delivers each restart begun; nil without a binary watch.
func (m *Manager) restartRequests() <-chan struct{} {
	if m.binary == nil {
		return nil
	}
	return m.binary.requests
}

// ServiceStatus reports the running and installed uam and the restart. look
// reads the binary first, at most once per binaryReadGap.
func (m *Manager) ServiceStatus(ctx context.Context, look bool) (ServiceStatus, error) {
	if m.binary == nil {
		return ServiceStatus{}, newError(http.StatusNotFound, "this service cannot restart itself")
	}
	if look {
		m.binary.look(ctx, m.now(), binaryReadGap)
	}
	return m.binary.status(), nil
}

// serviceMeta is the restart status for GET /api/meta, without a look.
func (m *Manager) serviceMeta() *ServiceStatus {
	if m.binary == nil {
		return nil
	}
	st := m.binary.status()
	return &st
}

// RequestRestart restarts the service onto the installed binary now when no
// Task works or waits, and otherwise as soon as none does. It is refused
// when the installed version is the running one or cannot be read.
func (m *Manager) RequestRestart(ctx context.Context) (ServiceStatus, error) {
	if m.binary == nil {
		return ServiceStatus{}, newError(http.StatusNotFound, "this service cannot restart itself")
	}
	m.binary.look(ctx, m.now(), 0)
	if err := m.binary.request(); err != nil {
		return ServiceStatus{}, err
	}
	m.tryRestart()
	return m.binary.status(), nil
}

// restartService replaces this process with path, run with args and env:
// the PID, flags and environment stay. It returns only when that fails.
func restartService(path string, args, env []string) error {
	log.Info("uam web restarting", "path", path)
	err := execve(path, append([]string{path}, args...), env)
	log.Error("uam web restart failed", "path", path, "error", err)
	return fmt.Errorf("restart uam web onto %s: %w", path, err)
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	st, err := s.m.ServiceStatus(r.Context(), true)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleRestartService(w http.ResponseWriter, r *http.Request) {
	st, err := s.m.RequestRestart(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
