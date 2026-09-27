package web

import (
	"fmt"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A create whose request ID already named a prompt of a Task returns that
// Task instead of opening another conversation.
func TestCreateRepeatingAPromptRequestIDReturnsItsTask(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "work", rid, ModeSend, SubmissionAccepted)
	got, err := m.Create(CreateRequest{Provider: "fake", ProjectID: sum.ProjectID, Prompt: "work", RequestID: rid})
	if err != nil || got.ID != sum.ID {
		t.Fatalf("create = %+v, %v; want task %s", got, err, sum.ID)
	}
	if len(prov.Opens()) != 1 || len(conv.Sends()) != 1 || len(m.List()) != 1 {
		t.Fatalf("repeat reached the provider: opens %d, sends %v, tasks %d", len(prov.Opens()), conv.Sends(), len(m.List()))
	}
}

// Outcomes are kept for the latest maxSubmissions request IDs: repeating one
// of those sends nothing, and an older one is a new prompt.
func TestOnlyTheLatestSubmissionsAreRemembered(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	var rids []string
	for i := range maxSubmissions + 1 {
		rid := mustUUID(t)
		rids = append(rids, rid)
		mustSubmit(t, m, sum.ID, fmt.Sprintf("p%d", i), rid, ModeSend, SubmissionAccepted)
		conv.EmitTurn(agentapi.TurnCompleted, "")
	}
	mustSubmit(t, m, sum.ID, "again", rids[1], ModeSend, SubmissionAccepted)
	if n := len(conv.Sends()); n != maxSubmissions+1 {
		t.Fatalf("a remembered request was sent again: %d sends", n)
	}
	mustSubmit(t, m, sum.ID, "again", rids[0], ModeSend, SubmissionAccepted)
	if sends := conv.Sends(); len(sends) != maxSubmissions+2 || sends[len(sends)-1] != "again" {
		t.Fatalf("a forgotten request was not sent: %d sends", len(sends))
	}
}
