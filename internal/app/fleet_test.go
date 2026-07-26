package app

import (
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/charmbracelet/x/ansi"
)

func TestDensityForPicksARungAndAlwaysFits(t *testing.T) {
	cases := []struct {
		name                              string
		visible, budget, overhead, expect int
	}{
		{"a small roster on a phone gets the full ladder", 3, 16, 1, 3},
		{"a medium roster gets two lines each", 7, 20, 2, 2},
		{"a large roster collapses to one dense line", 30, 26, 0, 1},
		{"exactly one line each", 26, 26, 0, 1},
		{"more sessions than rows still returns a rung", 100, 10, 0, 1},
		{"overhead is charged before the division", 5, 16, 6, 2},
		{"an empty roster is well defined", 0, 20, 0, 1},
		{"a zero budget is well defined", 4, 0, 0, 1},
		{"the ladder is capped", 1, 400, 0, densityMax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := densityFor(tc.visible, tc.budget, tc.overhead); got != tc.expect {
				t.Fatalf("densityFor(%d, %d, %d) = %d, want %d", tc.visible, tc.budget, tc.overhead, got, tc.expect)
			}
		})
	}
}

// TestDensityForFitInvariantOverASweep is the property the whole ladder rests
// on: whenever one line per session would fit, the chosen density fits too. A
// density that overflows would amputate a block's last line, which reads as a
// rendering fault rather than as scrolling.
func TestDensityForFitInvariantOverASweep(t *testing.T) {
	for visible := 1; visible <= 40; visible++ {
		for budget := 0; budget <= 60; budget++ {
			for overhead := 0; overhead <= 6; overhead++ {
				density := densityFor(visible, budget, overhead)
				if density < densityMin || density > densityMax {
					t.Fatalf("densityFor(%d,%d,%d) = %d, outside [%d,%d]",
						visible, budget, overhead, density, densityMin, densityMax)
				}
				onePerSessionFits := overhead+visible <= budget
				if onePerSessionFits && !densityFits(visible, budget, overhead, density) {
					t.Fatalf("densityFor(%d,%d,%d) = %d overflows a budget that fits at density 1",
						visible, budget, overhead, density)
				}
			}
		}
	}
}

func TestDensityForIsMonotonicInRosterSize(t *testing.T) {
	for budget := 4; budget <= 60; budget++ {
		previous := densityMax + 1
		for visible := 1; visible <= 40; visible++ {
			density := densityFor(visible, budget, 0)
			if density > previous {
				t.Fatalf("density rose from %d to %d as the roster grew to %d at budget %d",
					previous, density, visible, budget)
			}
			previous = density
		}
	}
}

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

func TestVitalsCountEveryLifecycleExactlyOnce(t *testing.T) {
	exit1, exit0 := 1, 0
	sessions := []adapter.Session{
		{ProcAlive: adapter.Alive},
		{ProcAlive: adapter.Alive, Pinned: true, PR: &adapter.PRRef{Number: 1, Status: adapter.PROpen}},
		{ProcAlive: adapter.Exited, ExitCode: &exit1},
		{ProcAlive: adapter.Exited, ExitCode: &exit0},
		{ProcAlive: adapter.Exited},
	}
	v := vitalsFor(sessions)
	if v.total != 5 || v.live != 2 || v.failed != 1 || v.stopped != 2 {
		t.Fatalf("vitals = %+v", v)
	}
	if v.live+v.failed+v.stopped != v.total {
		t.Fatalf("lifecycle buckets must partition the roster: %+v", v)
	}
	if v.pinned != 1 || v.withPR != 1 {
		t.Fatalf("pin and PR counts = %+v", v)
	}
	if v := vitalsFor(nil); v.chips() != "" {
		t.Fatalf("an empty roster should render no chips, got %q", v.chips())
	}
}

// TestVitalsDegradeInsteadOfVanishing is the mobile-header property: a narrow
// screen drops the least loaded category rather than the whole summary.
func TestVitalsDegradeInsteadOfVanishing(t *testing.T) {
	exit1 := 1
	v := vitalsFor([]adapter.Session{
		{ProcAlive: adapter.Alive},
		{ProcAlive: adapter.Exited, ExitCode: &exit1},
		{ProcAlive: adapter.Exited},
		{ProcAlive: adapter.Alive, Pinned: true},
		{ProcAlive: adapter.Alive, PR: &adapter.PRRef{Number: 2, Status: adapter.PRMerged}},
	})
	full := v.chipsWithin(200)
	if ansi.Strip(full) == "" {
		t.Fatal("a wide header should show every category")
	}
	previous := len(v.chipParts())
	for budget := 200; budget >= 0; budget-- {
		chips := v.chipsWithin(budget)
		if w := ansi.StringWidth(chips); w > budget {
			t.Fatalf("chipsWithin(%d) returned %d cells: %q", budget, w, ansi.Strip(chips))
		}
		count := 0
		if strings.TrimSpace(chips) != "" {
			count = strings.Count(ansi.Strip(chips), "  ") + 1
		}
		if count > previous {
			t.Fatalf("chip count rose from %d to %d as the budget shrank to %d", previous, count, budget)
		}
		previous = count
		// The live count is the last thing to go, so any non-empty rendering
		// must still carry it.
		if chips != "" && !strings.Contains(ansi.Strip(chips), toneOf(toneLive).glyph) {
			t.Fatalf("chipsWithin(%d) dropped the live count first: %q", budget, ansi.Strip(chips))
		}
	}
}

func TestSectionVitalsSummariseTheGroupWithoutIO(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	group := []adapter.Session{
		{ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Minute)},
		{ProcAlive: adapter.Alive, CreatedAt: now.Add(-30 * time.Minute)},
		{ProcAlive: adapter.Exited, CreatedAt: now.Add(-5 * time.Hour)},
	}
	v := sectionVitalsFor(group, now)
	if v.count != 3 || v.live != 2 {
		t.Fatalf("section vitals = %+v", v)
	}
	if v.oldest != 5*time.Hour {
		t.Fatalf("oldest age = %s, want 5h", v.oldest)
	}
	summary := ansi.Strip(v.summary())
	if !strings.Contains(summary, "3") || !strings.Contains(summary, "5h") {
		t.Fatalf("summary should carry the count and the oldest age, got %q", summary)
	}
	// A fresh group must not be labelled old.
	fresh := sectionVitalsFor([]adapter.Session{{ProcAlive: adapter.Alive, CreatedAt: now}}, now)
	if strings.Contains(ansi.Strip(fresh.summary()), "h") {
		t.Fatalf("a fresh group should not report an age, got %q", ansi.Strip(fresh.summary()))
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

		rendered := ansi.Strip(strings.Join(entryLines(m.sessionPanelEntries(100, 12), 100), "\n"))
		// Collapse runs of spaces: the right-aligned trailer legitimately shifts
		// by the length of the provider's name. Everything else must match.
		normalised := strings.Join(strings.Fields(strings.ReplaceAll(rendered, provider, "<provider>")), " ")
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
