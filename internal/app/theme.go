package app

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/charmbracelet/lipgloss"
)

// ─── tones ───────────────────────────────────────────────────────────────────
//
// A tone is the dashboard's unit of semantic styling: a color paired with the
// glyph and text attributes that carry the same meaning when the color is gone.
// Encoding a datum in hue alone breaks on monochrome terminals, on a NO_COLOR
// run, on an 8-color palette that collapses fail into warn, and for anyone who
// cannot separate those hues — so the rule is enforced rather than reviewed:
//
//	a non-decorative tone MUST own a unique one-cell glyph;
//	a decorative tone MUST have no glyph, because adjacent text already
//	carries its datum and the color is pure reinforcement.
//
// theme_test.go asserts both halves over the whole table, which is what lets
// the palette grow without any single addition quietly becoming color-only.

type tone struct {
	key   string
	color lipgloss.AdaptiveColor
	glyph string
	// ascii is the degraded spelling used when the terminal probe selects
	// GlyphsASCII — a non-UTF-8 locale, or a font that draws the Unicode set
	// two cells wide. Same invariants as glyph: one cell, pairwise distinct.
	ascii      string
	bold       bool
	faint      bool
	decorative bool
}

func (t tone) style() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.color).Bold(t.bold).Faint(t.faint)
}

// activeGlyph resolves the glyph for the terminal's active set.
func (t tone) activeGlyph() string {
	if asciiGlyphs() && t.glyph != "" {
		return t.ascii
	}
	return t.glyph
}

// mark renders the tone's glyph. Decorative tones have none, so mark is empty
// and callers can concatenate it unconditionally.
func (t tone) mark() string {
	glyph := t.activeGlyph()
	if glyph == "" {
		return ""
	}
	return t.style().Render(glyph)
}

func (t tone) render(text string) string { return t.style().Render(text) }

var (
	accentColor  = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#2DD4BF"}
	textColor    = lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#E8EDF4"}
	mutedColor   = lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#8B97AC"}
	dividerColor = lipgloss.AdaptiveColor{Light: "#D6DEE8", Dark: "#2B3547"}
	taskColor    = lipgloss.AdaptiveColor{Light: "#475569", Dark: "#AEBACD"}
	liveColor    = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	failColor    = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"}
	warnColor    = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	// pinColor and prColor widen the palette without widening its meaning: a
	// pin is a user intent rather than a health state, and pull-request data
	// comes from outside the session, so neither may reuse a lifecycle hue.
	pinColor = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FCD34D"}
	prColor  = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
	// idleColor is the cool stop on the age ramp — a session that has been up
	// for a few hours is not a warning yet.
	idleColor = lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#38BDF8"}
)

const (
	toneLive         = "live"
	toneStopped      = "stopped"
	toneFailed       = "failed"
	toneWarn         = "warn"
	tonePinned       = "pinned"
	toneWorkspace    = "workspace"
	toneSelected     = "selected"
	tonePROpen       = "pr-open"
	tonePRMerged     = "pr-merged"
	tonePRDraft      = "pr-draft"
	tonePRClosed     = "pr-closed"
	toneResumeExact  = "resume-exact"
	toneResumeRecent = "resume-recent"
	toneAgeFresh     = "age-fresh"
	toneAgeRecent    = "age-recent"
	toneAgeOld       = "age-old"
	toneAgeStale     = "age-stale"
)

// tones is ordered so theme_test.go can report a stable first offender and so
// the doctor surface can print the legend in a fixed order. The PR tones remain
// in the table for the doctor legend and `uam ls` even though the dashboard no
// longer renders forge state (a provider-coupled datum by the user's call).
var tones = []tone{
	{key: toneLive, color: liveColor, glyph: "●", ascii: "*", bold: true},
	{key: toneStopped, color: mutedColor, glyph: "○", ascii: ".", faint: true},
	{key: toneFailed, color: failColor, glyph: "✕", ascii: "x", bold: true},
	{key: toneWarn, color: warnColor, glyph: "▲", ascii: "!"},
	{key: tonePinned, color: pinColor, glyph: "★", ascii: "+"},
	{key: toneWorkspace, color: mutedColor, glyph: "▸", ascii: ">", bold: true},
	{key: toneSelected, color: accentColor, glyph: "▌", ascii: "|", bold: true},
	{key: tonePROpen, color: mutedColor, glyph: "◇", ascii: "o"},
	{key: tonePRMerged, color: prColor, glyph: "◆", ascii: "@"},
	{key: tonePRDraft, color: warnColor, glyph: "◐", ascii: "d"},
	{key: tonePRClosed, color: failColor, glyph: "⊘", ascii: "#"},
	{key: toneResumeExact, color: accentColor, glyph: "⇄", ascii: "="},
	{key: toneResumeRecent, color: mutedColor, glyph: "~", ascii: "~"},
	// Age is a ramp over an interval, and an interval has no natural glyph
	// vocabulary — so it tints the age text it sits on and never replaces it.
	{key: toneAgeFresh, color: liveColor, decorative: true},
	{key: toneAgeRecent, color: idleColor, decorative: true},
	{key: toneAgeOld, color: warnColor, decorative: true},
	{key: toneAgeStale, color: mutedColor, decorative: true},
}

var toneByKey = func() map[string]tone {
	byKey := make(map[string]tone, len(tones))
	for _, t := range tones {
		byKey[t.key] = t
	}
	return byKey
}()

func toneOf(key string) tone { return toneByKey[key] }

// toneForSession maps a session's lifecycle onto a tone using exactly the
// distinctions failureExitDetail already draws, so the glyph and the task-line
// text can never disagree about whether a session failed.
func toneForSession(sess adapter.Session) tone {
	switch {
	case sess.ProcAlive == adapter.Alive:
		return toneOf(toneLive)
	case failureExitDetail(sess) != "":
		return toneOf(toneFailed)
	default:
		return toneOf(toneStopped)
	}
}

func toneForPR(status adapter.PRStatus) tone {
	switch status {
	case adapter.PRMerged:
		return toneOf(tonePRMerged)
	case adapter.PRDraft:
		return toneOf(tonePRDraft)
	case adapter.PRClosed:
		return toneOf(tonePRClosed)
	default:
		return toneOf(tonePROpen)
	}
}

// ─── derived styles ──────────────────────────────────────────────────────────
// Text styles stay separate from the tone table: they carry no datum of their
// own, so the glyph-distinctness invariant does not apply to them.

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(textColor)
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(mutedColor)
	hintStyle     = lipgloss.NewStyle().Foreground(mutedColor)
	dividerStyle  = lipgloss.NewStyle().Foreground(dividerColor)
	taskStyle     = lipgloss.NewStyle().Foreground(taskColor)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	warnStyle     = toneOf(toneWarn).style()

	liveGlyphStyle = toneOf(toneLive).style()
	failGlyphStyle = toneOf(toneFailed).style()
)

// bar is the accent rule that marks the brand and command lines.
func bar() string { return brandStyle.Render(barGlyph()) }
