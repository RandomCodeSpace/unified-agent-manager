package app

import "testing"

func envOf(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// TestCapsFromEnvironmentPrecedence pins the resolution order: explicit
// overrides beat the locale, the locale beats the measurement, and the
// measurement beats the default. Each case flips exactly one input against the
// one below it in the chain.
func TestCapsFromEnvironmentPrecedence(t *testing.T) {
	utf8 := map[string]string{"LANG": "en_US.UTF-8"}
	cases := []struct {
		name    string
		env     map[string]string
		width   int
		probed  bool
		want    GlyphSet
		wantUTF bool
	}{
		{"default is unicode", utf8, 0, false, GlyphsUnicode, true},
		{"measured narrow stays unicode", utf8, 1, true, GlyphsUnicode, true},
		{"measured wide degrades", utf8, 2, true, GlyphsASCII, true},
		{"non-utf8 locale degrades without a probe", map[string]string{"LANG": "en_US.ISO-8859-1"}, 0, false, GlyphsASCII, false},
		{"UAM_WIDE=0 trusts the terminal over the measurement",
			map[string]string{"LANG": "en_US.UTF-8", "UAM_WIDE": "0"}, 2, true, GlyphsUnicode, true},
		{"UAM_ASCII beats everything",
			map[string]string{"LANG": "en_US.UTF-8", "UAM_ASCII": "1", "UAM_WIDE": "0"}, 1, true, GlyphsASCII, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caps := CapsFromEnvironment(envOf(tc.env), tc.width, tc.probed)
			if caps.Glyphs != tc.want {
				t.Fatalf("Glyphs = %v, want %v", caps.Glyphs, tc.want)
			}
			if caps.UTF8 != tc.wantUTF {
				t.Fatalf("UTF8 = %v, want %v", caps.UTF8, tc.wantUTF)
			}
			if caps.AmbiguousWide != tc.width || caps.Probed != tc.probed {
				t.Fatalf("diagnostics not preserved: %+v", caps)
			}
		})
	}
}

func TestLocaleIsUTF8FollowsThePOSIXChain(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"empty chain assumes utf8", nil, true},
		{"LANG utf8", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"LANG utf8 without hyphen", map[string]string{"LANG": "C.utf8"}, true},
		{"LANG legacy", map[string]string{"LANG": "en_US.ISO-8859-1"}, false},
		{"plain C locale", map[string]string{"LANG": "C"}, false},
		{"LC_CTYPE overrides LANG", map[string]string{"LANG": "en_US.UTF-8", "LC_CTYPE": "POSIX"}, false},
		{"LC_ALL overrides LC_CTYPE", map[string]string{"LC_CTYPE": "POSIX", "LC_ALL": "en_US.UTF-8"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localeIsUTF8(envOf(tc.env)); got != tc.want {
				t.Fatalf("localeIsUTF8 = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnvTruthy(t *testing.T) {
	for value, want := range map[string]bool{
		"": false, "0": false, "false": false, "no": false, "No": false,
		"1": true, "true": true, "yes": true, "anything": true,
	} {
		if got := envTruthy(value); got != want {
			t.Fatalf("envTruthy(%q) = %v, want %v", value, got, want)
		}
	}
}

// TestApplyTermCapsRoundTrips guards the restore idiom every glyph-set test
// depends on: install, read back, restore, and the previous value survives.
func TestApplyTermCapsRoundTrips(t *testing.T) {
	original := CurrentTermCaps()
	prev := ApplyTermCaps(TermCaps{Glyphs: GlyphsASCII, AmbiguousWide: 2, Probed: true})
	defer ApplyTermCaps(prev)
	if prev != original {
		t.Fatalf("ApplyTermCaps returned %+v, want the prior %+v", prev, original)
	}
	if got := CurrentTermCaps(); got.Glyphs != GlyphsASCII || !got.Probed {
		t.Fatalf("CurrentTermCaps = %+v after install", got)
	}
	if !asciiGlyphs() {
		t.Fatal("asciiGlyphs should report true under the ASCII set")
	}
}

// TestChromeVocabularySwitchesWholesale sweeps the structural glyphs: every
// helper must answer in both sets, the ASCII answer must be pure ASCII, and
// both spellings must be one cell wide — except hintEllipsis, which trails
// prose and owns its three dots.
func TestChromeVocabularySwitchesWholesale(t *testing.T) {
	helpers := []struct {
		name string
		fn   func() string
	}{
		{"bar", barGlyph},
		{"caret", caretGlyph},
		{"cursor", cursorGlyph},
		{"rule", ruleGlyph},
		{"truncTail", truncTail},
	}
	prev := ApplyTermCaps(TermCaps{Glyphs: GlyphsUnicode, UTF8: true})
	defer ApplyTermCaps(prev)
	unicodeSpelling := map[string]string{}
	for _, h := range helpers {
		got := h.fn()
		if got == "" || len([]rune(got)) != 1 {
			t.Fatalf("%s unicode spelling %q must be a single rune", h.name, got)
		}
		unicodeSpelling[h.name] = got
	}
	ApplyTermCaps(TermCaps{Glyphs: GlyphsASCII})
	for _, h := range helpers {
		got := h.fn()
		if got == "" || len(got) != 1 || got[0] > 127 {
			t.Fatalf("%s ascii spelling %q must be a single ASCII byte", h.name, got)
		}
		if got == unicodeSpelling[h.name] && h.name != "truncTail" {
			t.Fatalf("%s did not degrade: still %q under the ASCII set", h.name, got)
		}
	}
	if got := hintEllipsis(); got != "..." {
		t.Fatalf("hintEllipsis ascii = %q, want %q", got, "...")
	}
	ApplyTermCaps(TermCaps{Glyphs: GlyphsUnicode})
	if got := hintEllipsis(); got != "…" {
		t.Fatalf("hintEllipsis unicode = %q, want %q", got, "…")
	}
}

func TestProbeGlyphIsPartOfTheVocabulary(t *testing.T) {
	// The probe must measure a glyph the dashboard actually renders; measuring
	// anything else would answer a question nobody asked.
	if ProbeGlyph != toneOf(toneLive).glyph {
		t.Fatalf("ProbeGlyph %q is not the live mark %q", ProbeGlyph, toneOf(toneLive).glyph)
	}
}
