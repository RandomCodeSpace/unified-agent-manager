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

// F17 — sessionsLoadedMsg must clear the loading flag unconditionally, including
// the error path, or a single failed load wedges the ticker forever.
func TestSessionsLoadedAlwaysClearsInflightEvenOnError(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.loading = true

	model, _ := m.Update(sessionsLoadedMsg{err: errors.New("boom")})
	if model.(Model).loading {
		t.Fatal("loading must be cleared even when sessionsLoadedMsg carries an error")
	}

	m = NewWithDeps(nil, nil)
	m.loading = true
	model, _ = m.Update(sessionsLoadedMsg{sessions: []adapter.Session{{ID: "1"}}})
	if model.(Model).loading {
		t.Fatal("loading must be cleared on a successful sessionsLoadedMsg")
	}
}
