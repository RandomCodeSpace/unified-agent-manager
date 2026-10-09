package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type nativeEditProvider struct {
	*agenttest.Provider
	read func(context.Context, agentapi.ItemDiffRequest) (agentapi.ItemDiff, error)
}

func (p *nativeEditProvider) ReadItemDiff(ctx context.Context, req agentapi.ItemDiffRequest) (agentapi.ItemDiff, error) {
	return p.read(ctx, req)
}
func TestItemDiffClosedTaskUsesExactOwnRecordWithoutResume(t *testing.T) {
	base := agenttest.NewProvider("fake", allCaps)
	var got agentapi.ItemDiffRequest
	var recordedConversation string
	p := &nativeEditProvider{Provider: base, read: func(_ context.Context, req agentapi.ItemDiffRequest) (agentapi.ItemDiff, error) {
		got = req
		if recordedConversation != "" && req.ConversationID != recordedConversation {
			return agentapi.ItemDiff{}, agentapi.ErrItemNotFound
		}
		return agentapi.ItemDiff{Path: req.Path, Status: "available", Patch: "recorded"}, nil
	}}
	m := startManager(t, openTestStore(t), p)
	sum, _ := createSession(t, m, base)
	recordedConversation = sum.ConversationID
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(base.Opens())
	body, err := m.ItemDiff(context.Background(), sum.ID, "agent", "tool", "event", "/work/file")
	if err != nil || body.SessionID != sum.ID || body.AgentID != "agent" || body.ItemID != "tool" || body.EventID != "event" || body.Patch != "recorded" {
		t.Fatalf("body=%+v err=%v", body, err)
	}
	if got.ConversationID != sum.ConversationID || got.Workdir != sum.Workdir || got.AgentID != "agent" || got.ItemID != "tool" || got.EventID != "event" || got.Path != "/work/file" {
		t.Fatalf("request=%+v", got)
	}
	if len(base.Opens()) != opens {
		t.Fatal("closed task resumed")
	}
	other, _ := createSession(t, m, base)
	if _, err = m.ItemDiff(context.Background(), other.ID, "agent", "tool", "event", "/work/file"); statusOf(err) != 404 {
		t.Fatalf("wrong existing Task=%v", err)
	}
	if _, err = m.ItemDiff(context.Background(), "missing", "", "tool", "event", "/work/file"); statusOf(err) != 404 {
		t.Fatalf("missing task=%v", err)
	}
	if _, err = m.ItemDiff(context.Background(), sum.ID, "", "tool", "event", "/work/\nfile"); statusOf(err) != 400 {
		t.Fatalf("invalid path=%v", err)
	}
}
func TestItemDiffDoesNotReturnAfterTaskBindingChanges(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := &nativeEditProvider{Provider: agenttest.NewProvider("fake", allCaps), read: func(_ context.Context, req agentapi.ItemDiffRequest) (agentapi.ItemDiff, error) {
		close(started)
		<-release
		return agentapi.ItemDiff{Path: req.Path, Status: "available", Patch: "recorded"}, nil
	}}
	m := startManager(t, openTestStore(t), p)
	sum, _ := createSession(t, m, p.Provider)
	done := make(chan error, 1)
	go func() {
		_, err := m.ItemDiff(context.Background(), sum.ID, "", "tool", "event", "/work/file")
		done <- err
	}()
	<-started
	m.mu.Lock()
	m.sessions[sum.ID].historyGen++
	m.mu.Unlock()
	close(release)
	if err := <-done; statusOf(err) != http.StatusConflict {
		t.Fatalf("stale read=%v", err)
	}
}
func TestNativeEditMetadataIsBoundedAndCloned(t *testing.T) {
	edits := make([]agentapi.FileEdit, agentapi.MaxFileEdits+3)
	for i := range edits {
		edits[i] = agentapi.FileEdit{Path: "/work/界😀.txt", Kind: "edit", DiffStatus: "missing", Additions: 999}
	}
	original := agentapi.Item{Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", EditEventID: "event", FileEdits: edits}}
	checked := checkItem(original, time.Now())
	if len(checked.Tool.FileEdits) != agentapi.MaxFileEdits || !checked.Tool.FileEditsTruncated || checked.Tool.FileEdits[0].Additions != 0 {
		t.Fatalf("checked=%+v", checked.Tool)
	}
	checked.Tool.FileEdits[0].Path = "changed"
	if original.Tool.FileEdits[0].Path == "changed" {
		t.Fatal("provider metadata shared")
	}
	cloned := cloneBody(original)
	cloned.Tool.FileEdits[0].Path = "clone"
	if original.Tool.FileEdits[0].Path == "clone" {
		t.Fatal("body metadata shared")
	}
	if itemSize(original) <= len(strings.Repeat("x", len(original.ID))) {
		t.Fatal("edit metadata unaccounted")
	}
}

func TestItemDiffRouteAuthenticatesAndNeverCachesPatch(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	calls := 0
	p := &nativeEditProvider{Provider: ts.prov, read: func(_ context.Context, req agentapi.ItemDiffRequest) (agentapi.ItemDiff, error) {
		calls++
		return agentapi.ItemDiff{Path: req.Path, Status: "available", Patch: "recorded"}, nil
	}}
	ts.m.mu.Lock()
	ts.m.providers[p.Name()] = p
	ts.m.mu.Unlock()
	sum, _ := createSession(t, ts.m, ts.prov)
	route := "/api/sessions/" + sum.ID + "/items/tool/diff?agent_id=agent&event_id=event&path=" + url.QueryEscape("/work/file")
	if w := ts.do(http.MethodGet, route, ""); w.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated=%d reads=%d", w.Code, calls)
	}
	w := ts.do(http.MethodGet, route, "", withCookie(ts))
	var body itemDiffBody
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Patch != "recorded" || body.EventID != "event" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w = ts.do(http.MethodGet, route+"&event_id=second", "", withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate event=%d", w.Code)
	}
}
