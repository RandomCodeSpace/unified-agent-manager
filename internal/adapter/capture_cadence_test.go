package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// newCadenceAgent builds a TmuxAgent over a fake tmux that records every
// invocation (so capture-pane calls can be counted) and reports one live
// session whose pane prints a PR URL.
func newCadenceAgent(t *testing.T) (*TmuxAgent, string) {
	t.Helper()
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-fake-abc12345|1710000000|0|1|/tmp/repo|fakeagent" ;;
  *"capture-pane"*) printf 'Thinking...\ncreated https://github.com/o/r/pull/7\n' ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	return ag, logPath
}

func countCaptures(t *testing.T, logPath string) int {
	t.Helper()
	b, _ := os.ReadFile(logPath)
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "capture-pane") {
			n++
		}
	}
	return n
}

// F16 — capture-pane must NOT run on every List tick. After the first
// discovery, subsequent ticks within the rescan interval must reuse the prior
// result (no new capture), so the dashboard's 2s refresh doesn't fork a
// capture-pane per session per tick.
func TestListDoesNotCapturePerSessionEveryTick(t *testing.T) {
	ag, logPath := newCadenceAgent(t)
	clock := time.Unix(1710000000, 0)
	ag.now = func() time.Time { return clock }

	// First List: discovers the session, captures once to scrape the PR.
	if _, err := ag.List(context.Background()); err != nil {
		t.Fatalf("List 1: %v", err)
	}
	if got := countCaptures(t, logPath); got != 1 {
		t.Fatalf("first List should capture exactly once, got %d", got)
	}

	// Several more ticks within the rescan interval: no additional capture.
	for i := 0; i < 5; i++ {
		clock = clock.Add(2 * time.Second)
		ag.now = func() time.Time { return clock }
		if _, err := ag.List(context.Background()); err != nil {
			t.Fatalf("List tick %d: %v", i, err)
		}
	}
	if got := countCaptures(t, logPath); got != 1 {
		t.Fatalf("ticks within rescan interval must not re-capture, got %d captures", got)
	}
}

// F16 — once the rescan interval elapses, List must re-capture to pick up a PR
// URL that appeared after first discovery.
func TestListRescansForNewPRAfterInterval(t *testing.T) {
	ag, logPath := newCadenceAgent(t)
	clock := time.Unix(1710000000, 0)
	ag.now = func() time.Time { return clock }

	if _, err := ag.List(context.Background()); err != nil {
		t.Fatalf("List 1: %v", err)
	}
	if got := countCaptures(t, logPath); got != 1 {
		t.Fatalf("first List should capture once, got %d", got)
	}

	// Advance past the rescan interval.
	clock = clock.Add(61 * time.Second)
	ag.now = func() time.Time { return clock }
	sessions, err := ag.List(context.Background())
	if err != nil {
		t.Fatalf("List 2: %v", err)
	}
	if got := countCaptures(t, logPath); got != 2 {
		t.Fatalf("List past rescan interval must re-capture, got %d captures", got)
	}
	if len(sessions) != 1 || sessions[0].PR == nil || sessions[0].PR.Number != 7 {
		t.Fatalf("rescan should re-discover PR: %+v", sessions)
	}
}
