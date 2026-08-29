package app

import (
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestPaletteClearsContrastOnLightAndDarkTerminals(t *testing.T) {
	white := lipgloss.Color("#FFFFFF")
	black := lipgloss.Color("#000000")
	foregrounds := map[string]compat.AdaptiveColor{
		"accent":  accentColor,
		"text":    textColor,
		"muted":   mutedColor,
		"divider": dividerColor,
		"task":    taskColor,
		"live":    liveColor,
		"fail":    failColor,
		"warn":    warnColor,
		"pin":     pinColor,
		"pr":      prColor,
		"idle":    idleColor,
	}
	for name, adaptive := range foregrounds {
		if ratio := contrastRatio(adaptive.Light, white); ratio < 4.5 {
			t.Errorf("%s light contrast = %.2f:1, want at least 4.5:1", name, ratio)
		}
		if ratio := contrastRatio(adaptive.Dark, black); ratio < 4.5 {
			t.Errorf("%s dark contrast = %.2f:1, want at least 4.5:1", name, ratio)
		}
	}
	if ratio := contrastRatio(actionTextColor.Light, accentColor.Light); ratio < 4.5 {
		t.Errorf("light action contrast = %.2f:1, want at least 4.5:1", ratio)
	}
	if ratio := contrastRatio(actionTextColor.Dark, accentColor.Dark); ratio < 4.5 {
		t.Errorf("dark action contrast = %.2f:1, want at least 4.5:1", ratio)
	}
	if ratio := contrastRatio(actionTextColor.Light, dividerColor.Light); ratio < 4.5 {
		t.Errorf("light provider label contrast = %.2f:1, want at least 4.5:1", ratio)
	}
	if ratio := contrastRatio(actionTextColor.Dark, dividerColor.Dark); ratio < 4.5 {
		t.Errorf("dark provider label contrast = %.2f:1, want at least 4.5:1", ratio)
	}
	if ratio := contrastRatio(actionTextColor.Light, failColor.Light); ratio < 4.5 {
		t.Errorf("light destructive button contrast = %.2f:1, want at least 4.5:1", ratio)
	}
	if ratio := contrastRatio(actionTextColor.Dark, failColor.Dark); ratio < 4.5 {
		t.Errorf("dark destructive button contrast = %.2f:1, want at least 4.5:1", ratio)
	}
}

func TestInitRequestsTerminalBackgroundColor(t *testing.T) {
	batch, ok := NewWithDeps(nil, nil).Init()().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init command = %T, want tea.BatchMsg", NewWithDeps(nil, nil).Init()())
	}
	want := reflect.ValueOf(tea.RequestBackgroundColor).Pointer()
	for _, cmd := range batch {
		if reflect.ValueOf(cmd).Pointer() == want {
			return
		}
	}
	t.Fatal("Init does not request the terminal background color")
}

func TestBackgroundColorEventSwitchesAdaptivePalette(t *testing.T) {
	previous := compat.HasDarkBackground
	t.Cleanup(func() { compat.HasDarkBackground = previous })

	m := NewWithDeps(nil, nil)
	light, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#FFFFFF")})
	m = light.(Model)
	if m.darkBackground || compat.HasDarkBackground {
		t.Fatal("white terminal background did not select the light palette")
	}
	dark, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
	m = dark.(Model)
	if !m.darkBackground || !compat.HasDarkBackground {
		t.Fatal("black terminal background did not select the dark palette")
	}
}

func contrastRatio(a, b color.Color) float64 {
	left, right := relativeLuminance(a), relativeLuminance(b)
	if left < right {
		left, right = right, left
	}
	return (left + 0.05) / (right + 0.05)
}

func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	linear := func(component uint32) float64 {
		value := float64(component) / 65535
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// TestToneTableNeverEncodesADatumInHueAlone is the compiled form of the rule
// the palette expansion rests on: every tone that carries meaning owns a
// distinct one-cell glyph, and every tone that only reinforces adjacent text
// owns none. A terminal that loses color must lose nothing else.
func TestToneTableNeverEncodesADatumInHueAlone(t *testing.T) {
	if len(tones) == 0 {
		t.Fatal("tone table is empty")
	}
	seenKey := map[string]string{}
	seenGlyph := map[string]string{}
	for _, tn := range tones {
		if tn.key == "" {
			t.Fatalf("tone with empty key: %+v", tn)
		}
		if prior, dup := seenKey[tn.key]; dup {
			t.Fatalf("duplicate tone key %q (also %q)", tn.key, prior)
		}
		seenKey[tn.key] = tn.key

		if tn.decorative {
			if tn.glyph != "" {
				t.Fatalf("decorative tone %q must not own a glyph (it would become a second, "+
					"unsynchronised carrier of the datum its text already states), got %q", tn.key, tn.glyph)
			}
			continue
		}

		if tn.glyph == "" {
			t.Fatalf("non-decorative tone %q has no glyph, so its datum is carried by hue alone", tn.key)
		}
		// A two-cell glyph silently shifts every fixed-width slot to its right,
		// which is how a dashboard that renders correctly on the author's
		// terminal turns to confetti on the user's.
		if w := ansi.StringWidth(tn.glyph); w != 1 {
			t.Fatalf("tone %q glyph %q occupies %d cells, want exactly 1", tn.key, tn.glyph, w)
		}
		if prior, dup := seenGlyph[tn.glyph]; dup {
			t.Fatalf("tones %q and %q share glyph %q, so they are indistinguishable without color",
				tn.key, prior, tn.glyph)
		}
		seenGlyph[tn.glyph] = tn.key
	}
}

// TestASCIIColumnKeepsEveryGlyphInvariant sweeps the degraded set with the
// exact rules the Unicode set lives under, plus one of its own: the spelling
// must be pure ASCII, because the whole point of the column is a terminal that
// cannot render anything else.
func TestASCIIColumnKeepsEveryGlyphInvariant(t *testing.T) {
	seen := map[string]string{}
	for _, tn := range tones {
		if tn.decorative {
			if tn.ascii != "" {
				t.Fatalf("decorative tone %q must not own an ascii glyph, got %q", tn.key, tn.ascii)
			}
			continue
		}
		if tn.ascii == "" {
			t.Fatalf("tone %q has no ascii spelling, so a degraded terminal loses its datum", tn.key)
		}
		if len(tn.ascii) != 1 || tn.ascii[0] > 127 {
			t.Fatalf("tone %q ascii spelling %q must be a single ASCII byte", tn.key, tn.ascii)
		}
		if prior, dup := seen[tn.ascii]; dup {
			t.Fatalf("tones %q and %q share ascii glyph %q", tn.key, prior, tn.ascii)
		}
		seen[tn.ascii] = tn.key
	}
}

// TestGlyphSetSubstitutionIsWholesale pins that installing the ASCII caps
// switches every mark at once: a mixed vocabulary is harder to read than a
// plain one.
func TestGlyphSetSubstitutionIsWholesale(t *testing.T) {
	prev := ApplyTermCaps(TermCaps{Glyphs: GlyphsASCII})
	defer ApplyTermCaps(prev)
	for _, tn := range tones {
		if tn.decorative {
			continue
		}
		if got := tn.activeGlyph(); got != tn.ascii {
			t.Fatalf("tone %q renders %q under the ASCII set, want %q", tn.key, got, tn.ascii)
		}
		if plain := ansi.Strip(tn.mark()); plain != tn.ascii {
			t.Fatalf("tone %q mark stripped to %q under the ASCII set, want %q", tn.key, plain, tn.ascii)
		}
	}
}

func TestToneColorsAreAdaptiveAndCarryNoBackground(t *testing.T) {
	for _, tn := range tones {
		style := tn.style()
		if _, ok := style.GetForeground().(compat.AdaptiveColor); !ok {
			t.Fatalf("tone %q foreground should adapt to light/dark terminals, got %T", tn.key, style.GetForeground())
		}
		// A background fill cannot be truncated safely: ansi.Truncate can clip a
		// styled run before its reset and bleed the fill across the rest of the
		// frame. Selection is an accent edge bar, never a filled row.
		if _, ok := style.GetBackground().(lipgloss.NoColor); !ok {
			t.Fatalf("tone %q must not paint a background, got %T", tn.key, style.GetBackground())
		}
	}
}

func TestToneForSessionMatchesFailureExitDetail(t *testing.T) {
	exit1, exit0, signal := 1, 0, -1
	cases := []struct {
		name string
		sess adapter.Session
		want string
	}{
		{"alive", adapter.Session{ProcAlive: adapter.Alive}, toneLive},
		{"alive despite a stale exit code", adapter.Session{ProcAlive: adapter.Alive, ExitCode: &exit1}, toneLive},
		{"failed", adapter.Session{ProcAlive: adapter.Exited, ExitCode: &exit1}, toneFailed},
		{"clean exit", adapter.Session{ProcAlive: adapter.Exited, ExitCode: &exit0}, toneStopped},
		{"explicit stop is not a failure", adapter.Session{ProcAlive: adapter.Exited, ExitCode: &signal, Closed: true}, toneStopped},
		{"no exit recorded", adapter.Session{ProcAlive: adapter.Exited}, toneStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toneForSession(tc.sess).key; got != tc.want {
				t.Fatalf("toneForSession = %q, want %q", got, tc.want)
			}
			// The glyph and the task-line text must agree about failure.
			failed := failureExitDetail(tc.sess) != ""
			if failed != (tc.want == toneFailed) {
				t.Fatalf("failureExitDetail disagrees with tone %q", tc.want)
			}
		})
	}
}

func TestToneForPRCoversEveryStatusDistinctly(t *testing.T) {
	statuses := []adapter.PRStatus{adapter.PROpen, adapter.PRMerged, adapter.PRDraft, adapter.PRClosed}
	seen := map[string]adapter.PRStatus{}
	for _, status := range statuses {
		tn := toneForPR(status)
		if tn.glyph == "" {
			t.Fatalf("PR status %v has no glyph", status)
		}
		if prior, dup := seen[tn.glyph]; dup {
			t.Fatalf("PR statuses %v and %v share glyph %q", status, prior, tn.glyph)
		}
		seen[tn.glyph] = status
	}
}

// TestSessionGlyphComesFromTheToneTable pins the single-vocabulary property:
// the legacy row renderer and the dashboard must not drift apart.
func TestSessionGlyphComesFromTheToneTable(t *testing.T) {
	exit1 := 1
	for _, sess := range []adapter.Session{
		{ProcAlive: adapter.Alive},
		{ProcAlive: adapter.Exited, ExitCode: &exit1},
		{ProcAlive: adapter.Exited},
	} {
		glyph, _ := sessionGlyph(sess)
		if want := toneForSession(sess).glyph; glyph != want {
			t.Fatalf("sessionGlyph = %q, tone table says %q", glyph, want)
		}
	}
}

// TestToneMarksSurviveAColorlessProfile is the end of the argument: strip color
// entirely and every semantic mark is still present and still distinct.
func TestToneMarksSurviveAColorlessProfile(t *testing.T) {
	seen := map[string]string{}
	for _, tn := range tones {
		if tn.decorative {
			continue
		}
		plain := ansi.Strip(tn.mark())
		if plain != tn.glyph {
			t.Fatalf("tone %q stripped to %q, want its bare glyph %q", tn.key, plain, tn.glyph)
		}
		if prior, dup := seen[plain]; dup {
			t.Fatalf("tones %q and %q are identical without color (%q)", tn.key, prior, plain)
		}
		seen[plain] = tn.key
	}
	if len(seen) < 10 {
		t.Fatalf("expected a rich mark vocabulary, got %d distinct marks", len(seen))
	}
}

func TestDecorativeTonesRenderTextUnchangedWhenStripped(t *testing.T) {
	for _, key := range []string{toneAgeFresh, toneAgeRecent, toneAgeOld, toneAgeStale} {
		tn := toneOf(key)
		if !tn.decorative {
			t.Fatalf("tone %q should be decorative", key)
		}
		if got := ansi.Strip(tn.render("14m")); got != "14m" {
			t.Fatalf("decorative tone %q altered its text: %q", key, got)
		}
		if strings.TrimSpace(tn.mark()) != "" {
			t.Fatalf("decorative tone %q emitted a mark", key)
		}
	}
}
