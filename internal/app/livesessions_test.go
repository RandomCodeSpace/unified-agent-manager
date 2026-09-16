package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// captureWarn installs a capturing slog handler for the duration of fn and
// returns everything written, then restores the previous logger.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer log.SetLogger(prev)
	fn()
	return buf.String()
}

// F12 — a single adapter whose List fails must be logged (not silently
// dropped) and must not blank the dashboard for the healthy adapters.
func TestLiveSessionsLogsAdapterListFailure(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	healthy := &svcFakeAdapter{name: "healthy", available: true, sessions: []adapter.Session{
		{ID: "live0001", AgentType: "healthy", DisplayName: "live", Cwd: "/tmp", SessionName: "uam-healthy-live0001", State: adapter.Active, CreatedAt: time.Now()},
	}}
	broken := &svcFakeAdapter{name: "broken", available: true, listErr: errors.New("tmux exploded")}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{healthy, broken}))

	var sessions map[string]adapter.Session
	out := captureLog(t, func() {
		sessions, _ = svc.liveSessions(context.Background())
	})

	if len(sessions) != 1 || sessions[store.Key("healthy", "live0001")].ID == "" {
		t.Fatalf("healthy adapter sessions must survive a sibling failure, got %d", len(sessions))
	}
	if !strings.Contains(out, "broken") {
		t.Fatalf("adapter List failure should be logged with the agent name, got: %q", out)
	}
}

// F12 trap — a benign (nil error) List must not emit a warning.
func TestLiveSessionsDoesNotWarnOnSuccess(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	healthy := &svcFakeAdapter{name: "healthy", available: true, sessions: []adapter.Session{
		{ID: "live0001", AgentType: "healthy", DisplayName: "live", Cwd: "/tmp", SessionName: "uam-healthy-live0001", State: adapter.Active, CreatedAt: time.Now()},
	}}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{healthy}))

	out := captureLog(t, func() {
		_, _ = svc.liveSessions(context.Background())
	})
	if strings.Contains(strings.ToUpper(out), "WARN") {
		t.Fatalf("a successful List must not emit a warning, got: %q", out)
	}
}

// Audit 2026-09-12 row 12 — when the shared session backend itself cannot be
// listed, LoadSessions must fail (so the dashboard keeps its last good roster
// and shows Retry) instead of projecting every stored record as Stopped and
// persisting that lie.
func TestLoadSessionsFailsWhenBackendListingFails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", "11111111")] = store.SessionRecord{ID: "11111111", Agent: "fake", Name: "live", Workdir: "/tmp/shared", SessionName: "uam-fake-11111111", Status: store.StatusActive}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backend := &adaptertest.Backend{ListErr: errors.New("runtime dir unreadable")}
	agent := adapter.NewAgent("fake", "Fake", []adapter.CommandCandidate{{Display: "sh", Args: []string{"/bin/sh"}}}, nil, backend)
	svc := NewService(st, adapter.NewRegistryWithBackend(backend, []adapter.AgentAdapter{agent}))

	sessions, _, err := svc.LoadSessions(context.Background())
	if err == nil {
		t.Fatalf("LoadSessions should surface the backend listing failure, got sessions=%+v", sessions)
	}
	if !strings.Contains(err.Error(), "runtime dir unreadable") {
		t.Fatalf("error should carry the backend cause, got %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sessions[store.Key("fake", "11111111")].Status; got != store.StatusActive {
		t.Fatalf("stored status must not be rewritten on a failed listing, got %q", got)
	}
}
