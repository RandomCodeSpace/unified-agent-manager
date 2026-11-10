package web

import (
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// sharedReads lets concurrent callers share a store read: the store's lock
// serializes reads, so each page load's /api/meta (or each charts view's
// /api/projects/{id}/charts) would otherwise wait for every read ahead of
// it. A caller reuses only a read that started after it arrived, so it never
// misses an earlier write. Callers must not change the value they get.
type sharedReads[T any] struct {
	started atomic.Uint64 // reads started; written under mu
	mu      sync.Mutex
	last    uint64 // which read produced value; 0 when none succeeded
	value   T
}

// read returns a value load read after the call arrived, from load now or
// from a concurrent caller's call; only a read that succeeded is shared.
func (r *sharedReads[T]) read(load func() (T, error)) (T, error) {
	arrived := r.started.Load()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last > arrived {
		return r.value, nil
	}
	read := r.started.Add(1)
	out, err := load()
	if err == nil {
		r.last, r.value = read, out
	}
	return out, err
}

// RecentWorkdirs returns distinct workdirs of stored records of any surface,
// most recently seen first.
func (m *Manager) RecentWorkdirs() []string {
	out, err := m.recent.read(m.readRecentWorkdirs)
	if err != nil {
		log.Warn("load recent workdirs failed", "error", err)
	}
	return slices.Clone(out)
}

func (m *Manager) readRecentWorkdirs() ([]string, error) {
	out := []string{}
	cfg, err := m.store.Load()
	if err != nil {
		return out, err
	}
	recs := make([]store.SessionRecord, 0, len(cfg.Sessions))
	for _, rec := range cfg.Sessions {
		// A Thread's worktree is no folder to add as a Project.
		if rec.Workdir != "" && (rec.Web == nil || rec.Web.Place == nil || rec.Web.Place.Worktree == "") {
			recs = append(recs, rec)
		}
	}
	seen := func(rec store.SessionRecord) time.Time {
		if rec.LastSeenAt.IsZero() {
			return rec.CreatedAt
		}
		return rec.LastSeenAt
	}
	sort.Slice(recs, func(i, j int) bool {
		if a, b := seen(recs[i]), seen(recs[j]); !a.Equal(b) {
			return a.After(b)
		}
		return recs[i].Workdir < recs[j].Workdir
	})
	distinct := map[string]bool{}
	for _, rec := range recs {
		if distinct[rec.Workdir] {
			continue
		}
		distinct[rec.Workdir] = true
		out = append(out, rec.Workdir)
		if len(out) == recentWorkdirsN {
			break
		}
	}
	return out, nil
}
