package web

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const (
	gitLabPageSize   = 100
	gitLabMaxItems   = 200
	gitLabTraceLimit = 64 << 10
	gitLabHeaderMax  = 16 << 10
)

type GitLabPipeline struct {
	ID        int64    `json:"id"`
	Status    string   `json:"status"`
	SHA       string   `json:"sha"`
	Ref       string   `json:"ref"`
	WebURL    string   `json:"web_url"`
	Duration  *float64 `json:"duration,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type GitLabJob struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Stage        string   `json:"stage"`
	Status       string   `json:"status"`
	AllowFailure bool     `json:"allow_failure"`
	WebURL       string   `json:"web_url"`
	Duration     *float64 `json:"duration,omitempty"`
	// FailureReason is GitLab's reason a failed job failed, such as
	// script_failure or runner_system_failure.
	FailureReason string `json:"failure_reason,omitempty"`
}

type GitLabMergeRequest struct {
	IID    int64  `json:"iid"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	WebURL string `json:"web_url"`
}

type GitLabJobTrace struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type gitLabSnapshot struct {
	ProjectID     int64
	Pipeline      *GitLabPipeline
	Jobs          []GitLabJob
	MergeRequests []GitLabMergeRequest
}

// Never expose CLI output here: an auth or transport error can contain tokens.
type gitLabAPIError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *gitLabAPIError) Error() string {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "GitLab authentication expired. Check GitLab access in Settings."
	case http.StatusForbidden:
		return "GitLab access was denied. Check the account's project permissions."
	case http.StatusNotFound:
		return "The GitLab resource is unavailable or the account cannot access it."
	case http.StatusTooManyRequests:
		return "GitLab is rate limiting requests. Try again later."
	default:
		return "GitLab could not complete the request."
	}
}

type gitLabPipelineResponse struct {
	GitLabPipeline
	ProjectID int64 `json:"project_id"`
}

// readGitLabPipeline only considers the exact commit and branch supplied by the
// caller. A newer pipeline on a different commit never becomes eligible.
func readGitLabPipeline(ctx context.Context, gate glabGate, sha, branch string) (gitLabSnapshot, error) {
	return readGitLabPipelineSelection(ctx, gate, sha, branch, 0, 0)
}

// readGitLabPipelineAt follows a registered investigation's exact event even
// when GitLab has since created a newer pipeline for the same commit.
func readGitLabPipelineAt(ctx context.Context, gate glabGate, sha, branch string, projectID, pipelineID int64) (gitLabSnapshot, error) {
	if projectID <= 0 || pipelineID <= 0 {
		return gitLabSnapshot{}, errors.New("GitLab requires a valid project and pipeline")
	}
	return readGitLabPipelineSelection(ctx, gate, sha, branch, projectID, pipelineID)
}

func readGitLabPipelineSelection(ctx context.Context, gate glabGate, sha, branch string, projectID, pipelineID int64) (gitLabSnapshot, error) {
	result := gitLabSnapshot{Jobs: []GitLabJob{}, MergeRequests: []GitLabMergeRequest{}}
	if !gitLabValidGate(gate) || !gitLabValidSHA(sha) || branch == "" || strings.ContainsAny(branch, "\x00\r\n") {
		return result, errors.New("GitLab requires a valid repository, full commit SHA, and branch")
	}
	var project struct {
		ID                int64  `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
	}
	if _, err := gitLabGetJSON(ctx, gate, "projects/"+url.PathEscape(gate.Path), &project); err != nil {
		return result, err
	}
	if project.ID <= 0 || !strings.EqualFold(project.PathWithNamespace, gate.Path) || (projectID > 0 && project.ID != projectID) {
		return result, errors.New("GitLab returned a different project than the repository origin")
	}
	result.ProjectID = project.ID
	base := "projects/" + strconv.FormatInt(project.ID, 10)
	if pipelineID == 0 {
		query := url.Values{"sha": {sha}, "ref": {branch}, "order_by": {"id"}, "sort": {"desc"}, "per_page": {"1"}, "page": {"1"}}
		var pipelines []gitLabPipelineResponse
		if _, err := gitLabGetJSON(ctx, gate, base+"/pipelines?"+query.Encode(), &pipelines); err != nil {
			return result, err
		}
		if len(pipelines) == 0 {
			var err error
			result.MergeRequests, err = gitLabReadMergeRequests(ctx, gate, base, sha, branch, project.ID)
			return result, err
		}
		// GitLab orders the exact SHA/ref query by descending ID. Reject responses
		// outside the requested scope instead of silently investigating another ref.
		if len(pipelines) != 1 || !gitLabMatchingPipeline(pipelines[0], project.ID, sha, branch) {
			return result, errors.New("GitLab returned a pipeline outside the requested commit and branch")
		}
		pipelineID = pipelines[0].ID
	}
	var pipeline gitLabPipelineResponse
	if _, err := gitLabGetJSON(ctx, gate, base+"/pipelines/"+strconv.FormatInt(pipelineID, 10), &pipeline); err != nil {
		return result, err
	}
	if pipeline.ID != pipelineID || !gitLabMatchingPipeline(pipeline, project.ID, sha, branch) {
		return result, errors.New("GitLab returned a pipeline outside the requested commit and branch")
	}
	pipeline.WebURL = gitLabSafeURL(gate.Host, pipeline.WebURL)
	result.Pipeline = &pipeline.GitLabPipeline
	jobs, err := gitLabReadJobs(ctx, gate, base, pipeline)
	if err != nil {
		return result, err
	}
	result.Jobs = jobs
	result.MergeRequests, err = gitLabReadMergeRequests(ctx, gate, base, sha, branch, project.ID)
	return result, err
}

func gitLabMatchingPipeline(p gitLabPipelineResponse, projectID int64, sha, branch string) bool {
	return p.ID > 0 && p.ProjectID == projectID && p.SHA == sha && p.Ref == branch
}

func gitLabReadJobs(ctx context.Context, gate glabGate, base string, pipeline gitLabPipelineResponse) ([]GitLabJob, error) {
	jobs := make([]GitLabJob, 0)
	seen := make(map[int64]bool)
	for page := 1; page <= gitLabMaxItems/gitLabPageSize+1; page++ {
		var batch []struct {
			GitLabJob
			Pipeline gitLabPipelineResponse `json:"pipeline"`
		}
		endpoint := fmt.Sprintf("%s/pipelines/%d/jobs?include_retried=false&per_page=%d&page=%d", base, pipeline.ID, gitLabPageSize, page)
		if _, err := gitLabGetJSON(ctx, gate, endpoint, &batch); err != nil {
			return nil, err
		}
		if len(batch) > gitLabPageSize || len(jobs)+len(batch) > gitLabMaxItems {
			return nil, errors.New("this GitLab pipeline has more than 200 jobs. Open it in GitLab for the complete job list")
		}
		for _, job := range batch {
			if job.ID <= 0 || seen[job.ID] || job.Pipeline.ID != pipeline.ID || !gitLabMatchingPipeline(job.Pipeline, pipeline.ProjectID, pipeline.SHA, pipeline.Ref) {
				return nil, errors.New("GitLab returned inconsistent pipeline jobs")
			}
			seen[job.ID] = true
			job.Name = displaytext.Sanitize(job.Name)
			job.Stage = displaytext.Sanitize(job.Stage)
			job.FailureReason = displaytext.Sanitize(job.FailureReason)
			job.WebURL = gitLabSafeURL(gate.Host, job.WebURL)
			jobs = append(jobs, job.GitLabJob)
		}
		if len(batch) < gitLabPageSize {
			return jobs, nil
		}
	}
	return jobs, nil
}

func gitLabReadMergeRequests(ctx context.Context, gate glabGate, base, sha, branch string, projectID int64) ([]GitLabMergeRequest, error) {
	mrs := make([]GitLabMergeRequest, 0)
	count := 0
	for page := 1; page <= gitLabMaxItems/gitLabPageSize+1; page++ {
		var batch []struct {
			GitLabMergeRequest
			SourceBranch    string `json:"source_branch"`
			SourceProjectID int64  `json:"source_project_id"`
		}
		endpoint := fmt.Sprintf("%s/repository/commits/%s/merge_requests?per_page=%d&page=%d", base, sha, gitLabPageSize, page)
		if _, err := gitLabGetJSON(ctx, gate, endpoint, &batch); err != nil {
			return nil, err
		}
		count += len(batch)
		if len(batch) > gitLabPageSize || count > gitLabMaxItems {
			return nil, errors.New("this commit has more than 200 GitLab merge requests. Open it in GitLab for the complete list")
		}
		for _, mr := range batch {
			if mr.SourceBranch != branch || mr.SourceProjectID != projectID || mr.IID <= 0 {
				continue
			}
			mr.Title = displaytext.Sanitize(mr.Title)
			mr.WebURL = gitLabSafeURL(gate.Host, mr.WebURL)
			mrs = append(mrs, mr.GitLabMergeRequest)
		}
		if len(batch) < gitLabPageSize {
			return mrs, nil
		}
	}
	return mrs, nil
}

func gitLabGetJSON(ctx context.Context, gate glabGate, endpoint string, into any) (http.Header, error) {
	out, runErr := glabExec(ctx, glabTimeout, gate.Dir, gate.executable, false, gitLabAPIArgs(gate, endpoint)...)
	status, headers, body, parseErr := gitLabResponse(out)
	if parseErr == nil && (status < 200 || status >= 300) {
		return nil, gitLabResponseError(status, headers)
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return nil, runErr
		}
		return nil, gitLabCLIError(out)
	}
	if parseErr != nil || json.Unmarshal([]byte(body), into) != nil {
		return nil, errors.New("GitLab returned an invalid or oversized response")
	}
	return headers, nil
}

func gitLabAPIArgs(gate glabGate, endpoint string) []string {
	return []string{"api", endpoint, "--hostname", gate.Host, "--method", "GET", "--include"}
}

func gitLabResponse(out string) (int, http.Header, string, error) {
	end, width := strings.Index(out, "\r\n\r\n"), 4
	if end < 0 {
		end, width = strings.Index(out, "\n\n"), 2
	}
	if end < 0 || end+width > gitLabHeaderMax {
		return 0, nil, "", errors.New("invalid GitLab response headers")
	}
	// ReadResponse handles header syntax; do not use its body reader because glab
	// has already decoded HTTP transfer encoding before printing the body.
	response, err := http.ReadResponse(bufio.NewReader(strings.NewReader(out[:end+width])), nil)
	if err != nil {
		return 0, nil, "", errors.New("invalid GitLab response headers")
	}
	return response.StatusCode, response.Header, out[end+width:], nil
}

func gitLabResponseError(status int, headers http.Header) *gitLabAPIError {
	err := &gitLabAPIError{StatusCode: status}
	if seconds, parseErr := strconv.Atoi(headers.Get("Retry-After")); parseErr == nil && seconds > 0 {
		err.RetryAfter = time.Duration(min(seconds, 3600)) * time.Second
	} else if at, parseErr := http.ParseTime(headers.Get("Retry-After")); parseErr == nil {
		err.RetryAfter = min(max(time.Until(at), 0), time.Hour)
	}
	return err
}

func gitLabCLIError(out string) error {
	// Some glab versions print only their safe status marker for failed requests,
	// without --include headers. Classify it without returning any CLI text.
	for _, code := range []int{401, 403, 404, 429} {
		if strings.Contains(out, fmt.Sprintf("(HTTP %d)", code)) {
			return &gitLabAPIError{StatusCode: code}
		}
	}
	return &gitLabAPIError{}
}

func gitLabValidGate(gate glabGate) bool {
	u, err := url.Parse("https://" + gate.Host)
	return err == nil && gate.executable != "" && gate.Dir != "" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" &&
		strings.Contains(gate.Path, "/") && !strings.ContainsAny(gate.Path, "\x00\r\n?#\\")
}

func gitLabValidSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func gitLabSafeURL(host, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, host) || u.User != nil {
		return ""
	}
	return u.String()
}

// glabExec retains the start of combined output. Traces instead need a bounded
// tail, with stderr kept separate so diagnostics never become job evidence.
func readGitLabJobTrace(ctx context.Context, gate glabGate, projectID, jobID int64) (GitLabJobTrace, error) {
	if !gitLabValidGate(gate) || projectID <= 0 || jobID <= 0 {
		return GitLabJobTrace{}, errors.New("GitLab requires a valid project and job")
	}
	ctx, cancel := context.WithTimeout(ctx, glabTimeout)
	defer cancel()
	endpoint := fmt.Sprintf("projects/%d/jobs/%d/trace", projectID, jobID)
	cmd := exec.CommandContext(ctx, gate.executable, gitLabAPIArgs(gate, endpoint)...) // #nosec G204 -- resolved glab, fixed GET endpoint and numeric IDs, no shell.
	cmd.Dir, cmd.Env = gate.Dir, append(os.Environ(), "NO_COLOR=1")
	cmd.WaitDelay = time.Second
	out := gitLabTraceOutput{tail: tailBuffer{limit: gitLabTraceLimit}}
	var diagnostic glabOutput
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	err := cmd.Run()
	if ctx.Err() != nil {
		return GitLabJobTrace{}, ctx.Err()
	}
	if out.status != 0 && (out.status < 200 || out.status >= 300) {
		return GitLabJobTrace{}, gitLabResponseError(out.status, out.headers)
	}
	if err != nil {
		return GitLabJobTrace{}, gitLabCLIError(diagnostic.buf.String())
	}
	if !out.parsed || out.err != nil {
		return GitLabJobTrace{}, errors.New("GitLab returned invalid job log headers")
	}
	return GitLabJobTrace{Text: displaytext.SanitizeText(out.tail.String()), Truncated: out.bytes > gitLabTraceLimit}, nil
}

type gitLabTraceOutput struct {
	prefix  []byte
	parsed  bool
	status  int
	headers http.Header
	tail    tailBuffer
	bytes   int64
	err     error
}

func (out *gitLabTraceOutput) Write(p []byte) (int, error) {
	n := len(p)
	if out.err != nil {
		return n, nil
	}
	if !out.parsed {
		// Limit the header buffer independently of the trace size.
		take := min(len(p), gitLabHeaderMax-len(out.prefix))
		out.prefix = append(out.prefix, p[:take]...)
		status, headers, body, err := gitLabResponse(string(out.prefix))
		if err != nil {
			if len(out.prefix) == gitLabHeaderMax {
				out.err = err
			}
			return n, nil
		}
		out.parsed, out.status, out.headers = true, status, headers
		out.bytes += int64(len(body))
		_, _ = io.WriteString(&out.tail, body)
		out.prefix = nil
		p = p[take:]
	}
	out.bytes += int64(len(p))
	_, _ = out.tail.Write(p)
	return n, nil
}
