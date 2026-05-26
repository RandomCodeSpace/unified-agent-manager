package ipcclient

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
)

// attachStream multiplexes reads from the host PTY (TODO: dedicated UDS
// stream) and writes that go through the supervisor's Write RPC.
//
// v0.1.13 ships a placeholder implementation. The host already streams
// PTY output to attached clients (host.go pumpPTY), but a dedicated UDS
// per attach is not yet wired through the supervisor → host bridge. Read
// returns io.EOF immediately to keep the surface coherent for callers
// that test interface conformance; Write and Resize forward to the
// client RPC path.
type attachStream struct {
	client *Client
	handle mux.SessionHandle

	mu     sync.Mutex
	closed bool
}

func newAttachStream(c *Client, h mux.SessionHandle) (mux.PaneStream, error) {
	if c == nil {
		return nil, errors.New("attach: nil client")
	}
	if h == "" {
		return nil, errors.New("attach: empty handle")
	}
	return &attachStream{client: c, handle: h}, nil
}

// Read returns io.EOF until the dedicated PTY-output channel is wired.
// Callers that need live pane output should use Capture in a poll loop.
func (a *attachStream) Read(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return 0, errors.New("attach: stream closed")
	}
	// v0.1.13 placeholder: no live stream yet.
	return 0, fmt.Errorf("attach.Read: dedicated PTY stream not yet implemented")
}

// Write forwards bytes through the supervisor → host Write RPC.
func (a *attachStream) Write(p []byte) (int, error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return 0, errors.New("attach: stream closed")
	}
	a.mu.Unlock()
	if err := a.client.Write(context.Background(), a.handle, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close marks the stream closed; the underlying client conn is owned by
// the caller and is not closed here.
func (a *attachStream) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
}

// Resize forwards to the supervisor's Resize RPC.
func (a *attachStream) Resize(cols, rows uint16) error {
	return a.client.Resize(context.Background(), a.handle, cols, rows)
}
