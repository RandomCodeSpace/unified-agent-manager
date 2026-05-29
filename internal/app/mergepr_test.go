package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// ---------------------------------------------------------------------------
// C2-7 — the store schema does not persist PR Owner/Repo, only the URL. When a
// dead session's PR is rehydrated from the stored record, Owner/Repo must be
// re-derived from the (lossless) URL so downstream consumers that key on
// owner/repo are not handed empty strings.
// ---------------------------------------------------------------------------

func TestMergeStoredMetadataReDerivesPROwnerRepoFromURL(t *testing.T) {
	// A live session with no PR yet; the stored record carries a PR URL.
	sess := adapter.Session{ID: "abc123", AgentType: "claude"}
	rec := store.SessionRecord{
		ID:    "abc123",
		Agent: "claude",
		PR: &store.PRRecord{
			URL:        "https://github.com/acme/widgets/pull/42",
			Number:     42,
			LastStatus: "Open",
		},
	}

	got := mergeStoredMetadata(sess, rec)
	if got.PR == nil {
		t.Fatalf("PR not rehydrated from stored record")
	}
	if got.PR.Owner != "acme" {
		t.Fatalf("PR.Owner = %q, want %q (must be re-derived from URL)", got.PR.Owner, "acme")
	}
	if got.PR.Repo != "widgets" {
		t.Fatalf("PR.Repo = %q, want %q (must be re-derived from URL)", got.PR.Repo, "widgets")
	}
	// The persisted fields must be preserved unchanged.
	if got.PR.URL != rec.PR.URL || got.PR.Number != 42 || got.PR.Status != adapter.PRStatus("Open") {
		t.Fatalf("persisted PR fields altered: %+v", got.PR)
	}
}

func TestMergeStoredMetadataPRWithUnparseableURLLeavesOwnerRepoEmpty(t *testing.T) {
	// A record whose URL does not match the GitHub PR shape (shouldn't happen
	// after validation, but be defensive) keeps the record's URL and leaves
	// Owner/Repo empty rather than panicking.
	sess := adapter.Session{ID: "abc123", AgentType: "claude"}
	rec := store.SessionRecord{
		ID:    "abc123",
		Agent: "claude",
		PR:    &store.PRRecord{URL: "not-a-url", Number: 0, LastStatus: "Open"},
	}
	got := mergeStoredMetadata(sess, rec)
	if got.PR == nil {
		t.Fatalf("PR dropped for unparseable URL")
	}
	if got.PR.Owner != "" || got.PR.Repo != "" {
		t.Fatalf("expected empty owner/repo for unparseable URL, got %q/%q", got.PR.Owner, got.PR.Repo)
	}
	if got.PR.URL != "not-a-url" {
		t.Fatalf("URL altered: %q", got.PR.URL)
	}
}

func TestMergeStoredMetadataDoesNotOverrideLivePR(t *testing.T) {
	// When the live session already discovered a PR (Owner/Repo set), the stored
	// record must not clobber it.
	sess := adapter.Session{
		ID:        "abc123",
		AgentType: "claude",
		PR:        &adapter.PRRef{URL: "https://github.com/live/repo/pull/7", Owner: "live", Repo: "repo", Number: 7, Status: adapter.PROpen},
	}
	rec := store.SessionRecord{
		ID:    "abc123",
		Agent: "claude",
		PR:    &store.PRRecord{URL: "https://github.com/stored/old/pull/1", Number: 1, LastStatus: "Closed"},
	}
	got := mergeStoredMetadata(sess, rec)
	if got.PR.Owner != "live" || got.PR.Repo != "repo" || got.PR.Number != 7 {
		t.Fatalf("live PR was clobbered by stored record: %+v", got.PR)
	}
}
