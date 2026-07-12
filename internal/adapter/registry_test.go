package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestRegistryDefaultAndDisabledReasons(t *testing.T) {
	r := NewRegistry([]AgentAdapter{fakeAdapter{name: "b", available: true}, fakeAdapter{name: "a", available: true}, fakeAdapter{name: "x", available: false}})
	if r.DisabledReasons()["x"] == "" {
		t.Fatal("missing disabled reason")
	}
	if got := r.Default("b"); got == nil || got.Name() != "b" {
		t.Fatalf("default preferred=%v", got)
	}
	if got := r.Default("missing"); got == nil || got.Name() != "a" {
		t.Fatalf("default fallback=%v", got)
	}
	empty := NewRegistry(nil)
	if got := empty.Default("none"); got != nil {
		t.Fatalf("empty default=%v", got)
	}
}

func TestRegistrySkipsUnavailableAdapters(t *testing.T) {
	r := NewRegistry([]AgentAdapter{fakeAdapter{name: "ok", available: true}, fakeAdapter{name: "missing", available: false}})
	if len(r.Enabled()) != 1 || r.Enabled()[0].Name() != "ok" {
		t.Fatalf("enabled = %+v", r.Enabled())
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("missing adapter should not resolve")
	}
}

func TestRegistryListAllUsesSingleBackendSnapshot(t *testing.T) {
	backend := &adaptertest.Backend{Sessions: []session.Info{
		{Name: "uam-a-aaaa1111", CreatedUnix: time.Now().Unix(), Alive: true},
		{Name: "uam-b-bbbb2222", CreatedUnix: time.Now().Unix(), Alive: true},
	}}
	a := NewAgent("a", "A", []CommandCandidate{{Display: "sh", Args: []string{"/bin/sh"}}}, nil, backend)
	b := NewAgent("b", "B", []CommandCandidate{{Display: "sh", Args: []string{"/bin/sh"}}}, nil, backend)
	r := NewRegistryWithBackend(backend, []AgentAdapter{a, b})

	sessions, err := r.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2: %+v", len(sessions), sessions)
	}
	if got := len(backend.CallsOf("list")); got != 1 {
		t.Fatalf("backend List calls = %d, want 1", got)
	}
}

type fakeAdapter struct {
	name      string
	available bool
}

func (f fakeAdapter) Name() string        { return f.name }
func (f fakeAdapter) DisplayName() string { return f.name }
func (f fakeAdapter) Available() (bool, string) {
	if f.available {
		return true, ""
	}
	return false, "nope"
}
func (f fakeAdapter) Dispatch(ctx Context, req DispatchRequest) (Session, error) {
	return Session{}, nil
}
func (f fakeAdapter) List(ctx Context) ([]Session, error)             { return nil, nil }
func (f fakeAdapter) Peek(ctx Context, id string) (PeekResult, error) { return PeekResult{}, nil }
func (f fakeAdapter) Reply(ctx Context, id, text string) error        { return nil }
func (f fakeAdapter) Attach(id string) (AttachSpec, error)            { return AttachSpec{}, nil }
func (f fakeAdapter) Stop(ctx Context, id string) error               { return nil }
