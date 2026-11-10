package web

import (
	"context"
	"testing"
	"time"

	coremodel "github.com/RandomCodeSpace/aiusage-core/model"
	corestore "github.com/RandomCodeSpace/aiusage-core/store"
)

// Ownership of long-released Copilot sessions leaves the ledger at the next
// start, and a usage file read again then imports nothing from before that
// boundary: the old session's own calls are not counted as external ones.
func TestCopilotOwnershipIsPrunedAndItsOldCallsAreNotImported(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	old := now.Add(-2 * copilotOwnershipKeep)
	m, ledger := harnessTestManager(t, old)
	at := func(when time.Time) { m.now = func() time.Time { return when } }
	for _, step := range []struct {
		id     string
		when   time.Time
		active bool
	}{
		{"old-session", old.Add(time.Minute), true},
		{"old-session", old.Add(time.Hour), false},
		{"kept-session", old.Add(time.Minute), true},
		{"kept-session", old.Add(time.Hour), false},
		{"kept-session", now.Add(-time.Hour), true},
		{"kept-session", now.Add(-30 * time.Minute), false},
	} {
		at(step.when)
		if err := m.recordCopilotUsageSession(step.id, step.active); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	again := NewManager(m.store, nil)
	again.now = func() time.Time { return now }
	again.usageHome = m.usageHome
	again.loadTokenLedger()
	next, err := again.prepareHarnessUsage()
	if err != nil || next == nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = next.Close() }()
	boundary := now.Add(-copilotOwnershipKeep)
	if got := again.tokens.CopilotSessions; len(got) != 1 || len(got["kept-session"]) != 1 || !again.tokens.CopilotPruned.Equal(boundary) {
		t.Fatalf("ownership after the start = %+v, pruned %v", got, again.tokens.CopilotPruned)
	}

	_, err = usageOnlyStore{Ledger: next, m: again}.ApplyBatch(context.Background(), corestore.ObservationBatch{Events: []coremodel.UsageEvent{
		harnessTestEvent("copilot", "old-session", "was-owned", old.Add(30*time.Minute), 100, 20),
		harnessTestEvent("copilot", "kept-session", "still-owned", now.Add(-45*time.Minute), 100, 20),
		harnessTestEvent("copilot", "external-session", "external", now.Add(-time.Minute), 10, 2),
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := next.ListEvents(context.Background(), corestore.Filter{})
	if err != nil || len(rows) != 1 || rows[0].SessionID != "external-session" {
		t.Fatalf("imported rows = %+v, %v", rows, err)
	}

	// The boundary is saved with the ledger.
	reloaded := NewManager(m.store, nil)
	reloaded.loadTokenLedger()
	if !reloaded.tokens.CopilotPruned.Equal(boundary) || len(reloaded.tokens.CopilotSessions) != 1 {
		t.Fatalf("saved ledger: pruned %v, %d sessions", reloaded.tokens.CopilotPruned, len(reloaded.tokens.CopilotSessions))
	}
}
