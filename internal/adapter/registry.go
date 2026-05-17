package adapter

import "sort"

type Registry struct {
	adapters map[string]AgentAdapter
	reasons  map[string]string
}

func NewRegistry(adapters []AgentAdapter) *Registry {
	r := &Registry{adapters: map[string]AgentAdapter{}, reasons: map[string]string{}}
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
