package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestParseDiagramGraph(t *testing.T) {
	g, err := parseDiagramGraph("```json\n" + `{"direction":"LR","groups":[{"id":"g","label":"Back <b>end</b>","group":""}],
	"nodes":[{"id":"a","label":"Start","kind":"terminal","group":""},{"id":"b","label":"Is it\nok?","kind":"decision","group":"g"}],
	"edges":[{"from":"a","to":"b","label":"","dashed":true,"thick":false,"head":"sideways"},{"from":"b","to":"g","label":"yes","dashed":false,"thick":true,"head":"both"}]}` + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	if g.Direction != "LR" || len(g.Groups) != 1 || g.Groups[0].Group != "" || g.Nodes[1].Group != "g" || g.Nodes[1].Label != "Is it\nok?" {
		t.Fatalf("graph = %+v", g)
	}
	if e := g.Edges[0]; e.Head != "end" || !e.Dashed {
		t.Fatalf("an unknown head is an arrow at the target: %+v", e)
	}
	if e := g.Edges[1]; e.To != "g" || e.Head != "both" || !e.Thick || e.Label != "yes" {
		t.Fatalf("an edge may end at a group: %+v", e)
	}
}

func TestParseDiagramGraphRejects(t *testing.T) {
	node := func(id, kind, group string) string {
		return `{"id":"` + id + `","label":"x","kind":"` + kind + `","group":"` + group + `"}`
	}
	graph := func(groups, nodes, edges string) string {
		return `{"direction":"TB","groups":[` + groups + `],"nodes":[` + nodes + `],"edges":[` + edges + `]}`
	}
	many := make([]string, maxDiagramNodes+1)
	for i := range many {
		many[i] = node("n"+strings.Repeat("x", i), "step", "")
	}
	for name, reply := range map[string]string{
		"no nodes":       graph("", "", ""),
		"too many nodes": graph("", strings.Join(many, ","), ""),
		"duplicate id":   graph("", node("a", "step", "")+","+node("a", "step", ""), ""),
		"unknown kind":   graph("", node("a", "circle", ""), ""),
		"unknown group":  graph("", node("a", "step", "g"), ""),
		"group cycle":    graph(`{"id":"g","label":"G","group":"h"},{"id":"h","label":"H","group":"g"}`, node("a", "step", "g"), ""),
		"dangling edge":  graph("", node("a", "step", ""), `{"from":"a","to":"b","label":"","dashed":false,"thick":false,"head":"end"}`),
		"unknown field":  `{"direction":"TB","groups":[],"nodes":[` + node("a", "step", "") + `],"edges":[],"style":"x"}`,
		"second object":  graph("", node("a", "step", ""), "") + `{}`,
		"not json":       "I cannot draw that.",
	} {
		if _, err := parseDiagramGraph(reply); !errors.Is(err, errInvalidDiagramGraph) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// The route asks the Utility model once per source, answers from the cache
// after that, and refuses bad input before any call.
func TestDiagramGraphRoute(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m, prov: prov.Provider}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	reply := `{"direction":"TB","groups":[],"nodes":[{"id":"a","label":"A","kind":"step","group":""}],"edges":[]}`
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose != purposeDiagramGraph || req.System != diagramGraphSystem || req.ResponseSchema == nil || !strings.Contains(req.Prompt, "graph TD") {
			t.Errorf("request = %+v", req)
		}
		return reply, nil
	})
	path := "/api/sessions/" + sum.ID + "/diagram-graph"
	for range 2 {
		w := ts.do(http.MethodPost, path, `{"source":" graph TD; a "}`, withCookie(ts))
		var g DiagramGraph
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &g) != nil || len(g.Nodes) != 1 || g.Nodes[0].Label != "A" {
			t.Fatalf("graph = %d %s", w.Code, w.Body.String())
		}
	}
	if n := utilityCalls(prov, purposeDiagramGraph); n != 1 {
		t.Fatalf("calls = %d, want 1 (the second is cached)", n)
	}
	reply = "I cannot draw that."
	if w := ts.do(http.MethodPost, path, `{"source":"graph TD; b"}`, withCookie(ts)); w.Code != http.StatusBadGateway {
		t.Fatalf("invalid answer = %d %s", w.Code, w.Body.String())
	}
	for body, want := range map[string]int{
		`{"source":"  "}`: http.StatusBadRequest,
		`{"source":"graph TD; ` + strings.Repeat("x", maxDiagramSource) + `"}`: http.StatusRequestEntityTooLarge,
	} {
		if w := ts.do(http.MethodPost, path, body, withCookie(ts)); w.Code != want {
			t.Errorf("%.30s = %d, want %d", body, w.Code, want)
		}
	}
	if w := ts.do(http.MethodPost, "/api/sessions/missing/diagram-graph", `{"source":"graph TD; a"}`, withCookie(ts)); w.Code != http.StatusNotFound {
		t.Errorf("unknown Thread = %d", w.Code)
	}
	if n := utilityCalls(prov, purposeDiagramGraph); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}

func TestDiagramGraphCacheDropsOldest(t *testing.T) {
	var c diagramGraphCache
	for i := range maxDiagramGraphs + 1 {
		c.put(strings.Repeat("k", i+1), DiagramGraph{Direction: "TB"})
	}
	c.put("kk", DiagramGraph{Direction: "LR"})
	if _, ok := c.get("k"); ok || len(c.graphs) != maxDiagramGraphs || len(c.order) != maxDiagramGraphs {
		t.Fatalf("cache kept %d graphs, %d keys", len(c.graphs), len(c.order))
	}
	if g, ok := c.get("kk"); !ok || g.Direction != "LR" {
		t.Fatalf("replaced graph = %+v, %v", g, ok)
	}
}
