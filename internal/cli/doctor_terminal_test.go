package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestDoctorGlobalIncludesTheTerminalSection pins the client-side half of
// doctor: every input to the glyph-set decision is reported, in text and in
// JSON, so a "looks wrong from this client" report carries its own diagnosis.
func TestDoctorGlobalIncludesTheTerminalSection(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("UAM_ASCII", "")
	t.Setenv("UAM_WIDE", "")

	svc := diagnosticTestService(t)
	output := captureStdout(t, func() {
		if err := runDoctor(context.Background(), svc, nil); err != nil {
			t.Fatal(err)
		}
	})
	text := string(output)
	// Under a captured (non-TTY) stdout the probe cannot run, and the report
	// must say so instead of inventing a measurement.
	for _, want := range []string{
		"terminal\tglyphs=unicode",
		"ambiguous_width=unmeasured",
		"utf8=yes",
		"locale=LANG=en_US.UTF-8",
		"term=xterm-256color",
		"colorterm=unset",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor terminal line missing %q:\n%s", want, text)
		}
	}

	jsonOut := captureStdout(t, func() {
		if err := runDoctor(context.Background(), svc, []string{"--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded struct {
		Terminal terminalDoctorReport `json:"terminal"`
	}
	if err := json.Unmarshal(jsonOut, &decoded); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, jsonOut)
	}
	if decoded.Terminal.Glyphs != "unicode" || !decoded.Terminal.UTF8 || decoded.Terminal.Probed {
		t.Fatalf("terminal JSON = %+v", decoded.Terminal)
	}
}

// TestDoctorTerminalReportsOverridesAndDegradedLocale pins the two inputs that
// decide without a measurement: an explicit override and a non-UTF-8 locale.
func TestDoctorTerminalReportsOverridesAndDegradedLocale(t *testing.T) {
	t.Setenv("LC_ALL", "POSIX")
	t.Setenv("UAM_ASCII", "")
	t.Setenv("UAM_WIDE", "")
	report := doctorTerminal()
	if report.Glyphs != "ascii" || report.UTF8 {
		t.Fatalf("a POSIX locale must degrade: %+v", report)
	}
	if report.Locale != "LC_ALL=POSIX" {
		t.Fatalf("locale should name the entry that decided: %q", report.Locale)
	}

	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("UAM_ASCII", "1")
	report = doctorTerminal()
	if report.Glyphs != "ascii" || report.Override != "UAM_ASCII=1" {
		t.Fatalf("override must be reported: %+v", report)
	}
	if !strings.Contains(report.line(), "override=UAM_ASCII=1") {
		t.Fatalf("text line lost the override: %q", report.line())
	}
}
