package adapter

import "testing"

func TestClassifyAliveReturnsActive(t *testing.T) {
	state, alive := ClassifyPane(true)
	if state != Active || alive != Alive {
		t.Fatalf("got %s %s", state, alive)
	}
}

func TestClassifyExitedReturnsFailed(t *testing.T) {
	state, alive := ClassifyPane(false)
	if state != Failed || alive != Exited {
		t.Fatalf("got %s %s", state, alive)
	}
}

func TestExtractPR(t *testing.T) {
	pr := ExtractPR("see https://github.com/owner/repo/pull/123 for review")
	if pr == nil || pr.Number != 123 || pr.Owner != "owner" || pr.Repo != "repo" {
		t.Fatalf("bad pr: %+v", pr)
	}
}
