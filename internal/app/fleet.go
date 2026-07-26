package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/charmbracelet/x/ansi"
)

// ─── harness-independent derivations ─────────────────────────────────────────
//
// Everything in this file is a pure function of adapter.Session plus an
// injected now. Nothing here reads provider output, touches the filesystem, or
// probes a binary, so a claude session, a codex session, an opencode session
// and an omp session all produce the same shapes from the same fields. That is
// the property that lets the dashboard show more per-session data without the
// display drifting apart between harnesses.

// ageClass buckets how long ago a session was created, so the age the row has
// always shown numerically also reads pre-attentively as a colour ramp.
//
// It measures from CreatedAt, deliberately NOT from LastChange. LastChange looks
// like the better field — "time since the state last changed" would answer "is
// this waiting on me" — but discovery re-stamps it with time.Now() on every scan
// (internal/adapter/agent.go, and internal/app/service.go for stopped records),
// so it is always approximately now. A ramp built on it would render every
// session as fresh and would be decoration that lies. Model.now's comment
// records the same constraint; anyone tempted to "improve" this by switching
// fields has to make LastChange meaningful in discovery first.
type ageClass uint8

const (
	ageFresh ageClass = iota
	ageRecent
	ageOld
	ageStale
)

const (
	ageRecentAfter = time.Hour
	ageOldAfter    = 24 * time.Hour
	ageStaleAfter  = 7 * 24 * time.Hour
)

// ageStamp is the timestamp a session's age is measured from.
func ageStamp(sess adapter.Session) time.Time { return sess.CreatedAt }

func ageClassFor(sess adapter.Session, now time.Time) ageClass {
	return ageClassOf(now.Sub(ageStamp(sess)))
}

func ageClassOf(since time.Duration) ageClass {
	switch {
	case since < ageRecentAfter:
		return ageFresh
	case since < ageOldAfter:
		return ageRecent
	case since < ageStaleAfter:
		return ageOld
	default:
		return ageStale
	}
}

// tone returns the decorative ramp entry for the bucket. It is decorative by
// construction: the duration text it tints always states the same fact.
func (a ageClass) tone() tone {
	switch a {
	case ageFresh:
		return toneOf(toneAgeFresh)
	case ageRecent:
		return toneOf(toneAgeRecent)
	case ageOld:
		return toneOf(toneAgeOld)
	default:
		return toneOf(toneAgeStale)
	}
}

// shortDuration renders an interval in at most three cells. Callers pin fixed
// column widths against it, so the vocabulary is deliberately closed:
// "now", "<n>m", "<n>h", "<n>d".
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// ageText is the session's age, tinted by its bucket. The text is identical to
// what sessionAge has always produced; only the colour is new.
func ageText(sess adapter.Session, now time.Time) string {
	stamp := ageStamp(sess)
	if stamp.IsZero() || stamp.After(now) {
		return toneOf(toneAgeFresh).render("now")
	}
	since := now.Sub(stamp)
	return ageClassOf(since).tone().render(shortDuration(since))
}

// resumeTone reports whether a stopped session can be resumed exactly or only
// heuristically. ProviderSessionID is recorded at dispatch for providers that
// let uam seed or learn it; without one, resume falls back to the provider's
// "most recent conversation in this cwd". Both branches are pure field reads —
// no binary probing, no stat — so this stays safe on the render path.
func resumeTone(sess adapter.Session) tone {
	if strings.TrimSpace(sess.ProviderSessionID) != "" {
		return toneOf(toneResumeExact)
	}
	return toneOf(toneResumeRecent)
}

// fleetVitals is the whole-roster summary rendered in the header. Every field
// is a count over adapter.Session, so it is identical across harnesses.
type fleetVitals struct {
	total   int
	live    int
	stopped int
	failed  int
	pinned  int
	withPR  int
}

func vitalsFor(sessions []adapter.Session) fleetVitals {
	var v fleetVitals
	v.total = len(sessions)
	for _, sess := range sessions {
		switch toneForSession(sess).key {
		case toneLive:
			v.live++
		case toneFailed:
			v.failed++
		default:
			v.stopped++
		}
		if sess.Pinned {
			v.pinned++
		}
		if sess.PR != nil {
			v.withPR++
		}
	}
	return v
}

// chipParts renders the vitals as glyph+count pairs, omitting zeroes so a
// healthy fleet stays quiet. The slice is ordered by how much the operator
// would miss each one, so a narrow header can drop from the tail.
func (v fleetVitals) chipParts() []string {
	parts := make([]string, 0, 5)
	add := func(key string, n int) {
		if n > 0 {
			parts = append(parts, toneOf(key).mark()+" "+hintStyle.Render(fmt.Sprintf("%d", n)))
		}
	}
	add(toneLive, v.live)
	add(toneFailed, v.failed)
	add(toneStopped, v.stopped)
	add(tonePinned, v.pinned)
	add(tonePROpen, v.withPR)
	return parts
}

func (v fleetVitals) chips() string { return strings.Join(v.chipParts(), "  ") }

// chipsWithin fits the vitals into budget cells by dropping the least
// operationally loaded category first. A phone header is where the fleet
// summary matters most, so degrading it beats omitting it — the previous
// all-or-nothing fit left the narrowest screen with no summary at all.
func (v fleetVitals) chipsWithin(budget int) string {
	parts := v.chipParts()
	for len(parts) > 0 {
		joined := strings.Join(parts, "  ")
		if ansi.StringWidth(joined) <= budget {
			return joined
		}
		parts = parts[:len(parts)-1]
	}
	return ""
}

// sectionVitals summarises one workspace group.
type sectionVitals struct {
	count  int
	live   int
	oldest time.Duration
}

func sectionVitalsFor(sessions []adapter.Session, now time.Time) sectionVitals {
	var v sectionVitals
	v.count = len(sessions)
	for _, sess := range sessions {
		if sess.ProcAlive == adapter.Alive {
			v.live++
		}
		if stamp := ageStamp(sess); !stamp.IsZero() && !stamp.After(now) {
			if since := now.Sub(stamp); since > v.oldest {
				v.oldest = since
			}
		}
	}
	return v
}

func (v sectionVitals) summary() string {
	parts := []string{hintStyle.Render(fmt.Sprintf("%d", v.count))}
	if v.live > 0 {
		parts = append(parts, toneOf(toneLive).mark()+" "+hintStyle.Render(fmt.Sprintf("%d", v.live)))
	}
	if v.oldest >= ageRecentAfter {
		parts = append(parts, ageClassOf(v.oldest).tone().render(shortDuration(v.oldest)))
	}
	return strings.Join(parts, " · ")
}

// ─── density ladder ──────────────────────────────────────────────────────────

// densityMin and densityMax bound the rungs. Three rungs is deliberate: each
// one adds a whole category of information (identity, then intent, then
// provenance) rather than a slightly longer line.
const (
	densityMin = 1
	densityMax = 3
)

// densityFor divides the body's line budget among the sessions that must fit in
// it, instead of giving every session one line and truncating. Three sessions
// on a 20-row phone can each afford name+task+cwd+profile+PR; thirty sessions
// on a wide desktop each get one dense line. It is the mechanism that makes
// "more data per session" and "usable at 40x20" true at the same time rather
// than a trade-off between them.
//
// overhead is the number of body lines the caller will spend on section
// headings and warnings — it must be computed before the density is chosen,
// because those lines are emitted after it and would otherwise silently push
// the last block off screen.
//
// When even one line per session does not fit, densityFor returns densityMin
// and the caller windows and scrolls: the fit invariant
// overhead + density*visibleCount <= budget holds whenever density 1 fits.
func densityFor(visibleCount, budget, overhead int) int {
	if visibleCount <= 0 {
		return densityMin
	}
	usable := budget - overhead
	if usable < visibleCount {
		return densityMin
	}
	per := usable / visibleCount
	if per > densityMax {
		per = densityMax
	}
	if per < densityMin {
		per = densityMin
	}
	return per
}

// densityFits reports whether the chosen density leaves the body within budget.
// Rendering asserts nothing; this exists so the tests can state the invariant
// once and sweep it.
func densityFits(visibleCount, budget, overhead, density int) bool {
	return overhead+density*visibleCount <= budget
}
