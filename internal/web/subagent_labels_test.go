package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestValidSubagentLabel(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"Inventory bug review", "Inventory bug review", true},
		{"  Restock  fix-check ", "Restock fix-check", true},
		{"Docs summary's", "Docs summary's", true},
		{"2FA review", "2FA review", true},
		{"Inventory review for the bug", "", false},   // 4 words
		{"Extraordinarily long subagents", "", false}, // 30 characters
		{"Bug review!", "", false},                    // punctuation
		{"Bug review: inventory", "", false},          // punctuation
		{"", "", false},                               // empty
		{"   ", "", false},                            // empty
		{"inventory bug review", "", false},           // not sentence case
		{"Bug review\nsecond line", "", false},        // two lines
		{"Überprüfung", "Überprüfung", true},          // letters beyond ASCII
	} {
		got, ok := validSubagentLabel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("validSubagentLabel(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if _, err := parseSubagentLabel(`{"name":"Bug review","why":"x"}`); err == nil {
		t.Error("an answer with an unknown field passed")
	}
	if got, err := parseSubagentLabel("Here:\n```json\n{\"name\":\"Bug review\"}\n```"); err != nil || got != "Bug review" {
		t.Errorf("fenced answer = %q, %v", got, err)
	}
}

func subagentLabelOf(m *Manager, id, agentID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	return s.compactSubagent(*s.subIdx[agentID]).Label
}

// A new subagent gets one naming job; a later update of it starts none, an
// invalid answer leaves the agent's own name, and the name is saved.
func TestSubagentLabelOncePerAgent(t *testing.T) {
	prov := newAssistProvider()
	st := openTestStore(t)
	m, project := assistManager(t, st, prov)
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose != purposeSubagentLabel || req.ResponseSchema == nil {
			return "", fmt.Errorf("unexpected request %+v", req)
		}
		if strings.Contains(req.Prompt, "audit") {
			return `{"name":"Audit the whole repository now"}`, nil
		}
		if !strings.Contains(req.Prompt, "check the inventory module") || !strings.Contains(req.Prompt, "inventory-bug-review") {
			return "", fmt.Errorf("prompt lacks its input: %s", req.Prompt)
		}
		return `{"name":"Inventory bug review"}`, nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	sub := agentapi.Subagent{ID: "a1", Name: "inventory-bug-review", Description: "Review bugs", Prompt: "Please check the inventory module for bugs", Status: agentapi.SubagentRunning}
	conv.Emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &sub})
	waitUntil(t, "the subagent name", func() bool { return subagentLabelOf(m, sum.ID, "a1") == "Inventory bug review" })
	again := sub
	again.Status = agentapi.SubagentCompleted
	conv.Emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &again})
	// No prompt and no description: nothing to name it by.
	conv.Emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "a2", Name: "quiet"}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "a3", Name: "auditor", Prompt: "audit"}})
	waitUntil(t, "the invalid answer", func() bool { return utilityCalls(prov, purposeSubagentLabel) == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := utilityCalls(prov, purposeSubagentLabel); n != 2 {
		t.Fatalf("naming jobs = %d, want 2", n)
	}
	if got := subagentLabelOf(m, sum.ID, "a3"); got != "" {
		t.Fatalf("invalid answer kept as %q", got)
	}
	m.mu.Lock()
	kept := m.sessions[sum.ID].subIdx["a1"].Prompt
	m.mu.Unlock()
	if kept != "" {
		t.Fatalf("the prompt stayed in memory: %q", kept)
	}

	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	if rec.Web == nil || len(rec.Web.SubagentLabels) != 1 || rec.Web.SubagentLabels["a1"] != "Inventory bug review" {
		t.Fatalf("saved names = %+v", rec.Web)
	}
	rec.Web.SubagentLabels["bad"] = "not valid!"
	if got := loadSubagentLabels(rec.Web.SubagentLabels); len(got) != 1 || got["a1"] != "Inventory bug review" {
		t.Fatalf("loaded names = %v", got)
	}
}
