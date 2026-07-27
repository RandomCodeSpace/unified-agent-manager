package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
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

// sessionAge is the bare age text: how long ago the session was created,
// spelled by shortDuration. ageText is the tinted form the board renders.
func sessionAge(createdAt, now time.Time) string {
	if createdAt.IsZero() || createdAt.After(now) {
		return "now"
	}
	return shortDuration(now.Sub(createdAt))
}
