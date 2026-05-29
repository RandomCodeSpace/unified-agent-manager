package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// C1-3 — `uam new` must preserve interior whitespace in the prompt. The old
// implementation rebuilt the prompt with strings.Fields + single-space rejoin,
// collapsing runs of spaces and tabs (corrupting code blocks, indentation, and
// aligned text the user typed). Only a leading #name token may be split off.
func TestRunNewPreservesPromptWhitespace(t *testing.T) {
	cases := []struct {
		name       string
		stdin      string
		wantName   string
		wantPrompt string
	}{
		{
			name:       "double spaces preserved",
			stdin:      "fake\n/tmp\nfix  the   parser\n",
			wantName:   "",
			wantPrompt: "fix  the   parser",
		},
		{
			name:       "named prompt keeps interior spacing",
			stdin:      "fake\n/tmp\n#bugfix do  this   thing\n",
			wantName:   "bugfix",
			wantPrompt: "do  this   thing",
		},
		{
			name:       "leading whitespace after name preserved",
			stdin:      "fake\n/tmp\n#bugfix   indented\n",
			wantName:   "bugfix",
			wantPrompt: "  indented",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newCLITestService(t)
			withCLIStdin(t, tc.stdin, func() {
				_ = captureCLIStdout(t, func() { must(t, runNew(context.Background(), svc)) })
			})
			if len(fake.sessions) != 1 {
				t.Fatalf("expected one dispatched session, got %d", len(fake.sessions))
			}
			if fake.sessions[0].Prompt != tc.wantPrompt {
				t.Fatalf("prompt = %q, want %q", fake.sessions[0].Prompt, tc.wantPrompt)
			}
			if tc.wantName == "" {
				return // unnamed sessions get a derived display name; only the prompt matters here
			}
			cfg, err := svc.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			rec := cfg.Sessions[store.Key("fake", fake.sessions[0].ID)]
			if rec.Name != tc.wantName {
				t.Fatalf("name = %q, want %q", rec.Name, tc.wantName)
			}
		})
	}
}

// F54 — `uam new` must reject an empty final prompt (EOF / blank input) instead
// of dispatching an empty-prompt session and exiting 0.
func TestRunNewRejectsEmptyPrompt(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
	}{
		{"empty stdin (EOF)", ""},
		{"blank prompt line", "fake\n/tmp\n\n"},
		{"whitespace-only prompt", "fake\n/tmp\n   \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newCLITestService(t)
			var err error
			withCLIStdin(t, tc.stdin, func() {
				_ = captureCLIStdout(t, func() { err = runNew(context.Background(), svc) })
			})
			if err == nil {
				t.Fatal("expected runNew to reject an empty prompt")
			}
			if len(fake.sessions) != 0 {
				t.Fatalf("a rejected new must not dispatch a session, got %d", len(fake.sessions))
			}
		})
	}
}

// F54 — data and io.EOF can co-arrive on the final read; the prompt typed on the
// last line (no trailing newline) must still be used.
func TestRunNewUsesPromptOnEOFWithoutNewline(t *testing.T) {
	svc, fake := newCLITestService(t)
	withCLIStdin(t, "fake\n/tmp\nlast line no newline", func() {
		_ = captureCLIStdout(t, func() { must(t, runNew(context.Background(), svc)) })
	})
	if len(fake.sessions) != 1 {
		t.Fatalf("expected one dispatched session, got %d", len(fake.sessions))
	}
	if fake.sessions[0].Prompt != "last line no newline" {
		t.Fatalf("prompt = %q, want %q", fake.sessions[0].Prompt, "last line no newline")
	}
}

// C1-6 — `uam last` must select the session with the maximum persisted
// LastSeenAt (with a deterministic id tiebreak), not the top sorted row. The
// selection logic lives in lastSeenID; assert it picks the newest-seen record.
func TestLastSeenIDSelectsMaxLastSeenAt(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := store.Config{Sessions: map[string]store.SessionRecord{
		store.Key("fake", "aaaaaaaa"): {ID: "aaaaaaaa", Agent: "fake", TmuxSession: "uam-fake-aaaaaaaa", LastSeenAt: base},
		store.Key("fake", "bbbbbbbb"): {ID: "bbbbbbbb", Agent: "fake", TmuxSession: "uam-fake-bbbbbbbb", LastSeenAt: base.Add(2 * time.Hour)},
		store.Key("fake", "cccccccc"): {ID: "cccccccc", Agent: "fake", TmuxSession: "uam-fake-cccccccc", LastSeenAt: base.Add(time.Hour)},
	}}
	if got := lastSeenID(cfg); got != "bbbbbbbb" {
		t.Fatalf("lastSeenID = %q, want bbbbbbbb (max last_seen_at)", got)
	}
}

// C1-6 — equal LastSeenAt must resolve deterministically (largest id wins) so
// repeated `uam last` invocations are stable.
func TestLastSeenIDTiebreakIsDeterministic(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := store.Config{Sessions: map[string]store.SessionRecord{
		store.Key("fake", "aaaaaaaa"): {ID: "aaaaaaaa", Agent: "fake", LastSeenAt: ts},
		store.Key("fake", "zzzzzzzz"): {ID: "zzzzzzzz", Agent: "fake", LastSeenAt: ts},
	}}
	first := lastSeenID(cfg)
	for i := 0; i < 10; i++ {
		if got := lastSeenID(cfg); got != first {
			t.Fatalf("lastSeenID not deterministic: %q vs %q", got, first)
		}
	}
	if first != "zzzzzzzz" {
		t.Fatalf("tiebreak = %q, want zzzzzzzz", first)
	}
}

// C1-6 — `uam last` with no persisted records still surfaces the existing
// "no sessions" error rather than panicking on an empty selection.
func TestRunLastWithNoRecordsFails(t *testing.T) {
	svc, _ := newCLITestService(t)
	runTUI := func(context.Context, tea.Model) error { return nil }
	if err := runLast(context.Background(), svc, runTUI); err == nil {
		t.Fatal("expected runLast to fail when no sessions exist")
	}
}

var _ = strings.TrimSpace
