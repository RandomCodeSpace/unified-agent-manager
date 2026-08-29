package app

import (
	"strings"
)

// ─── terminal capabilities ────────────────────────────────────────────────────
//
// uam runs on the VPS; the terminal rendering it is whatever the user happens
// to SSH from — Windows Terminal today, Termius on a phone tomorrow, a JediTerm
// pane the day after. The binary is a constant; the client terminal is the
// variable, and it can change on every attach. So capabilities are measured per
// startup and never inferred from TERM, which every one of those terminals
// spells identically ("xterm-256color") while behaving differently.
//
// The renderer consumes exactly one decision from all of this: which glyph set
// is active. Everything else (color depth, theme) is handled by lipgloss's own
// negotiation; the probe only covers what nothing else measures.

// GlyphSet selects the vocabulary the tone table renders with.
type GlyphSet uint8

const (
	// GlyphsUnicode is the full set: ● ○ ✕ ▲ ▸ ▌ ⇄ and hairline rules.
	GlyphsUnicode GlyphSet = iota
	// GlyphsASCII is the degraded set for terminals without UTF-8 or whose
	// fonts draw East-Asian-Ambiguous glyphs two cells wide. Substitution is
	// wholesale rather than per-glyph: a mixed vocabulary is harder to read
	// than a plain one, and the failure is per-terminal, not per-glyph.
	GlyphsASCII
)

// TermCaps is the result of the startup probe. The zero value is the full
// Unicode experience, which keeps every existing test rendering exactly what it
// rendered before.
type TermCaps struct {
	Glyphs GlyphSet
	// AmbiguousWide records the measured width of an East-Asian-Ambiguous
	// glyph when the probe ran (0 = not probed, 1 = narrow, 2 = wide). It is
	// diagnostic — doctor reports it — while Glyphs carries the decision.
	AmbiguousWide int
	// UTF8 records whether the locale advertises UTF-8.
	UTF8 bool
	// Probed reports whether the CPR measurement actually ran (false when
	// stdout is not a TTY, the terminal never answered, or an override
	// short-circuited it).
	Probed bool
}

// activeCaps is set once at startup by ApplyTermCaps before the Bubble Tea
// program renders a frame, mirroring how the style variables are package-level.
// Tests that want the ASCII set call ApplyTermCaps explicitly and restore.
var activeCaps = TermCaps{Glyphs: GlyphsUnicode, UTF8: true}
var mouseReportingEnabled = true

// ApplyTermCaps installs the probe result. It returns the previous value so
// tests can restore it with defer.
func ApplyTermCaps(caps TermCaps) TermCaps {
	prev := activeCaps
	activeCaps = caps
	return prev
}

// CurrentTermCaps exposes the active capabilities (doctor reports them).
func CurrentTermCaps() TermCaps { return activeCaps }

func ApplyMouseReporting(enabled bool) bool {
	previous := mouseReportingEnabled
	mouseReportingEnabled = enabled
	return previous
}

func MouseReportingEnabled() bool { return mouseReportingEnabled }

func asciiGlyphs() bool { return activeCaps.Glyphs == GlyphsASCII }

// CapsFromEnvironment resolves the non-probe inputs: locale and overrides.
// The CPR measurement lives in the cli package (it needs the real TTY before
// Bubble Tea starts); this half is pure and unit-testable.
//
// Precedence: UAM_ASCII forces ASCII; UAM_WIDE=0 forces Unicode (trust the
// terminal); a non-UTF-8 locale forces ASCII; otherwise the measured width
// decides (2 → ASCII), defaulting to Unicode when unmeasured.
func CapsFromEnvironment(getenv func(string) string, measuredWidth int, probed bool) TermCaps {
	caps := TermCaps{UTF8: localeIsUTF8(getenv), AmbiguousWide: measuredWidth, Probed: probed}
	switch {
	case envTruthy(getenv("UAM_ASCII")):
		caps.Glyphs = GlyphsASCII
	case getenv("UAM_WIDE") == "0":
		caps.Glyphs = GlyphsUnicode
	case !caps.UTF8:
		caps.Glyphs = GlyphsASCII
	case measuredWidth == 2:
		caps.Glyphs = GlyphsASCII
	default:
		caps.Glyphs = GlyphsUnicode
	}
	return caps
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// localeIsUTF8 checks the POSIX locale chain. LC_ALL overrides LC_CTYPE
// overrides LANG; an empty chain is treated as UTF-8 because every modern
// distro defaults there and assuming ASCII would degrade good terminals to
// punish broken ones.
func localeIsUTF8(getenv func(string) string) bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			continue
		}
		upper := strings.ToUpper(value)
		return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
	}
	return true
}

// ProbeGlyph is the character the cli-side CPR measurement prints: it must be
// East-Asian-Ambiguous (the class that misrenders) and part of our vocabulary.
const ProbeGlyph = "●"

// ─── chrome vocabulary ────────────────────────────────────────────────────────
// Structural characters that are not tones still need an ASCII spelling, or the
// fallback would fix the marks and leave the frame itself broken.

func barGlyph() string {
	if asciiGlyphs() {
		return "|"
	}
	return "▌"
}

func caretGlyph() string {
	if asciiGlyphs() {
		return ">"
	}
	return "›"
}

func cursorGlyph() string {
	if asciiGlyphs() {
		return "|"
	}
	return "▏"
}

func ruleGlyph() string {
	if asciiGlyphs() {
		return "-"
	}
	return "─"
}

// truncTail is the ellipsis ansi.Truncate appends. U+2026 is ambiguous-width
// on some East Asian fonts, so the ASCII set uses a plain tilde.
func truncTail() string {
	if asciiGlyphs() {
		return "~"
	}
	return "…"
}

// hintEllipsis trails prose placeholders ("type a command…"). Prose has room
// for three dots, so the ASCII spelling reads naturally instead of borrowing
// truncTail's tilde.
func hintEllipsis() string {
	if asciiGlyphs() {
		return "..."
	}
	return "…"
}

// dotSep separates masthead and hint segments. U+00B7 is not renderable on a
// non-UTF-8 terminal, so the ASCII set falls back to a plain dash.
func dotSep() string {
	if asciiGlyphs() {
		return " - "
	}
	return " · "
}

// arrowsHint and enterHint spell the navigation keys in the footer.
func arrowsHint() string {
	if asciiGlyphs() {
		return "up/dn"
	}
	return "↑↓"
}

func enterHint() string {
	if asciiGlyphs() {
		return "enter"
	}
	return "⏎"
}
