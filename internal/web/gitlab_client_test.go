package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const gitLabFixtureSHA = "1234567890abcdef1234567890abcdef12345678"

type gitLabFixtureResponse struct {
	body       string
	status     int
	retryAfter string
}

func gitLabClientFake(t *testing.T, responses map[string]gitLabFixtureResponse) glabGate {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "glab")
	var script strings.Builder
	script.WriteString("#!/bin/sh\n[ \"$1\" = api ] && [ \"$3\" = --hostname ] && [ \"$4\" = gitlab.example.com ] && [ \"$5\" = --method ] && [ \"$6\" = GET ] && [ \"$7\" = --include ] && [ \"$#\" = 7 ] || exit 97\nprintf '%s\\n' \"$2\" >> \"$0.calls\"\ncase \"$2\" in\n")
	keys := make([]string, 0, len(responses))
	for endpoint := range responses {
		keys = append(keys, endpoint)
	}
	sort.Strings(keys)
	for i, endpoint := range keys {
		response := responses[endpoint]
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		header := fmt.Sprintf("HTTP/2.0 %d %s\r\nContent-Type: application/json\r\n", status, http.StatusText(status))
		if response.retryAfter != "" {
			header += "Retry-After: " + response.retryAfter + "\r\n"
		}
		fixture := fmt.Sprintf("%s.%d", bin, i)
		if err := os.WriteFile(fixture, []byte(header+"\r\n"+response.body), 0600); err != nil {
			t.Fatal(err)
		}
		quotedEndpoint := "'" + strings.ReplaceAll(endpoint, "'", "'\"'\"'") + "'"
		fmt.Fprintf(&script, "%s) cat \"$0.%d\"", quotedEndpoint, i)
		if status >= 400 {
			script.WriteString("; printf '%s' 'credential-secret-never-display' >&2; exit 1")
		}
		script.WriteString(";;\n")
	}
	script.WriteString("*) exit 98;;\nesac\n")
	if err := os.WriteFile(bin, []byte(script.String()), 0700); err != nil {
		t.Fatal(err)
	}
	return glabGate{Host: "gitlab.example.com", Path: "group/sub/repo", Dir: dir, executable: bin}
}

func gitLabFixturePipeline(sha, ref string) gitLabPipelineResponse {
	duration := 12.5
	return gitLabPipelineResponse{GitLabPipeline: GitLabPipeline{ID: 91, SHA: sha, Ref: ref, Status: "failed", Duration: &duration, WebURL: "https://gitlab.example.com/group/sub/repo/-/pipelines/91"}, ProjectID: 42}
}

func gitLabFixtureJSON(value any) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func gitLabClientResponses(branch string) map[string]gitLabFixtureResponse {
	pipeline := gitLabFixturePipeline(gitLabFixtureSHA, branch)
	query := url.Values{"sha": {gitLabFixtureSHA}, "ref": {branch}, "order_by": {"id"}, "sort": {"desc"}, "per_page": {"1"}, "page": {"1"}}
	return map[string]gitLabFixtureResponse{
		"projects/group%2Fsub%2Frepo":                                                                {body: `{"id":42,"path_with_namespace":"group/sub/repo"}`},
		"projects/42/pipelines?" + query.Encode():                                                    {body: gitLabFixtureJSON([]gitLabPipelineResponse{pipeline})},
		"projects/42/pipelines/91":                                                                   {body: gitLabFixtureJSON(pipeline)},
		"projects/42/pipelines/91/jobs?include_retried=false&per_page=100&page=1":                    {body: gitLabFixtureJobs(branch, 1, 1)},
		"projects/42/repository/commits/" + gitLabFixtureSHA + "/merge_requests?per_page=100&page=1": {body: `[]`},
	}
}

func gitLabFixtureJobs(branch string, start, count int) string {
	jobs := make([]any, 0, count)
	for i := start; i < start+count; i++ {
		jobs = append(jobs, map[string]any{
			"id": i, "name": "test-" + strconv.Itoa(i), "stage": "test", "status": "failed", "allow_failure": false, "failure_reason": "script_failure",
			"web_url":  "https://gitlab.example.com/group/sub/repo/-/jobs/" + strconv.Itoa(i),
			"pipeline": gitLabFixturePipeline(gitLabFixtureSHA, branch),
		})
	}
	return gitLabFixtureJSON(jobs)
}

func TestGitLabClientExactCommitAndBranch(t *testing.T) {
	branch := "feat/a&other=value"
	responses := gitLabClientResponses(branch)
	responses["projects/42/repository/commits/"+gitLabFixtureSHA+"/merge_requests?per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]any{
		map[string]any{"iid": 7, "source_branch": branch, "source_project_id": 42, "title": "\x1b[31mDraft: feature\x1b[0m", "draft": true, "state": "opened", "web_url": "https://gitlab.example.com/group/sub/repo/-/merge_requests/7"},
		map[string]any{"iid": 8, "source_branch": "other", "source_project_id": 42},
		map[string]any{"iid": 9, "source_branch": branch, "source_project_id": 43},
	})}
	gate := gitLabClientFake(t, responses)
	got, err := readGitLabPipeline(t.Context(), gate, gitLabFixtureSHA, branch)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != 42 || got.Pipeline == nil || got.Pipeline.ID != 91 || got.Pipeline.SHA != gitLabFixtureSHA || got.Pipeline.Ref != branch || got.Pipeline.Duration == nil || *got.Pipeline.Duration != 12.5 {
		t.Fatalf("wrong pipeline: %+v", got)
	}
	if len(got.Jobs) != 1 || len(got.MergeRequests) != 1 || got.MergeRequests[0].IID != 7 || got.MergeRequests[0].Title != "Draft: feature" {
		t.Fatalf("wrong context: %+v", got)
	}
	data, err := os.ReadFile(gate.executable + ".calls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ref=feat%2Fa%26other%3Dvalue") || !strings.Contains(string(data), "sha="+gitLabFixtureSHA) {
		t.Fatalf("missing exact escaped filters: %s", data)
	}
}

func TestGitLabClientRejectsDifferentPipeline(t *testing.T) {
	for _, field := range []string{"sha", "ref", "project", "id"} {
		t.Run(field, func(t *testing.T) {
			responses := gitLabClientResponses("main")
			pipeline := gitLabFixturePipeline(gitLabFixtureSHA, "main")
			switch field {
			case "sha":
				pipeline.SHA = strings.Repeat("a", 40)
			case "ref":
				pipeline.Ref = "other"
			case "project":
				pipeline.ProjectID = 43
			case "id":
				pipeline.ID = 92
			}
			responses["projects/42/pipelines/91"] = gitLabFixtureResponse{body: gitLabFixtureJSON(pipeline)}
			gate := gitLabClientFake(t, responses)
			got, err := readGitLabPipeline(t.Context(), gate, gitLabFixtureSHA, "main")
			if err == nil || got.Pipeline != nil {
				t.Fatalf("accepted wrong pipeline: %+v, %v", got, err)
			}
		})
	}
}

func TestGitLabClientNoPipeline(t *testing.T) {
	responses := gitLabClientResponses("main")
	for endpoint := range responses {
		if strings.Contains(endpoint, "/pipelines?") {
			responses[endpoint] = gitLabFixtureResponse{body: "[]"}
		}
	}
	got, err := readGitLabPipeline(t.Context(), gitLabClientFake(t, responses), gitLabFixtureSHA, "main")
	if err != nil || got.ProjectID != 42 || got.Pipeline != nil {
		t.Fatalf("no pipeline: %+v, %v", got, err)
	}
}

func TestGitLabClientPinnedInvestigationPipeline(t *testing.T) {
	responses := gitLabClientResponses("main")
	newer := gitLabFixturePipeline(gitLabFixtureSHA, "main")
	newer.ID = 92
	for endpoint := range responses {
		if strings.Contains(endpoint, "/pipelines?") {
			responses[endpoint] = gitLabFixtureResponse{body: gitLabFixtureJSON([]gitLabPipelineResponse{newer})}
		}
	}
	gate := gitLabClientFake(t, responses)
	got, err := readGitLabPipelineAt(t.Context(), gate, gitLabFixtureSHA, "main", 42, 91)
	if err != nil || got.Pipeline == nil || got.Pipeline.ID != 91 || len(got.Jobs) != 1 {
		t.Fatalf("investigation did not retain its exact pipeline: %+v, %v", got, err)
	}
	calls, err := os.ReadFile(gate.executable + ".calls")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "/pipelines?") {
		t.Fatal("pinned investigation selected the latest pipeline")
	}
	if _, err := readGitLabPipelineAt(t.Context(), gate, gitLabFixtureSHA, "main", 43, 91); err == nil {
		t.Fatal("pinned pipeline accepted a different numeric project behind the origin")
	}
	responses["projects/42/pipelines/91"] = gitLabFixtureResponse{body: gitLabFixtureJSON(newer)}
	if _, err := readGitLabPipelineAt(t.Context(), gitLabClientFake(t, responses), gitLabFixtureSHA, "main", 42, 91); err == nil {
		t.Fatal("pinned pipeline accepted a different pipeline in the detail response")
	}
}

func TestGitLabClientMergeRequestsWithoutPipeline(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(strconv.FormatBool(denied), func(t *testing.T) {
			responses := gitLabClientResponses("main")
			for endpoint := range responses {
				if strings.Contains(endpoint, "/pipelines?") {
					responses[endpoint] = gitLabFixtureResponse{body: `[]`}
				}
			}
			mr := gitLabFixtureResponse{body: gitLabFixtureJSON([]any{
				map[string]any{"iid": 7, "source_branch": "main", "source_project_id": 42, "title": "Review without CI", "state": "opened", "web_url": "https://gitlab.example.com/group/sub/repo/-/merge_requests/7"},
			})}
			if denied {
				mr = gitLabFixtureResponse{status: http.StatusForbidden, body: `{}`}
			}
			responses["projects/42/repository/commits/"+gitLabFixtureSHA+"/merge_requests?per_page=100&page=1"] = mr
			got, err := readGitLabPipeline(t.Context(), gitLabClientFake(t, responses), gitLabFixtureSHA, "main")
			if denied {
				if err == nil {
					t.Fatal("failed merge request read was presented as an empty result")
				}
				return
			}
			if err != nil || got.Pipeline != nil || len(got.MergeRequests) != 1 || got.MergeRequests[0].IID != 7 {
				t.Fatalf("merge request was lost because the commit has no pipeline: %+v, %v", got, err)
			}
		})
	}
}

func TestGitLabClientJobPagination(t *testing.T) {
	for _, count := range []int{101, 200, 201} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			responses := gitLabClientResponses("main")
			for page := 1; page <= 3; page++ {
				start := (page-1)*100 + 1
				endpoint := fmt.Sprintf("projects/42/pipelines/91/jobs?include_retried=false&per_page=100&page=%d", page)
				responses[endpoint] = gitLabFixtureResponse{body: gitLabFixtureJobs("main", start, min(max(count-start+1, 0), 100))}
			}
			gate := gitLabClientFake(t, responses)
			got, err := readGitLabPipeline(t.Context(), gate, gitLabFixtureSHA, "main")
			if count > 200 {
				if err == nil || !strings.Contains(err.Error(), "more than 200 jobs") {
					t.Fatalf("overflow accepted: %v", err)
				}
			} else if err != nil || len(got.Jobs) != count {
				t.Fatalf("jobs=%d wanted=%d error=%v", len(got.Jobs), count, err)
			}
			calls, _ := os.ReadFile(gate.executable + ".calls")
			if strings.Contains(string(calls), "page=4") {
				t.Fatal("pagination exceeded its bound")
			}
		})
	}
}

func TestGitLabClientSafeErrors(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			gate := gitLabClientFake(t, map[string]gitLabFixtureResponse{
				"projects/group%2Fsub%2Frepo": {status: code, body: `{"message":"credential-secret-never-display"}`, retryAfter: "30"},
			})
			_, err := readGitLabPipeline(t.Context(), gate, gitLabFixtureSHA, "main")
			var apiErr *gitLabAPIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != code || apiErr.RetryAfter != 30*time.Second || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or wrong error: %v", err)
			}
		})
	}
	t.Run("oversized", func(t *testing.T) {
		gate := gitLabClientFake(t, map[string]gitLabFixtureResponse{
			"projects/group%2Fsub%2Frepo": {body: strings.Repeat("credential-secret-never-display", 40000)},
		})
		_, err := readGitLabPipeline(t.Context(), gate, gitLabFixtureSHA, "main")
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("oversized response error=%v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := readGitLabPipeline(ctx, gitLabClientFake(t, gitLabClientResponses("main")), gitLabFixtureSHA, "main")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	})
}

func TestGitLabClientTraceTail(t *testing.T) {
	for _, size := range []int{12, gitLabTraceLimit, glabOutputLimit * 2} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := "PREFIX" + strings.Repeat("x", size) + "\x1b[31mLAST FAILURE\x1b[0m\n\x00"
			gate := gitLabClientFake(t, map[string]gitLabFixtureResponse{
				"projects/42/jobs/3/trace": {body: body},
			})
			got, err := readGitLabJobTrace(t.Context(), gate, 42, 3)
			if err != nil {
				t.Fatal(err)
			}
			start := max(len(body)-gitLabTraceLimit, 0)
			if got.Text != displaytext.SanitizeText(body[start:]) || got.Truncated != (len(body) > gitLabTraceLimit) || len(got.Text) > gitLabTraceLimit {
				t.Fatalf("wrong tail: size=%d truncated=%v suffix=%q", len(got.Text), got.Truncated, got.Text[max(0, len(got.Text)-40):])
			}
		})
	}
}

func TestGitLabClientTraceFailuresDoNotExposeOutput(t *testing.T) {
	gate := gitLabClientFake(t, map[string]gitLabFixtureResponse{
		"projects/42/jobs/3/trace": {status: 403, body: "credential-secret-never-display"},
	})
	got, err := readGitLabJobTrace(t.Context(), gate, 42, 3)
	var apiErr *gitLabAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 || got.Text != "" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("failed trace=%+v error=%v", got, err)
	}
}

func TestGitLabClientSafeLinks(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "http://gitlab.example.com/path", "https://other.example/path", "https://user:secret@gitlab.example.com/path", "//gitlab.example.com/path", "https://gitlab.example.com:444/path"} {
		if got := gitLabSafeURL("gitlab.example.com", raw); got != "" {
			t.Fatalf("unsafe link accepted: %q", got)
		}
	}
	valid := "https://gitlab.example.com/group/repo/-/pipelines/91"
	if got := gitLabSafeURL("gitlab.example.com", valid); got != valid {
		t.Fatalf("valid link discarded: %q", got)
	}
}
