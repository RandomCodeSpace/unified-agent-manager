package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type gitLabItemsFixture struct {
	m   *Manager
	id  string
	bin string
}

// newGitLabItemsFixture serves REST GETs by endpoint and the fixed GraphQL
// query by "graphql:<namespace>:<iid>". auth is glab auth status's exit code.
func newGitLabItemsFixture(t *testing.T, auth int, responses map[string]gitLabFixtureResponse) gitLabItemsFixture {
	t.Helper()
	gitIdentity(t)
	repo := branchRepo(t)
	gitIn(t, repo, "remote", "add", "origin", "https://gitlab.example.com/group/sub/repo.git")
	dir := t.TempDir()
	bin := filepath.Join(dir, "glab")
	var script strings.Builder
	fmt.Fprintf(&script, "#!/bin/sh\nprintf '%%s %%s\\n' \"$1\" \"$2\" >> \"$0.calls\"\n[ \"$1\" = auth ] && exit %d\nkey=\"$2\"\n", auth)
	script.WriteString("if [ \"$2\" = graphql ]; then [ \"$6\" = POST ] && [ \"$#\" = 13 ] || exit 96; key=\"graphql:${11#path=}:${13#iid=}\"; fi\ncase \"$key\" in\n")
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
		fixture := fmt.Sprintf("%s.%d", bin, i)
		if err := os.WriteFile(fixture, fmt.Appendf(nil, "HTTP/2.0 %d %s\r\nContent-Type: application/json\r\n\r\n%s", status, http.StatusText(status), response.body), 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&script, "'%s') cat \"$0.%d\"", endpoint, i)
		if status >= 400 {
			script.WriteString("; exit 1")
		}
		script.WriteString(";;\n")
	}
	script.WriteString("*) exit 98;;\nesac\n")
	if err := os.WriteFile(bin, []byte(script.String()), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prov := agenttest.NewProvider("fake", utilityCaps)
	prov.SetModels(selectionModels(), nil)
	m := startManager(t, openTestStore(t), prov)
	source, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, repo), Name: "Source Thread"})
	if err != nil {
		t.Fatal(err)
	}
	return gitLabItemsFixture{m: m, id: source.ID, bin: bin}
}

func (f gitLabItemsFixture) calls(t *testing.T) []string {
	t.Helper()
	data, _ := os.ReadFile(f.bin + ".calls")
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.HasPrefix(line, "auth ") {
			out = append(out, line)
		}
	}
	return out
}

func gitLabItemResponses() map[string]gitLabFixtureResponse {
	graph := func(iid, kind, state, url, extra string) gitLabFixtureResponse {
		return gitLabFixtureResponse{body: `{"data":{"namespace":{"workItem":{"iid":"` + iid + `","title":"\u001b[31m` + kind + ` title","state":"` + state + `","webUrl":"` + url + `","createdAt":"2026-10-01T00:00:00Z","author":{"name":"Priya","username":"priya"},"workItemType":{"name":"` + kind + `"},"widgets":[` + extra + `]}}}}`}
	}
	return map[string]gitLabFixtureResponse{
		"projects/group%2Fsub%2Frepo":                                             {body: `{"id":42,"path_with_namespace":"group/sub/repo"}`},
		"projects/42/merge_requests/2":                                            {body: `{"id":700,"iid":2,"project_id":42,"title":"Draft: demo","state":"opened","draft":true,"web_url":"https://gitlab.example.com/group/sub/repo/-/merge_requests/2","source_branch":"demo","target_branch":"main","sha":"` + strings.Repeat("a", 40) + `","author":{"name":"Amit","username":"amit"},"head_pipeline":{"id":91,"status":"failed","ref":"demo","sha":"` + strings.Repeat("a", 40) + `","web_url":"https://evil.example.com/p/91"}}`},
		"projects/42/merge_requests/2/discussions?per_page=100&page=1":            {body: `[{"id":"` + strings.Repeat("d", 40) + `","individual_note":false,"notes":[{"id":5,"body":"Return 503","author":{"name":"Priya","username":"priya"},"resolvable":true,"resolved":false}]}]`},
		"projects/42/issues/14":                                                   {body: `{"iid":14,"project_id":42,"title":"Health endpoint","state":"opened","issue_type":"issue","web_url":"https://gitlab.example.com/group/sub/repo/-/issues/14","description":"GET /healthz","labels":["bug"],"assignees":[{"name":"Amit","username":"amit"}],"milestone":{"title":"v0.4"}}`},
		"projects/42/issues/22":                                                   {body: `{"iid":22,"project_id":42,"title":"Timeout test","state":"opened","issue_type":"task"}`},
		"projects/42/pipelines/91":                                                {body: gitLabFixtureJSON(gitLabFixturePipeline(gitLabFixtureSHA, "demo"))},
		"projects/42/pipelines/91/jobs?include_retried=false&per_page=100&page=1": {body: gitLabFixtureJobs("demo", 1, 1)},
		"projects/42/jobs/1":                                                      {body: `{"id":1,"name":"lint","stage":"test","status":"failed","web_url":"https://gitlab.example.com/group/sub/repo/-/jobs/1","pipeline":{"id":91,"project_id":42}}`},
		"projects/42/jobs/2":                                                      {body: `{"id":2,"name":"other","stage":"test","status":"success","pipeline":{"id":92,"project_id":42}}`},
		"projects/42/jobs/1/trace":                                                {body: "lint failed\n"},
		"graphql:group/sub/repo:22": graph("22", "Task", "OPEN", "https://gitlab.example.com/group/sub/repo/-/work_items/22",
			`{"type":"HIERARCHY","parent":{"iid":"14","title":"Health endpoint","state":"OPEN","workItemType":{"name":"Issue"},"namespace":{"fullPath":"group/sub/repo"}},"children":{"count":0,"nodes":[]}}`),
		"graphql:group/sub:3": graph("3", "Epic", "OPEN", "https://gitlab.example.com/groups/group/sub/-/work_items/3",
			`{"type":"DESCRIPTION","description":"Readiness"},{"type":"LABELS","labels":{"nodes":[{"title":"health"}]}},{"type":"HIERARCHY","parent":null,"children":{"count":3,"nodes":[{"iid":"14","title":"Health endpoint","state":"OPEN","workItemType":{"name":"Issue"},"namespace":{"fullPath":"group/sub/repo"}},{"iid":"4","title":"Sub-epic","state":"CLOSED","workItemType":{"name":"Epic"},"namespace":{"fullPath":"group/sub"}},{"iid":"9","title":"Elsewhere","state":"OPEN","workItemType":{"name":"Issue"},"namespace":{"fullPath":"other/repo"}}]}}`),
	}
}

func TestGitLabItemsReadStateWordsWithinTheThreadProject(t *testing.T) {
	f := newGitLabItemsFixture(t, 0, gitLabItemResponses())
	refs := []string{"mr:2", "issue:14", "issue:22", "epic:3", "pipeline:91", "job:1", "epic:3:other", "mr:x", "issue:15"}
	items, err := f.m.gitLabItems(t.Context(), f.id, refs)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ kind, state, title string }{
		{"mr", "opened", "Draft: demo"}, {"issue", "opened", "Health endpoint"}, {"task", "opened", "Task title"}, {"epic", "opened", "Epic title"},
		{"pipeline", "failed", "demo"}, {"job", "failed", "lint"},
	}
	for i, w := range want {
		got := items[i]
		if got.Ref != refs[i] || got.Kind != w.kind || got.State != w.state || got.Title != w.title || got.Error != "" {
			t.Fatalf("item %d = %+v, want %+v", i, got, w)
		}
	}
	if !items[0].Draft || items[5].PipelineID != 91 {
		t.Fatalf("draft or job pipeline missing: %+v", items)
	}
	for _, i := range []int{6, 7, 8} {
		if items[i].Error == "" {
			t.Fatalf("out-of-scope or unreadable ref %q read: %+v", refs[i], items[i])
		}
	}
	before := len(f.calls(t))
	if _, err := f.m.gitLabItems(t.Context(), f.id, refs[:6]); err != nil {
		t.Fatal(err)
	}
	if after := len(f.calls(t)); after != before {
		t.Fatalf("cached items were read again: %d calls, want %d", after, before)
	}
	if _, err := f.m.gitLabItems(t.Context(), f.id, nil); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("empty ref list = %v", err)
	}
}

func TestGitLabItemsShareOneReadAndKeepTheLastStateWhileRateLimited(t *testing.T) {
	responses := gitLabItemResponses()
	f := newGitLabItemsFixture(t, 0, responses)
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	f.m.now = func() time.Time { return at }
	refs := []string{"mr:2", "pipeline:91"}
	read := func() []GitLabItemSummary {
		t.Helper()
		items, err := f.m.gitLabItems(t.Context(), f.id, refs)
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	items := read()
	if items[0].PipelineID != 91 || items[0].PipelineState != "failed" || items[0].Stale {
		t.Fatalf("merge request head pipeline = %+v", items[0])
	}
	base := len(f.calls(t))
	// Several viewers inside the cache window cost GitLab nothing more.
	at = at.Add(10 * time.Second)
	for range 3 {
		read()
	}
	if got := len(f.calls(t)); got != base {
		t.Fatalf("viewers within %s read GitLab again: %d calls, want %d", gitLabItemTTL, got, base)
	}
	at = at.Add(6 * time.Second)
	read()
	if got := len(f.calls(t)); got != base+2 {
		t.Fatalf("expired items: %d calls, want %d", got, base+2)
	}

	// GitLab rate limits the next read: the last states come back stale, never blank.
	keys := make([]string, 0, len(responses))
	for endpoint := range responses {
		keys = append(keys, endpoint)
	}
	sort.Strings(keys)
	fixture := fmt.Sprintf("%s.%d", f.bin, slices.Index(keys, "projects/42/merge_requests/2"))
	if err := os.WriteFile(fixture, []byte("HTTP/2.0 429 Too Many Requests\r\nRetry-After: 120\r\n\r\n{}"), 0600); err != nil {
		t.Fatal(err)
	}
	at = at.Add(gitLabItemTTL)
	items = read()
	for i, want := range []string{"opened", "failed"} {
		if !items[i].Stale || items[i].State != want || items[i].Error != "" {
			t.Fatalf("rate limited item %d = %+v, want last state %q marked stale", i, items[i], want)
		}
	}
	if items[0].PipelineState != "failed" {
		t.Fatalf("stale merge request lost its pipeline: %+v", items[0])
	}
	limited := len(f.calls(t))
	if limited != base+3 {
		t.Fatalf("rate limited pass: %d calls, want %d (the limited read only)", limited, base+3)
	}
	// While GitLab asks to wait, nothing is read; an item never read says so.
	at = at.Add(time.Minute)
	refs = []string{"mr:2", "issue:14"}
	items = read()
	if !items[0].Stale || items[0].State != "opened" || !items[1].Stale || items[1].Error == "" || items[1].State != "" {
		t.Fatalf("backing off = %+v", items)
	}
	if got := len(f.calls(t)); got != limited {
		t.Fatalf("read GitLab while backing off: %d calls, want %d", got, limited)
	}
}

func TestGitLabItemsNeedSignedInOrigin(t *testing.T) {
	f := newGitLabItemsFixture(t, 1, gitLabItemResponses())
	if _, err := f.m.gitLabItems(t.Context(), f.id, []string{"mr:2"}); statusOf(err) != http.StatusConflict {
		t.Fatalf("signed-out items = %v", err)
	}
	if _, ok := f.m.gitLabOrigin(t.Context(), f.id); ok {
		t.Fatal("signed-out origin offered")
	}
	if calls := f.calls(t); len(calls) != 0 {
		t.Fatalf("signed-out read reached GitLab: %v", calls)
	}
}

func TestGitLabWorkItemDetail(t *testing.T) {
	f := newGitLabItemsFixture(t, 0, gitLabItemResponses())
	issue, err := f.m.gitLabWorkItem(t.Context(), f.id, "issue:14")
	if err != nil || issue.Kind != "issue" || issue.Milestone != "v0.4" || len(issue.Labels) != 1 || len(issue.Assignees) != 1 || issue.Description != "GET /healthz" {
		t.Fatalf("issue = %+v, %v", issue, err)
	}
	task, err := f.m.gitLabWorkItem(t.Context(), f.id, "issue:22")
	if err != nil || task.Kind != "task" || task.Parent == nil || task.Parent.Ref != "issue:14" {
		t.Fatalf("task = %+v, %v", task, err)
	}
	epic, err := f.m.gitLabWorkItem(t.Context(), f.id, "epic:3:group/sub")
	if err != nil || epic.Kind != "epic" || epic.ChildCount != 3 || len(epic.Children) != 3 || epic.Description != "Readiness" || epic.Labels[0] != "health" {
		t.Fatalf("epic = %+v, %v", epic, err)
	}
	if refs := []string{epic.Children[0].Ref, epic.Children[1].Ref, epic.Children[2].Ref}; refs[0] != "issue:14" || refs[1] != "epic:4:group/sub" || refs[2] != "" || epic.Children[1].State != "closed" {
		t.Fatalf("child refs = %v (%+v)", refs, epic.Children)
	}
	for _, ref := range []string{"mr:2", "epic:3:group/sub/repo2", "epic:3:elsewhere"} {
		if _, err := f.m.gitLabWorkItem(t.Context(), f.id, ref); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("work item %q = %v", ref, err)
		}
	}
}

func TestGitLabProjectReviewPipelineAndTrace(t *testing.T) {
	f := newGitLabItemsFixture(t, 0, gitLabItemResponses())
	review, err := f.m.gitLabProjectReview(t.Context(), f.id, 2)
	if err != nil || review.MergeRequest.IID != 2 || len(review.Discussions) != 1 || review.MergeRequest.HeadPipeline == nil || review.MergeRequest.HeadPipeline.ID != 91 {
		t.Fatalf("review = %+v, %v", review, err)
	}
	if review.MergeRequest.HeadPipeline.WebURL != "" || review.MergeRequest.Author == nil || review.MergeRequest.Author.Name != "Amit" {
		t.Fatalf("unsafe or missing MR fields: %+v", review.MergeRequest)
	}
	detail, err := f.m.gitLabPipelineDetail(t.Context(), f.id, 91)
	if err != nil || detail.Pipeline.ID != 91 || len(detail.Jobs) != 1 {
		t.Fatalf("pipeline = %+v, %v", detail, err)
	}
	trace, err := f.m.gitLabProjectTrace(t.Context(), f.id, 91, 1)
	if err != nil || trace.Text != "lint failed\n" {
		t.Fatalf("trace = %+v, %v", trace, err)
	}
	if _, err := f.m.gitLabProjectTrace(t.Context(), f.id, 91, 2); statusOf(err) != http.StatusNotFound {
		t.Fatalf("job of another pipeline = %v", err)
	}
}

func TestParseGitLabItemRef(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{"mr:2", "mr:2", true},
		{"issue:14", "issue:14", true},
		{"pipeline:91", "pipeline:91", true},
		{"job:7", "job:7", true},
		{"epic:3", "epic:3:group/sub", true},
		{"epic:3:group", "epic:3:group", true},
		{"epic:3:group/sub/repo", "", false},
		{"epic:3:grou", "", false},
		{"mr:2:group", "", false},
		{"mr:0", "", false},
		{"mr:-1", "", false},
		{"commit:2", "", false},
		{"issue", "", false},
	} {
		ref, ok := parseGitLabItemRef(tc.raw, "group/sub/repo")
		if ok != tc.ok || (ok && ref.String() != tc.want) {
			t.Errorf("%q = %v %v, want %q %v", tc.raw, ref.String(), ok, tc.want, tc.ok)
		}
	}
}

func TestGitLabItemRoutesRegistered(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	for _, path := range []string{
		"/api/sessions/thread/gitlab/items?ref=mr:2",
		"/api/sessions/thread/gitlab/work-items?ref=issue:14",
		"/api/sessions/thread/gitlab/pipelines/91",
	} {
		_, pattern := ts.srv.mux.Handler(httptest.NewRequest("GET", path, nil))
		if !connectedWorkloadPatternAllowed(pattern) || connectedAccountGated(pattern) {
			t.Fatalf("item route %s is not open to connected instances: %q", path, pattern)
		}
	}
}
