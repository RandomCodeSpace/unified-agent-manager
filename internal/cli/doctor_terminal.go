package cli

import (
	"os"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
)

// ─── doctor: the terminal section ─────────────────────────────────────────────
//
// The host half of doctor never changes between attaches; the terminal half
// changes on every one, because the terminal is whatever the user happens to
// be SSHing from. This section runs the same measurement RunTUI runs and
// reports every input to the glyph-set decision, so "the dashboard looks
// wrong from this client" can be diagnosed from the client in question.

type terminalDoctorReport struct {
	Term      string `json:"term"`
	Colorterm string `json:"colorterm,omitempty"`
	Locale    string `json:"locale,omitempty"`
	UTF8      bool   `json:"utf8"`
	// Probed reports whether the CPR measurement ran and was answered; when
	// false AmbiguousWidth is meaningless and omitted.
	Probed         bool   `json:"probed"`
	AmbiguousWidth int    `json:"ambiguous_width,omitempty"`
	Glyphs         string `json:"glyphs"`
	Override       string `json:"override,omitempty"`
}

func doctorTerminal() terminalDoctorReport {
	caps := probeTermCaps()
	report := terminalDoctorReport{
		Term:           os.Getenv("TERM"),
		Colorterm:      os.Getenv("COLORTERM"),
		Locale:         doctorLocale(),
		UTF8:           caps.UTF8,
		Probed:         caps.Probed,
		AmbiguousWidth: caps.AmbiguousWide,
		Glyphs:         "unicode",
	}
	if caps.Glyphs == app.GlyphsASCII {
		report.Glyphs = "ascii"
	}
	switch {
	case os.Getenv("UAM_ASCII") != "":
		report.Override = "UAM_ASCII=" + os.Getenv("UAM_ASCII")
	case os.Getenv("UAM_WIDE") == "0":
		report.Override = "UAM_WIDE=0"
	}
	return report
}

// doctorLocale reports the locale entry the glyph decision actually read:
// LC_ALL beats LC_CTYPE beats LANG, matching the POSIX resolution order.
func doctorLocale() string {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return key + "=" + value
		}
	}
	return ""
}

func (r terminalDoctorReport) line() string {
	fields := []string{"terminal", "glyphs=" + r.Glyphs}
	if r.Probed {
		fields = append(fields, "ambiguous_width="+map[int]string{1: "1", 2: "2"}[r.AmbiguousWidth])
	} else {
		fields = append(fields, "ambiguous_width=unmeasured")
	}
	utf8State := "utf8=yes"
	if !r.UTF8 {
		utf8State = "utf8=no"
	}
	fields = append(fields, utf8State)
	if r.Locale != "" {
		fields = append(fields, "locale="+r.Locale)
	}
	if r.Term != "" {
		fields = append(fields, "term="+r.Term)
	}
	// An empty COLORTERM over SSH usually means the client's truecolor
	// capability was not forwarded; name it so the fix (SetEnv/AcceptEnv) has
	// something to point at.
	colorterm := r.Colorterm
	if colorterm == "" {
		colorterm = "unset"
	}
	fields = append(fields, "colorterm="+colorterm)
	if r.Override != "" {
		fields = append(fields, "override="+r.Override)
	}
	return strings.Join(fields, "\t")
}
