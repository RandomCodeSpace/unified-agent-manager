package mux

import "context"

// Backend is the contract every multiplexer implementation honors.
//
// Implementations in this repository:
//   - internal/mux/tmuxbackend: wraps *tmux.Client (transitional, deleted at v0.4.0)
//   - internal/ipcclient:        RPC stub against the native uam supervisor
//
// All methods MUST be safe for concurrent use across many goroutines.
type Backend interface {
	Spawn(ctx context.Context, spec SpawnSpec) (SessionHandle, error)
	Has(ctx context.Context, h SessionHandle) (bool, error)
	List(ctx context.Context, prefix string) ([]SessionInfo, error)
	Capture(ctx context.Context, h SessionHandle, lines int) (PaneCapture, error)
	Write(ctx context.Context, h SessionHandle, data []byte) error
	Resize(ctx context.Context, h SessionHandle, cols, rows uint16) error
	Kill(ctx context.Context, h SessionHandle) error
	Attach(ctx context.Context, h SessionHandle) (PaneStream, error)
	Subscribe(ctx context.Context, h SessionHandle) (<-chan Event, error)
}
