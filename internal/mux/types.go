// Package mux defines the contract uam adapters use to talk to whichever
// session-backend is in play (tmux today, native supervisor tomorrow).
package mux

import (
	"io"
	"time"
)

// SpawnSpec is everything a backend needs to create a session.
type SpawnSpec struct {
	SessionName string   // "uam-<agent>-<id>" — naming preserved
	Argv        []string // [bin, args...]; never goes through a shell
	Env         []string // KEY=VAL pairs merged on top of inherited env
	Cwd         string
	Cols, Rows  uint16 // defaults applied by backend if zero
	Scrollback  int    // bytes retained; 0 = backend default
}

// SessionHandle identifies a live session. Returned from Spawn, accepted by
// every other call. Treat as opaque.
type SessionHandle string

// PaneCapture is the structured output of a "peek" — equivalent to
// `tmux capture-pane -p -S -200 -J`.
type PaneCapture struct {
	Lines      []string // wrap-joined, last N rows
	PaneCmd    string   // current foreground command name (for ProcAlive)
	PanePID    int      // foreground pid; 0 if unknown
	CapturedAt time.Time
}

// SessionInfo is the per-row listing data — analog of tmux list-sessions -F.
type SessionInfo struct {
	Handle    SessionHandle
	CreatedAt time.Time
	Attached  bool
	PanePID   int
	Cwd       string
	PaneCmd   string
}

// Event is a state-change emitted by Subscribe.
type Event struct {
	Kind      EventKind
	Handle    SessionHandle
	PanePID   int
	ExitCode  int
	Timestamp time.Time
}

// EventKind enumerates Subscribe payload kinds.
type EventKind string

const (
	EventOutput EventKind = "output" // pane bytes changed since last event
	EventExit   EventKind = "exit"   // child terminated
	EventResize EventKind = "resize" // winsize updated
)

// PaneStream is the bidirectional pane handle returned from Attach.
type PaneStream interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}
