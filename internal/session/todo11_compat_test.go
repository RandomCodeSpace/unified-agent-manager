package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

var todo11Run atomic.Int32

type todo11Report struct {
	RunsIdentical bool              `json:"runs_identical"`
	Matrix        map[string]string `json:"matrix"`
	Terms         []string          `json:"terms"`
	Scenarios     []string          `json:"scenarios"`
}

func TestTodo11CompatibilityRealPTYMatrix(t *testing.T) {
	// Given: an explicitly scoped absolute artifact directory and checked-in binary fixtures.
	evidenceDir := os.Getenv("UAM_TASK11_EVIDENCE_DIR")
	if evidenceDir == "" {
		t.Skip("UAM_TASK11_EVIDENCE_DIR is required for artifact collection")
	}
	if !filepath.IsAbs(evidenceDir) {
		t.Fatalf("UAM_TASK11_EVIDENCE_DIR must be absolute: %q", evidenceDir)
	}
	if filepath.Base(evidenceDir) != "task-11-compat" {
		t.Fatalf("refusing unexpected evidence directory: %q", evidenceDir)
	}
	for _, child := range []string{"transcripts", "normalized"} {
		if err := os.MkdirAll(filepath.Join(evidenceDir, child), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture := todo11LoadFixture(t)
	terms := []string{
		"xterm-256color", "screen-256color", "tmux-256color", "xterm-kitty",
		"alacritty", "wezterm", "ghostty",
	}
	run := int(todo11Run.Add(1))
	hashes := make(map[string]string, len(terms))
	goroutinesBefore := runtime.NumGoroutine()
	cleanupObserved := true

	// When: every TERM metadata input drives the same real-PTY logical replay.
	for index, termName := range terms {
		normalized, transcript := todo11ExerciseHost(t, termName, fixture, run*100+index)
		data, err := json.MarshalIndent(normalized, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		hashes[termName] = fmt.Sprintf("%x", sha256.Sum256(data))
		name := todo11SafeName(termName)
		if err := os.WriteFile(filepath.Join(evidenceDir, "transcripts", name+".bin"), transcript, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "normalized", name+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		cleanupObserved = cleanupObserved && normalized.SocketRemoved && normalized.RuntimeEntries == 0
		if err := todo11ValidateNegotiatedTermHint(
			todo11ExpectedDiagnosticTermHint(termName),
			normalized.NegotiatedTermHint,
		); err != nil {
			t.Fatalf("%s: %v", termName, err)
		}
		if normalized.CapabilityInferred || !normalized.ObserverSuppressed ||
			!normalized.DisconnectReattached || !normalized.MalformedDropped ||
			!normalized.TruncatedDropped || !normalized.WINCHObserved ||
			!normalized.ReplayObserved || !normalized.ReplayModesObserved {
			t.Fatalf("%s behavioral assertions failed", termName)
		}
	}
	unsupported, unsupportedTranscript := todo11ExerciseHost(t, "unsupported-uam-term", fixture, run*100+99)
	if unsupported.NegotiatedTermHint != "redacted" {
		t.Fatalf("unsupported negotiated TERM = %q", unsupported.NegotiatedTermHint)
	}
	cleanupObserved = cleanupObserved && unsupported.SocketRemoved && unsupported.RuntimeEntries == 0
	unsupportedData, err := json.MarshalIndent(unsupported, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "transcripts", "unsupported_term.bin"), unsupportedTranscript, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "normalized", "unsupported_term.json"), append(unsupportedData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	hashPath := filepath.Join(evidenceDir, fmt.Sprintf("hashes-run-%d.txt", run))
	if err := os.WriteFile(hashPath, todo11HashLines(hashes), 0o644); err != nil {
		t.Fatal(err)
	}

	// Then: the second repetition must be byte-identical to the first.
	if run == 2 {
		first, err := os.ReadFile(filepath.Join(evidenceDir, "hashes-run-1.txt"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(hashPath)
		if err != nil {
			t.Fatal(err)
		}
		identical := bytes.Equal(first, second)
		diff := []byte("no normalized hash differences\n")
		if !identical {
			diff = []byte("normalized hash lists differ\n")
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "hash-diff.txt"), diff, 0o644); err != nil {
			t.Fatal(err)
		}
		report := todo11Report{RunsIdentical: identical, Matrix: hashes, Terms: terms, Scenarios: []string{
			"printable_utf8", "arrows_modifiers", "control_prefix", "sgr_mouse",
			"bracketed_paste", "focus", "alternate_screen", "resize_burst",
			"disconnect_reconnect", "observer", "provider_replay", "malformed_escape_frame",
			"large_paste", "unsupported_term_metadata",
		}}
		todo11WriteJSON(t, filepath.Join(evidenceDir, "report.json"), report)
		goroutinesAfter := runtime.NumGoroutine()
		cleanup := map[string]any{
			"hosts_cleaned": cleanupObserved, "connections_closed": unsupported.DisconnectReattached,
			"goroutines_bounded":        goroutinesAfter <= goroutinesBefore+1,
			"runtime_entries_remaining": unsupported.RuntimeEntries,
			"socket_removed":            unsupported.SocketRemoved,
			"go_routines_before":        goroutinesBefore, "go_routines_after": goroutinesAfter,
		}
		todo11WriteJSON(t, filepath.Join(evidenceDir, "cleanup-receipt.json"), cleanup)
		if !identical {
			t.Fatal("normalized hashes differ across repetitions")
		}
	}
}

func todo11HashLines(hashes map[string]string) []byte {
	terms := make([]string, 0, len(hashes))
	for termName := range hashes {
		terms = append(terms, termName)
	}
	sort.Strings(terms)
	var result strings.Builder
	for _, termName := range terms {
		fmt.Fprintf(&result, "%s  %s\n", hashes[termName], termName)
	}
	return []byte(result.String())
}
