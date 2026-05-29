package app

import (
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func peekCadenceModel() Model {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "fake", DisplayName: "one", ProcAlive: adapter.Alive},
		{ID: "two", AgentType: "fake", DisplayName: "two", ProcAlive: adapter.Alive},
	}
	return m
}

// C2-11 — Init must arm the peek-focus ticker so an open peek panel updates
// live, in addition to the session-refresh ticker.
func TestInitArmsPeekFocusTicker(t *testing.T) {
	m := peekCadenceModel()
	if m.Init() == nil {
		t.Fatal("Init must return a command batch (refresh + peek-focus tickers)")
	}
	// A peek tick must always re-arm its ticker, even when the panel is closed,
	// so the cadence never stops.
	_, cmd := m.Update(peekTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("a peek tick must re-arm the peek-focus ticker unconditionally")
	}
}

// C2-11 — when the peek panel is open, a peek-focus tick re-fires the capture
// for the focused session, but only once per focus interval (1s) — keyed by
// session id, not row index, since rows reorder.
func TestPeekFocusTickRefiresFocusedSessionAtInterval(t *testing.T) {
	m := peekCadenceModel()
	m.peekOpen = true
	m.selected = 0
	clock := time.Unix(1710000000, 0)
	m.peekClock = func() time.Time { return clock }

	// First focus tick: focused session hasn't been polled → re-fire.
	model, cmd := m.Update(peekTickMsg(clock))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("first peek-focus tick with the panel open must re-fire the focused peek")
	}

	// A tick within the focus interval: must NOT re-fire (only re-arm).
	clock = clock.Add(500 * time.Millisecond)
	m.peekClock = func() time.Time { return clock }
	model, _ = m.Update(peekTickMsg(clock))
	m = model.(Model)
	// Re-arming returns a non-nil cmd, so assert the focused capture was NOT
	// stamped again by checking the gate directly.
	if m.shouldPollFocusedPeek("one", clock) {
		t.Fatal("a tick within the focus interval must not re-poll the focused session")
	}

	// Past the focus interval: re-fire allowed again.
	clock = clock.Add(1 * time.Second)
	if !m.shouldPollFocusedPeek("one", clock) {
		t.Fatal("past the focus interval the focused session must be pollable again")
	}
}

// C2-11 — a peek-focus tick with the panel closed must not capture anything
// (avoids a background N+1 capture storm).
func TestPeekFocusTickNoCaptureWhenPanelClosed(t *testing.T) {
	m := peekCadenceModel()
	m.peekOpen = false
	clock := time.Unix(1710000000, 0)
	m.peekClock = func() time.Time { return clock }

	model, cmd := m.Update(peekTickMsg(clock))
	m = model.(Model)
	// The ticker is re-armed (non-nil), but no peek capture batched. Confirm via
	// the focused-poll gate: with the panel closed nothing should be stamped.
	if cmd == nil {
		t.Fatal("peek ticker must re-arm even when closed")
	}
	if _, polled := m.lastPeekAt["one"]; polled {
		t.Fatal("a closed panel must not poll/stamp any session")
	}
}
