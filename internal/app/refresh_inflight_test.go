package app

import (
	"errors"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// F17 — a refresh tick must NOT dispatch a second loadSessionsCmd while one is
// already in flight, but it MUST keep re-arming the ticker unconditionally
// (otherwise refreshes stop forever after a single in-flight load).
func TestRefreshDoesNotReArmWhileLoadInFlight(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.loading = false

	// First tick from idle: schedules a load and marks loading.
	m1, started1 := m.refreshStep(time.Now())
	if !started1 {
		t.Fatal("first refresh from idle must start a load")
	}
	if !m1.loading {
		t.Fatal("first refresh must set loading=true")
	}

	// Second tick while the first load is still in flight: must NOT start another
	// load, but the returned model is still loading (tick is re-armed by Update).
	m2, started2 := m1.refreshStep(time.Now())
	if started2 {
		t.Fatal("refresh must not stack a second load while one is in flight")
	}
	if !m2.loading {
		t.Fatal("loading must remain true until sessionsLoadedMsg clears it")
	}

	// The Update wrapper must always re-arm the ticker, even while loading.
	model, cmd := m1.Update(refreshMsg(time.Now()))
	if cmd == nil {
		t.Fatal("refresh tick must be re-armed unconditionally")
	}
	if !model.(Model).loading {
		t.Fatal("loading must persist across an in-flight refresh tick")
	}
}

// F17 — the matching refresh result must clear the loading flag on success and
// error. Results from unrelated actions must not release the refresh guard.
func TestSessionsLoadedAlwaysClearsInflightEvenOnError(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.loading = true

	model, _ := m.Update(sessionsLoadedMsg{refresh: true, err: errors.New("boom")})
	if model.(Model).loading {
		t.Fatal("loading must be cleared even when sessionsLoadedMsg carries an error")
	}

	m = NewWithDeps(nil, nil)
	m.loading = true
	model, _ = m.Update(sessionsLoadedMsg{refresh: true, sessions: []adapter.Session{{ID: "1"}}})
	if model.(Model).loading {
		t.Fatal("loading must be cleared on a successful sessionsLoadedMsg")
	}

	m = NewWithDeps(nil, nil)
	model, _ = m.Update(sessionsLoadedMsg{err: errors.New("action failed")})
	if !model.(Model).loading {
		t.Fatal("unrelated action result released the refresh guard")
	}
}

func TestInitialAndPRRefreshShareOneInflightGuard(t *testing.T) {
	m := NewWithDeps(nil, nil)
	if !m.loading {
		t.Fatal("initial load did not acquire the refresh guard")
	}
	model, cmd := m.Update(prRefreshedMsg{})
	if cmd != nil || !model.(Model).loading {
		t.Fatalf("PR refresh overlapped initial load: loading=%v cmd=%v", model.(Model).loading, cmd)
	}

	m.reloadSessions = func() sessionsLoadedMsg { return sessionsLoadedMsg{} }
	msg := m.refreshSessionsCmd()().(sessionsLoadedMsg)
	if !msg.refresh {
		t.Fatal("refreshSessionsCmd returned an untagged refresh result")
	}
}

func TestActionReloadDoesNotReleaseRefreshGuard(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.loading = true
	m.reloadSessions = func() sessionsLoadedMsg {
		return sessionsLoadedMsg{sessions: []adapter.Session{{ID: "action"}}}
	}
	msg := m.loadSessionsCmd()().(sessionsLoadedMsg)
	if msg.refresh {
		t.Fatal("action reload was tagged as a guarded refresh")
	}
	model, _ := m.Update(msg)
	if !model.(Model).loading {
		t.Fatal("action reload released a concurrent refresh guard")
	}
}

func TestOlderSessionLoadResultCannotReplaceNewerRoster(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.loading = false
	m = m.handleSessionsLoaded(sessionsLoadedMsg{
		loadGeneration: 2,
		sessions:       []adapter.Session{{ID: "newer"}},
	})
	m = m.handleSessionsLoaded(sessionsLoadedMsg{
		loadGeneration: 1,
		refresh:        true,
		sessions:       []adapter.Session{{ID: "older"}},
	})
	if len(m.sessions) != 1 || m.sessions[0].ID != "newer" {
		t.Fatalf("older load replaced newer roster: %+v", m.sessions)
	}
	if m.loading {
		t.Fatal("stale guarded refresh result did not release its guard")
	}
}
