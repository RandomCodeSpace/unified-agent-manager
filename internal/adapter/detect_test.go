package adapter

import "testing"

func TestClassifyNeedsInput(t *testing.T) {
	patterns := DefaultPatterns("codex")
	state, alive, summary := ClassifyPane([]string{"working", "Do you want to continue?"}, "codex", true, true, patterns)
	if state != NeedsInput || alive != Alive || summary != "Do you want to continue?" {
		t.Fatalf("got %s %s %q", state, alive, summary)
	}
}

func TestClassifyCompletedWhenProcessExited(t *testing.T) {
	patterns := DefaultPatterns("claude")
	state, alive, _ := ClassifyPane([]string{"done"}, "bash", false, false, patterns)
	if state != Completed || alive != Exited {
		t.Fatalf("got %s %s", state, alive)
	}
}

func TestClassifySummaryIgnoresDecorativeDividers(t *testing.T) {
	patterns := DefaultPatterns("claude")
	lines := []string{
		"working on parser",
		"────────────────────────────────────────────────────────────────────────────────",
		"",
	}

	_, _, summary := ClassifyPane(lines, "claude", true, true, patterns)
	if summary != "working on parser" {
		t.Fatalf("summary = %q", summary)
	}
}

func TestClassifyReadyForReviewWhenPRURLPresent(t *testing.T) {
	patterns := DefaultPatterns("claude")
	state, _, _ := ClassifyPane([]string{"created https://github.com/o/r/pull/42", ">"}, "claude", true, false, patterns)
	if state != ReadyForReview {
		t.Fatalf("state = %s", state)
	}
}

func TestExtractPR(t *testing.T) {
	pr := ExtractPR("see https://github.com/owner/repo/pull/123 for review")
	if pr == nil || pr.Number != 123 || pr.Owner != "owner" || pr.Repo != "repo" {
		t.Fatalf("bad pr: %+v", pr)
	}
}
