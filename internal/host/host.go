// Package host implements the per-session host subprocess that owns one
// PTY pair, one journal file, and one listening socket. The supervisor
// spawns one host per session; the host outlives the spawning request but
// not the child agent (when the agent exits, the host shuts down).
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	defer conn.Close()
	if uconn, ok := conn.(*net.UnixConn); ok {
		if uid, err := ipc.PeerUID(uconn); err == nil {
			if uid != uint32(os.Getuid()) {
				return // reject cross-uid peers silently
			}
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
		resp := h.dispatch(req)
		if err := ipc.WriteFrame(conn, ipc.Request{ID: req.ID, Payload: resp}); err != nil {
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
