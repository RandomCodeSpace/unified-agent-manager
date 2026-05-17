package adapter

import "testing"

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
func (f fakeAdapter) List(ctx Context) ([]Session, error)                { return nil, nil }
func (f fakeAdapter) Peek(ctx Context, id string) (PeekResult, error)    { return PeekResult{}, nil }
func (f fakeAdapter) Reply(ctx Context, id, text string) error           { return nil }
func (f fakeAdapter) Attach(id string) (AttachSpec, error)               { return AttachSpec{}, nil }
func (f fakeAdapter) Stop(ctx Context, id string) error                  { return nil }
func (f fakeAdapter) Rename(ctx Context, id, newName string) error       { return nil }
func (f fakeAdapter) Subscribe(ctx Context) (<-chan SessionEvent, error) { return nil, nil }
