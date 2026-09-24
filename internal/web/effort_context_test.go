package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func setting(s string) *string { return &s }

func selectionModels() []agentapi.Model {
	return []agentapi.Model{
		{ID: "a", Efforts: []string{"low", "high"}, ContextSizes: []agentapi.ContextSize{{ID: "default", Tokens: 200}, {ID: "long_context", Tokens: 900}}},
		{ID: "b", Efforts: []string{"high"}},
		{ID: "c", Efforts: []string{"low"}},
		{ID: "auto"},
	}
}

func TestTaskEffortContextValidationAndSwitch(t *testing.T) {
	caps := allCaps
	caps.ContextSize = true
	p := agenttest.NewProvider("fake", caps)
	p.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, p)
	project := addProject(t, m, t.TempDir())
	for _, req := range []CreateRequest{
		{Effort: "high"}, {Model: "auto", Effort: "high"}, {Model: "c", Effort: "high"},
		{Model: "b", ContextSize: "long_context"}, {Model: "auto", ContextSize: "long_context"}, {Model: "a", ContextSize: "bogus"},
	} {
		req.Provider, req.ProjectID = "fake", project
		if _, err := m.Create(req); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("Create(%+v) = %v", req, err)
		}
	}
	if len(p.Opens()) != 0 {
		t.Fatal("invalid selections reached provider")
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "a", Effort: "high", ContextSize: "long_context"})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Last()
	if req := c.Request(); req.Effort != "high" || req.ContextSize != "long_context" {
		t.Fatalf("open = %+v", req)
	}
	if sum.Effort != "high" || sum.ContextSize != "long_context" || sum.Context != nil {
		t.Fatalf("summary = %+v", sum)
	}
	c.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.SetModel(sum.ID, nil, setting("low"), nil); statusOf(err) != http.StatusConflict {
		t.Fatalf("busy effort = %v", err)
	}
	if _, err := m.SetModel(sum.ID, nil, nil, setting("default")); statusOf(err) != http.StatusConflict {
		t.Fatalf("busy size = %v", err)
	}
	c.EmitTurn(agentapi.TurnCompleted, "")
	if _, err := m.SetModel(sum.ID, setting("c"), setting("high"), nil); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("invalid combined selection = %v", err)
	}
	if len(c.ModelSets()) != 0 {
		t.Fatal("invalid or busy selection reached provider")
	}
	c.SetModelError(errors.New("declined"))
	if _, err := m.SetModel(sum.ID, nil, setting("low"), nil); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("refused = %v", err)
	}
	if got := detail(t, m, sum.ID); got.Effort != "high" || got.ContextSize != "long_context" {
		t.Fatalf("refusal changed settings: %+v", got)
	}
	c.SetModelError(nil)
	sum, err = m.SetModel(sum.ID, setting("b"), nil, nil)
	if err != nil || sum.Effort != "high" || sum.ContextSize != "default" {
		t.Fatalf("compatible switch = %+v, %v", sum, err)
	}
	sum, err = m.SetModel(sum.ID, setting("c"), nil, nil)
	if err != nil || sum.Effort != "" {
		t.Fatalf("incompatible switch = %+v, %v", sum, err)
	}
	sum, err = m.SetModel(sum.ID, setting("a"), setting("low"), setting("long_context"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	if rec.Web.Effort != "low" || rec.Web.ContextSize != "long_context" {
		t.Fatalf("persisted = %+v", rec.Web)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, "reopen", mustUUID(t), ModeSend); err != nil {
		t.Fatal(err)
	}
	sets := p.Last().ModelSettings()
	if len(sets) != 1 || sets[0].Model != "a" || sets[0].Effort != "low" || sets[0].ContextSize != "long_context" {
		t.Fatalf("reopen settings = %+v", sets)
	}
	p.Last().EmitTurn(agentapi.TurnCompleted, "")
	if _, err := m.SetModel(sum.ID, nil, setting(""), setting("default")); err != nil {
		t.Fatal(err)
	}
	if got := detail(t, m, sum.ID); got.Effort != "" || got.ContextSize != "default" {
		t.Fatalf("reset = %+v", got)
	}
	if _, err := m.SetModel(sum.ID, nil, setting("high"), setting("long_context")); err != nil {
		t.Fatal(err)
	}
	p.Last().Emit(agentapi.Event{Kind: agentapi.EventContext, Context: &agentapi.Context{Used: 250, Limit: 900}})
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	p2 := agenttest.NewProvider("fake", caps)
	p2.SetModels(selectionModels(), nil)
	p2.AddConversation(sum.ConversationID, nil)
	m2 := startManager(t, st, p2)
	restored := detail(t, m2, sum.ID)
	if restored.Effort != "high" || restored.ContextSize != "long_context" || restored.Context != nil {
		t.Fatalf("restart = %+v", restored.SessionSummary)
	}
	if _, err := m2.Submit(sum.ID, "after restart", mustUUID(t), ModeSend); err != nil {
		t.Fatal(err)
	}
	sets = p2.Last().ModelSettings()
	if len(sets) != 1 || sets[0].Effort != "high" || sets[0].ContextSize != "long_context" || len(p2.Last().Sends()) != 1 {
		t.Fatalf("restart settings = %+v", sets)
	}
}

func TestTaskContextLiveOnly(t *testing.T) {
	m, p, st := newTestManager(t)
	sum, c := createSession(t, m, p)
	if data, _ := json.Marshal(sum); strings.Contains(string(data), `"context":`) {
		t.Fatalf("new task context = %s", data)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	beforeKey := m.sessions[sum.ID].key()
	m.mu.Unlock()
	usage := &agentapi.Context{Used: 250, Limit: 200}
	c.Emit(agentapi.Event{Kind: agentapi.EventContext, Context: usage})
	got := detail(t, m, sum.ID)
	if got.Context == nil || *got.Context != *usage {
		t.Fatalf("context = %+v", got.Context)
	}
	usage.Used = 1
	if detail(t, m, sum.ID).Context.Used != 250 {
		t.Fatal("retained provider-owned context pointer")
	}
	for _, usage := range []agentapi.Context{{Used: -1, Limit: 200}, {Used: 5, Limit: 0}} {
		c.Emit(agentapi.Event{Kind: agentapi.EventContext, Context: &usage})
	}
	if detail(t, m, sum.ID).Context.Used != 250 {
		t.Fatal("invalid report replaced usage")
	}
	m.mu.Lock()
	afterKey := m.sessions[sum.ID].key()
	m.mu.Unlock()
	if beforeKey != afterKey {
		t.Fatal("live usage changed durable key")
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	data, _ := json.Marshal(rec.Web)
	if strings.Contains(string(data), `"context":`) {
		t.Fatalf("context persisted: %s", data)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, "reopen", mustUUID(t), ModeSend); err != nil {
		t.Fatal(err)
	}
	if got := detail(t, m, sum.ID); got.Context != nil {
		t.Fatalf("reopened context = %+v", got.Context)
	}
	if stale := c; stale != p.Last() {
		stale.Emit(agentapi.Event{Kind: agentapi.EventContext, Context: &agentapi.Context{Used: 4, Limit: 5}})
		if detail(t, m, sum.ID).Context != nil {
			t.Fatal("stale conversation changed context")
		}
	}
}

func TestTaskContextCapabilityAndHTTPPatch(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	ts.prov.SetModels(selectionModels(), nil)
	ts.m.mu.Lock()
	info := ts.m.infos["fake"]
	info.Models = selectionModels()
	ts.m.infos["fake"] = info
	ts.m.mu.Unlock()
	project := addProject(t, ts.m, t.TempDir())
	body := `{"provider":"fake","project_id":"` + project + `","model":"a","effort":"high","context_size":"long_context"}`
	if w := ts.do(http.MethodPost, "/api/sessions", body, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("unsupported size = %d %s", w.Code, w.Body)
	}
	body = strings.Replace(body, "long_context", "default", 1)
	w := ts.do(http.MethodPost, "/api/sessions", body, withCookie(ts))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var sum SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	url := "/api/sessions/" + sum.ID
	w = ts.do(http.MethodPatch, url, `{"effort":""}`, withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"effort":""`) {
		t.Fatalf("reset PATCH = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPatch, url, `{"model":"c","effort":"high","name":"must not apply"}`, withCookie(ts))
	if w.Code != http.StatusBadRequest || detail(t, ts.m, sum.ID).Model != "a" || detail(t, ts.m, sum.ID).Name != "" {
		t.Fatalf("invalid PATCH = %d %s", w.Code, w.Body)
	}
	for _, closed := range []bool{false, true} {
		if closed {
			if _, err := ts.m.Close(sum.ID); err != nil {
				t.Fatal(err)
			}
		}
		w = ts.do(http.MethodPatch, url, `{"model":""}`, withCookie(ts))
		if w.Code != http.StatusBadRequest || detail(t, ts.m, sum.ID).Model != "a" {
			t.Fatalf("empty model PATCH closed=%v = %d %s", closed, w.Code, w.Body)
		}
	}
}
