package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestAcceptanceRetentionPreservesPendingInteractionsAndRunningSubagents(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitInteraction(onceRequest("pending", ""))
	conv.EmitSubagent(agentapi.Subagent{ID: "running", Status: agentapi.SubagentRunning})
	for i := range maxInteractions {
		conv.EmitInteraction(agentapi.Interaction{ID: fmt.Sprintf("ended-%d", i), Kind: agentapi.InteractionPermission, State: agentapi.InteractionExpired})
	}
	for i := range maxSubagents {
		conv.EmitSubagent(agentapi.Subagent{ID: fmt.Sprintf("ended-%d", i), Status: agentapi.SubagentCompleted})
	}
	d := detail(t, m, sum.ID)
	if len(d.Interactions) != maxInteractions || d.Interactions[0].ID != "pending" || len(d.Subagents) != maxSubagents || d.Subagents[0].ID != "running" {
		t.Fatalf("retention dropped live work: interactions=%d first=%s subagents=%d first=%s", len(d.Interactions), d.Interactions[0].ID, len(d.Subagents), d.Subagents[0].ID)
	}
	if _, err := m.Answer(sum.ID, "ended-0", agentapi.Answer{Decision: "once"}); statusOf(err) != http.StatusNotFound {
		t.Fatalf("evicted interaction remains addressable: %v", err)
	}
	if _, err := m.Subagent(sum.ID, "ended-0"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("evicted subagent remains addressable: %v", err)
	}
	if _, err := m.Answer(sum.ID, "pending", agentapi.Answer{Decision: "once"}); err != nil {
		t.Fatalf("retained permission cannot be answered: %v", err)
	}
	if sa, err := m.Subagent(sum.ID, "running"); err != nil || sa.Subagent.Status != agentapi.SubagentRunning {
		t.Fatalf("retained subagent = %+v, %v", sa, err)
	}
}

func TestAcceptanceTranscriptByteLimitKeepsNewestOutput(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	text := strings.Repeat("x", maxItemText)
	count := maxSessionBytes/maxItemText + 2
	for i := range count {
		conv.EmitItem(agentapi.Item{ID: fmt.Sprintf("large-%d", i), Kind: agentapi.ItemAssistant, Text: text})
	}
	d := detail(t, m, sum.ID)
	if !d.HistoryTruncated || len(d.Items) == 0 || d.Items[0].ID == "large-0" || d.Items[len(d.Items)-1].ID != fmt.Sprintf("large-%d", count-1) {
		t.Fatalf("bounded transcript: truncated=%v items=%d", d.HistoryTruncated, len(d.Items))
	}
	bytes := 0
	for _, item := range d.Items {
		bytes += len(item.Text)
	}
	if bytes > maxSessionBytes {
		t.Fatalf("retained %d text bytes", bytes)
	}
	// An update to retained output must replace it, not duplicate a stale index.
	last := d.Items[len(d.Items)-1].ID
	conv.EmitItem(agentapi.Item{ID: last, Kind: agentapi.ItemAssistant, Text: "final"})
	after := detail(t, m, sum.ID)
	if len(after.Items) != len(d.Items) || after.Items[len(after.Items)-1].Text != "final" {
		t.Fatal("trimming broke subsequent item updates")
	}
}

func TestAcceptanceCancelFailureKeepsQueuePausedAndTurnTruthful(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"unsupported", agentapi.ErrUnsupported, http.StatusConflict},
		{"closed", agentapi.ErrClosed, http.StatusConflict},
		{"provider refusal", errors.New("provider refused"), http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{})
			sum, conv := busySession(t, ts.m, ts.prov)
			mustSubmit(t, ts.m, sum.ID, "later", mustUUID(t), ModeQueue, SubmissionQueued)
			conv.SetCancelError(tc.err)
			w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/cancel", "", withCookie(ts))
			if w.Code != tc.status {
				t.Fatalf("cancel = %d %s", w.Code, w.Body)
			}
			d := detail(t, ts.m, sum.ID)
			if d.State != StateWorking || !d.QueuePaused || queueTexts(d) != "later" || conv.Cancels() != 1 {
				t.Fatalf("refusal fabricated completion or resumed work: state=%s paused=%v queue=%s", d.State, d.QueuePaused, queueTexts(d))
			}
			conv.EmitTurn(agentapi.TurnCompleted, "")
			if queueTexts(detail(t, ts.m, sum.ID)) != "later" || len(conv.Sends()) != 1 {
				t.Fatal("normal completion after failed Stop ran queued work")
			}
		})
	}
}

func TestAcceptanceAnswerFailureAllowsExplicitRetry(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.EmitInteraction(onceRequest("permission", ""))
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { return errors.New("provider unavailable") })
	path := "/api/sessions/" + sum.ID + "/interactions/permission"
	if w := ts.do(http.MethodPost, path, "{\"decision\":\"once\"}", withCookie(ts)); w.Code != http.StatusBadGateway {
		t.Fatalf("refused answer = %d %s", w.Code, w.Body)
	}
	if d := detail(t, ts.m, sum.ID); d.Pending != 1 || d.Interactions[0].State != agentapi.InteractionPending {
		t.Fatal("failed answer was reported as resolved")
	}
	conv.SetRespondHook(nil)
	if w := ts.do(http.MethodPost, path, "{\"decision\":\"once\"}", withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("explicit retry = %d %s", w.Code, w.Body)
	}
	if d := detail(t, ts.m, sum.ID); d.Pending != 0 || d.Interactions[0].State != agentapi.InteractionAnswered {
		t.Fatal("successful retry did not resolve permission")
	}
}

type acceptanceReopenProvider struct {
	*agenttest.Provider
	failure string
}

type acceptanceWrongConversation struct{ agentapi.Conversation }

func (acceptanceWrongConversation) ID() string { return "another-conversation" }

func (p acceptanceReopenProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	conv, err := p.Provider.Open(ctx, req)
	if err != nil || req.ConversationID == "" {
		return conv, err
	}
	if p.failure == "wrong identity" {
		return acceptanceWrongConversation{conv}, nil
	}
	req.Events.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: "exited while reopening"})
	return conv, nil
}

func TestAcceptanceReopenRejectsWrongOrAlreadyExitedConversation(t *testing.T) {
	for _, failure := range []string{"wrong identity", "exited"} {
		t.Run(failure, func(t *testing.T) {
			prov := agenttest.NewProvider("fake", allCaps)
			m := startManager(t, openTestStore(t), acceptanceReopenProvider{prov, failure})
			sum, _ := createSession(t, m, prov)
			if _, err := m.Close(sum.ID); err != nil {
				t.Fatal(err)
			}
			sub, err := m.Submit(sum.ID, "must not reach a replacement", mustUUID(t), ModeSend)
			if err != nil || sub.Status != SubmissionRejected {
				t.Fatalf("failed reopen submission = %+v, %v", sub, err)
			}
			d := detail(t, m, sum.ID)
			if d.Open || d.ConversationID != sum.ConversationID || d.State != StateFailed {
				t.Fatalf("failed reopen changed identity or attached: %+v", d.SessionSummary)
			}
			if len(prov.Opens()) != 2 || prov.Last().Closes() != 1 || len(prov.Last().Sends()) != 0 {
				t.Fatal("reopen failed to close the unusable conversation or sent a prompt")
			}
		})
	}
}

func TestAcceptancePersistenceFailureRetriesAndNeverRecreatesRemovedRecord(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	// A directory at the store path produces a deterministic I/O failure,
	// including when tests run as root. All paths belong to this test.
	backup := st.Path() + ".saved"
	if err := os.Rename(st.Path(), backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(st.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if !restored {
			// Keep the background writer from treating the temporary gap
			// between Remove and Rename as an intentionally empty store.
			m.persistMu.Lock()
			defer m.persistMu.Unlock()
			_ = os.Remove(st.Path())
			if err := os.Rename(backup, st.Path()); err != nil {
				t.Errorf("restore test store: %v", err)
			}
			restored = true
		}
	}
	defer restore()
	if _, err := m.Rename(sum.ID, "retained through failure"); err != nil {
		t.Fatal(err)
	}
	if err := m.flush(); err == nil {
		t.Fatal("store failure was hidden")
	}
	restore()
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	if rec.Name != "retained through failure" {
		t.Fatalf("pending update lost after I/O failure: %q", rec.Name)
	}
	if err := st.Update(func(cfg *store.Config) error {
		delete(cfg.Sessions, store.Key("fake", sum.ID))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rename(sum.ID, "must not resurrect"); err != nil {
		t.Fatal(err)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sessions[store.Key("fake", sum.ID)]; ok {
		t.Fatal("flush recreated an externally removed record")
	}
}
