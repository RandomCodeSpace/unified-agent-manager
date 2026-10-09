package web

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func resendRequest(t *testing.T, m *Manager, id, mode string) ResendRequest {
	t.Helper()
	return ResendRequest{
		Rewind: RewindRequest{UserItemID: "u-second", Mode: mode, Token: previewToken(t, m, id), RequestID: mustUUID(t)},
		Prompt: PromptRequest{Text: "second, edited", RequestID: mustUUID(t)},
	}
}

func rewindResult(outcome string) agentapi.RewindResult {
	r := agentapi.RewindResult{Outcome: outcome, RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}
	switch outcome {
	case "success", "checkpoint-cleanup-failed", "snapshot-prune-failed":
		n := int64(6)
		r.EventsRemoved = &n
	}
	return r
}

func TestResendRewindsThenSendsTheEditedPromptOnce(t *testing.T) {
	m, p, _, sum, rc := rewindManager(t)
	rc.rewind = func(string, string) (agentapi.RewindResult, error) { return rewindResult("success"), nil }
	// The authoritative reread is slow: the send must wait for it, not race the hold.
	reread := make(chan struct{})
	p.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
		<-reread
		return agentapi.History{Items: []agentapi.Item{{ID: "u-first", Kind: agentapi.ItemUser, Text: "first"}}}, nil
	})
	go func() { time.Sleep(100 * time.Millisecond); close(reread) }()
	req := resendRequest(t, m, sum.ID, agentapi.RewindConversationAndFiles)
	got, err := m.Resend(sum.ID, req)
	if err != nil || got.Rewind.State == rewindUncertain || got.Submission == nil || got.Submission.Status != "accepted" || got.SendError != "" {
		t.Fatalf("resend = %+v, %v", got, err)
	}
	if len(p.Opens()) != 2 || rc.Closes() != 1 {
		t.Fatalf("opens %d closes %d: the send must reopen the conversation explicitly after the rewind", len(p.Opens()), rc.Closes())
	}
	sent := p.Last().Prompts()
	if len(sent) != 1 || sent[0].Text != "second, edited" {
		t.Fatalf("sent = %+v", sent)
	}
	again, err := m.Resend(sum.ID, req)
	if err != nil || again.Submission == nil || again.Submission.RequestID != req.Prompt.RequestID {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	if rc.rewinds() != 1 || len(p.Last().Prompts()) != 1 {
		t.Fatalf("repeat rewound %d times or resent %d prompts", rc.rewinds(), len(p.Last().Prompts()))
	}
}

func TestResendSendsNothingUnlessHistoryWasTruncated(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(string, string) (agentapi.RewindResult, error)
		hold bool
	}{
		{"uncertain", func(string, string) (agentapi.RewindResult, error) {
			return agentapi.RewindResult{}, fmt.Errorf("%w: lost", agentapi.ErrRewindUncertain)
		}, true},
		{"truncation-failed", func(string, string) (agentapi.RewindResult, error) { return rewindResult("truncation-failed"), nil }, false},
		{"rollback-incomplete", func(string, string) (agentapi.RewindResult, error) { return rewindResult("rollback-incomplete"), nil }, false},
		{"files-rolled-back", func(string, string) (agentapi.RewindResult, error) { return rewindResult("files-rolled-back"), nil }, false},
		{"session-busy", func(string, string) (agentapi.RewindResult, error) { return rewindResult("session-busy"), nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p, _, sum, rc := rewindManager(t)
			rc.rewind = tc.run
			got, err := m.Resend(sum.ID, resendRequest(t, m, sum.ID, agentapi.RewindConversationAndFiles))
			if err != nil || got.Submission != nil || got.SendError != "" {
				t.Fatalf("resend = %+v, %v", got, err)
			}
			for _, c := range p.Conversations() {
				if len(c.Prompts()) != 0 {
					t.Fatal("an edited prompt was sent after a rewind that did not truncate history")
				}
			}
			if tc.hold != (detail(t, m, sum.ID).Rewind.State == rewindUncertain) {
				t.Fatalf("hold = %+v", detail(t, m, sum.ID).Rewind)
			}
		})
	}
}

func TestResendKeepsARejectedSendWithoutRewindingAgain(t *testing.T) {
	m, p, _, sum, rc := rewindManager(t)
	rc.rewind = func(string, string) (agentapi.RewindResult, error) { return rewindResult("success"), nil }
	req := resendRequest(t, m, sum.ID, agentapi.RewindConversation)
	req.Prompt.Attachments = []string{"missing-upload"}
	got, err := m.Resend(sum.ID, req)
	if err != nil || got.Submission != nil || got.SendError == "" || got.Rewind.Result == nil || got.Rewind.Result.Outcome != "success" {
		t.Fatalf("rejected send = %+v, %v", got, err)
	}
	for _, c := range p.Conversations() {
		if len(c.Prompts()) != 0 {
			t.Fatal("rejected prompt reached the provider")
		}
	}
	// Retrying the same request with the attachment fixed sends once and never rewinds again.
	req.Prompt.Attachments = nil
	got, err = m.Resend(sum.ID, req)
	if err != nil || got.Submission == nil || rc.rewinds() != 1 || len(p.Last().Prompts()) != 1 {
		t.Fatalf("retry = %+v, %v, rewinds %d", got, err, rc.rewinds())
	}
}

func TestResendRefusesInvalidAndStaleRequestsBeforeAnyMutation(t *testing.T) {
	m, _, _, sum, rc := rewindManager(t)
	rc.rewind = func(string, string) (agentapi.RewindResult, error) {
		t.Fatal("refused resend reached the provider")
		return agentapi.RewindResult{}, nil
	}
	req := resendRequest(t, m, sum.ID, agentapi.RewindConversation)
	same := req
	same.Prompt.RequestID = same.Rewind.RequestID
	if _, err := m.Resend(sum.ID, same); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("shared request ID = %v", err)
	}
	queued := req
	queued.Prompt.Mode = ModeQueue
	if _, err := m.Resend(sum.ID, queued); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("queued = %v", err)
	}
	stale := req
	stale.Rewind.Token = "old"
	if _, err := m.Resend(sum.ID, stale); statusOf(err) != http.StatusConflict {
		t.Fatalf("stale = %v", err)
	}
	if rc.rewinds() != 0 || len(rc.Prompts()) != 0 || detail(t, m, sum.ID).Rewind != (RewindStatus{}) {
		t.Fatal("refused resend changed the task")
	}
}
