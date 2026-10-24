package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type gitLabWorkflowFixture struct {
	m      *Manager
	prov   *agenttest.Provider
	st     *store.Store
	repo   string
	sha    string
	source SessionSummary
	gate   glabGate
}

func newGitLabWorkflowFixture(t *testing.T, customize ...func(map[string]gitLabFixtureResponse)) gitLabWorkflowFixture {
	t.Helper()
	gitIdentity(t)
	repo := branchRepo(t)
	gitIn(t, repo, "remote", "add", "origin", "https://gitlab.example.com/group/sub/repo.git")
	sha := gitOutput(t, repo, "rev-parse", "HEAD")
	responses := make(map[string]gitLabFixtureResponse)
	for endpoint, response := range gitLabClientResponses("main") {
		response.body = strings.ReplaceAll(response.body, gitLabFixtureSHA, sha)
		responses[strings.ReplaceAll(endpoint, gitLabFixtureSHA, sha)] = response
	}
	responses["projects/42/jobs/1/trace"] = gitLabFixtureResponse{body: "FAIL: fixture test\nexpected ready, got pending\n"}
	for _, change := range customize {
		change(responses)
	}
	gate := gitLabClientFake(t, responses)
	t.Setenv("PATH", filepath.Dir(gate.executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	prov := agenttest.NewProvider("fake", utilityCaps)
	prov.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	off := false
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "b"}, SuggestReplies: &off}); err != nil {
		t.Fatal(err)
	}
	source, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, repo), Name: "Source Task"})
	if err != nil {
		t.Fatal(err)
	}
	return gitLabWorkflowFixture{m: m, prov: prov, st: st, repo: repo, sha: sha, source: source, gate: gate}
}

func (f gitLabWorkflowFixture) record(t *testing.T) {
	t.Helper()
	if err := f.m.recordGitLabCommit(t.Context(), f.source.ID, f.sha, "main"); err != nil {
		t.Fatal(err)
	}
}

func TestGitLabMonitorCommitScope(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	state, err := f.m.gitLabState(t.Context(), f.source.ID, true)
	if err != nil || !state.Supported || state.Commit != nil || state.Pipeline != nil || state.CanInvestigate {
		t.Fatalf("an external commit was selected: %+v, %v", state, err)
	}
	if _, err := os.Stat(f.gate.executable + ".calls"); !os.IsNotExist(err) {
		t.Fatalf("unowned commit reached GitLab API: %v", err)
	}
	f.record(t)
	state, err = f.m.gitLabState(t.Context(), f.source.ID, true)
	if err != nil || state.Commit == nil || state.Commit.SHA != f.sha || state.Pipeline == nil || !state.CanInvestigate {
		t.Fatalf("owned commit was not selected: %+v, %v", state, err)
	}
	if len(f.prov.Opens()) != 1 || len(f.prov.UtilityRequests()) != 0 {
		t.Fatal("manual monitoring started a model without an investigation request")
	}
	other, err := f.m.Create(CreateRequest{Provider: "fake", ProjectID: f.source.ProjectID, Name: "Other Task"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.m.recordGitLabCommit(t.Context(), other.ID, f.sha, "main"); err != nil {
		t.Fatal(err)
	}
	state, err = f.m.gitLabState(t.Context(), other.ID, true)
	if err != nil || state.Commit != nil || state.Pipeline != nil {
		t.Fatalf("another Task claimed the commit: %+v, %v", state, err)
	}
	if _, err := f.m.investigateGitLab(t.Context(), other.ID, f.sha, 91); statusOf(err) != http.StatusConflict {
		t.Fatalf("another Task investigated the commit: %v", err)
	}
	if _, err := f.m.investigateGitLab(t.Context(), f.source.ID, strings.Repeat("a", 40), 91); statusOf(err) != http.StatusConflict {
		t.Fatalf("unowned SHA was investigated: %v", err)
	}
	gitIn(t, f.repo, "switch", "-q", "-c", "other")
	state, err = f.m.gitLabState(t.Context(), f.source.ID, false)
	if err != nil || state.Commit != nil || state.Pipeline != nil {
		t.Fatalf("cached pipeline survived a branch change: %+v, %v", state, err)
	}
	gitIn(t, f.repo, "switch", "-q", "main")
	gitIn(t, f.repo, "remote", "set-url", "origin", "https://gitlab.example.com/group/other.git")
	state, err = f.m.gitLabState(t.Context(), f.source.ID, false)
	if err != nil || state.Commit != nil || state.Pipeline != nil {
		t.Fatalf("cached pipeline survived an origin change: %+v, %v", state, err)
	}
}

func TestGitLabInvestigationConcurrentAutoManualAndRestart(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	f.record(t)
	on := true
	if _, err := f.m.UpdateSettings(SettingsPatch{GitLabAutoInvestigate: &on, TaskDefaults: &TaskDefaults{Provider: "fake", Model: "a", Mode: "yolo"}}); err != nil {
		t.Fatal(err)
	}
	const requests = 8
	type result struct {
		id  string
		err error
	}
	results := make(chan result, requests)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() {
			<-start
			if i%2 == 0 {
				state, err := f.m.gitLabState(t.Context(), f.source.ID, true)
				results <- result{state.InvestigationTaskID, err}
			} else {
				id, err := f.m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91)
				results <- result{id, err}
			}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var investigation string
	for got := range results {
		if got.err != nil || got.id == "" {
			t.Fatalf("investigation failed: %+v", got)
		}
		if investigation == "" {
			investigation = got.id
		}
		if got.id != investigation {
			t.Fatalf("concurrent requests created different Tasks: %s and %s", investigation, got.id)
		}
	}
	if opens := f.prov.Opens(); len(opens) != 2 || opens[1].Model != "b" || opens[1].SessionID != investigation {
		t.Fatalf("expected one Task using Utility model b, got %+v", opens)
	}
	if got := summaryOf(t, f.m, investigation); got.Mode != "safe" {
		t.Fatalf("mechanical investigation inherited unsafe defaults: %+v", got)
	}
	conv := f.prov.Last()
	waitUntil(t, "one investigation prompt", func() bool { return len(conv.Sends()) == 1 })
	if prompt := conv.Sends()[0]; !strings.Contains(prompt, f.sha) || !strings.Contains(prompt, "FAIL: fixture test") || !strings.Contains(prompt, "untrusted data") {
		t.Fatalf("investigation did not receive bounded commit evidence: %q", prompt)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if err := f.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, f.st, f.prov)
	before := len(f.prov.Opens())
	got, err := again.investigateGitLab(t.Context(), f.source.ID, f.sha, 91)
	if err != nil || got != investigation || len(f.prov.Opens()) != before {
		t.Fatalf("restart recreated or replayed investigation: got %s, err %v, opens %d -> %d", got, err, before, len(f.prov.Opens()))
	}
	if _, err := again.Archive(investigation); err != nil {
		t.Fatal(err)
	}
	if err := again.Delete(investigation); err != nil {
		t.Fatal(err)
	}
	state, err := again.gitLabState(t.Context(), f.source.ID, true)
	if err != nil || state.CanInvestigate || state.InvestigationTaskID != "" || !strings.Contains(state.Reason, "already investigated") {
		t.Fatalf("deleted investigation was eligible again: %+v, %v", state, err)
	}
	if _, err := again.investigateGitLab(t.Context(), f.source.ID, f.sha, 91); statusOf(err) != http.StatusConflict {
		t.Fatalf("deleted investigation was recreated: %v", err)
	}
	if len(f.prov.Opens()) != before {
		t.Fatal("deleted investigation reached the provider")
	}
}

func TestGitLabAutoInvestigationSkipsRunnerFailures(t *testing.T) {
	f := newGitLabWorkflowFixture(t, func(responses map[string]gitLabFixtureResponse) {
		const jobs = "projects/42/pipelines/91/jobs?include_retried=false&per_page=100&page=1"
		r := responses[jobs]
		r.body = strings.ReplaceAll(r.body, "script_failure", "runner_system_failure")
		responses[jobs] = r
	})
	f.record(t)
	on := true
	if _, err := f.m.UpdateSettings(SettingsPatch{GitLabAutoInvestigate: &on, TaskDefaults: &TaskDefaults{Provider: "fake", Model: "a", Mode: "yolo"}}); err != nil {
		t.Fatal(err)
	}
	state, err := f.m.gitLabState(t.Context(), f.source.ID, true)
	if err != nil || state.InvestigationTaskID != "" || !state.CanInvestigate || state.AutoInvestigate || len(state.Jobs) != 1 || state.Jobs[0].FailureReason != "runner_system_failure" {
		t.Fatalf("runner failure opened an investigation or lost the manual one: %+v, %v", state, err)
	}
	if cached, err := f.m.gitLabState(t.Context(), f.source.ID, false); err != nil || cached.AutoInvestigate || !cached.CanInvestigate {
		t.Fatalf("cached read of a runner failure hides Investigate: %+v, %v", cached, err)
	}
	if len(f.prov.Opens()) != 1 {
		t.Fatalf("runner failure reached the provider: %+v", f.prov.Opens())
	}
	if id, err := f.m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91); err != nil || id == "" {
		t.Fatalf("manual investigation of a runner failure: %q, %v", id, err)
	}
}

func TestGitLabInvestigationReservationSurvivesFailedOpen(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	f.record(t)
	f.prov.SetOpenError(errors.New("fixture provider unavailable"))
	if _, err := f.m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91); err == nil {
		t.Fatal("provider open failure was ignored")
	}
	cfg, err := f.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	key := gitLabCommitKey("gitlab.example.com", "group/sub/repo", f.sha)
	reserved := cfg.WebGitLab[key].Investigations[gitLabEventKey(42, 91)]
	if reserved.TaskID == "" || reserved.Registered {
		t.Fatalf("failed open receipt = %+v", reserved)
	}
	if err := f.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.prov.SetOpenError(nil)
	again := startManager(t, f.st, f.prov)
	id, err := again.investigateGitLab(t.Context(), f.source.ID, f.sha, 91)
	if err != nil || id != reserved.TaskID {
		t.Fatalf("reservation was not reused after restart: %s, %v", id, err)
	}
	cfg, err = f.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if receipt := cfg.WebGitLab[key].Investigations[gitLabEventKey(42, 91)]; !receipt.Registered || receipt.TaskID != id {
		t.Fatalf("created Task was not durably registered: %+v", receipt)
	}
}

func TestGitLabAutoSettingRoutesAndRestart(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	for _, body := range []string{`{"gitlab_auto_investigate":null}`, `{"gitlab_auto_investigate":"true"}`, `{"gitlab_auto_investigate":1}`, `{"gitlab_auto_investigate":{}}`} {
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid boolean accepted: %s => %d %s", body, w.Code, w.Body)
		}
	}
	if ts.m.Settings().GitLabAutoInvestigate {
		t.Fatal("invalid requests enabled automation")
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"gitlab_auto_investigate":true}`, withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("enable automation = %d %s", w.Code, w.Body)
	}
	if err := ts.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, ts.m.store, ts.prov)
	if !again.Settings().GitLabAutoInvestigate {
		t.Fatal("global automation setting did not survive restart")
	}
	off := false
	if _, err := again.UpdateSettings(SettingsPatch{GitLabAutoInvestigate: &off}); err != nil {
		t.Fatal(err)
	}
	cfg, err := ts.m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebSettings.GitLabAutoInvestigate {
		t.Fatal("disabling global automation was not persisted")
	}
	encoded, err := json.Marshal(again.Settings())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "gitlab_auto_investigate") {
		t.Fatalf("disabled default is not omitted: %s", encoded)
	}
}

func TestGitLabTraceRequiresOwnedPipelineJob(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	f.record(t)
	srv, err := NewServer(ServerConfig{Manager: f.m, Token: testToken, Version: "test", Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: f.m, prov: f.prov}
	base := "/api/sessions/" + f.source.ID + "/gitlab/jobs/"
	for _, tc := range []struct {
		name, suffix string
		want         int
	}{
		{"owned job", "1/trace?pipeline_id=91&commit_sha=" + f.sha, http.StatusOK},
		{"other job", "2/trace?pipeline_id=91&commit_sha=" + f.sha, http.StatusNotFound},
		{"other pipeline", "1/trace?pipeline_id=92&commit_sha=" + f.sha, http.StatusConflict},
		{"other commit", "1/trace?pipeline_id=91&commit_sha=" + strings.Repeat("a", 40), http.StatusConflict},
		{"invalid ID", "0/trace?pipeline_id=91&commit_sha=" + f.sha, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := ts.do(http.MethodGet, base+tc.suffix, "", withCookie(ts))
			if w.Code != tc.want {
				t.Fatalf("trace status = %d %s, want %d", w.Code, w.Body, tc.want)
			}
			if tc.want == http.StatusOK {
				var trace GitLabJobTrace
				if err := json.Unmarshal(w.Body.Bytes(), &trace); err != nil || !strings.Contains(trace.Text, "FAIL: fixture test") {
					t.Fatalf("owned trace = %+v, %v", trace, err)
				}
			}
		})
	}
}

func TestGitLabInvestigationBoundsUnicodeEvidence(t *testing.T) {
	f := newGitLabWorkflowFixture(t, func(responses map[string]gitLabFixtureResponse) {
		const jobsEndpoint = "projects/42/pipelines/91/jobs?include_retried=false&per_page=100&page=1"
		var fixture []map[string]any
		if err := json.Unmarshal([]byte(responses[jobsEndpoint].body), &fixture); err != nil {
			t.Fatal(err)
		}
		jobs := make([]map[string]any, 0, 6)
		for id := 1; id <= 6; id++ {
			job := make(map[string]any, len(fixture[0]))
			for key, value := range fixture[0] {
				job[key] = value
			}
			job["id"], job["name"] = id, strings.Repeat("検", 2200)
			jobs = append(jobs, job)
			responses["projects/42/jobs/"+strconv.Itoa(id)+"/trace"] = gitLabFixtureResponse{
				body: strings.Repeat("検🔬", 20000) + "\nFAIL: Unicode fixture " + strconv.Itoa(id) + "\n",
			}
		}
		responses[jobsEndpoint] = gitLabFixtureResponse{body: gitLabFixtureJSON(jobs)}
	})
	f.record(t)
	id, err := f.m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91)
	if err != nil || id == "" {
		t.Fatalf("oversized pipeline evidence prevented Task creation: %s, %v", id, err)
	}
	conv := f.prov.Last()
	waitUntil(t, "bounded investigation prompt", func() bool { return len(conv.Sends()) == 1 })
	prompt := conv.Sends()[0]
	if len(prompt) >= maxPromptBytes {
		t.Fatalf("prompt is %d bytes, must stay below %d", len(prompt), maxPromptBytes)
	}
	if !utf8.ValidString(prompt) {
		t.Fatal("evidence truncation split a Unicode character")
	}
	if !strings.Contains(prompt, "[truncated by uam]") || !strings.HasSuffix(prompt, "\nEND PIPELINE EVIDENCE") {
		t.Fatal("bounded prompt lost its truncation notice or closing evidence marker")
	}
}

func TestGitLabInvestigationRechecksScopeAfterProviderOpen(t *testing.T) {
	for _, change := range []string{"branch", "origin", "Auto disabled"} {
		t.Run(change, func(t *testing.T) {
			f := newGitLabWorkflowFixture(t)
			f.record(t)
			if err := f.m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			prov := &scriptedProvider{Provider: f.prov}
			m := startManager(t, f.st, prov)
			if change == "Auto disabled" {
				on := true
				if _, err := m.UpdateSettings(SettingsPatch{GitLabAutoInvestigate: &on}); err != nil {
					t.Fatal(err)
				}
			}
			prov.script(func(p *scriptedProvider) {
				p.onOpen = func(agentapi.OpenRequest) error {
					switch change {
					case "branch":
						gitIn(t, f.repo, "switch", "-q", "-c", "changed-during-open")
					case "origin":
						gitIn(t, f.repo, "remote", "set-url", "origin", "https://gitlab.example.com/group/other.git")
					case "Auto disabled":
						off := false
						_, err := m.UpdateSettings(SettingsPatch{GitLabAutoInvestigate: &off})
						return err
					}
					return nil
				}
			})
			if change == "Auto disabled" {
				state, err := m.gitLabState(t.Context(), f.source.ID, true)
				if err != nil || state.Error == "" || state.InvestigationTaskID != "" {
					t.Fatalf("Auto disabled during open was not refused: %+v, %v", state, err)
				}
			} else if _, err := m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91); statusOf(err) != http.StatusConflict {
				t.Fatalf("scope changed during provider open but registration was not refused: %v", err)
			}
			cfg, err := f.st.Load()
			if err != nil {
				t.Fatal(err)
			}
			key := gitLabCommitKey("gitlab.example.com", "group/sub/repo", f.sha)
			receipt := cfg.WebGitLab[key].Investigations[gitLabEventKey(42, 91)]
			if receipt.TaskID == "" || receipt.Registered {
				t.Fatalf("refused investigation lost its unregistered reservation: %+v", receipt)
			}
			if _, exists := cfg.Sessions[store.Key("fake", receipt.TaskID)]; exists || len(m.List()) != 1 {
				t.Fatal("refused investigation left a persisted or visible Task")
			}
			if len(prov.Opens()) != 2 {
				t.Fatalf("expected scope change during investigation open, got %d opens", len(prov.Opens()))
			}
			if conv := prov.Last(); conv.Closes() != 1 || len(conv.Sends()) != 0 {
				t.Fatalf("refused investigation was not closed before prompting: closes %d, sends %d", conv.Closes(), len(conv.Sends()))
			}
		})
	}
}

func TestGitLabCommitRejectsMismatchedReceiptOrigin(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	wrongOrigin := gitLabOriginFingerprint(glabGate{Host: "gitlab.example.com", Path: "group/other"})
	if err := f.m.recordGitLabCommit(t.Context(), f.source.ID, f.sha, "main", wrongOrigin); err != nil {
		t.Fatal(err)
	}
	cfg, err := f.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebGitLab) != 0 {
		t.Fatalf("a receipt for a different origin persisted commit ownership: %+v", cfg.WebGitLab)
	}
	if err := f.m.recordGitLabCommit(t.Context(), f.source.ID, f.sha, "main", gitLabOriginFingerprint(f.gate)); err != nil {
		t.Fatal(err)
	}
	cfg, err = f.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	key := gitLabCommitKey("gitlab.example.com", "group/sub/repo", f.sha)
	if rec := cfg.WebGitLab[key]; len(cfg.WebGitLab) != 1 || rec.TaskID != f.source.ID || rec.SHA != f.sha {
		t.Fatalf("matching receipt origin did not persist ownership: %+v", cfg.WebGitLab)
	}
}

func TestGitLabInvestigationShowsSourcePipeline(t *testing.T) {
	f := newGitLabWorkflowFixture(t, func(responses map[string]gitLabFixtureResponse) {
		for endpoint := range responses {
			if strings.Contains(endpoint, "/merge_requests?") {
				responses[endpoint] = gitLabFixtureResponse{body: gitLabFixtureJSON([]any{
					map[string]any{"iid": 7, "source_branch": "main", "source_project_id": 42, "title": "Source merge request", "state": "opened", "web_url": "https://gitlab.example.com/group/sub/repo/-/merge_requests/7"},
				})}
			}
		}
	})
	f.record(t)
	id, err := f.m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91)
	if err != nil {
		t.Fatal(err)
	}
	assertSource := func(m *Manager, refresh bool) {
		t.Helper()
		state, err := m.gitLabState(t.Context(), id, refresh)
		if err != nil || state.Commit == nil || state.Commit.TaskID != f.source.ID || state.Commit.TaskName != "Source Task" || state.Commit.SHA != f.sha || state.Pipeline == nil || state.Pipeline.ID != 91 || len(state.Jobs) != 1 || len(state.MergeRequests) != 1 || state.MergeRequests[0].IID != 7 {
			t.Fatalf("investigation lost source pipeline context: %+v, %v", state, err)
		}
		if state.CanInvestigate || state.InvestigationTaskID != id {
			t.Fatalf("investigation became eligible to create another Task: %+v", state)
		}
	}
	assertSource(f.m, false)
	on := true
	if _, err := f.m.UpdateSettings(SettingsPatch{GitLabAutoInvestigate: &on}); err != nil {
		t.Fatal(err)
	}
	assertSource(f.m, true)
	if got, err := f.m.investigateGitLab(t.Context(), id, f.sha, 91); err != nil || got != id {
		t.Fatalf("investigation did not return its existing Task: %s, %v", got, err)
	}
	if _, err := f.m.investigateGitLab(t.Context(), id, f.sha, 92); statusOf(err) != http.StatusConflict {
		t.Fatalf("investigation accepted a different event: %v", err)
	}
	gitIn(t, f.repo, "commit", "-q", "--allow-empty", "-m", "new source commit")
	newSHA := gitOutput(t, f.repo, "rev-parse", "HEAD")
	if err := f.m.recordGitLabCommit(t.Context(), f.source.ID, newSHA, "main"); err != nil {
		t.Fatal(err)
	}
	assertSource(f.m, true)
	if len(f.prov.Opens()) != 2 {
		t.Fatal("refreshing an investigation opened another Task")
	}
	if err := f.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, f.st, f.prov)
	assertSource(again, true)
	srv, err := NewServer(ServerConfig{Manager: again, Token: testToken, Version: "test", Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: again, prov: f.prov}
	w := ts.do(http.MethodGet, "/api/sessions/"+id+"/gitlab/jobs/1/trace?pipeline_id=91&commit_sha="+f.sha, "", withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "FAIL: fixture test") {
		t.Fatalf("investigation could not read its source pipeline log: %d %s", w.Code, w.Body)
	}
}

func TestGitLabInvestigationAssociationScope(t *testing.T) {
	for _, change := range []string{"branch", "origin", "project", "unregistered", "source removed"} {
		t.Run(change, func(t *testing.T) {
			f := newGitLabWorkflowFixture(t)
			f.record(t)
			id, err := f.m.investigateGitLab(t.Context(), f.source.ID, f.sha, 91)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "branch":
				gitIn(t, f.repo, "switch", "-q", "-c", "other")
			case "origin":
				gitIn(t, f.repo, "remote", "set-url", "origin", "https://gitlab.example.com/group/other.git")
			case "project", "unregistered":
				if err := f.st.Update(func(cfg *store.Config) error {
					key := gitLabCommitKey("gitlab.example.com", "group/sub/repo", f.sha)
					rec := cfg.WebGitLab[key]
					if change == "project" {
						rec.ProjectID = "different-project"
					} else {
						receipt := rec.Investigations[gitLabEventKey(42, 91)]
						receipt.Registered = false
						rec.Investigations[gitLabEventKey(42, 91)] = receipt
					}
					cfg.WebGitLab[key] = rec
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "source removed":
				if _, err := f.m.Archive(f.source.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.m.Delete(f.source.ID); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(f.gate.executable + ".calls")
			if err != nil {
				t.Fatal(err)
			}
			for _, refresh := range []bool{false, true} {
				state, err := f.m.gitLabState(t.Context(), id, refresh)
				if err != nil || state.Commit != nil || state.Pipeline != nil || len(state.Jobs) != 0 || len(state.MergeRequests) != 0 || state.CanInvestigate {
					t.Fatalf("invalid association exposed pipeline context: %+v, %v", state, err)
				}
			}
			after, err := os.ReadFile(f.gate.executable + ".calls")
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("invalid association fetched GitLab data")
			}
		})
	}
}

func TestGitLabMonitorStopsOnSettledPipelinesAndPrunesDeletedThreads(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	f.record(t)
	calls := func() int {
		data, _ := os.ReadFile(f.gate.executable + ".calls")
		return strings.Count(string(data), "\n")
	}
	f.m.gitLabMonitorPass(t.Context())
	first := calls()
	if first == 0 {
		t.Fatal("the first pass did not read the pipeline")
	}
	// The fixture pipeline failed with no update time: settled, never polled again.
	f.m.gitLabMonitorPass(t.Context())
	if got := calls(); got != first {
		t.Fatalf("a settled pipeline was polled: %d calls, want %d", got, first)
	}
	// A pipeline that finished a moment ago may still be retried.
	f.m.gitlab.mu.Lock()
	for k, state := range f.m.gitlab.cache {
		p := *state.Pipeline
		p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		state.Pipeline = &p
		f.m.gitlab.cache[k] = state
	}
	f.m.gitlab.mu.Unlock()
	f.m.gitLabMonitorPass(t.Context())
	if calls() == first {
		t.Fatal("a recently finished pipeline was not polled")
	}
	if _, err := f.m.Archive(f.source.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Delete(f.source.ID); err != nil {
		t.Fatal(err)
	}
	cfg, err := f.st.Load()
	if err != nil || len(cfg.WebGitLab) != 0 {
		t.Fatalf("deleted Thread kept its receipts: %+v, %v", cfg.WebGitLab, err)
	}
	// A receipt left by an older build is pruned by the next pass.
	if err := f.st.Update(func(cfg *store.Config) error {
		cfg.WebGitLab = map[string]store.WebGitLabCommit{"stale": {TaskID: f.source.ID, SHA: f.sha, Branch: "main"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := calls()
	f.m.gitLabMonitorPass(t.Context())
	if cfg, err := f.st.Load(); err != nil || len(cfg.WebGitLab) != 0 {
		t.Fatalf("stale receipt survived a pass: %+v, %v", cfg.WebGitLab, err)
	}
	if calls() != before {
		t.Fatal("a deleted Thread was polled")
	}
}

func TestGitLabSettled(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	for _, tc := range []struct {
		status, updated string
		want            bool
	}{
		{"running", at(time.Hour), false},
		{"manual", at(time.Hour), false},
		{"success", at(time.Minute), false},
		{"failed", at(gitLabSettleAfter), true},
		{"canceled", at(time.Hour), true},
		{"skipped", "", true},
	} {
		if got := gitLabSettled(&GitLabPipeline{Status: tc.status, UpdatedAt: tc.updated}, now); got != tc.want {
			t.Errorf("%s at %q: settled=%v", tc.status, tc.updated, got)
		}
	}
	if gitLabSettled(nil, now) {
		t.Error("no pipeline settled")
	}
}

// A Thread whose repository has no GitLab origin says so instead of a pipeline.
func TestGitLabStateWithoutAGitLabOrigin(t *testing.T) {
	f := newGitLabWorkflowFixture(t)
	gitIn(t, f.repo, "remote", "remove", "origin")
	state, err := f.m.gitLabState(t.Context(), f.source.ID, false)
	if err != nil || state.Message != "This Thread has no GitLab origin." {
		t.Fatalf("state = %+v, %v", state, err)
	}
}
