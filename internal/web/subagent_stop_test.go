package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestCancelSubagentRouteIsolatesTargetAndWaitsForProvider(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	for _, id := range []string{"target", "sibling"} {
		conv.EmitSubagent(agentapi.Subagent{ID: id, ParentToolCallID: "call-" + id, Status: agentapi.SubagentRunning})
	}
	for _, id := range []string{"target", "sibling", ""} {
		ix := onceRequest("permission-"+id, id)
		conv.EmitInteraction(ix)
	}
	path := "/api/sessions/" + sum.ID + "/subagents/target/cancel"
	if w := ts.do(http.MethodPost, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", w.Code)
	}
	if w := ts.do(http.MethodPost, path, "", auth, withHeader("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site = %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		w := ts.do(http.MethodPost, path, "", auth)
		var sa agentapi.Subagent
		if err := json.Unmarshal(w.Body.Bytes(), &sa); err != nil || w.Code != http.StatusOK || sa.ID != "target" || sa.Status != agentapi.SubagentRunning {
			t.Fatalf("cancel = %d %s, %v", w.Code, w.Body, err)
		}
	}
	if ids := conv.SubagentCancels(); len(ids) != 1 || ids[0] != "target" {
		t.Fatalf("provider calls = %v", ids)
	}
	if conv.Cancels() != 0 || len(conv.Sends()) != 0 {
		t.Fatalf("parent touched: cancels %d, sends %v", conv.Cancels(), conv.Sends())
	}
	d := detail(t, ts.m, sum.ID)
	for _, ix := range d.Interactions {
		want := agentapi.InteractionPending
		if ix.AgentID == "target" {
			want = agentapi.InteractionExpired
		}
		if ix.State != want {
			t.Fatalf("interaction = %+v, want %s", ix, want)
		}
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/interactions/permission-target", `{"decision":"once"}`, auth); w.Code != http.StatusGone {
		t.Fatalf("late approval = %d %s", w.Code, w.Body)
	}
	sub, _, err := ts.m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.m.Unsubscribe(sub)
	conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentCancelled})
	var event agentapi.Subagent
	decodeField(t, frameOf(t, sub, "subagent"), "subagent", &event)
	if event.ID != "target" || event.Status != agentapi.SubagentCancelled {
		t.Fatalf("SSE = %+v", event)
	}
	if sibling, _ := ts.m.Subagent(sum.ID, "sibling"); sibling.Subagent.Status != agentapi.SubagentRunning {
		t.Fatalf("sibling = %+v", sibling)
	}
	// The parent can continue after the target stops.
	conv.EmitItem(agentapi.Item{ID: "parent-reply", Kind: agentapi.ItemAssistant, Text: "continuing"})
	if d := detail(t, ts.m, sum.ID); len(d.Items) != 1 || d.Items[0].Text != "continuing" {
		t.Fatalf("parent transcript = %+v", d.Items)
	}
	if w := ts.do(http.MethodPost, path, "", auth); w.Code != http.StatusOK {
		t.Fatalf("repeat terminal = %d %s", w.Code, w.Body)
	}
	if len(conv.SubagentCancels()) != 1 {
		t.Fatal("terminal repeat reached provider")
	}
	if _, err := ts.m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(ts.prov.Opens())
	if w := ts.do(http.MethodPost, path, "", auth); w.Code != http.StatusOK {
		t.Fatalf("closed terminal = %d %s", w.Code, w.Body)
	}
	if len(ts.prov.Opens()) != opens {
		t.Fatal("terminal cancellation reopened conversation")
	}
}

func TestCancelSubagentFailuresAndLifecycle(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentRunning})
	path := "/api/sessions/" + sum.ID + "/subagents/target/cancel"
	for _, path := range []string{"/api/sessions/missing/subagents/target/cancel", "/api/sessions/" + sum.ID + "/subagents/missing/cancel"} {
		if w := ts.do(http.MethodPost, path, "", auth); w.Code != http.StatusNotFound {
			t.Fatalf("unknown = %d %s", w.Code, w.Body)
		}
	}
	conv.EmitInteraction(onceRequest("permission", "target"))
	conv.SetCancelSubagentHook(func(context.Context, string) error { return errors.New("provider refused") })
	if w := ts.do(http.MethodPost, path, "", auth); w.Code != http.StatusBadGateway {
		t.Fatalf("provider failure = %d %s", w.Code, w.Body)
	}
	d := detail(t, ts.m, sum.ID)
	if d.Subagents[0].Status != agentapi.SubagentRunning || d.Interactions[0].State != agentapi.InteractionPending {
		t.Fatalf("failed cancel changed state: %+v", d)
	}
	conv.SetCancelSubagentHook(nil)
	if w := ts.do(http.MethodPost, path, "", auth); w.Code != http.StatusOK {
		t.Fatalf("retry = %d %s", w.Code, w.Body)
	}
	if len(conv.SubagentCancels()) != 2 {
		t.Fatalf("retry calls = %v", conv.SubagentCancels())
	}
}

func TestCancelledSubagentInteractionsStayExpired(t *testing.T) {
	for _, source := range []string{"response", "event", "history"} {
		t.Run(source, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := createSession(t, m, prov)
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentRunning})
			conv.EmitInteraction(onceRequest("permission", "target"))
			conv.EmitInteraction(agentapi.Interaction{ID: "question", AgentID: "target", Kind: agentapi.InteractionQuestion, State: agentapi.InteractionPending})
			conv.EmitInteraction(onceRequest("sibling", "sibling"))
			switch source {
			case "response":
				if _, err := m.CancelSubagent(sum.ID, "target"); err != nil {
					t.Fatal(err)
				}
			case "event":
				conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentCancelled})
			case "history":
				// A fresh manager reconstructs cancellation from provider history,
				// then receives the permission the provider left pending.
				st := openTestStore(t)
				id := mustUUID(t)
				seedWebRecord(t, st, id, "cancelled-history", StateCompleted)
				prov.AddConversation("cancelled-history", nil, agentapi.Subagent{ID: "target", Status: agentapi.SubagentCancelled})
				m = startManager(t, st, prov)
				if err := m.View(context.Background(), id); err != nil {
					t.Fatal(err)
				}
				sum.ID, conv = id, prov.Last()
			}
			conv.EmitInteraction(onceRequest("permission", "target"))
			conv.EmitInteraction(agentapi.Interaction{ID: "question", AgentID: "target", Kind: agentapi.InteractionQuestion, State: agentapi.InteractionPending})
			conv.EmitInteraction(onceRequest("new-permission", "target"))
			conv.EmitInteraction(onceRequest("sibling", "sibling"))
			for _, ix := range detail(t, m, sum.ID).Interactions {
				want := agentapi.InteractionExpired
				if ix.AgentID == "sibling" {
					want = agentapi.InteractionPending
				}
				if ix.State != want {
					t.Fatalf("replayed interaction = %+v, want %s", ix, want)
				}
			}
			if _, err := m.Answer(sum.ID, "permission", agentapi.Answer{Decision: "once"}); statusOf(err) != http.StatusGone {
				t.Fatalf("late Respond = %v", err)
			}
		})
	}
}

func TestCancelSubagentTerminalDoesNotReopenInactiveTask(t *testing.T) {
	for _, terminal := range []agentapi.SubagentStatus{agentapi.SubagentCompleted, agentapi.SubagentFailed, agentapi.SubagentCancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{})
			sum, conv := createSession(t, ts.m, ts.prov)
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: terminal})
			if _, err := ts.m.Settle(sum.ID); err != nil {
				t.Fatal(err)
			}
			path := "/api/sessions/" + sum.ID + "/subagents/target/cancel"
			for _, archive := range []bool{false, true} {
				if archive {
					if _, err := ts.m.Archive(sum.ID); err != nil {
						t.Fatal(err)
					}
				}
				if w := ts.do(http.MethodPost, path, "", withCookie(ts)); w.Code != http.StatusOK {
					t.Fatalf("terminal inactive = %d %s", w.Code, w.Body)
				}
				if len(conv.SubagentCancels()) != 0 || len(ts.prov.Opens()) != 1 {
					t.Fatal("terminal inactive cancellation called provider")
				}
			}
		})
	}
}

func TestCancelSubagentProviderConflictAndCompletionRace(t *testing.T) {
	for _, providerErr := range []error{agentapi.ErrUnsupported, agentapi.ErrClosed} {
		t.Run(providerErr.Error(), func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{})
			sum, conv := createSession(t, ts.m, ts.prov)
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentRunning})
			conv.SetCancelSubagentHook(func(context.Context, string) error { return providerErr })
			path := "/api/sessions/" + sum.ID + "/subagents/target/cancel"
			if w := ts.do(http.MethodPost, path, "", withCookie(ts)); w.Code != http.StatusConflict {
				t.Fatalf("conflict = %d %s", w.Code, w.Body)
			}
		})
	}
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentRunning})
	conv.SetCancelSubagentHook(func(context.Context, string) error {
		conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentCompleted})
		return errors.New("already finished")
	})
	path := "/api/sessions/" + sum.ID + "/subagents/target/cancel"
	w := ts.do(http.MethodPost, path, "", withCookie(ts))
	var sa agentapi.Subagent
	if err := json.Unmarshal(w.Body.Bytes(), &sa); err != nil || w.Code != http.StatusOK || sa.Status != agentapi.SubagentCompleted {
		t.Fatalf("completion race = %d %s, %v", w.Code, w.Body, err)
	}
}

func TestCancelSubagentAcceptedCompletionRaceExpiresInteractions(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentRunning})
	conv.EmitInteraction(onceRequest("pending", "target"))
	conv.SetCancelSubagentHook(func(context.Context, string) error {
		conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentCompleted})
		return nil
	})
	if sa, err := m.CancelSubagent(sum.ID, "target"); err != nil || sa.Status != agentapi.SubagentCompleted {
		t.Fatalf("cancel = %+v, %v", sa, err)
	}
	if ix := detail(t, m, sum.ID).Interactions[0]; ix.State != agentapi.InteractionExpired {
		t.Fatalf("accepted stop left interaction = %+v", ix)
	}
}
