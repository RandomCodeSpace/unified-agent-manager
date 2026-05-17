package tmux

import "testing"

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
