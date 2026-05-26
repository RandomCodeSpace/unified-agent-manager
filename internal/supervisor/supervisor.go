// Package supervisor implements the per-user uam daemon. It listens on a
// control UDS, spawns one host subprocess per session via fork+exec,
// tracks them in an in-memory session table, and adopts orphaned hosts
// on restart.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/host"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
)

// Options configures a Supervisor.
type Options struct {
	// RuntimeDir is the per-user base directory. Defaults to
	// $XDG_RUNTIME_DIR/uam when empty.
	RuntimeDir string
	// HostExe is the absolute path of the uam binary used to fork+exec
	// "uam internal-host ...". Defaults to os.Executable() when empty.
	HostExe string
}

// SessionRecord is one in-memory row in the supervisor's table.
type SessionRecord struct {
	ID         string `json:"id"`
	SocketPath string `json:"socket_path"`
	Pid        int    `json:"pid"`
	CreatedAt  int64  `json:"created_at"`
}

// Supervisor is the daemon's state.
type Supervisor struct {
	runtimeDir string
	hostsDir   string
	socketPath string
	pidPath    string
	ownExe     string
	releasePid func()
	listener   net.Listener
	stop       chan struct{}
	stopOnce   sync.Once

	mu       sync.Mutex
	sessions map[string]SessionRecord
}

// New validates options and prepares paths. It does not yet acquire the
// lock or listen — call Run for that.
func New(opts Options) (*Supervisor, error) {
	runtimeDir := opts.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = DefaultRuntimeDir()
	}
	exe := opts.HostExe
	if exe == "" {
		got, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("supervisor: os.Executable: %w", err)
		}
		exe = got
	}
	return &Supervisor{
		runtimeDir: runtimeDir,
		hostsDir:   filepath.Join(runtimeDir, "hosts"),
		socketPath: filepath.Join(runtimeDir, "control.sock"),
		pidPath:    filepath.Join(runtimeDir, "uam.pid"),
		ownExe:     exe,
		stop:       make(chan struct{}),
		sessions:   make(map[string]SessionRecord),
	}, nil
}

// DefaultRuntimeDir returns $XDG_RUNTIME_DIR/uam or a $TMPDIR fallback.
func DefaultRuntimeDir() string {
	if v := os.Getenv("UAM_RUNTIME_DIR"); v != "" {
		return v
	}
	if rd := os.Getenv("XDG_RUNTIME_DIR"); rd != "" {
		return filepath.Join(rd, "uam")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("uam-%d", os.Getuid()))
}

// ControlSocketPath returns the absolute path of the control UDS.
func (s *Supervisor) ControlSocketPath() string { return s.socketPath }

// HostsDir returns the absolute path of the per-session hosts directory.
func (s *Supervisor) HostsDir() string { return s.hostsDir }

// Run starts listening and serving until ctx is canceled or Shutdown is
// called. The lock is acquired here; concurrent supervisors fail fast.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.bootstrap(); err != nil {
		s.cleanup()
		return err
	}
	defer s.cleanup()

	if err := s.AdoptOrphans(); err != nil {
		// Non-fatal: log via stderr; the supervisor still starts.
		fmt.Fprintf(os.Stderr, "supervisor: AdoptOrphans: %v\n", err)
	}

	acceptDone := make(chan struct{})
	go s.acceptLoop(acceptDone)

	select {
	case <-ctx.Done():
	case <-s.stop:
	}
	_ = s.listener.Close()
	<-acceptDone
	return nil
}

// Shutdown signals Run to return. Safe to call multiple times.
func (s *Supervisor) Shutdown() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *Supervisor) bootstrap() error {
	if err := os.MkdirAll(s.runtimeDir, 0o700); err != nil {
		return fmt.Errorf("supervisor mkdir runtime dir: %w", err)
	}
	if err := os.MkdirAll(s.hostsDir, 0o700); err != nil {
		return fmt.Errorf("supervisor mkdir hosts dir: %w", err)
	}
	release, err := AcquirePidfile(s.pidPath)
	if err != nil {
		return err
	}
	s.releasePid = release
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("supervisor remove stale control socket: %w", err)
	}
	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("supervisor listen: %w", err)
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("supervisor chmod control socket: %w", err)
	}
	s.listener = ln
	return nil
}

func (s *Supervisor) cleanup() {
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	if s.releasePid != nil {
		s.releasePid()
		s.releasePid = nil
	}
	_ = os.Remove(s.socketPath)
}

func (s *Supervisor) acceptLoop(done chan<- struct{}) {
	defer close(done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Supervisor) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if uconn, ok := conn.(*net.UnixConn); ok {
		uid, err := ipc.PeerUID(uconn)
		// #nosec G115 -- POSIX UIDs are always within uint32 range.
		if err == nil && uid != uint32(os.Getuid()) {
			return // reject cross-uid
		}
	}
	for {
		req, err := ipc.ReadFrame(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		resp := s.dispatch(req)
		if err := ipc.WriteFrame(conn, ipc.Request{ID: req.ID, Payload: resp}); err != nil {
			return
		}
	}
}

// listSessions returns a copy of the session table.
func (s *Supervisor) listSessions() []SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionRecord, 0, len(s.sessions))
	for _, r := range s.sessions {
		out = append(out, r)
	}
	return out
}

// hostSocketPath computes the absolute path of a session's host socket.
func (s *Supervisor) hostSocketPath(id string) string {
	return filepath.Join(s.hostsDir, id+".sock")
}

// hostJournalPath computes the absolute path of a session's journal file.
func (s *Supervisor) hostJournalPath(id string) string {
	return filepath.Join(s.runtimeDir, "journals", id+".log")
}

// proxyToHost dials the per-session host socket and forwards one RPC,
// returning the host's response payload.
func (s *Supervisor) proxyToHost(id string, kind ipc.Kind, payload []byte) ([]byte, error) {
	s.mu.Lock()
	rec, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("supervisor: unknown session %q", id)
	}
	conn, err := net.DialTimeout("unix", rec.SocketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("supervisor proxy dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: kind, ID: 1, Payload: payload}); err != nil {
		return nil, fmt.Errorf("supervisor proxy write: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return nil, fmt.Errorf("supervisor proxy deadline: %w", err)
	}
	resp, err := ipc.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("supervisor proxy read: %w", err)
	}
	return resp.Payload, nil
}

// hostConfigFor builds the host.Config for a new session.
func (s *Supervisor) hostConfigFor(id string, spec SpawnSpec) host.Config {
	return host.Config{
		SessionID:   id,
		Argv:        spec.Argv,
		Cwd:         spec.Cwd,
		Env:         spec.Env,
		Cols:        spec.Cols,
		Rows:        spec.Rows,
		JournalPath: s.hostJournalPath(id),
		SocketPath:  s.hostSocketPath(id),
	}
}

// AdoptOrphans scans hostsDir for .sock files left from a prior supervisor
// crash. Sockets with a listening peer are added to the session table;
// dead sockets are unlinked.
func (s *Supervisor) AdoptOrphans() error {
	entries, err := os.ReadDir(s.hostsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("supervisor read hosts dir: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sock") {
			continue
		}
		path := filepath.Join(s.hostsDir, e.Name())
		id := strings.TrimSuffix(e.Name(), ".sock")
		if probeHostAlive(path) {
			s.mu.Lock()
			s.sessions[id] = SessionRecord{ID: id, SocketPath: path}
			s.mu.Unlock()
			continue
		}
		_ = os.Remove(path)
	}
	return nil
}

func probeHostAlive(sockPath string) bool {
	c, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// removeSession drops the record and removes the lingering socket file.
func (s *Supervisor) removeSession(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
	_ = os.Remove(s.hostSocketPath(id))
}

// asJSON marshals v to bytes, returning {} on error so the wire frame is
// still valid JSON.
func asJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
