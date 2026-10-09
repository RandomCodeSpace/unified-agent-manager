package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestNativeChangesDefaultIsNewTaskOnlyAndSurvivesReload(t *testing.T) {
	st := openTestStore(t)
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	regular := agenttest.NewProvider("regular", agentapi.Capabilities{History: true, SessionDiff: true})
	m := startManager(t, st, prov, regular)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, t.TempDir())})
	if err != nil || !sum.Capabilities.SessionDiff {
		t.Fatalf("new tracked Task = %+v, %v", sum, err)
	}
	legacy := sessionFromRecord(store.SessionRecord{ID: "legacy", Agent: "native", ProviderSessionID: "old-session"})
	m.mu.Lock()
	old := m.summaryLocked(legacy)
	m.mu.Unlock()
	if old.Capabilities.SessionDiff {
		t.Fatal("legacy Task default changed to native scope without recorded capture opt-in")
	}
	legacy.provider = "regular"
	m.mu.Lock()
	regularOld := m.summaryLocked(legacy)
	m.mu.Unlock()
	if !regularOld.Capabilities.SessionDiff {
		t.Fatal("existing non-Copilot native scope changed")
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range cfg.Sessions {
		if rec.ID != sum.ID {
			continue
		}
		encoded, err := json.Marshal(rec.Web)
		if err != nil || !strings.Contains(string(encoded), `"native_changes":true`) {
			t.Fatalf("capture marker not persisted: %s, %v", encoded, err)
		}
		loaded := sessionFromRecord(rec)
		m.mu.Lock()
		again := m.summaryLocked(loaded)
		m.mu.Unlock()
		if !again.Capabilities.SessionDiff {
			t.Fatal("reloaded tracked Task lost native scope")
		}
		return
	}
	t.Fatal("created Task not recorded")
}

func TestNativeChangesListingOmitsPatchesAndUnknownCounts(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	patch := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,2 +1,2 @@\n----\n-old\n++++\n+new\n"
	conv.SetDiff([]agentapi.FileDiff{
		{Path: "main.go", Status: "modified", Patch: patch},
		{Path: "new.txt", OldPath: "old.txt", Status: "renamed", Patch: "diff --git a/old.txt b/new.txt\nsimilarity index 100%\nrename from old.txt\nrename to new.txt\n"},
		{Path: "image.bin", Status: "modified", Binary: true, CountsUnknown: true},
		{Path: "huge.txt", Status: "modified", Truncated: true, CountsUnknown: true},
	}, nil)
	auth := withCookie(ts)
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes?scope=session", "", auth)
	var got Changes
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.Supported || len(got.Files) != 4 {
		t.Fatalf("native listing = %d %s, %v", w.Code, w.Body, err)
	}
	if strings.Contains(w.Body.String(), `"patch"`) || strings.Contains(w.Body.String(), `"before"`) || strings.Contains(w.Body.String(), `"after"`) {
		t.Fatalf("listing leaked patch text: %s", w.Body)
	}
	if got.Files[0].Additions != 2 || got.Files[0].Deletions != 2 {
		t.Fatalf("native patch counts = %+v", got.Files[0])
	}
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	files := raw["files"].([]any)
	if files[1].(map[string]any)["old_path"] != "old.txt" || files[2].(map[string]any)["counts_unknown"] != true || files[3].(map[string]any)["truncated"] != true {
		t.Fatalf("native metadata omitted: %s", w.Body)
	}
	body := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes/file?scope=session&path=main.go", "", auth)
	var file agentapi.FileDiff
	if err := json.Unmarshal(body.Body.Bytes(), &file); err != nil || body.Code != http.StatusOK || file.Patch != patch {
		t.Fatalf("on-demand body = %d %s, %v", body.Code, body.Body, err)
	}
}

func TestNativeChangesBusyDoesNotBecomePermanentUnsupported(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.SetDiff(nil, agentapi.ErrBusy)
	out, err := ts.m.Changes(context.Background(), sum.ID, ScopeSession)
	if err != nil || out.Supported || !strings.Contains(out.Reason, "settle") {
		t.Fatalf("busy availability = %+v, %v", out, err)
	}
	conv.SetDiff([]agentapi.FileDiff{{Path: "ready.txt", Patch: "--- a/ready.txt\n+++ b/ready.txt\n@@ -0,0 +1 @@\n+ready\n"}}, nil)
	out, err = ts.m.Changes(context.Background(), sum.ID, ScopeSession)
	if err != nil || !out.Supported || len(out.Files) != 1 {
		t.Fatalf("settled availability = %+v, %v", out, err)
	}
}

// A closed native Task's "This task" falls back to the Git-attributed task
// scope, labelled as such, without resuming the conversation or publishing
// the Git count as the native total. An open one keeps its native scope.
func TestNativeChangesClosedFallsBackToGitTaskScope(t *testing.T) {
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, gitRepoFixture(t))})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitItem(toolEdit("e1", "edit", "tracked.txt", agentapi.ToolCompleted, time.Now()))
	conv.SetDiff([]agentapi.FileDiff{{Path: "native.txt", Patch: "--- a/native.txt\n+++ b/native.txt\n@@ -0,0 +1 @@\n+native\n"}}, nil)
	open, paths := scopePaths(t, m, sum.ID, ScopeSession)
	if !open.Supported || open.Scope != ScopeSession || open.Label != sessionLabel || !slices.Equal(paths, []string{"native.txt"}) {
		t.Fatalf("open native scope = %+v", open)
	}

	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens, total := len(prov.Opens()), cachedDiff(m, sum.ID)
	closed, paths := scopePaths(t, m, sum.ID, ScopeSession)
	if !closed.Supported || closed.Scope != ScopeTask || closed.Label != closedSessionLabel || !slices.Equal(paths, []string{"tracked.txt"}) {
		t.Fatalf("closed native scope = %+v", closed)
	}
	if f, err := m.FileChange(t.Context(), sum.ID, ScopeSession, "tracked.txt"); err != nil || f.Additions != 2 {
		t.Fatalf("closed fallback file = %+v, %v", f, err)
	}
	if _, err := m.FileChange(t.Context(), sum.ID, ScopeSession, "native.txt"); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("closed fallback served a file the Task did not edit: %v", err)
	}
	if after := len(prov.Opens()); after != opens {
		t.Fatalf("read resumed conversation: opens %d -> %d", opens, after)
	}
	if got := cachedDiff(m, sum.ID); got != total && (got == nil || total == nil || *got != *total) {
		t.Fatalf("Git fallback replaced native total: %+v -> %+v", total, got)
	}
}

func TestNativeChangesTotalIsNotReplacedByGitScopes(t *testing.T) {
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, gitRepoFixture(t))})
	if err != nil {
		t.Fatal(err)
	}
	prov.Last().SetDiff([]agentapi.FileDiff{{Path: "native.txt", Patch: "--- a/native.txt\n+++ b/native.txt\n@@ -0,0 +1 @@\n+native\n"}}, nil)
	if _, err := m.Changes(t.Context(), sum.ID, ScopeSession); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Changes(t.Context(), sum.ID, ScopeTask); err != nil {
		t.Fatal(err)
	}
	if got := cachedDiff(m, sum.ID); got == nil || *got != (DiffStat{Files: 1, Additions: 1}) {
		t.Fatalf("Git scope replaced native total: %+v", got)
	}
	prov.Last().SetDiff([]agentapi.FileDiff{{Path: "unknown.bin", Binary: true, CountsUnknown: true}}, nil)
	if _, err := m.Changes(t.Context(), sum.ID, ScopeSession); err != nil {
		t.Fatal(err)
	}
	if got := cachedDiff(m, sum.ID); got != nil {
		t.Fatalf("unknown native counts published a total: %+v", got)
	}
}

func TestNativeChangesTotalInvalidatedByLaterActivity(t *testing.T) {
	for _, event := range []struct {
		name string
		emit func(*agenttest.Conversation)
	}{
		{"new prompt", func(c *agenttest.Conversation) {
			c.EmitItem(agentapi.Item{ID: "later-prompt", Kind: agentapi.ItemUser, Text: "edit again"})
		}},
		{"new turn", func(c *agenttest.Conversation) { c.EmitTurn(agentapi.TurnWorking, "") }},
		{"completed turn", func(c *agenttest.Conversation) { c.EmitTurn(agentapi.TurnCompleted, "") }},
		{"completed tool", func(c *agenttest.Conversation) {
			c.EmitItem(agentapi.Item{ID: "later-edit", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted}})
		}},
		{"failed partial tool", func(c *agenttest.Conversation) {
			c.EmitItem(agentapi.Item{ID: "partial-edit", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "apply_patch", Status: agentapi.ToolFailed}})
		}},
	} {
		t.Run(event.name, func(t *testing.T) {
			prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
			m := startManager(t, openTestStore(t), prov)
			sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, t.TempDir())})
			if err != nil {
				t.Fatal(err)
			}
			conv := prov.Last()
			conv.SetDiff([]agentapi.FileDiff{{Path: "native.txt", Patch: "--- a/native.txt\n+++ b/native.txt\n@@ -0,0 +1 @@\n+old\n"}}, nil)
			if _, err := m.Changes(t.Context(), sum.ID, ScopeSession); err != nil {
				t.Fatal(err)
			}
			if got := cachedDiff(m, sum.ID); got == nil || got.Additions != 1 {
				t.Fatalf("initial native total = %+v", got)
			}
			event.emit(conv)
			if got := cachedDiff(m, sum.ID); got != nil {
				t.Fatalf("later activity retained stale native total: %+v", got)
			}
			m.mu.Lock()
			running := m.sessions[sum.ID].diffRunning
			m.mu.Unlock()
			if running {
				t.Fatal("native invalidation started a background Git recount")
			}
			conv.SetDiff([]agentapi.FileDiff{{Path: "native.txt", Patch: "--- a/native.txt\n+++ b/native.txt\n@@ -0,0 +1,2 @@\n+new\n+line\n"}}, nil)
			if _, err := m.Changes(t.Context(), sum.ID, ScopeSession); err != nil {
				t.Fatal(err)
			}
			if got := cachedDiff(m, sum.ID); got == nil || got.Additions != 2 {
				t.Fatalf("later on-demand native total = %+v", got)
			}
		})
	}
}

type invalidatingNativeDiffConversation struct {
	agentapi.Conversation
	invalidate func()
}

func (c *invalidatingNativeDiffConversation) Diff(ctx context.Context) ([]agentapi.FileDiff, error) {
	files, err := c.Conversation.Diff(ctx)
	c.invalidate()
	return files, err
}

func TestNativeChangesInFlightTotalDoesNotSurviveNewTurn(t *testing.T) {
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.SetDiff([]agentapi.FileDiff{{Path: "native.txt", Patch: "--- a/native.txt\n+++ b/native.txt\n@@ -0,0 +1 @@\n+old\n"}}, nil)
	m.mu.Lock()
	m.sessions[sum.ID].conv = &invalidatingNativeDiffConversation{Conversation: conv, invalidate: func() { conv.EmitTurn(agentapi.TurnWorking, "") }}
	m.mu.Unlock()
	if _, err := m.Changes(t.Context(), sum.ID, ScopeSession); err != nil {
		t.Fatal(err)
	}
	if got := cachedDiff(m, sum.ID); got != nil {
		t.Fatalf("older in-flight read restored invalidated native total: %+v", got)
	}
}
