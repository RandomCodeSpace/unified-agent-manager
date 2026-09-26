package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestSubagentSnapshotSeparatesCapturedAndLaterEvents(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := ts.login(t, "127.0.0.1:8260")
	sum, conv := createSession(t, ts.m, ts.prov)
	sub, _, err := ts.m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.m.Unsubscribe(sub)
	conv.EmitSubagent(agentapi.Subagent{ID: "helper", Name: "Helper", Status: agentapi.SubagentRunning})
	conv.EmitItem(agentapi.Item{ID: "reply", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "a"})
	var capturedSeq uint64
	decodeField(t, frameOf(t, sub, "item"), "seq", &capturedSeq)
	path := "/api/sessions/" + sum.ID + "/subagents/helper"
	w := ts.do(http.MethodGet, path, "", auth)
	// Decode the wire contract independently so a missing seq is a failure,
	// including when the manager's service-wide sequence is zero.
	var snapshot struct {
		Seq      *uint64           `json:"seq"`
		Subagent agentapi.Subagent `json:"subagent"`
		Items    []agentapi.Item   `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || w.Code != http.StatusOK {
		t.Fatalf("subagent response = %d %s, %v", w.Code, w.Body, err)
	}
	if snapshot.Seq == nil || *snapshot.Seq < capturedSeq {
		t.Fatalf("snapshot must include the captured item's sequence %d: %s", capturedSeq, w.Body)
	}
	if snapshot.Subagent.Status != agentapi.SubagentRunning || len(snapshot.Items) != 1 || snapshot.Items[0].Text != "a" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: "reply", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "b"}})
	var delta deltaEvent
	f := frameOf(t, sub, "delta")
	decodeField(t, f, "seq", &delta.Seq)
	decodeField(t, f, "text", &delta.Text)
	if delta.Seq <= *snapshot.Seq || delta.Text != "b" {
		t.Fatalf("later delta = %+v, snapshot seq = %d", delta, *snapshot.Seq)
	}
	conv.EmitSubagent(agentapi.Subagent{ID: "helper", Status: agentapi.SubagentCompleted})
	var completedSeq uint64
	decodeField(t, frameOf(t, sub, "subagent"), "seq", &completedSeq)
	if completedSeq <= delta.Seq {
		t.Fatalf("completion seq = %d, delta seq = %d", completedSeq, delta.Seq)
	}
	w = ts.do(http.MethodGet, path, "", auth)
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || w.Code != http.StatusOK || snapshot.Seq == nil || *snapshot.Seq < completedSeq {
		t.Fatalf("next snapshot = %d %s, %v", w.Code, w.Body, err)
	}
	if snapshot.Subagent.Status != agentapi.SubagentCompleted || len(snapshot.Items) != 1 || snapshot.Items[0].Text != "ab" {
		t.Fatalf("next snapshot = %+v", snapshot)
	}
}
