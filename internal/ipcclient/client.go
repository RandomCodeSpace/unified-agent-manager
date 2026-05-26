// Package ipcclient implements mux.Backend by speaking the uam supervisor
// RPC over a Unix domain socket. It is the in-process face of the native
// multiplexer: callers see a normal mux.Backend; the wire protocol is
// hidden behind a single Client.
package ipcclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
)

// Options configures a Client.
type Options struct {
	// SocketPath is the supervisor's control socket. Empty falls back to
	// DefaultSocketPath().
	SocketPath string
	// DialTimeout caps the initial dial. Defaults to 2s.
	DialTimeout time.Duration
	// AutoStart asks the client to fork+exec `uam daemon start --detach`
	// when the supervisor is not yet running.
	AutoStart bool
}

// Client implements mux.Backend by speaking ipc over a Unix socket.
type Client struct {
	socketPath  string
	dialTimeout time.Duration

	mu      sync.Mutex
	conn    *net.UnixConn
	pending map[uint32]chan ipc.Request
	closed  bool

	nextID atomic.Uint32
}

// New dials the supervisor and starts the readLoop. If AutoStart is set
// and the dial fails, New attempts to ensure the daemon exists before
// retrying once.
func New(opts Options) (*Client, error) {
	sp := opts.SocketPath
	if sp == "" {
		sp = DefaultSocketPath()
	}
	dt := opts.DialTimeout
	if dt == 0 {
		dt = 2 * time.Second
	}
	c := &Client{
		socketPath:  sp,
		dialTimeout: dt,
		pending:     make(map[uint32]chan ipc.Request),
	}
	if err := c.dial(opts.AutoStart); err != nil {
		return nil, err
	}
	go c.readLoop()
	return c, nil
}

// Close shuts the connection and unblocks all pending callers.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (c *Client) dial(autostart bool) error {
	conn, err := net.DialTimeout("unix", c.socketPath, c.dialTimeout)
	if err != nil {
		if !autostart {
			return fmt.Errorf("ipcclient dial: %w", err)
		}
		if startErr := EnsureDaemon(c.socketPath); startErr != nil {
			return fmt.Errorf("ipcclient autostart: %w", startErr)
		}
		conn, err = net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			return fmt.Errorf("ipcclient dial after autostart: %w", err)
		}
	}
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return fmt.Errorf("ipcclient: dialed conn is not *net.UnixConn (%T)", conn)
	}
	c.conn = uconn
	return nil
}

func (c *Client) readLoop() {
	for {
		req, err := ipc.ReadFrame(c.conn)
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[req.ID]
		c.mu.Unlock()
		if ok {
			ch <- req
		}
	}
}

// call sends one Request and waits for the matching Response.
func (c *Client) call(ctx context.Context, kind ipc.Kind, payload []byte) ([]byte, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("ipcclient: closed")
	}
	id := c.nextID.Add(1)
	ch := make(chan ipc.Request, 1)
	c.pending[id] = ch
	conn := c.conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := ipc.WriteFrame(conn, ipc.Request{ID: id, Kind: kind, Payload: payload}); err != nil {
		return nil, fmt.Errorf("ipcclient write: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r, ok := <-ch:
		if !ok {
			return nil, errors.New("ipcclient: connection closed mid-call")
		}
		// Server-reported errors carry a JSON {"error":"..."} payload.
		if maybeErr := decodeError(r.Payload); maybeErr != "" {
			return nil, errors.New(maybeErr)
		}
		return r.Payload, nil
	}
}

func decodeError(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var p struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Error
}

// Spawn implements mux.Backend.
func (c *Client) Spawn(ctx context.Context, spec mux.SpawnSpec) (mux.SessionHandle, error) {
	payload, err := json.Marshal(struct {
		SessionName string   `json:"session_name"`
		Argv        []string `json:"argv"`
		Env         []string `json:"env"`
		Cwd         string   `json:"cwd"`
		Cols        uint16   `json:"cols"`
		Rows        uint16   `json:"rows"`
	}{
		SessionName: spec.SessionName,
		Argv:        spec.Argv,
		Env:         spec.Env,
		Cwd:         spec.Cwd,
		Cols:        spec.Cols,
		Rows:        spec.Rows,
	})
	if err != nil {
		return "", fmt.Errorf("ipcclient marshal spawn: %w", err)
	}
	resp, err := c.call(ctx, ipc.KindSpawn, payload)
	if err != nil {
		return "", err
	}
	var out struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return "", fmt.Errorf("ipcclient parse spawn response: %w", err)
	}
	return mux.SessionHandle(out.Handle), nil
}

// Has implements mux.Backend.
func (c *Client) Has(ctx context.Context, h mux.SessionHandle) (bool, error) {
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: string(h)})
	resp, err := c.call(ctx, ipc.KindHas, payload)
	if err != nil {
		return false, err
	}
	var out struct {
		Has bool `json:"has"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return false, fmt.Errorf("ipcclient parse has: %w", err)
	}
	return out.Has, nil
}

// List implements mux.Backend.
func (c *Client) List(ctx context.Context, prefix string) ([]mux.SessionInfo, error) {
	payload, _ := json.Marshal(struct {
		Prefix string `json:"prefix"`
	}{Prefix: prefix})
	resp, err := c.call(ctx, ipc.KindList, payload)
	if err != nil {
		return nil, err
	}
	var out struct {
		Sessions []struct {
			ID         string `json:"id"`
			SocketPath string `json:"socket_path"`
			Pid        int    `json:"pid"`
			CreatedAt  int64  `json:"created_at"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("ipcclient parse list: %w", err)
	}
	infos := make([]mux.SessionInfo, 0, len(out.Sessions))
	for _, r := range out.Sessions {
		infos = append(infos, mux.SessionInfo{
			Handle:    mux.SessionHandle(r.ID),
			CreatedAt: time.Unix(r.CreatedAt, 0),
			PanePID:   r.Pid,
		})
	}
	return infos, nil
}

// Capture implements mux.Backend.
func (c *Client) Capture(ctx context.Context, h mux.SessionHandle, lines int) (mux.PaneCapture, error) {
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Lines  int    `json:"lines"`
	}{Handle: string(h), Lines: lines})
	resp, err := c.call(ctx, ipc.KindCapture, payload)
	if err != nil {
		return mux.PaneCapture{}, err
	}
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return mux.PaneCapture{}, fmt.Errorf("ipcclient parse capture: %w", err)
	}
	if lines > 0 && len(out.Lines) > lines {
		out.Lines = out.Lines[len(out.Lines)-lines:]
	}
	return mux.PaneCapture{Lines: out.Lines, CapturedAt: time.Now()}, nil
}

// Write implements mux.Backend.
func (c *Client) Write(ctx context.Context, h mux.SessionHandle, data []byte) error {
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Data   []byte `json:"data"`
	}{Handle: string(h), Data: data})
	_, err := c.call(ctx, ipc.KindWrite, payload)
	return err
}

// Resize implements mux.Backend.
func (c *Client) Resize(ctx context.Context, h mux.SessionHandle, cols, rows uint16) error {
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Cols   uint16 `json:"cols"`
		Rows   uint16 `json:"rows"`
	}{Handle: string(h), Cols: cols, Rows: rows})
	_, err := c.call(ctx, ipc.KindResize, payload)
	return err
}

// Kill implements mux.Backend.
func (c *Client) Kill(ctx context.Context, h mux.SessionHandle) error {
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: string(h)})
	_, err := c.call(ctx, ipc.KindKill, payload)
	return err
}

// Attach implements mux.Backend by dialing the host's per-session socket.
// The returned PaneStream multiplexes reads from the PTY master and writes
// to it through the host's Write RPC. See attach.go for the impl.
func (c *Client) Attach(ctx context.Context, h mux.SessionHandle) (mux.PaneStream, error) {
	return newAttachStream(c, h)
}

// Subscribe is not yet implemented; callers fall back to polling
// List+Capture, matching the tmux backend's behavior.
func (c *Client) Subscribe(ctx context.Context, h mux.SessionHandle) (<-chan mux.Event, error) {
	return nil, nil
}

// DefaultSocketPath returns the per-user supervisor socket location.
// Resolution order (each wins over the next):
//  1. UAM_SOCKET — explicit full path override
//  2. UAM_RUNTIME_DIR — supervisor runtime dir; socket sits at its root
//  3. XDG_RUNTIME_DIR/uam/control.sock — XDG convention
//  4. $TMPDIR/uam-$UID/control.sock — fallback for systems without XDG
//
// Must match supervisor.DefaultRuntimeDir's resolution so the client and
// supervisor agree on where to meet.
func DefaultSocketPath() string {
	if v := os.Getenv("UAM_SOCKET"); v != "" {
		return v
	}
	if rd := os.Getenv("UAM_RUNTIME_DIR"); rd != "" {
		return filepath.Join(rd, "control.sock")
	}
	if rd := os.Getenv("XDG_RUNTIME_DIR"); rd != "" {
		return filepath.Join(rd, "uam", "control.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("uam-%d", os.Getuid()), "control.sock")
}
