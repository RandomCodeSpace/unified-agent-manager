package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const queuedAttachmentBytes = "ATTACHMENT-BYTES"

// restartWithQueue queues "a" and "b" (which carries an attachment) behind a
// running turn, stops the service and starts it again on the same store. It
// returns the new service and the queue as it was before the stop.
func restartWithQueue(t *testing.T) (m2 *Manager, prov2 *agenttest.Provider, st *store.Store, sum SessionSummary, a, b string, queued []QueuedPrompt) {
	t.Helper()
	m, prov, st := newTestManager(t)
	sum, _ = busySession(t, m, prov)
	att := mustUpload(t, m, sum.ID, "notes.txt", []byte(queuedAttachmentBytes))
	a, b = mustUUID(t), mustUUID(t)
	mustSubmit(t, m, sum.ID, "a", a, ModeQueue, SubmissionQueued)
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "b", RequestID: b, Mode: ModeQueue, Attachments: []string{att.ID}}); err != nil || sub.Status != SubmissionQueued {
		t.Fatalf("queue with an attachment = %+v, %v", sub, err)
	}
	queued = detail(t, m, sum.ID).Queue
	// A kill loses nothing: the queue is on disk as soon as the change is
	// saved, by reference and without the attachment's bytes.
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ := loadRecord(t, st, prov.Name(), sum.ID)
	if saved := string(rec.Web.Queue); !strings.Contains(saved, a) || !strings.Contains(saved, att.ID) || strings.Contains(saved, queuedAttachmentBytes) {
		t.Fatalf("saved queue = %s", saved)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 = agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(sum.ConversationID, nil)
	return startManager(t, st, prov2), prov2, st, sum, a, b, queued
}

func queueJSON(t *testing.T, queue []QueuedPrompt) string {
	t.Helper()
	data, err := json.Marshal(queue)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A Thread's queued prompts come back after a restart as they were, paused:
// nothing is sent until the owner resumes the queue.
func TestQueuedPromptsSurviveARestartPaused(t *testing.T) {
	m, prov, _, sum, a, _, queued := restartWithQueue(t)
	d := detail(t, m, sum.ID)
	if d.State != StateInterrupted || !d.QueuePaused || d.Queued != 2 || queueJSON(t, d.Queue) != queueJSON(t, queued) {
		t.Fatalf("after the restart: state %s, paused %v, queued %d, queue %s, want %s", d.State, d.QueuePaused, d.Queued, queueJSON(t, d.Queue), queueJSON(t, queued))
	}
	if len(d.Queue[1].Attachments) != 1 || d.Queue[1].Settings != queued[1].Settings {
		t.Fatalf("restored prompt = %+v", d.Queue[1])
	}
	// The same request sent again is still the queued prompt, not a new one.
	mustSubmit(t, m, sum.ID, "a", a, ModeQueue, SubmissionQueued)
	if opens := prov.Opens(); len(opens) != 0 || len(detail(t, m, sum.ID).Queue) != 2 {
		t.Fatalf("the restored queue did not wait: %d opens", len(opens))
	}
}

// The owner removes a restored prompt or resumes the restored queue as with
// any paused queue, and both are saved.
func TestRestoredQueueIsRemovedAndResumedByTheOwner(t *testing.T) {
	m, prov, st, sum, a, b, _ := restartWithQueue(t)
	if err := m.CancelQueued(sum.ID, a); err != nil {
		t.Fatal(err)
	}
	if d := detail(t, m, sum.ID); queueTexts(d) != "b" || !d.QueuePaused {
		t.Fatalf("after removing one: queue %q, paused %v", queueTexts(d), d.QueuePaused)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ := loadRecord(t, st, prov.Name(), sum.ID)
	if saved := string(rec.Web.Queue); strings.Contains(saved, a) || !strings.Contains(saved, b) {
		t.Fatalf("saved queue after removing one = %s", saved)
	}

	if err := m.ResumeQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	if last := waitLastSubmission(t, m, sum.ID, b); last.Status != SubmissionAccepted {
		t.Fatalf("resumed prompt = %+v", last)
	}
	if sends := prov.Last().Sends(); len(sends) != 1 || sends[0] != "b" {
		t.Fatalf("sends after the resume = %q", sends)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	rec, _ = loadRecord(t, st, prov.Name(), sum.ID)
	if d := detail(t, m, sum.ID); len(d.Queue) != 0 || d.QueuePaused || len(rec.Web.Queue) != 0 {
		t.Fatalf("after the resume: queue %q, paused %v, saved %s", queueTexts(d), d.QueuePaused, rec.Web.Queue)
	}
}

// A saved queue is untrusted: only the owner's well-formed prompts load.
func TestLoadedQueueKeepsOnlyWellFormedOwnerPrompts(t *testing.T) {
	good, peer := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	saved, err := json.Marshal([]QueuedPrompt{
		{RequestID: good, Text: "keep"},
		{RequestID: good, Text: "same request again"},
		{RequestID: "not-a-uuid", Text: "bad id"},
		{RequestID: peer, Text: "from another Thread", From: &store.TurnPeer{Pair: "p"}},
		{RequestID: "33333333-3333-4333-8333-333333333333", Text: strings.Repeat("x", maxPromptBytes+1)},
		{RequestID: "44444444-4444-4444-8444-444444444444", Text: " "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loadedQueue(saved); len(got) != 1 || got[0].Text != "keep" {
		t.Fatalf("loaded queue = %+v", got)
	}
	if got := loadedQueue(json.RawMessage(`{"not":"a list"}`)); got != nil {
		t.Fatalf("a queue that is not a list = %+v", got)
	}
	var many []QueuedPrompt
	for range maxQueue + 3 {
		many = append(many, QueuedPrompt{RequestID: mustUUID(t), Text: "x"})
	}
	saved, _ = json.Marshal(many)
	if got := loadedQueue(saved); len(got) != maxQueue {
		t.Fatalf("loaded %d prompts, want %d", len(got), maxQueue)
	}
}

// A queued prompt the stop caught on its way out, before anything was sent,
// is back in the queue after the restart.
func TestQueuedPromptTakenForSendingWhenTheServiceStopsIsKept(t *testing.T) {
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum, conv := busySession(t, m, prov)
	mustSubmit(t, m, sum.ID, "B", mustUUID(t), ModeQueue, SubmissionQueued)
	// A git action in the Thread's folder holds the next turn's start.
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.gitWrites = map[string]chan struct{}{realPath(s.workdir): make(chan struct{})}
	m.mu.Unlock()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the queued prompt is taken for sending", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return s.queueSending != ""
	})
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(conv.Sends()) != 1 {
		t.Fatalf("stopping sent the queue: %q", conv.Sends())
	}
	m2 := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if d := detail(t, m2, sum.ID); queueTexts(d) != "B" || !d.QueuePaused {
		t.Fatalf("after the restart: queue %q, paused %v", queueTexts(d), d.QueuePaused)
	}
}
