package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const gitLabReviewFixtureDiscussion = "1234567890abcdef1234567890abcdef12345678"

func gitLabReviewFixtureMR(sha string) gitLabReviewMRResponse {
	return gitLabReviewMRResponse{
		GitLabReviewMergeRequest: GitLabReviewMergeRequest{GitLabMergeRequest: GitLabMergeRequest{IID: 7, Title: "\x1b[31mCheckout fix\x1b[0m", State: "opened", WebURL: "https://gitlab.example.com/group/sub/repo/-/merge_requests/7"}, SourceBranch: "main", TargetBranch: "trunk"},
		ID:                       123, ProjectID: 42, SourceProjectID: 42, TargetProjectID: 42, SHA: sha,
	}
}

func gitLabReviewFixtureThread() GitLabDiscussion {
	line := 12
	return GitLabDiscussion{ID: gitLabReviewFixtureDiscussion, Notes: []GitLabReviewNote{{ID: 81, Body: "\x1b[31mPlease fix\x1b[0m\nthis timeout.", Author: GitLabReviewAuthor{Name: "\x1b[31mReviewer\x1b[0m", Username: "reviewer"}, Resolvable: true, Position: &GitLabReviewPosition{NewPath: "checkout.go", NewLine: &line, HeadSHA: gitLabFixtureSHA}}}}
}

func newGitLabReviewFixture(t *testing.T, change ...func(map[string]gitLabFixtureResponse, string)) gitLabWorkflowFixture {
	t.Helper()
	f := newGitLabWorkflowFixture(t, func(responses map[string]gitLabFixtureResponse) {
		var sha string
		for endpoint := range responses {
			if strings.Contains(endpoint, "/repository/commits/") {
				sha = strings.Split(strings.Split(endpoint, "/repository/commits/")[1], "/")[0]
			}
		}
		mr := gitLabReviewFixtureMR(sha)
		responses["projects/42/repository/commits/"+sha+"/merge_requests?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]gitLabReviewMRResponse{mr})}
		responses["projects/42/merge_requests/7"] = gitLabFixtureResponse{body: gitLabFixtureJSON(mr)}
		responses["projects/42/merge_requests/7/discussions?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]GitLabDiscussion{gitLabReviewFixtureThread()})}
		for _, fn := range change {
			fn(responses, sha)
		}
	})
	f.record(t)
	return f
}

func gitLabReviewWriteFixture(t *testing.T, f gitLabWorkflowFixture, response string) {
	t.Helper()
	data, err := os.ReadFile(f.gate.executable)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(data), `[ "$6" = GET ]`, `{ [ "$6" = GET ] || [ "$6" = POST ] || [ "$6" = PUT ]; }`, 1)
	script = strings.Replace(script, `[ "$#" = 7 ]`, `{ [ "$#" = 7 ] || [ "$#" = 9 ]; }`, 1)
	write := `if [ "$6" != GET ]; then
[ "$8" = --raw-field ] || exit 96
printf '%s\n' "$6" "$2" "$9" >> "$0.writes"
cat "$0.write-response"
exit 0
fi
`
	script = strings.Replace(script, `case "$2" in`, write+`case "$2" in`, 1)
	if err := os.WriteFile(f.gate.executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.gate.executable+".write-response", []byte(response), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGitLabReviewReadsWithoutPipelineAndSanitizes(t *testing.T) {
	f := newGitLabReviewFixture(t)
	review, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7)
	if err != nil {
		t.Fatal(err)
	}
	if review.MergeRequest.Title != "Checkout fix" || review.MergeRequest.HeadSHA != f.sha || len(review.Discussions) != 1 || review.Truncated || review.CheckedAt.IsZero() {
		t.Fatalf("bad review: %+v", review)
	}
	note := review.Discussions[0].Notes[0]
	if note.Body != "Please fix\nthis timeout." || note.Author.Name != "Reviewer" || note.Position.NewLine == nil || *note.Position.NewLine != 12 {
		t.Fatalf("bad note: %+v", note)
	}
	calls, _ := os.ReadFile(f.gate.executable + ".calls")
	if strings.Contains(string(calls), "/pipelines") {
		t.Fatalf("MR read depends on pipeline: %s", calls)
	}
}

func TestGitLabReviewRejectsUnassociatedAndForkMR(t *testing.T) {
	for _, field := range []string{"iid", "global_id", "project", "target_project", "source_project", "branch"} {
		t.Run(field, func(t *testing.T) {
			f := newGitLabReviewFixture(t, func(responses map[string]gitLabFixtureResponse, sha string) {
				mr := gitLabReviewFixtureMR(sha)
				switch field {
				case "iid":
					mr.IID = 8
				case "global_id":
					mr.ID = 999
				case "project":
					mr.ProjectID = 43
				case "target_project":
					mr.TargetProjectID = 43
				case "source_project":
					mr.SourceProjectID = 43
				case "branch":
					mr.SourceBranch = "unrelated"
				}
				if field == "global_id" {
					responses["projects/42/merge_requests/7"] = gitLabFixtureResponse{body: gitLabFixtureJSON(mr)}
				} else {
					responses["projects/42/repository/commits/"+sha+"/merge_requests?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]gitLabReviewMRResponse{mr})}
				}
			})
			if _, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7); statusOf(err) != http.StatusConflict {
				t.Fatalf("scope accepted: %v", err)
			}
		})
	}
}

func TestGitLabReviewRequiresCurrentOwnedCommit(t *testing.T) {
	f := newGitLabReviewFixture(t)
	if _, err := f.m.gitLabReview(t.Context(), f.source.ID, strings.Repeat("a", 40), 7); statusOf(err) != http.StatusConflict {
		t.Fatalf("stale commit accepted: %v", err)
	}
	gitIn(t, f.repo, "switch", "-q", "-c", "other")
	if _, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7); statusOf(err) != http.StatusConflict {
		t.Fatalf("stale branch accepted: %v", err)
	}
	if _, err := os.Stat(f.gate.executable + ".calls"); !os.IsNotExist(err) {
		t.Fatalf("invalid selection reached GitLab: %v", err)
	}
}

func TestGitLabReviewCapsDiscussionEvidence(t *testing.T) {
	f := newGitLabReviewFixture(t, func(responses map[string]gitLabFixtureResponse, _ string) {
		thread := gitLabReviewFixtureThread()
		thread.Notes[0].Body = strings.Repeat("a", gitLabReviewBodyLimit+1)
		for i := 1; i < 205; i++ {
			note := thread.Notes[0]
			note.ID += int64(i)
			note.Body = "reply"
			thread.Notes = append(thread.Notes, note)
		}
		responses["projects/42/merge_requests/7/discussions?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]GitLabDiscussion{thread})}
	})
	review, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Truncated || len(review.Discussions[0].Notes) != gitLabMaxItems || len(review.Discussions[0].Notes[0].Body) > gitLabReviewBodyLimit {
		t.Fatalf("unbounded review: truncated=%t notes=%d body=%d", review.Truncated, len(review.Discussions[0].Notes), len(review.Discussions[0].Notes[0].Body))
	}
}

func TestGitLabReviewPaginatesAndAllowsHistoricalCommit(t *testing.T) {
	f := newGitLabReviewFixture(t, func(responses map[string]gitLabFixtureResponse, _ string) {
		mr := gitLabReviewFixtureMR(strings.Repeat("a", 40))
		responses["projects/42/merge_requests/7"] = gitLabFixtureResponse{body: gitLabFixtureJSON(mr)}
		threads := make([]GitLabDiscussion, gitLabPageSize)
		for i := range threads {
			threads[i] = gitLabReviewFixtureThread()
			threads[i].ID = fmt.Sprintf("%040x", i+1)
		}
		responses["projects/42/merge_requests/7/discussions?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON(threads)}
		responses["projects/42/merge_requests/7/discussions?per_page=100&page=2"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]GitLabDiscussion{gitLabReviewFixtureThread()})}
	})
	review, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Discussions) != 101 || review.Truncated || review.MergeRequest.HeadSHA == f.sha {
		t.Fatalf("incorrect pagination or historical read: threads=%d truncated=%t head=%s", len(review.Discussions), review.Truncated, review.MergeRequest.HeadSHA)
	}
}

func TestGitLabReviewSystemAndUnresolvableNotesCannotBeResolved(t *testing.T) {
	for _, system := range []bool{false, true} {
		t.Run(fmt.Sprint(system), func(t *testing.T) {
			f := newGitLabReviewFixture(t, func(responses map[string]gitLabFixtureResponse, _ string) {
				thread := gitLabReviewFixtureThread()
				thread.Notes[0].System = system
				thread.Notes[0].Resolvable = system
				responses["projects/42/merge_requests/7/discussions?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]GitLabDiscussion{thread})}
			})
			if err := f.m.gitLabResolve(t.Context(), f.source.ID, f.sha, 7, gitLabReviewFixtureDiscussion, true); statusOf(err) != http.StatusConflict {
				t.Fatalf("invalid resolve accepted: %v", err)
			}
			if system {
				if _, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, gitLabReviewFixtureDiscussion, "reply"); statusOf(err) != http.StatusConflict {
					t.Fatalf("system reply accepted: %v", err)
				}
			}
		})
	}
}

func TestGitLabReviewCommentsUseLiteralBodyAndScopedEndpoint(t *testing.T) {
	for _, discussion := range []string{"", gitLabReviewFixtureDiscussion} {
		t.Run(discussion, func(t *testing.T) {
			f := newGitLabReviewFixture(t)
			gitLabReviewWriteFixture(t, f, "HTTP/2.0 201 Created\r\n\r\n{\"id\":82}")
			body := "@/etc/passwd\nKeep $HOME and `commands` as literal text.\x1b[31m"
			id, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, discussion, body)
			if err != nil || id != 82 {
				t.Fatalf("comment failed: %d, %v", id, err)
			}
			writes, _ := os.ReadFile(f.gate.executable + ".writes")
			endpoint := "projects/42/merge_requests/7"
			if discussion != "" {
				endpoint += "/discussions/" + discussion
			}
			want := "POST\n" + endpoint + "/notes\nbody=@/etc/passwd\nKeep $HOME and `commands` as literal text.\n"
			if string(writes) != want {
				t.Fatalf("unsafe or duplicate write: %q", writes)
			}
		})
	}
}

func TestGitLabReviewRejectsInvalidWrites(t *testing.T) {
	f := newGitLabReviewFixture(t)
	for _, body := range []string{"", "\x1b[31m", strings.Repeat("a", gitLabReviewBodyLimit+1), "reply\n /merge", "reply\n/close"} {
		if _, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, "", body); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("invalid body accepted: %v", err)
		}
	}
	for _, id := range []string{"../../notes", strings.Repeat("a", 40)} {
		if _, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, id, "reply"); err == nil {
			t.Fatal("foreign discussion accepted")
		}
	}
	if _, err := os.Stat(f.gate.executable + ".writes"); !os.IsNotExist(err) {
		t.Fatal("invalid request sent a write")
	}
}

func TestGitLabReviewResolveAndReopen(t *testing.T) {
	for _, resolved := range []bool{true, false} {
		t.Run(fmt.Sprint(resolved), func(t *testing.T) {
			f := newGitLabReviewFixture(t)
			updated := gitLabReviewFixtureThread()
			updated.Notes[0].Resolved = resolved
			gitLabReviewWriteFixture(t, f, "HTTP/2.0 200 OK\r\n\r\n"+gitLabFixtureJSON(updated))
			if err := f.m.gitLabResolve(t.Context(), f.source.ID, f.sha, 7, gitLabReviewFixtureDiscussion, resolved); err != nil {
				t.Fatal(err)
			}
			writes, _ := os.ReadFile(f.gate.executable + ".writes")
			want := fmt.Sprintf("PUT\nprojects/42/merge_requests/7/discussions/%s\nresolved=%t\n", gitLabReviewFixtureDiscussion, resolved)
			if string(writes) != want {
				t.Fatalf("bad resolve write: %q", writes)
			}
		})
	}
}

func TestGitLabReviewWriteFailuresDoNotRetry(t *testing.T) {
	for _, test := range []struct {
		name, response string
		status         int
		uncertain      bool
	}{
		{"permission", "HTTP/2.0 403 Forbidden\r\n\r\ncredential-secret-never-display", 403, false},
		{"rate-limit", "HTTP/2.0 429 Too Many Requests\r\nRetry-After: 120\r\n\r\n{}", 429, false},
		{"server", "HTTP/2.0 500 Internal Server Error\r\n\r\ncredential-secret-never-display", 0, true},
		{"lost-response", "credential-secret-never-display", 0, true},
		{"missing-note", "HTTP/2.0 201 Created\r\n\r\n{}", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGitLabReviewFixture(t)
			gitLabReviewWriteFixture(t, f, test.response)
			_, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, "", "reply")
			var uncertain *gitLabWriteUncertain
			if err == nil || errors.As(err, &uncertain) != test.uncertain || strings.Contains(err.Error(), "credential-secret") {
				t.Fatalf("bad write failure: %v", err)
			}
			if !test.uncertain && statusOf(err) != test.status {
				t.Fatalf("status: %v", err)
			}
			writes, _ := os.ReadFile(f.gate.executable + ".writes")
			if strings.Count(string(writes), "POST\n") != 1 {
				t.Fatalf("write retried: %s", writes)
			}
			if test.status == 429 {
				f.m.gitlab.work.Lock()
				until := f.m.gitlab.retryAt[f.gate.Host]
				f.m.gitlab.work.Unlock()
				if until.Before(f.m.now().Add(119 * time.Second)) {
					t.Fatal("Retry-After ignored")
				}
				if _, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7); statusOf(err) != 429 {
					t.Fatalf("backoff bypassed: %v", err)
				}
			}
		})
	}
}

func TestGitLabReviewRechecksMRBeforeWrite(t *testing.T) {
	f := newGitLabReviewFixture(t)
	gitLabReviewWriteFixture(t, f, "HTTP/2.0 201 Created\r\n\r\n{\"id\":82}")
	mr := gitLabReviewFixtureMR(strings.Repeat("a", 40))
	if err := os.WriteFile(f.gate.executable+".changed", []byte("HTTP/2.0 200 OK\r\n\r\n"+gitLabFixtureJSON(mr)), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(f.gate.executable)
	script := strings.Replace(string(data), `case "$2" in`, `if [ "$6" = GET ] && [ "$2" = projects/42/merge_requests/7 ]; then
if [ -f "$0.seen" ]; then cat "$0.changed"; exit 0; fi
touch "$0.seen"
fi
case "$2" in`, 1)
	if err := os.WriteFile(f.gate.executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, "", "reply"); statusOf(err) != http.StatusConflict {
		t.Fatalf("changed MR accepted: %v", err)
	}
	if _, err := os.Stat(f.gate.executable + ".writes"); !os.IsNotExist(err) {
		t.Fatal("stale MR was written")
	}
}

func TestGitLabReviewUnknownOutcomeResponse(t *testing.T) {
	w := httptest.NewRecorder()
	writeGitLabReviewFailure(w, &gitLabWriteUncertain{})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"outcome_unknown":true`) {
		t.Fatalf("missing recovery flag: %d %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := gitLabWriteJSON(ctx, glabGate{}, "ignored", "POST", "body=x", new(any)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled before write: %v", err)
	}
}

func TestGitLabReviewReadOnlyTasksCannotWrite(t *testing.T) {
	for _, stage := range []string{StageSettled, StageArchived} {
		t.Run(stage, func(t *testing.T) {
			f := newGitLabReviewFixture(t)
			gitLabReviewWriteFixture(t, f, "HTTP/2.0 201 Created\r\n\r\n{\"id\":82}")
			var err error
			if stage == StageSettled {
				_, err = f.m.Settle(f.source.ID)
			} else {
				_, err = f.m.Archive(f.source.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.m.gitLabReview(t.Context(), f.source.ID, f.sha, 7); err != nil {
				t.Fatalf("read-only Task cannot view review: %v", err)
			}
			if _, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, "", "reply"); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), stage) {
				t.Fatalf("read-only Task posted: %v", err)
			}
			if err := f.m.gitLabResolve(t.Context(), f.source.ID, f.sha, 7, gitLabReviewFixtureDiscussion, true); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), stage) {
				t.Fatalf("read-only Task resolved: %v", err)
			}
			if _, err := os.Stat(f.gate.executable + ".writes"); !os.IsNotExist(err) {
				t.Fatal("read-only Task reached GitLab mutation")
			}
		})
	}
}

func TestGitLabReviewArchiveDuringReadPreventsWrite(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(fmt.Sprint(resolve), func(t *testing.T) {
			f := newGitLabReviewFixture(t)
			gitLabReviewWriteFixture(t, f, "HTTP/2.0 201 Created\r\n\r\n{\"id\":82}")
			data, err := os.ReadFile(f.gate.executable)
			if err != nil {
				t.Fatal(err)
			}
			script := strings.Replace(string(data), `case "$2" in`, `if [ "$6" = GET ] && [ "$2" = projects/42/merge_requests/7 ]; then
touch "$0.fetching"
while [ ! -f "$0.resume" ]; do sleep 0.01; done
fi
case "$2" in`, 1)
			if err := os.WriteFile(f.gate.executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.WriteFile(f.gate.executable+".resume", nil, 0600) })
			done := make(chan error, 1)
			go func() {
				if resolve {
					done <- f.m.gitLabResolve(t.Context(), f.source.ID, f.sha, 7, gitLabReviewFixtureDiscussion, true)
				} else {
					_, err := f.m.gitLabComment(t.Context(), f.source.ID, f.sha, 7, "", "reply")
					done <- err
				}
			}()
			waitUntil(t, "GitLab freshness read", func() bool { _, err := os.Stat(f.gate.executable + ".fetching"); return err == nil })
			if _, err := f.m.Archive(f.source.ID); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.gate.executable+".resume", nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := <-done; statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "archived") {
				t.Fatalf("archived during read but wrote: %v", err)
			}
			if _, err := os.Stat(f.gate.executable + ".writes"); !os.IsNotExist(err) {
				t.Fatal("archived Task reached GitLab mutation")
			}
		})
	}
}
