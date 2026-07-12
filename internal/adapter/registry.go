package adapter

import (
	"errors"
	"fmt"
	"sort"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

type Registry struct {
	adapters map[string]AgentAdapter
	reasons  map[string]string
	backend  Backend
}

func NewRegistry(adapters []AgentAdapter) *Registry {
	return newRegistry(nil, adapters)
}

// NewRegistryWithBackend records the backend shared by production adapters so
// ListAll can enumerate it once and fan the immutable snapshot out by provider.
func NewRegistryWithBackend(backend Backend, adapters []AgentAdapter) *Registry {
	return newRegistry(backend, adapters)
}

func newRegistry(backend Backend, adapters []AgentAdapter) *Registry {
	r := &Registry{adapters: map[string]AgentAdapter{}, reasons: map[string]string{}, backend: backend}
	for _, a := range adapters {
		ok, reason := a.Available()
		if ok {
			r.adapters[a.Name()] = a
		} else {
			r.reasons[a.Name()] = reason
		}
	}
	return r
}

type snapshotListingAdapter interface {
	ListFromSnapshot(ctx Context, infos []session.Info) ([]Session, error)
}

// ListAll returns every enabled provider's sessions. Production adapters share
// one backend snapshot; custom adapters without snapshot support fall back to
// their ordinary List implementation. Partial results survive adapter errors.
func (r *Registry) ListAll(ctx Context) ([]Session, error) {
	enabled := r.Enabled()
	var infos []session.Info
	if r.backend != nil {
		var err error
		infos, err = r.backend.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list shared session backend: %w", err)
		}
	}
	var out []Session
	var joined error
	for _, a := range enabled {
		var sessions []Session
		var err error
		if lister, ok := a.(snapshotListingAdapter); ok && r.backend != nil {
			sessions, err = lister.ListFromSnapshot(ctx, infos)
		} else {
			sessions, err = a.List(ctx)
		}
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("list %s sessions: %w", a.Name(), err))
			continue
		}
		out = append(out, sessions...)
	}
	return out, joined
}

func (r *Registry) Get(name string) (AgentAdapter, bool) { a, ok := r.adapters[name]; return a, ok }

func (r *Registry) Enabled() []AgentAdapter {
	out := make([]AgentAdapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func (r *Registry) DisabledReasons() map[string]string { return r.reasons }

func (r *Registry) Default(preferred string) AgentAdapter {
	if a, ok := r.Get(preferred); ok {
		return a
	}
	enabled := r.Enabled()
	if len(enabled) == 0 {
		return nil
	}
	return enabled[0]
}
