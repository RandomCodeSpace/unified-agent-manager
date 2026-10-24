package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// GitLab items a Thread's conversation names (merge requests, work items,
// pipelines and jobs) are read here for the side panel. Reads are scoped to
// the Thread's own signed-in GitLab origin: its project, and for epics the
// project's ancestor groups. Nothing here writes to GitLab.

const (
	gitLabItemRefLimit = 20
	gitLabItemTTL      = 15 * time.Second
	// gitLabItemKeep is how long a read state stays as the last known one
	// when GitLab rate limits the next read.
	gitLabItemKeep  = 30 * time.Minute
	gitLabDescLimit = 16 << 10
)

// gitLabWorkItemQuery is the one GraphQL query the server runs, read-only,
// for work items the REST API does not describe (epics, tasks). Agents never
// run GraphQL; the shell rules refuse glab api.
const gitLabWorkItemQuery = `query($path: ID!, $iid: String!) { namespace(fullPath: $path) { workItem(iid: $iid) { iid title state webUrl createdAt author { name username } workItemType { name } widgets { type ... on WorkItemWidgetDescription { description } ... on WorkItemWidgetLabels { labels(first: 20) { nodes { title } } } ... on WorkItemWidgetAssignees { assignees(first: 10) { nodes { name username } } } ... on WorkItemWidgetMilestone { milestone { title } } ... on WorkItemWidgetHierarchy { parent { iid title state webUrl workItemType { name } namespace { fullPath } } children(first: 20) { count nodes { iid title state webUrl workItemType { name } namespace { fullPath } } } } } } } }`

// GitLabItemSummary is one item's state word for the conversation's strip.
type GitLabItemSummary struct {
	Ref        string `json:"ref"`
	Kind       string `json:"kind"`
	Title      string `json:"title,omitempty"`
	State      string `json:"state,omitempty"`
	Draft      bool   `json:"draft,omitempty"`
	PipelineID int64  `json:"pipeline_id,omitempty"`
	// PipelineState is a merge request's head pipeline status.
	PipelineState string `json:"pipeline_state,omitempty"`
	WebURL        string `json:"web_url,omitempty"`
	Error         string `json:"error,omitempty"`
	// Stale marks the last known state, kept while GitLab rate limits reads.
	Stale bool `json:"stale,omitempty"`
	// FailureKind and FailureDetail say what a failed job failed on
	// ("Tests failed", "2 tests"); see gitlab_failure.go.
	FailureKind   string `json:"failure_kind,omitempty"`
	FailureDetail string `json:"failure_detail,omitempty"`
	job           *gitLabProjectJob
	failure       *store.WebGitLabFailure
}

// GitLabWorkItemLink is a parent or child of a work item. Ref is set when
// the item is in the Thread's project or one of its groups, so it can open.
type GitLabWorkItemLink struct {
	Ref    string `json:"ref,omitempty"`
	IID    int64  `json:"iid"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	State  string `json:"state"`
	WebURL string `json:"web_url,omitempty"`
}

type GitLabWorkItem struct {
	Ref         string               `json:"ref"`
	IID         int64                `json:"iid"`
	Kind        string               `json:"kind"`
	Title       string               `json:"title"`
	State       string               `json:"state"`
	WebURL      string               `json:"web_url,omitempty"`
	Author      *GitLabReviewAuthor  `json:"author,omitempty"`
	CreatedAt   string               `json:"created_at,omitempty"`
	Labels      []string             `json:"labels"`
	Assignees   []GitLabReviewAuthor `json:"assignees"`
	Milestone   string               `json:"milestone,omitempty"`
	Description string               `json:"description,omitempty"`
	Parent      *GitLabWorkItemLink  `json:"parent,omitempty"`
	Children    []GitLabWorkItemLink `json:"children"`
	ChildCount  int                  `json:"child_count"`
}

type GitLabPipelineDetail struct {
	Pipeline GitLabPipeline `json:"pipeline"`
	Jobs     []GitLabJob    `json:"jobs"`
}

// gitLabItemRef is a parsed "kind:iid" or "epic:iid:group/path" reference.
type gitLabItemRef struct {
	kind  string
	id    int64
	group string
}

func (r gitLabItemRef) String() string {
	out := r.kind + ":" + strconv.FormatInt(r.id, 10)
	if r.group != "" {
		out += ":" + r.group
	}
	return out
}

// parseGitLabItemRef accepts only the Thread project's items and, for epics,
// a group that contains the project. An epic without a group is in the
// project's parent group, as GitLab reads "&iid".
func parseGitLabItemRef(raw, project string) (gitLabItemRef, bool) {
	kind, rest, _ := strings.Cut(raw, ":")
	id, group, _ := strings.Cut(rest, ":")
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return gitLabItemRef{}, false
	}
	switch kind {
	case "mr", "issue", "pipeline", "job":
		return gitLabItemRef{kind: kind, id: n}, group == ""
	case "epic":
		if group == "" {
			group = project[:max(strings.LastIndex(project, "/"), 0)]
		}
		return gitLabItemRef{kind: kind, id: n, group: group}, gitLabAncestorGroup(group, project)
	}
	return gitLabItemRef{}, false
}

func gitLabAncestorGroup(group, project string) bool {
	return group != "" && strings.HasPrefix(strings.ToLower(project), strings.ToLower(group)+"/") && !strings.ContainsAny(group, "\x00\r\n?#\\")
}

type gitLabCachedItem struct {
	at      time.Time
	summary GitLabItemSummary
}

// gitLabItemGate is the Thread's GitLab origin once glab is signed in to its
// host. The caller holds gitlab.work.
func (m *Manager) gitLabItemGate(ctx context.Context, id string) (glabGate, error) {
	t, err := m.gitTarget(id)
	if err != nil {
		return glabGate{}, err
	}
	gate, ok := glabOrigin(ctx, t.workdir)
	if !ok || !m.glab.authenticated(ctx, gate) {
		return glabGate{}, newError(http.StatusConflict, "This Thread has no signed-in GitLab origin.")
	}
	return gate, m.gitLabBackoff(gate.Host)
}

// gitLabProjectID reads the origin's numeric project ID once per process.
// The caller holds gitlab.work.
func (m *Manager) gitLabProjectID(ctx context.Context, gate glabGate) (int64, error) {
	key := gate.Host + "\n" + gate.Path
	if id := m.gitlab.projects[key]; id > 0 {
		return id, nil
	}
	var project struct {
		ID   int64  `json:"id"`
		Path string `json:"path_with_namespace"`
	}
	if _, err := gitLabGetJSON(ctx, gate, "projects/"+url.PathEscape(gate.Path), &project); err != nil {
		return 0, err
	}
	if project.ID <= 0 || !strings.EqualFold(project.Path, gate.Path) {
		return 0, newError(http.StatusConflict, "GitLab returned a different project than the repository origin")
	}
	if m.gitlab.projects == nil {
		m.gitlab.projects = make(map[string]int64)
	}
	m.gitlab.projects[key] = project.ID
	return project.ID, nil
}

// gitLabItems reads the state of up to 20 items. Each read is cached briefly
// and shared by every viewer, so open strips and panels in any number of
// browsers cost GitLab one read per item per gitLabItemTTL. While GitLab rate
// limits reads the last known states come back marked stale.
func (m *Manager) gitLabItems(ctx context.Context, id string, refs []string) ([]GitLabItemSummary, error) {
	if len(refs) == 0 || len(refs) > gitLabItemRefLimit {
		return nil, newError(http.StatusBadRequest, "ask for 1 to %d GitLab items", gitLabItemRefLimit)
	}
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	gate, err := m.gitLabItemGate(ctx, id)
	if err != nil && gate.Host != "" && gitLabLimited(err) {
		return m.staleGitLabItems(gate, refs), nil
	}
	if err != nil {
		return nil, err
	}
	projectID, err := m.gitLabProjectID(ctx, gate)
	if gitLabLimited(err) {
		m.gitLabRememberBackoff(gate.Host, err)
		return m.staleGitLabItems(gate, refs), nil
	}
	if err != nil {
		return nil, gitLabReviewError(err)
	}
	now := m.now()
	out := make([]GitLabItemSummary, 0, len(refs))
	for i, raw := range refs {
		ref, ok := parseGitLabItemRef(raw, gate.Path)
		if !ok {
			out = append(out, GitLabItemSummary{Ref: raw, Error: "Not an item of this Thread's GitLab project."})
			continue
		}
		key := gitLabItemKey(gate, ref)
		cached, found := m.gitlab.items[key]
		if found && now.Sub(cached.at) < gitLabItemTTL {
			cached.summary.Ref = raw
			out = append(out, cached.summary)
			continue
		}
		summary, readErr := readGitLabItemSummary(ctx, gate, projectID, ref)
		summary.Ref = raw
		if gitLabLimited(readErr) {
			m.gitLabRememberBackoff(gate.Host, readErr)
			return append(out, m.staleGitLabItems(gate, refs[i:])...), nil
		}
		if readErr != nil {
			summary.Error = gitLabReviewError(readErr).Error()
		} else {
			if summary.job != nil && summary.State == "failed" {
				m.gitLabJobFailure(ctx, gate, projectID, key, &summary, cached.summary.failure)
			}
			m.rememberGitLabItem(key, now, summary)
		}
		out = append(out, summary)
	}
	return out, nil
}

func gitLabItemKey(gate glabGate, ref gitLabItemRef) string {
	return gate.Host + "\n" + gate.Path + "\n" + ref.String()
}

// gitLabLimited is true for GitLab's 429 and for the backoff it leaves.
func gitLabLimited(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *gitLabAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests
	}
	status, _ := errorStatus(err)
	return status == http.StatusTooManyRequests
}

// staleGitLabItems answers from the last known states without reading
// GitLab. The caller holds gitlab.work.
func (m *Manager) staleGitLabItems(gate glabGate, refs []string) []GitLabItemSummary {
	out := make([]GitLabItemSummary, 0, len(refs))
	for _, raw := range refs {
		summary := GitLabItemSummary{Ref: raw, Error: "GitLab is rate limiting requests. Try again later.", Stale: true}
		if ref, ok := parseGitLabItemRef(raw, gate.Path); !ok {
			summary = GitLabItemSummary{Ref: raw, Error: "Not an item of this Thread's GitLab project."}
		} else if cached, found := m.gitlab.items[gitLabItemKey(gate, ref)]; found {
			summary = cached.summary
			summary.Ref, summary.Stale = raw, true
		}
		out = append(out, summary)
	}
	return out
}

func (m *Manager) rememberGitLabItem(key string, now time.Time, summary GitLabItemSummary) {
	if m.gitlab.items == nil {
		m.gitlab.items = make(map[string]gitLabCachedItem)
	}
	if len(m.gitlab.items) >= 512 {
		for k, cached := range m.gitlab.items {
			if now.Sub(cached.at) >= gitLabItemKeep {
				delete(m.gitlab.items, k)
			}
		}
	}
	if _, found := m.gitlab.items[key]; found || len(m.gitlab.items) < 1024 {
		m.gitlab.items[key] = gitLabCachedItem{at: now, summary: summary}
	}
}

func readGitLabItemSummary(ctx context.Context, gate glabGate, projectID int64, ref gitLabItemRef) (GitLabItemSummary, error) {
	base := "projects/" + strconv.FormatInt(projectID, 10)
	out := GitLabItemSummary{Kind: ref.kind}
	switch ref.kind {
	case "mr":
		var mr gitLabReviewMRResponse
		if _, err := gitLabGetJSON(ctx, gate, fmt.Sprintf("%s/merge_requests/%d", base, ref.id), &mr); err != nil {
			return out, err
		}
		if mr.IID != ref.id || mr.ProjectID != projectID {
			return out, errors.New("GitLab returned a different merge request")
		}
		out.Title, out.State, out.Draft, out.WebURL = displaytext.Sanitize(mr.Title), displaytext.Sanitize(mr.State), mr.Draft, gitLabSafeURL(gate.Host, mr.WebURL)
		if p := mr.HeadPipeline; p != nil && p.ID > 0 {
			out.PipelineID, out.PipelineState = p.ID, displaytext.Sanitize(p.Status)
		}
	case "pipeline":
		var p gitLabPipelineResponse
		if _, err := gitLabGetJSON(ctx, gate, fmt.Sprintf("%s/pipelines/%d", base, ref.id), &p); err != nil {
			return out, err
		}
		if p.ID != ref.id || p.ProjectID != projectID {
			return out, errors.New("GitLab returned a different pipeline")
		}
		out.Title, out.State, out.PipelineID, out.WebURL = displaytext.Sanitize(p.Ref), displaytext.Sanitize(p.Status), p.ID, gitLabSafeURL(gate.Host, p.WebURL)
	case "job":
		job, err := readGitLabJob(ctx, gate, projectID, ref.id)
		if err != nil {
			return out, err
		}
		out.Title, out.State, out.PipelineID, out.WebURL, out.job = job.Name, job.Status, job.pipelineID, job.WebURL, &job
	default:
		item, err := readGitLabWorkItem(ctx, gate, projectID, ref)
		if err != nil {
			return out, err
		}
		out.Kind, out.Title, out.State, out.WebURL = item.Kind, item.Title, item.State, item.WebURL
	}
	return out, nil
}

type gitLabProjectJob struct {
	GitLabJob
	pipelineID int64
	sha        string
}

func readGitLabJob(ctx context.Context, gate glabGate, projectID, jobID int64) (gitLabProjectJob, error) {
	var job struct {
		GitLabJob
		Pipeline gitLabPipelineResponse `json:"pipeline"`
	}
	if _, err := gitLabGetJSON(ctx, gate, fmt.Sprintf("projects/%d/jobs/%d", projectID, jobID), &job); err != nil {
		return gitLabProjectJob{}, err
	}
	if job.ID != jobID || job.Pipeline.ID <= 0 || job.Pipeline.ProjectID != projectID {
		return gitLabProjectJob{}, errors.New("GitLab returned a job outside this project")
	}
	job.Name, job.Stage, job.Status = displaytext.Sanitize(job.Name), displaytext.Sanitize(job.Stage), displaytext.Sanitize(job.Status)
	job.WebURL = gitLabSafeURL(gate.Host, job.WebURL)
	return gitLabProjectJob{GitLabJob: job.GitLabJob, pipelineID: job.Pipeline.ID, sha: job.Pipeline.SHA}, nil
}

// readGitLabWorkItem reads an issue through REST. Epics, tasks and items the
// issues API does not return come from the fixed GraphQL query.
func readGitLabWorkItem(ctx context.Context, gate glabGate, projectID int64, ref gitLabItemRef) (GitLabWorkItem, error) {
	if ref.kind == "epic" {
		return readGitLabWorkItemGraph(ctx, gate, ref.group, ref)
	}
	var issue struct {
		IID         int64                `json:"iid"`
		ProjectID   int64                `json:"project_id"`
		Title       string               `json:"title"`
		State       string               `json:"state"`
		Type        string               `json:"issue_type"`
		WebURL      string               `json:"web_url"`
		Description string               `json:"description"`
		CreatedAt   string               `json:"created_at"`
		Author      *GitLabReviewAuthor  `json:"author"`
		Labels      []string             `json:"labels"`
		Assignees   []GitLabReviewAuthor `json:"assignees"`
		Milestone   *struct {
			Title string `json:"title"`
		} `json:"milestone"`
	}
	_, err := gitLabGetJSON(ctx, gate, fmt.Sprintf("projects/%d/issues/%d", projectID, ref.id), &issue)
	var apiErr *gitLabAPIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return readGitLabWorkItemGraph(ctx, gate, gate.Path, ref)
	}
	if err != nil {
		return GitLabWorkItem{}, err
	}
	if issue.IID != ref.id || issue.ProjectID != projectID {
		return GitLabWorkItem{}, errors.New("GitLab returned a different issue")
	}
	if issue.Type != "" && issue.Type != "issue" && issue.Type != "incident" {
		return readGitLabWorkItemGraph(ctx, gate, gate.Path, ref)
	}
	out := GitLabWorkItem{Ref: ref.String(), IID: issue.IID, Kind: "issue", Title: displaytext.Sanitize(issue.Title), State: displaytext.Sanitize(issue.State), WebURL: gitLabSafeURL(gate.Host, issue.WebURL),
		CreatedAt: displaytext.Sanitize(issue.CreatedAt), Description: gitLabDescription(issue.Description), Labels: []string{}, Assignees: []GitLabReviewAuthor{}, Children: []GitLabWorkItemLink{}}
	if issue.Type == "incident" {
		out.Kind = "incident"
	}
	if issue.Author != nil {
		out.Author = &GitLabReviewAuthor{Name: displaytext.Sanitize(issue.Author.Name), Username: displaytext.Sanitize(issue.Author.Username)}
	}
	for _, label := range issue.Labels[:min(len(issue.Labels), 20)] {
		out.Labels = append(out.Labels, displaytext.Sanitize(label))
	}
	for _, user := range issue.Assignees[:min(len(issue.Assignees), 10)] {
		out.Assignees = append(out.Assignees, GitLabReviewAuthor{Name: displaytext.Sanitize(user.Name), Username: displaytext.Sanitize(user.Username)})
	}
	if issue.Milestone != nil {
		out.Milestone = displaytext.Sanitize(issue.Milestone.Title)
	}
	return out, nil
}

func gitLabDescription(text string) string {
	text = displaytext.SanitizeText(text)
	if len(text) > gitLabDescLimit {
		text = clampText(text, gitLabDescLimit-len(truncatedMarker))
	}
	return text
}

type gitLabGraphLink struct {
	IID          string `json:"iid"`
	Title        string `json:"title"`
	State        string `json:"state"`
	WebURL       string `json:"webUrl"`
	WorkItemType struct {
		Name string `json:"name"`
	} `json:"workItemType"`
	Namespace struct {
		FullPath string `json:"fullPath"`
	} `json:"namespace"`
}

type gitLabGraphUser struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

type gitLabGraphItem struct {
	gitLabGraphLink
	CreatedAt string           `json:"createdAt"`
	Author    *gitLabGraphUser `json:"author"`
	Widgets   []struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Labels      *struct {
			Nodes []struct {
				Title string `json:"title"`
			} `json:"nodes"`
		} `json:"labels"`
		Assignees *struct {
			Nodes []gitLabGraphUser `json:"nodes"`
		} `json:"assignees"`
		Milestone *struct {
			Title string `json:"title"`
		} `json:"milestone"`
		Parent   *gitLabGraphLink `json:"parent"`
		Children *struct {
			Count int               `json:"count"`
			Nodes []gitLabGraphLink `json:"nodes"`
		} `json:"children"`
	} `json:"widgets"`
}

// readGitLabWorkItemGraph runs the fixed query in a namespace the caller has
// already scoped to the Thread's project or one of its groups.
func readGitLabWorkItemGraph(ctx context.Context, gate glabGate, namespace string, ref gitLabItemRef) (GitLabWorkItem, error) {
	if !gitLabValidGate(gate) || (namespace != gate.Path && !gitLabAncestorGroup(namespace, gate.Path)) {
		return GitLabWorkItem{}, errors.New("GitLab requires this Thread's project or one of its groups")
	}
	raw, runErr := glabExec(ctx, glabTimeout, gate.Dir, gate.executable, false, "api", "graphql", "--hostname", gate.Host, "--method", "POST", "--include",
		"--raw-field", "query="+gitLabWorkItemQuery, "--raw-field", "path="+namespace, "--raw-field", "iid="+strconv.FormatInt(ref.id, 10))
	status, headers, body, parseErr := gitLabResponse(raw)
	if parseErr == nil && (status < 200 || status >= 300) {
		return GitLabWorkItem{}, gitLabResponseError(status, headers)
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return GitLabWorkItem{}, runErr
		}
		return GitLabWorkItem{}, gitLabCLIError(raw)
	}
	var response struct {
		Data struct {
			Namespace *struct {
				WorkItem *gitLabGraphItem `json:"workItem"`
			} `json:"namespace"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if parseErr != nil || json.Unmarshal([]byte(body), &response) != nil {
		return GitLabWorkItem{}, errors.New("GitLab returned an invalid or oversized response")
	}
	if len(response.Errors) > 0 || response.Data.Namespace == nil || response.Data.Namespace.WorkItem == nil {
		return GitLabWorkItem{}, &gitLabAPIError{StatusCode: http.StatusNotFound}
	}
	item := response.Data.Namespace.WorkItem
	if item.IID != strconv.FormatInt(ref.id, 10) {
		return GitLabWorkItem{}, errors.New("GitLab returned a different work item")
	}
	link := gitLabWorkItemLink(gate, item.gitLabGraphLink)
	out := GitLabWorkItem{Ref: ref.String(), IID: ref.id, Kind: link.Kind, Title: link.Title, State: link.State, WebURL: link.WebURL, CreatedAt: displaytext.Sanitize(item.CreatedAt), Labels: []string{}, Assignees: []GitLabReviewAuthor{}, Children: []GitLabWorkItemLink{}}
	if item.Author != nil {
		out.Author = &GitLabReviewAuthor{Name: displaytext.Sanitize(item.Author.Name), Username: displaytext.Sanitize(item.Author.Username)}
	}
	for _, w := range item.Widgets {
		switch {
		case w.Type == "DESCRIPTION":
			out.Description = gitLabDescription(w.Description)
		case w.Labels != nil:
			for _, label := range w.Labels.Nodes[:min(len(w.Labels.Nodes), 20)] {
				out.Labels = append(out.Labels, displaytext.Sanitize(label.Title))
			}
		case w.Assignees != nil:
			for _, user := range w.Assignees.Nodes[:min(len(w.Assignees.Nodes), 10)] {
				out.Assignees = append(out.Assignees, GitLabReviewAuthor{Name: displaytext.Sanitize(user.Name), Username: displaytext.Sanitize(user.Username)})
			}
		case w.Milestone != nil:
			out.Milestone = displaytext.Sanitize(w.Milestone.Title)
		case w.Type == "HIERARCHY":
			if w.Parent != nil {
				parent := gitLabWorkItemLink(gate, *w.Parent)
				out.Parent = &parent
			}
			if w.Children != nil {
				out.ChildCount = w.Children.Count
				for _, child := range w.Children.Nodes[:min(len(w.Children.Nodes), 20)] {
					out.Children = append(out.Children, gitLabWorkItemLink(gate, child))
				}
			}
		}
	}
	return out, nil
}

// gitLabWorkItemLink maps a GraphQL work item to its display fields and, when
// it belongs to this Thread's project or groups, a ref that can open it.
func gitLabWorkItemLink(gate glabGate, link gitLabGraphLink) GitLabWorkItemLink {
	iid, _ := strconv.ParseInt(link.IID, 10, 64)
	kind := strings.ToLower(displaytext.Sanitize(link.WorkItemType.Name))
	state := "opened"
	if strings.EqualFold(link.State, "CLOSED") {
		state = "closed"
	}
	out := GitLabWorkItemLink{IID: iid, Kind: kind, Title: displaytext.Sanitize(link.Title), State: state, WebURL: gitLabSafeURL(gate.Host, link.WebURL)}
	path := link.Namespace.FullPath
	switch {
	case iid <= 0:
	case strings.EqualFold(path, gate.Path):
		out.Ref = gitLabItemRef{kind: "issue", id: iid}.String()
	case gitLabAncestorGroup(path, gate.Path):
		out.Ref = gitLabItemRef{kind: "epic", id: iid, group: path}.String()
	}
	return out
}

func (m *Manager) gitLabWorkItem(ctx context.Context, id, raw string) (out GitLabWorkItem, err error) {
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	gate, err := m.gitLabItemGate(ctx, id)
	if err != nil {
		return out, err
	}
	defer func() {
		m.gitLabRememberBackoff(gate.Host, err)
		err = gitLabReviewError(err)
	}()
	ref, ok := parseGitLabItemRef(raw, gate.Path)
	if !ok || (ref.kind != "issue" && ref.kind != "epic") {
		return out, newError(http.StatusBadRequest, "invalid work item")
	}
	projectID, err := m.gitLabProjectID(ctx, gate)
	if err != nil {
		return out, err
	}
	return readGitLabWorkItem(ctx, gate, projectID, ref)
}

func (m *Manager) gitLabPipelineDetail(ctx context.Context, id string, pipelineID int64) (out GitLabPipelineDetail, err error) {
	if pipelineID <= 0 {
		return out, newError(http.StatusBadRequest, "invalid pipeline ID")
	}
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	gate, err := m.gitLabItemGate(ctx, id)
	if err != nil {
		return out, err
	}
	defer func() {
		m.gitLabRememberBackoff(gate.Host, err)
		err = gitLabReviewError(err)
	}()
	projectID, err := m.gitLabProjectID(ctx, gate)
	if err != nil {
		return out, err
	}
	base := "projects/" + strconv.FormatInt(projectID, 10)
	var pipeline gitLabPipelineResponse
	if _, err = gitLabGetJSON(ctx, gate, fmt.Sprintf("%s/pipelines/%d", base, pipelineID), &pipeline); err != nil {
		return out, err
	}
	if pipeline.ID != pipelineID || pipeline.ProjectID != projectID {
		return out, errors.New("GitLab returned a different pipeline")
	}
	pipeline.Status, pipeline.Ref, pipeline.SHA = displaytext.Sanitize(pipeline.Status), displaytext.Sanitize(pipeline.Ref), displaytext.Sanitize(pipeline.SHA)
	pipeline.WebURL = gitLabSafeURL(gate.Host, pipeline.WebURL)
	jobs, err := gitLabReadJobs(ctx, gate, base, pipeline)
	if err != nil {
		return out, err
	}
	return GitLabPipelineDetail{Pipeline: pipeline.GitLabPipeline, Jobs: jobs}, nil
}

// gitLabProjectReview reads any merge request of the Thread's project and its
// discussions, read-only. Writes stay on the Thread's own commit's MRs.
func (m *Manager) gitLabProjectReview(ctx context.Context, id string, iid int64) (out GitLabReview, err error) {
	out.Discussions = []GitLabDiscussion{}
	if iid <= 0 {
		return out, newError(http.StatusBadRequest, "invalid merge request")
	}
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	gate, err := m.gitLabItemGate(ctx, id)
	if err != nil {
		return out, err
	}
	defer func() {
		m.gitLabRememberBackoff(gate.Host, err)
		err = gitLabReviewError(err)
	}()
	projectID, err := m.gitLabProjectID(ctx, gate)
	if err != nil {
		return out, err
	}
	base := fmt.Sprintf("projects/%d/merge_requests/%d", projectID, iid)
	var mr gitLabReviewMRResponse
	if _, err = gitLabGetJSON(ctx, gate, base, &mr); err != nil {
		return out, err
	}
	if mr.IID != iid || mr.ProjectID != projectID {
		return out, errors.New("GitLab returned a different merge request")
	}
	out.MergeRequest = gitLabReviewMR(gate, mr)
	if err = readGitLabDiscussions(ctx, gate, base, &out); err != nil {
		return out, err
	}
	out.CheckedAt = m.now()
	return out, nil
}

// gitLabProjectTrace reads a job log of any pipeline of the Thread's project.
func (m *Manager) gitLabProjectTrace(ctx context.Context, id string, pipelineID, jobID int64) (out GitLabJobTrace, err error) {
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	gate, err := m.gitLabItemGate(ctx, id)
	if err != nil {
		return out, err
	}
	defer func() {
		m.gitLabRememberBackoff(gate.Host, err)
		err = gitLabReviewError(err)
	}()
	projectID, err := m.gitLabProjectID(ctx, gate)
	if err != nil {
		return out, err
	}
	job, err := readGitLabJob(ctx, gate, projectID, jobID)
	if err != nil {
		return out, err
	}
	if job.pipelineID != pipelineID {
		return out, newError(http.StatusNotFound, "job does not belong to this pipeline")
	}
	return readGitLabJobTrace(ctx, gate, projectID, jobID)
}

// gitLabOrigin names the Thread's signed-in GitLab project, so the
// conversation can tell its items from other links.
func (m *Manager) gitLabOrigin(ctx context.Context, id string) (glabGate, bool) {
	t, err := m.gitTarget(id)
	if err != nil {
		return glabGate{}, false
	}
	gate, ok := glabOrigin(ctx, t.workdir)
	return gate, ok && m.glab.authenticated(ctx, gate)
}

func (s *Server) registerGitLabItemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/gitlab/items", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.m.gitLabItems(r.Context(), r.PathValue("id"), r.URL.Query()["ref"])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("GET /api/sessions/{id}/gitlab/work-items", func(w http.ResponseWriter, r *http.Request) {
		item, err := s.m.gitLabWorkItem(r.Context(), r.PathValue("id"), r.URL.Query().Get("ref"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	mux.HandleFunc("GET /api/sessions/{id}/gitlab/pipelines/{pipeline_id}", func(w http.ResponseWriter, r *http.Request) {
		pipelineID, err := strconv.ParseInt(r.PathValue("pipeline_id"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid pipeline ID")
			return
		}
		detail, err := s.m.gitLabPipelineDetail(r.Context(), r.PathValue("id"), pipelineID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	})
}
