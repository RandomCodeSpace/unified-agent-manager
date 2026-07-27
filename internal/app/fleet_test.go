package app

import (
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/charmbracelet/x/ansi"
)

func TestAgeClassBucketsTheIntervalsAtTheirBoundaries(t *testing.T) {
	cases := []struct {
		since time.Duration
		want  ageClass
	}{
		{0, ageFresh},
		{ageRecentAfter - time.Nanosecond, ageFresh},
		{ageRecentAfter, ageRecent},
		{ageOldAfter - time.Nanosecond, ageRecent},
		{ageOldAfter, ageOld},
		{ageStaleAfter - time.Nanosecond, ageOld},
		{ageStaleAfter, ageStale},
		{365 * 24 * time.Hour, ageStale},
	}
	for _, tc := range cases {
		if got := ageClassOf(tc.since); got != tc.want {
			t.Fatalf("ageClassOf(%s) = %d, want %d", tc.since, got, tc.want)
		}
	}
	// Every bucket must map to a distinct decorative tone, or the ramp says
	// nothing.
	seen := map[string]struct{}{}
	for _, class := range []ageClass{ageFresh, ageRecent, ageOld, ageStale} {
		tn := class.tone()
		if !tn.decorative {
			t.Fatalf("age tone %q must be decorative", tn.key)
		}
		if _, dup := seen[tn.key]; dup {
			t.Fatalf("age buckets share tone %q", tn.key)
		}
		seen[tn.key] = struct{}{}
	}
}

// TestAgeIgnoresLastChangeBecauseDiscoveryRestampsIt guards the reason the ramp
// is built on CreatedAt. Discovery re-stamps LastChange with time.Now() on every
// scan, so a ramp reading that field would paint every session "fresh" forever.
// If someone rewires ageStamp to LastChange, this fails.
func TestAgeIgnoresLastChangeBecauseDiscoveryRestampsIt(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	// This is exactly what a discovery scan produces: an old session whose
	// LastChange was just refreshed.
	rescanned := adapter.Session{CreatedAt: now.Add(-10 * time.Hour), LastChange: now}
	if got := ansi.Strip(ageText(rescanned, now)); got != "10h" {
		t.Fatalf("age must come from CreatedAt, not a rescanned LastChange, got %q", got)
	}
	if got := ageClassFor(rescanned, now); got != ageRecent {
		t.Fatalf("a 10h-old session is recent, got %d", got)
	}
	fresh := adapter.Session{CreatedAt: now.Add(-2 * time.Minute)}
	if got := ansi.Strip(ageText(fresh, now)); got != "2m" {
		t.Fatalf("age = %q, want 2m", got)
	}
	if got := ageClassFor(fresh, now); got != ageFresh {
		t.Fatalf("a 2m-old session is fresh, got %d", got)
	}
	if got := ageClassFor(adapter.Session{CreatedAt: now.Add(-8 * 24 * time.Hour)}, now); got != ageStale {
		t.Fatalf("an 8-day-old session is stale, got %d", got)
	}
	// The tinted text must always equal what the plain age label produces.
	for _, sess := range []adapter.Session{rescanned, fresh, {}} {
		if got, want := ansi.Strip(ageText(sess, now)), sessionAge(sess.CreatedAt, now); got != want {
			t.Fatalf("tinted age %q disagrees with sessionAge %q", got, want)
		}
	}
	// A clock skew must not render a negative or nonsense interval.
	if got := ansi.Strip(ageText(adapter.Session{CreatedAt: now.Add(time.Hour)}, now)); got != "now" {
		t.Fatalf("a future timestamp should read as now, got %q", got)
	}
	if got := ansi.Strip(ageText(adapter.Session{}, now)); got != "now" {
		t.Fatalf("an empty record should read as now, got %q", got)
	}
}

func TestShortDurationStaysWithinThreeCells(t *testing.T) {
	for _, d := range []time.Duration{
		0, time.Second, time.Minute, 59 * time.Minute, time.Hour,
		47 * time.Hour, 48 * time.Hour, 400 * 24 * time.Hour,
	} {
		got := shortDuration(d)
		if got == "" {
			t.Fatalf("shortDuration(%s) is empty", d)
		}
		if d < 100*24*time.Hour && ansi.StringWidth(got) > 3 {
			t.Fatalf("shortDuration(%s) = %q, wider than 3 cells", d, got)
		}
	}
}

func TestResumeToneReflectsProviderSessionIDOnly(t *testing.T) {
	exact := resumeTone(adapter.Session{ProviderSessionID: "abc"})
	recent := resumeTone(adapter.Session{})
	if exact.key != toneResumeExact || recent.key != toneResumeRecent {
		t.Fatalf("resume tones = %q / %q", exact.key, recent.key)
	}
	if exact.glyph == recent.glyph {
		t.Fatal("exact and heuristic resume must be distinguishable without color")
	}
	// Whitespace is not an id.
	if got := resumeTone(adapter.Session{ProviderSessionID: "   "}); got.key != toneResumeRecent {
		t.Fatalf("a blank provider session id is not an exact resume, got %q", got.key)
	}
}

// TestHarnessIndependentRenderingIsIdenticalAcrossProviders is the deterministic
// -across-harnesses guarantee stated as a test: two sessions differing only in
// their provider name must produce byte-identical rows once that name is
// substituted. Nothing on the row may come from provider-specific behaviour.
func TestHarnessIndependentRenderingIsIdenticalAcrossProviders(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	base := adapter.Session{
		ID: "same-id", DisplayName: "same-name", Prompt: "same task",
		Cwd: "/work/same", ProcAlive: adapter.Alive,
		CreatedAt: now.Add(-time.Hour),
		PR:        &adapter.PRRef{Number: 7, Status: adapter.PROpen},
	}
	reference := ""
	for _, provider := range []string{"claude", "codex", "opencode", "omp"} {
		sess := base
		sess.AgentType = provider
		m := NewWithDeps(nil, nil)
		m.now = func() time.Time { return now }
		m.sessions = []adapter.Session{sess}
		m.width, m.height, m.sizeKnown = 100, 30, true

		rendered := ansi.Strip(strings.Join(entryLines(m.dashboardBodyEntries(100, 12), 100), "\n"))
		// Collapse runs of spaces: the right-aligned trailer legitimately shifts
		// by the length of the provider's name. Everything else must match.
		normalised := strings.Join(strings.Fields(strings.ReplaceAll(rendered, strings.ToUpper(provider), "<provider>")), " ")
		if reference == "" {
			reference = normalised
			continue
		}
		if normalised != reference {
			t.Fatalf("provider %q renders differently once its name is substituted:\n%s\n---\n%s",
				provider, normalised, reference)
		}
	}
}
