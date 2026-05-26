package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/journal"
)

type expectedFixture struct {
	State     string `json:"state"`
	Summary   string `json:"summary"`
	ProcAlive bool   `json:"proc_alive"`
}

// TestClassifyOnFixturesAgreesAcrossSources is the classifier parity GATE.
//
// For each captured fixture under testdata/fixtures/<agent>/<scenario>/, we
// run ClassifyPane twice:
//  1. With tmux-capture.txt split on newlines (the legacy tmux path).
//  2. With pty-bytes.bin run through journal.ExtractLines (the upcoming
//     PTY+journal path).
//
// Both runs MUST produce the same State as expected.json. If they diverge,
// we cannot swap the tmux backend for the native multiplexer without
// regressing user-visible state classification.
func TestClassifyOnFixturesAgreesAcrossSources(t *testing.T) {
	root := "testdata/fixtures"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no fixtures directory: %v", err)
	}
	hadFixtures := false
	for _, agentDir := range entries {
		if !agentDir.IsDir() {
			continue
		}
		scenarioRoot := filepath.Join(root, agentDir.Name())
		scenarios, err := os.ReadDir(scenarioRoot)
		if err != nil {
			continue
		}
		for _, scenario := range scenarios {
			if !scenario.IsDir() {
				continue
			}
			hadFixtures = true
			runFixturePair(t, agentDir.Name(), filepath.Join(scenarioRoot, scenario.Name()))
		}
	}
	if !hadFixtures {
		t.Skip("no fixtures captured yet; populate testdata/fixtures/<agent>/<scenario>/ before tagging v0.1.13")
	}
}

func runFixturePair(t *testing.T, agent, dir string) {
	t.Run(filepath.Base(dir), func(t *testing.T) {
		expBytes, err := os.ReadFile(filepath.Join(dir, "expected.json"))
		if err != nil {
			t.Skipf("no expected.json: %v", err)
		}
		var exp expectedFixture
		if err := json.Unmarshal(expBytes, &exp); err != nil {
			t.Fatalf("expected.json: %v", err)
		}

		tmuxRaw, err := os.ReadFile(filepath.Join(dir, "tmux-capture.txt"))
		if err != nil {
			t.Fatalf("tmux-capture.txt: %v", err)
		}
		ptyRaw, err := os.ReadFile(filepath.Join(dir, "pty-bytes.bin"))
		if err != nil {
			t.Skipf("no pty-bytes.bin yet")
		}

		tmuxLines := strings.Split(strings.TrimRight(string(tmuxRaw), "\n"), "\n")
		ptyLines := journal.ExtractLines(ptyRaw)
		patterns := DefaultPatterns(agent)

		tmuxState, _, tmuxSummary := ClassifyPane(tmuxLines, agent, exp.ProcAlive, true, patterns)
		ptyState, _, ptySummary := ClassifyPane(ptyLines, agent, exp.ProcAlive, true, patterns)

		if string(tmuxState) != exp.State {
			t.Errorf("tmux path mismatch: got state=%q want %q (summary=%q)", tmuxState, exp.State, tmuxSummary)
		}
		if string(ptyState) != exp.State {
			t.Errorf("pty-journal path mismatch: got state=%q want %q (summary=%q)", ptyState, exp.State, ptySummary)
		}
	})
}
