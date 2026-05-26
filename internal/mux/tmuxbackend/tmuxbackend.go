// Package tmuxbackend wraps *tmux.Client so the existing tmux engine
// implements mux.Backend. Transitional — deleted at v0.4.0.
package tmuxbackend

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// Backend wraps a *tmux.Client and implements mux.Backend.
type Backend struct {
	c *tmux.Client
}

// New constructs a tmux-backed mux.Backend. Passing a nil client falls back
// to the package default ("uam" socket).
func New(c *tmux.Client) *Backend {
	if c == nil {
		c = tmux.New("uam")
	}
	return &Backend{c: c}
}

// Spawn creates a tmux session whose argv runs through the existing
// tmux.Client.CreateSession path. Env is merged via the same `env KEY=VAL`
// prefix the legacy adapter used.
func (b *Backend) Spawn(ctx context.Context, spec mux.SpawnSpec) (mux.SessionHandle, error) {
	envMap := envSliceToMap(spec.Env)
	if err := b.c.CreateSession(ctx, spec.SessionName, spec.Cwd, envMap, spec.Argv); err != nil {
		return "", fmt.Errorf("tmuxbackend spawn: %w", err)
	}
	return mux.SessionHandle(spec.SessionName), nil
}

// Has returns whether tmux still knows about the session.
func (b *Backend) Has(ctx context.Context, h mux.SessionHandle) (bool, error) {
	return b.c.HasSession(ctx, string(h)), nil
}

// List returns sessions whose name starts with prefix. Empty prefix yields
// every session the tmux server reports.
func (b *Backend) List(ctx context.Context, prefix string) ([]mux.SessionInfo, error) {
	infos, err := b.c.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("tmuxbackend list: %w", err)
	}
	out := make([]mux.SessionInfo, 0, len(infos))
	for _, info := range infos {
		if prefix != "" && !strings.HasPrefix(info.Name, prefix) {
			continue
		}
		out = append(out, mux.SessionInfo{
			Handle:    mux.SessionHandle(info.Name),
			CreatedAt: time.Unix(info.CreatedUnix, 0),
			Attached:  info.Attached,
			PanePID:   info.PanePID,
			Cwd:       info.CurrentPath,
			PaneCmd:   info.CurrentCommand,
		})
	}
	return out, nil
}

// Capture returns the most recent `lines` rows of the session's pane.
func (b *Backend) Capture(ctx context.Context, h mux.SessionHandle, lines int) (mux.PaneCapture, error) {
	raw, err := b.c.Capture(ctx, string(h), lines)
	if err != nil {
		return mux.PaneCapture{}, fmt.Errorf("tmuxbackend capture: %w", err)
	}
	return mux.PaneCapture{
		Lines:      splitCapture(raw),
		CapturedAt: time.Now(),
	}, nil
}

// Write sends bytes to the session. A trailing '\r' is translated to a
// literal write followed by an Enter keypress, matching the legacy
// SendLine semantics.
func (b *Backend) Write(ctx context.Context, h mux.SessionHandle, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	text := string(data)
	if strings.HasSuffix(text, "\r") {
		body := strings.TrimSuffix(text, "\r")
		if body != "" {
			if err := b.c.SendKeysLiteral(ctx, string(h), body); err != nil {
				return fmt.Errorf("tmuxbackend write literal: %w", err)
			}
		}
		return b.c.SendEnter(ctx, string(h))
	}
	return b.c.SendKeysLiteral(ctx, string(h), text)
}

// Resize is a no-op for the tmux backend; the legacy client does not expose
// per-session resize and v0.1.11 preserves zero behavior change.
func (b *Backend) Resize(ctx context.Context, h mux.SessionHandle, cols, rows uint16) error {
	return nil
}

// Kill tears down the tmux session.
func (b *Backend) Kill(ctx context.Context, h mux.SessionHandle) error {
	return b.c.Kill(ctx, string(h))
}

// Attach is unsupported on the tmux backend; callers go through the
// AttachArgv path (`tmux attach -t`) via exec instead.
func (b *Backend) Attach(ctx context.Context, h mux.SessionHandle) (mux.PaneStream, error) {
	return nil, fmt.Errorf("tmuxbackend attach: use AttachArgv path via cmd.ExecProcess; in-process attach is unsupported on tmux")
}

// Subscribe returns (nil, nil) on the tmux backend; the caller falls back to
// polling via List+Capture, exactly as the legacy adapter did.
func (b *Backend) Subscribe(ctx context.Context, h mux.SessionHandle) (<-chan mux.Event, error) {
	return nil, nil
}

// envSliceToMap converts KEY=VAL entries to the map form CreateSession expects.
// Malformed entries (missing '=' or empty key) are dropped silently.
func envSliceToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out
}

// splitCapture trims one trailing newline and splits on '\n'. Empty input
// returns nil so callers see no spurious blank lines.
func splitCapture(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(raw, "\n"), "\n")
}
