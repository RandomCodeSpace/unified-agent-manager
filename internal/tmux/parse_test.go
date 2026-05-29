package tmux

import (
	"strings"
	"testing"
)

func TestParseSessionLine(t *testing.T) {
	line := "uam-claude-abc12345|1710000000|0|4242|/tmp/repo|claude"
	got, err := ParseSessionLine(line)
	if err != nil {
		t.Fatalf("ParseSessionLine: %v", err)
	}
	if got.Name != "uam-claude-abc12345" || got.CreatedUnix != 1710000000 || got.Attached || got.PanePID != 4242 || got.CurrentPath != "/tmp/repo" || got.CurrentCommand != "claude" {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestParseListSessionsSkipsBlankLines(t *testing.T) {
	input := "uam-a|1|1|2|/a|bash\n\n uam-b|3|0|4|/b|codex \n"
	got, err := ParseListSessions(input)
	if err != nil {
		t.Fatalf("ParseListSessions: %v", err)
	}
	if len(got) != 2 || !got[0].Attached || got[1].Name != "uam-b" {
		t.Fatalf("unexpected sessions: %+v", got)
	}
}

func TestParseSessionLineRejectsMalformed(t *testing.T) {
	if _, err := ParseSessionLine("too|few"); err == nil {
		t.Fatal("expected error")
	}
}

// F11 — a cwd that legally contains '|' must not break parsing. The split is
// right-anchored: the first four fields and the trailing command are fixed, so
// the path keeps its embedded separators.
func TestParseSessionLine_PipeInPath(t *testing.T) {
	line := "uam-claude-abc12345|1710000000|0|4242|/tmp/weird|dir|claude"
	got, err := ParseSessionLine(line)
	if err != nil {
		t.Fatalf("ParseSessionLine: %v", err)
	}
	if got.CurrentPath != "/tmp/weird|dir" {
		t.Fatalf("path lost embedded separator: %q", got.CurrentPath)
	}
	if got.CurrentCommand != "claude" || got.Name != "uam-claude-abc12345" || got.PanePID != 4242 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// F11 — one unparseable line (e.g. a non-numeric pid) must not discard the
// whole batch; healthy sessions survive and a sentinel error is returned.
func TestParseListSessions_PipeInPathKeepsHealthySessions(t *testing.T) {
	input := strings.Join([]string{
		"uam-a|1|0|10|/home/a|bash",
		"uam-b|2|0|notapid|/home/b|codex", // malformed: pid not numeric
		"uam-c|3|0|30|/srv/proj|with|pipe|claude",
	}, "\n")
	got, err := ParseListSessions(input)
	if err == nil {
		t.Fatal("expected a sentinel error reporting the skipped malformed line")
	}
	if len(got) != 2 {
		t.Fatalf("healthy sessions should survive, got %d: %+v", len(got), got)
	}
	if got[0].Name != "uam-a" || got[1].Name != "uam-c" {
		t.Fatalf("unexpected survivors: %+v", got)
	}
	if got[1].CurrentPath != "/srv/proj|with|pipe" {
		t.Fatalf("path with multiple pipes lost data: %q", got[1].CurrentPath)
	}
}

// F11 — CRLF line endings and unicode paths must round-trip cleanly.
func TestParseListSessions_CRLFAndUnicode(t *testing.T) {
	input := "uam-a|1|0|10|/home/üser/проект|bash\r\nuam-b|2|0|20|/tmp|codex\r\n"
	got, err := ParseListSessions(input)
	if err != nil {
		t.Fatalf("ParseListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(got), got)
	}
	if got[0].CurrentPath != "/home/üser/проект" {
		t.Fatalf("unicode path mangled: %q", got[0].CurrentPath)
	}
}
