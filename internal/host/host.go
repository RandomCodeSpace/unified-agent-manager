// Package host implements the per-session host subprocess that owns one
// PTY pair, one journal file, and one listening socket. The supervisor
// spawns one host per session; the host outlives the spawning request but
// not the child agent (when the agent exits, the host shuts down).
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/journal"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/pty"
)

// Default PTY dimensions when Config leaves them zero.
const (
	defaultCols uint16 = 200
	defaultRows uint16 = 50
)

// Config configures a Host. JSON tags allow the supervisor to ship the
// config across the fork+exec boundary.
type Config struct {
	SessionID   string   `json:"session_id"`
	Argv        []string `json:"argv"`
	Cwd         string   `json:"cwd"`
	Env         []string `json:"env"`
	Cols        uint16   `json:"cols"`
	Rows        uint16   `json:"rows"`
	JournalPath string   `json:"journal_path"`
	SocketPath  string   `json:"socket_path"`
}

// Host owns one PTY pair, one journal file, and one listening UDS.
type Host struct {
	cfg Config

	mu        sync.Mutex
	p         *pty.PTY
	child     *pty.Child
	j         *journal.Journal
	listener  net.Listener
	attached  []net.Conn
	exitCode  int
	childDone chan struct{}
}

// New validates cfg and prepares a Host without yet allocating runtime
// resources. Resources are acquired in Run.
func New(cfg Config) (*Host, error) {
	if cfg.SessionID == "" {
		return nil, fmt.Errorf("host: empty SessionID")
	}
	if len(cfg.Argv) == 0 {
		return nil, fmt.Errorf("host: empty Argv")
	}
	if cfg.JournalPath == "" {
		return nil, fmt.Errorf("host: empty JournalPath")
	}
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("host: empty SocketPath")
	}
	return &Host{cfg: cfg, childDone: make(chan struct{})}, nil
}

// Run blocks until ctx is canceled or the child exits. Cleanup runs in
// either case.
func (h *Host) Run(ctx context.Context) error {
	if err := h.bootstrap(); err != nil {
		h.cleanup()
		return err
	}
	defer h.cleanup()

	go h.acceptLoop()
	go h.pumpPTY()
	go h.waitChild()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.childDone:
		return nil
	}
}

// bootstrap allocates the PTY, opens the journal, listens on the socket,
// and fork+exec's the child agent.
func (h *Host) bootstrap() error {
	p, err := pty.Open()
	if err != nil {
		return fmt.Errorf("host pty.Open: %w", err)
	}
	h.p = p
	cols := h.cfg.Cols
	if cols == 0 {
		cols = defaultCols
	}
	rows := h.cfg.Rows
	if rows == 0 {
		rows = defaultRows
	}
	if err := pty.Resize(p.Master, cols, rows); err != nil {
		return fmt.Errorf("host pty.Resize: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(h.cfg.JournalPath), 0o700); err != nil {
		return fmt.Errorf("host mkdir journal dir: %w", err)
	}
	j, err := journal.Open(h.cfg.JournalPath)
	if err != nil {
		return fmt.Errorf("host journal.Open: %w", err)
	}
	h.j = j
	if err := os.MkdirAll(filepath.Dir(h.cfg.SocketPath), 0o700); err != nil {
		return fmt.Errorf("host mkdir socket dir: %w", err)
	}
	if err := os.Remove(h.cfg.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("host remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", h.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("host listen: %w", err)
	}
	if err := os.Chmod(h.cfg.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("host chmod socket: %w", err)
	}
	h.listener = ln
	child, err := pty.Spawn(p, pty.SpawnArgs{
		Argv: h.cfg.Argv,
		Env:  h.cfg.Env,
		Cwd:  h.cfg.Cwd,
	})
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("host pty.Spawn: %w", err)
	}
	h.child = child
	return nil
}

// pumpPTY copies PTY master output into the journal and to any attached
// clients. Returns when the master is closed.
func (h *Host) pumpPTY() {
	buf := make([]byte, 32*1024)
	for {
		n, err := h.p.Master.Read(buf)
		if n > 0 {
			data := buf[:n]
			if _, werr := h.j.Write(data); werr != nil {
				// journal failure is fatal for visibility; keep streaming
				// to attached clients but record nothing else here.
				_ = werr
			}
			// Flush so direct readers (peek via direct journal read, or
			// process inspecting the file on disk) see the bytes promptly.
			// Per-chunk flush is cheap; bufio still amortizes small writes.
			_ = h.j.Flush()
			h.mu.Lock()
			attached := h.attached
			h.mu.Unlock()
			for _, c := range attached {
				_, _ = c.Write(data)
			}
		}
		if err != nil {
			return
		}
	}
}

// acceptLoop accepts UDS connections and dispatches them.
func (h *Host) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

// handleConn handles one connected client until it disconnects.
func (h *Host) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if !h.checkPeerUID(conn) {
		return
	}
	for {
		req, err := ipc.ReadFrame(conn)
		if err != nil {
			return
		}
		if h.handleFrame(conn, req) {
			return
		}
	}
}

// checkPeerUID enforces the same-user invariant on Unix-domain peers.
// Non-UDS conns (e.g. an in-process net.Pipe used by tests) are
// allowed through unconditionally because the UID check does not apply.
func (h *Host) checkPeerUID(conn net.Conn) bool {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return true
	}
	uid, err := ipc.PeerUID(uconn)
	if err != nil {
		return true
	}
	// #nosec G115 -- POSIX UIDs are always within uint32 range.
	return uid == uint32(os.Getuid())
}

// handleFrame dispatches one IPC frame received on conn. Returns true
// when the caller should stop reading from conn — either runRawAttach
// has taken ownership of it or the reply write failed.
func (h *Host) handleFrame(conn net.Conn, req ipc.Request) bool {
	if req.Kind == ipc.KindAttach {
		// One-time ACK, then this conn becomes a raw PTY stream:
		// pumpPTY broadcasts to it; runRawAttach forwards inbound bytes
		// into the PTY master. IPC framing no longer applies past this
		// point, so we relinquish the conn after runRawAttach returns.
		if err := ipc.WriteFrame(conn, ipc.Request{ID: req.ID, Payload: []byte(`{"ok":true}`)}); err != nil {
			return true
		}
		h.runRawAttach(conn)
		return true
	}
	resp := h.dispatch(req)
	if err := ipc.WriteFrame(conn, ipc.Request{ID: req.ID, Payload: resp}); err != nil {
		return true
	}
	return false
}

// runRawAttach takes ownership of conn as a raw PTY stream after the
// KindAttach handshake. Outbound PTY bytes are broadcast to this conn
// by pumpPTY; inbound bytes are written directly to the PTY master.
// Returns when conn closes (client disconnect) or the PTY master
// rejects a write (child exiting). The conn is detached from h.attached
// on exit so pumpPTY no longer broadcasts to a dead client.
func (h *Host) runRawAttach(conn net.Conn) {
	h.mu.Lock()
	h.attached = append(h.attached, conn)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		for i, c := range h.attached {
			if c == conn {
				h.attached = append(h.attached[:i], h.attached[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if _, werr := h.p.Master.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// dispatch routes one RPC to its handler and returns the response payload.
func (h *Host) dispatch(req ipc.Request) []byte {
	switch req.Kind {
	case ipc.KindWrite:
		var p struct {
			Data []byte `json:"data"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		_, _ = h.p.Master.Write(p.Data)
		return nil
	case ipc.KindCapture:
		var p struct {
			Bytes int64 `json:"bytes"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		if p.Bytes <= 0 {
			p.Bytes = 64 * 1024
		}
		raw, _ := h.j.Tail(p.Bytes)
		lines := journal.ExtractLines(raw)
		out, _ := json.Marshal(struct {
			Lines []string `json:"lines"`
		}{Lines: lines})
		return out
	case ipc.KindResize:
		var p struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		_ = pty.Resize(h.p.Master, p.Cols, p.Rows)
		return nil
	case ipc.KindKill:
		if h.child != nil {
			_ = h.child.Kill()
		}
		return nil
	case ipc.KindStatus:
		h.mu.Lock()
		code := h.exitCode
		h.mu.Unlock()
		out, _ := json.Marshal(struct {
			ExitCode int    `json:"exit_code"`
			Pid      int    `json:"pid"`
			Session  string `json:"session_id"`
		}{ExitCode: code, Pid: h.child.Pid(), Session: h.cfg.SessionID})
		return out
	}
	return nil
}

// waitChild blocks until the child exits, then closes childDone.
func (h *Host) waitChild() {
	if h.child == nil {
		close(h.childDone)
		return
	}
	state, _ := h.child.Wait()
	h.mu.Lock()
	if state != nil {
		h.exitCode = state.ExitCode()
	}
	h.mu.Unlock()
	close(h.childDone)
}

// cleanup tears down host resources. Safe to call multiple times.
func (h *Host) cleanup() {
	if h.listener != nil {
		_ = h.listener.Close()
		h.listener = nil
	}
	// Close any attached client conns so their reads return cleanly
	// instead of blocking after the child has exited.
	h.mu.Lock()
	conns := h.attached
	h.attached = nil
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	if h.j != nil {
		_ = h.j.Close()
		h.j = nil
	}
	if h.p != nil {
		_ = h.p.Close()
		h.p = nil
	}
	_ = os.Remove(h.cfg.SocketPath)
}
