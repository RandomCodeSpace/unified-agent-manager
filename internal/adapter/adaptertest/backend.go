// Package adaptertest provides a recording fake of adapter.Backend for tests.
// It replaces the old fake-tmux-binary harness: instead of grepping a shell
// log of tmux argv, tests assert directly on the recorded backend calls.
package adaptertest

import (
	"context"
	"strings"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

// Call is one recorded backend invocation.
type Call struct {
	Op      string
	Name    string
	Cwd     string
	Env     map[string]string
	Command []string
	Text    string
	Lines   int
	Label   string
}

// Backend is an in-memory adapter.Backend that records every call and serves
// configurable results.
type Backend struct {
	mu    sync.Mutex
	calls []Call

	// Sessions is returned by List.
	Sessions []session.Info
	// CaptureText is returned by Capture.
	CaptureText string

	CreateErr  error
	ListErr    error
	CaptureErr error
	SendErr    error
	KillErr    error
	LabelErr   error
	HasResult  bool
	AttachExe  string
}

func (b *Backend) record(c Call) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, c)
}

// Calls returns a copy of all recorded calls.
func (b *Backend) Calls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Call, len(b.calls))
	copy(out, b.calls)
	return out
}

// CallsOf returns the recorded calls with the given op.
func (b *Backend) CallsOf(op string) []Call {
	var out []Call
	for _, c := range b.Calls() {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

// CommandLog renders the created sessions' argv as lines, for substring
// asserts equivalent to the old fake-tmux log greps.
func (b *Backend) CommandLog() string {
	var lines []string
	for _, c := range b.CallsOf("create") {
		lines = append(lines, strings.Join(c.Command, " "))
	}
	return strings.Join(lines, "\n")
}

func (b *Backend) CreateSession(_ context.Context, name, cwd string, env map[string]string, command []string) error {
	b.record(Call{Op: "create", Name: name, Cwd: cwd, Env: env, Command: append([]string{}, command...)})
	return b.CreateErr
}

func (b *Backend) SetSessionLabel(_ context.Context, name, label string) error {
	b.record(Call{Op: "label", Name: name, Label: label})
	return b.LabelErr
}

func (b *Backend) List(_ context.Context) ([]session.Info, error) {
	b.record(Call{Op: "list"})
	if b.ListErr != nil {
		return nil, b.ListErr
	}
	out := make([]session.Info, len(b.Sessions))
	copy(out, b.Sessions)
	return out, nil
}

func (b *Backend) Capture(_ context.Context, name string, lines int) (string, error) {
	b.record(Call{Op: "capture", Name: name, Lines: lines})
	if b.CaptureErr != nil {
		return "", b.CaptureErr
	}
	return b.CaptureText, nil
}

func (b *Backend) SendLine(_ context.Context, name, text string) error {
	b.record(Call{Op: "send", Name: name, Text: text})
	return b.SendErr
}

func (b *Backend) Kill(_ context.Context, name string) error {
	b.record(Call{Op: "kill", Name: name})
	return b.KillErr
}

func (b *Backend) HasSession(_ context.Context, name string) bool {
	b.record(Call{Op: "has", Name: name})
	return b.HasResult
}

func (b *Backend) AttachArgv(name string) ([]string, error) {
	b.record(Call{Op: "attach", Name: name})
	exe := b.AttachExe
	if exe == "" {
		exe = "/usr/local/bin/uam"
	}
	return []string{exe, "__attach", name}, nil
}
