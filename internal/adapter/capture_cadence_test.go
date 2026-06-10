package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

// newCadenceAgent builds an Agent over a recording backend that reports one
// live session whose output contains a PR URL, so capture calls can be
// counted.
func newCadenceAgent() (*Agent, *adaptertest.Backend) {
	be := &adaptertest.Backend{
		Sessions:    []session.Info{{Name: "uam-fake-abc12345", CreatedUnix: 1710000000, ChildPID: 1, Cwd: "/tmp/repo", Alive: true}},
		CaptureText: "Thinking...\ncreated https://github.com/o/r/pull/7\n",
	}
	ag := NewAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, be)
	return ag, be
}

// F16 — capture must NOT run on every List tick. After the first discovery,
// subsequent ticks within the rescan interval must reuse the prior result (no
// new capture), so the dashboard's 2s refresh doesn't fire a capture
// round-trip per session per tick.
func TestListDoesNotCapturePerSessionEveryTick(t *testing.T) {
	ag, be := newCadenceAgent()
	clock := time.Unix(1710000000, 0)
	ag.now = func() time.Time { return clock }

	// First List: discovers the session, captures once to scrape the PR.
	if _, err := ag.List(context.Background()); err != nil {
		t.Fatalf("List 1: %v", err)
	}
	if got := len(be.CallsOf("capture")); got != 1 {
		t.Fatalf("first List should capture exactly once, got %d", got)
	}

	// Several more ticks within the rescan interval: no additional capture.
	for i := 0; i < 5; i++ {
		clock = clock.Add(2 * time.Second)
		if _, err := ag.List(context.Background()); err != nil {
			t.Fatalf("List tick %d: %v", i, err)
		}
	}
	if got := len(be.CallsOf("capture")); got != 1 {
		t.Fatalf("ticks within rescan interval must not re-capture, got %d captures", got)
	}
}

// F16 — once the rescan interval elapses, List must re-capture to pick up a PR
// URL that appeared after first discovery.
func TestListRescansForNewPRAfterInterval(t *testing.T) {
	ag, be := newCadenceAgent()
	clock := time.Unix(1710000000, 0)
	ag.now = func() time.Time { return clock }

	if _, err := ag.List(context.Background()); err != nil {
		t.Fatalf("List 1: %v", err)
	}
	if got := len(be.CallsOf("capture")); got != 1 {
		t.Fatalf("first List should capture once, got %d", got)
	}

	// Advance past the rescan interval.
	clock = clock.Add(61 * time.Second)
	sessions, err := ag.List(context.Background())
	if err != nil {
		t.Fatalf("List 2: %v", err)
	}
	if got := len(be.CallsOf("capture")); got != 2 {
		t.Fatalf("List past rescan interval must re-capture, got %d captures", got)
	}
	if len(sessions) != 1 || sessions[0].PR == nil || sessions[0].PR.Number != 7 {
		t.Fatalf("rescan should re-discover PR: %+v", sessions)
	}
}

// The PR-scan throttle map must not grow without bound: stamps for sessions
// that disappeared are pruned on the next List.
func TestListPrunesPRScanStampsForGoneSessions(t *testing.T) {
	ag, be := newCadenceAgent()
	if _, err := ag.List(context.Background()); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ag.lastPRScan) != 1 {
		t.Fatalf("scan stamp not recorded: %v", ag.lastPRScan)
	}
	be.Sessions = nil
	if _, err := ag.List(context.Background()); err != nil {
		t.Fatalf("List 2: %v", err)
	}
	if len(ag.lastPRScan) != 0 {
		t.Fatalf("gone session's stamp must be pruned: %v", ag.lastPRScan)
	}
}
