package adapter

import "testing"

func TestClassifyAliveReturnsActive(t *testing.T) {
	state, alive, summary := ClassifyPane([]string{"working on parser", ""}, true)
	if state != Active || alive != Alive || summary != "working on parser" {
		t.Fatalf("got %s %s %q", state, alive, summary)
	}
}

func TestClassifyExitedReturnsFailed(t *testing.T) {
	state, alive, _ := ClassifyPane([]string{"done"}, false)
	if state != Failed || alive != Exited {
		t.Fatalf("got %s %s", state, alive)
	}
}

func TestSummaryIgnoresDecorativeDividers(t *testing.T) {
	lines := []string{
		"working on parser",
		"────────────────────────────────────────────────────────────────────────────────",
		"",
	}

	_, _, summary := ClassifyPane(lines, true)
	if summary != "working on parser" {
		t.Fatalf("summary = %q", summary)
	}
}

func TestExtractPR(t *testing.T) {
	pr := ExtractPR("see https://github.com/owner/repo/pull/123 for review")
	if pr == nil || pr.Number != 123 || pr.Owner != "owner" || pr.Repo != "repo" {
		t.Fatalf("bad pr: %+v", pr)
	}
}
