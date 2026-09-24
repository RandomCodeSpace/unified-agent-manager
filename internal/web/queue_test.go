package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// busySession creates a Task whose first prompt ("first") is running.
func busySession(t *testing.T, m *Manager, prov *agenttest.Provider) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	sum, conv := createSession(t, m, prov)
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "first", RequestID: mustUUID(t), Mode: ModeSend}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("first prompt = %+v, %v", sub, err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	return sum, conv
}

func mustSubmit(t *testing.T, m *Manager, id, text, rid, mode, status string) Submission {
	t.Helper()
	sub, err := m.Submit(id, PromptRequest{Text: text, RequestID: rid, Mode: mode})
	if err != nil || sub.Status != status || sub.RequestID != rid {
		t.Fatalf("Submit(%q, %s) = %+v, %v; want %s", text, mode, sub, err, status)
	}
	return sub
}

func queueTexts(d SessionDetail) string {
	var texts []string
	for _, q := range d.Queue {
		texts = append(texts, q.Text)
	}
	return strings.Join(texts, ",")
}

func waitLastSubmission(t *testing.T, m *Manager, id, rid string) Submission {
	t.Helper()
	var last Submission
	waitUntil(t, "submission "+rid, func() bool {
		d := detail(t, m, id)
		if d.LastSubmission != nil {
			last = *d.LastSubmission
		}
		return last.RequestID == rid
	})
	return last
}

func TestPromptModesWhileIdleAndBusy(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	// While idle, queue and steer send like send.
	for _, mode := range []string{ModeQueue, ModeSteer, ""} {
		mustSubmit(t, m, sum.ID, "idle "+mode, mustUUID(t), mode, SubmissionAccepted)
		conv.EmitTurn(agentapi.TurnCompleted, "")
	}
	if sends, d := conv.Sends(), detail(t, m, sum.ID); len(sends) != 3 || len(conv.Steers()) != 0 || len(d.Queue) != 0 {
		t.Fatalf("idle modes: sends=%q steers=%q queue=%+v", sends, conv.Steers(), d.Queue)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Mode: "later"}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown mode = %v, want 400", err)
	}

	// While a turn runs: send is refused, queue waits, steer joins the turn.
	for _, busy := range []func(){
		func() { conv.EmitTurn(agentapi.TurnWorking, "") },
		func() { conv.EmitInteraction(permissionRequest("p1")) }, // waiting for the user is a running turn too
	} {
		busy()
		if _, err := m.Submit(sum.ID, PromptRequest{Text: "send", RequestID: mustUUID(t), Mode: ModeSend}); statusOf(err) != http.StatusConflict {
			t.Fatalf("send while busy = %v, want 409", err)
		}
		mustSubmit(t, m, sum.ID, "queued", mustUUID(t), ModeQueue, SubmissionQueued)
		mustSubmit(t, m, sum.ID, "steered", mustUUID(t), ModeSteer, SubmissionAccepted)
	}
	d := detail(t, m, sum.ID)
	if d.State != StateAwaitingPermission || d.Queued != 2 || queueTexts(d) != "queued,queued" || len(conv.Sends()) != 3 {
		t.Fatalf("after busy modes: state=%s queued=%d queue=%s sends=%d", d.State, d.Queued, queueTexts(d), len(conv.Sends()))
	}
	if steers := conv.Steers(); strings.Join(steers, ",") != "steered,steered" {
		t.Fatalf("steers = %q", steers)
	}
}

func TestQueueDrainsInOrderAfterCompletedTurns(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	ridB, ridC := mustUUID(t), mustUUID(t)
	mustSubmit(t, m, sum.ID, "B", ridB, ModeQueue, SubmissionQueued)
	mustSubmit(t, m, sum.ID, "C", ridC, ModeQueue, SubmissionQueued)
	// A steer joins the running turn; its item arrives marked.
	mustSubmit(t, m, sum.ID, "steer", mustUUID(t), ModeSteer, SubmissionAccepted)
	conv.EmitItem(agentapi.Item{ID: "m-steer", Kind: agentapi.ItemUser, Text: "steer", Delivery: agentapi.DeliverySteer})
	time.Sleep(20 * time.Millisecond)
	if sends := conv.Sends(); len(sends) != 1 {
		t.Fatalf("queued prompts were sent while the turn ran: %q", sends)
	}

	conv.EmitTurn(agentapi.TurnCompleted, "")
	if last := waitLastSubmission(t, m, sum.ID, ridB); last.Status != SubmissionAccepted {
		t.Fatalf("drained B = %+v", last)
	}
	d := detail(t, m, sum.ID)
	if queueTexts(d) != "C" || d.Queued != 1 || d.State != StateWorking || strings.Join(conv.Sends(), ",") != "first,B" {
		t.Fatalf("after one completed turn: queue=%s state=%s sends=%q", queueTexts(d), d.State, conv.Sends())
	}
	raw, _ := json.Marshal(d.Items[0])
	if d.Items[0].Delivery != agentapi.DeliverySteer || !strings.Contains(string(raw), `"delivery":"steer"`) {
		t.Fatalf("steer item = %s", raw)
	}

	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitLastSubmission(t, m, sum.ID, ridC)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	time.Sleep(20 * time.Millisecond)
	if d := detail(t, m, sum.ID); len(d.Queue) != 0 || d.Queued != 0 || strings.Join(conv.Sends(), ",") != "first,B,C" {
		t.Fatalf("after draining: queue=%+v sends=%q", d.Queue, conv.Sends())
	}
}

func TestCancelPausesQueueBeforeProviderCompletesTurn(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	mustSubmit(t, m, sum.ID, "B", mustUUID(t), ModeQueue, SubmissionQueued)
	conv.SetCancelHook(func(context.Context) error {
		if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "B" {
			t.Fatalf("before provider cancellation: paused=%v queue=%s", d.QueuePaused, queueTexts(d))
		}
		// The turn finishes normally while Stop is reaching the provider.
		conv.EmitTurn(agentapi.TurnCompleted, "")
		return nil
	})
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "B" || d.State != StateCompleted || strings.Join(conv.Sends(), ",") != "first" {
		t.Fatalf("after Stop raced completion: paused=%v queue=%s state=%s sends=%q", d.QueuePaused, queueTexts(d), d.State, conv.Sends())
	}
}

func TestQueuePausesUntilResumedOrCleared(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	ridB := mustUUID(t)
	mustSubmit(t, m, sum.ID, "B", ridB, ModeQueue, SubmissionQueued)
	conv.EmitTurn(agentapi.TurnCancelled, "") // Stop turn
	// A later turn completing does not unpause it.
	mustSubmit(t, m, sum.ID, "next", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	time.Sleep(20 * time.Millisecond)
	if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "B" || strings.Join(conv.Sends(), ",") != "first,next" {
		t.Fatalf("after stop: paused=%v queue=%s sends=%q", d.QueuePaused, queueTexts(d), conv.Sends())
	}
	// While idle, queue sends at once even with a paused queue.
	mustSubmit(t, m, sum.ID, "now", mustUUID(t), ModeQueue, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnCompleted, "")

	if err := m.ResumeQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitLastSubmission(t, m, sum.ID, ridB)
	if d := detail(t, m, sum.ID); d.QueuePaused || len(d.Queue) != 0 || strings.Join(conv.Sends(), ",") != "first,next,now,B" {
		t.Fatalf("after resume: paused=%v queue=%s sends=%q", d.QueuePaused, queueTexts(d), conv.Sends())
	}
	// An empty queue is never paused.
	conv.EmitTurn(agentapi.TurnFailed, "boom")
	if d := detail(t, m, sum.ID); d.QueuePaused {
		t.Fatal("an empty queue was paused")
	}

	conv.EmitTurn(agentapi.TurnWorking, "")
	ridC := mustUUID(t)
	mustSubmit(t, m, sum.ID, "C", ridC, ModeQueue, SubmissionQueued)
	mustSubmit(t, m, sum.ID, "D", mustUUID(t), ModeQueue, SubmissionQueued)
	conv.EmitTurn(agentapi.TurnFailed, "boom")
	if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "C,D" {
		t.Fatalf("after a failed turn: paused=%v queue=%s", d.QueuePaused, queueTexts(d))
	}
	if err := m.ClearQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	if d := detail(t, m, sum.ID); d.QueuePaused || len(d.Queue) != 0 || d.Queued != 0 {
		t.Fatalf("after clear: paused=%v queue=%+v", d.QueuePaused, d.Queue)
	}
	// A retried request for a cleared prompt does not queue it again.
	mustSubmit(t, m, sum.ID, "C", ridC, ModeQueue, SubmissionCancelled)
	time.Sleep(20 * time.Millisecond)
	if d := detail(t, m, sum.ID); len(d.Queue) != 0 || len(conv.Sends()) != 4 || conv.Cancels() != 0 {
		t.Fatalf("clear reached the provider or re-queued: queue=%+v sends=%d cancels=%d", d.Queue, len(conv.Sends()), conv.Cancels())
	}
	if d := detail(t, m, sum.ID); d.LastSubmission.RequestID != ridB {
		t.Fatalf("clearing changed last_submission to %+v", d.LastSubmission)
	}
}

func TestQueuePausesWhenADrainedPromptIsNotAccepted(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	ridB, ridC := mustUUID(t), mustUUID(t)
	for _, q := range []struct{ text, rid string }{{"B", ridB}, {"C", ridC}, {"D", mustUUID(t)}} {
		mustSubmit(t, m, sum.ID, q.text, q.rid, ModeQueue, SubmissionQueued)
	}
	conv.SetSendHook(func(_ context.Context, prompt string) error {
		switch prompt {
		case "B":
			return errors.New("quota exceeded")
		case "C":
			return fmt.Errorf("stream reset: %w", agentapi.ErrSubmissionUncertain)
		}
		return nil
	})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if last := waitLastSubmission(t, m, sum.ID, ridB); last.Status != SubmissionRejected || last.Error != "quota exceeded" {
		t.Fatalf("drained B = %+v", last)
	}
	if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "C,D" {
		t.Fatalf("after a rejected prompt: paused=%v queue=%s", d.QueuePaused, queueTexts(d))
	}
	if err := m.ResumeQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	if last := waitLastSubmission(t, m, sum.ID, ridC); last.Status != SubmissionUncertain {
		t.Fatalf("drained C = %+v", last)
	}
	if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "D" || strings.Join(conv.Sends(), ",") != "first,B,C" {
		t.Fatalf("after an uncertain prompt: paused=%v queue=%s sends=%q", d.QueuePaused, queueTexts(d), conv.Sends())
	}
	// An uncertain prompt is never resent.
	mustSubmit(t, m, sum.ID, "C", ridC, ModeQueue, SubmissionUncertain)
	if n := len(conv.Sends()); n != 3 {
		t.Fatalf("sends = %d", n)
	}
}

func TestCancelQueuedPromptBeingSentReturnsConflict(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "B", rid, ModeQueue, SubmissionQueued)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	conv.SetSendHook(func(ctx context.Context, _ string) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("queued prompt did not reach Send")
	}
	if err := m.CancelQueued(sum.ID, rid); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "already sent") {
		t.Fatalf("cancel while sending = %v, want 409 already sent", err)
	}
	if d := detail(t, m, sum.ID); len(d.Queue) != 0 || d.LastSubmission.RequestID == rid || strings.Join(conv.Sends(), ",") != "first,B" || conv.Cancels() != 0 {
		t.Fatalf("in-flight cancellation changed delivery: queue=%+v last=%+v sends=%q cancels=%d", d.Queue, d.LastSubmission, conv.Sends(), conv.Cancels())
	}
}

func TestQueueCancelLimitAndRepeatedRequestIDs(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	rids := make([]string, maxQueue)
	for i := range rids {
		rids[i] = mustUUID(t)
		mustSubmit(t, m, sum.ID, fmt.Sprintf("p%d", i), rids[i], ModeQueue, SubmissionQueued)
	}
	first := detail(t, m, sum.ID).Queue[0]
	// A repeated request ID reports the queued prompt, in any mode.
	for _, mode := range []string{ModeQueue, ModeSend, ModeSteer} {
		if again := mustSubmit(t, m, sum.ID, "p0", rids[0], mode, SubmissionQueued); !again.Time.Equal(first.QueuedAt) {
			t.Fatalf("repeat time = %s, queued at %s", again.Time, first.QueuedAt)
		}
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "one more", RequestID: mustUUID(t), Mode: ModeQueue}); statusOf(err) != http.StatusConflict {
		t.Fatalf("21st prompt = %v, want 409", err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: strings.Repeat("x", maxPromptBytes+1), RequestID: mustUUID(t), Mode: ModeQueue}); statusOf(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized queued prompt = %v, want 413", err)
	}

	for range 2 { // cancelling again succeeds
		if err := m.CancelQueued(sum.ID, rids[1]); err != nil {
			t.Fatal(err)
		}
	}
	d := detail(t, m, sum.ID)
	if len(d.Queue) != maxQueue-1 || d.Queue[0].RequestID != rids[0] || d.Queue[1].RequestID != rids[2] {
		t.Fatalf("queue after cancel = %+v", d.Queue)
	}
	mustSubmit(t, m, sum.ID, "p1", rids[1], ModeQueue, SubmissionCancelled)
	if err := m.CancelQueued(sum.ID, mustUUID(t)); statusOf(err) != http.StatusNotFound {
		t.Fatalf("cancel unknown = %v, want 404", err)
	}
	if err := m.CancelQueued("missing", rids[0]); statusOf(err) != http.StatusNotFound {
		t.Fatalf("cancel in unknown session = %v, want 404", err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitLastSubmission(t, m, sum.ID, rids[0])
	if err := m.CancelQueued(sum.ID, rids[0]); statusOf(err) != http.StatusConflict {
		t.Fatalf("cancel of a sent prompt = %v, want 409", err)
	}
	if conv.Cancels() != 0 || len(conv.Sends()) != 2 || len(conv.Steers()) != 0 {
		t.Fatalf("provider calls: cancels=%d sends=%d steers=%d", conv.Cancels(), len(conv.Sends()), len(conv.Steers()))
	}
}

func TestSteerOutcomes(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	mustSubmit(t, m, sum.ID, "later", mustUUID(t), ModeQueue, SubmissionQueued)

	rid := mustUUID(t)
	accepted := mustSubmit(t, m, sum.ID, "use tabs", rid, ModeSteer, SubmissionAccepted)
	if again := mustSubmit(t, m, sum.ID, "use tabs", rid, ModeSteer, SubmissionAccepted); again != accepted || len(conv.Steers()) != 1 {
		t.Fatalf("repeated steer = %+v, steers %q", again, conv.Steers())
	}
	if d := detail(t, m, sum.ID); d.State != StateWorking || d.LastSubmission.RequestID != rid {
		t.Fatalf("after steer: state=%s last=%+v", d.State, d.LastSubmission)
	}
	// Delivered or not, the adapter reports it as an item.
	conv.EmitItem(agentapi.Item{ID: "m1", Kind: agentapi.ItemUser, Text: "use tabs", Delivery: agentapi.DeliverySteer})
	conv.EmitItem(agentapi.Item{ID: "steer-undelivered:m2", Kind: agentapi.ItemNotice, Text: "Steer not delivered: the turn was stopped"})

	conv.SetSteerHook(func(context.Context, string) error {
		return fmt.Errorf("pipe closed: %w", agentapi.ErrSubmissionUncertain)
	})
	ridU := mustUUID(t)
	uncertain := mustSubmit(t, m, sum.ID, "maybe", ridU, ModeSteer, SubmissionUncertain)
	if !strings.Contains(uncertain.Error, "did not resend") {
		t.Fatalf("uncertain steer = %+v", uncertain)
	}
	mustSubmit(t, m, sum.ID, "maybe", ridU, ModeSteer, SubmissionUncertain)
	conv.SetSteerHook(func(context.Context, string) error { return errors.New("refused") })
	mustSubmit(t, m, sum.ID, "no", mustUUID(t), ModeSteer, SubmissionRejected)
	conv.SetSteerHook(func(context.Context, string) error { return agentapi.ErrUnsupported })
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Mode: ModeSteer}); statusOf(err) != http.StatusConflict {
		t.Fatalf("unsupported steer = %v, want 409", err)
	}
	if n := len(conv.Steers()); n != 4 {
		t.Fatalf("steers = %d, want 4 (no resend)", n)
	}
	// None of it ended the turn, sent a prompt or paused the queue.
	d := detail(t, m, sum.ID)
	if d.State != StateWorking || len(conv.Sends()) != 1 || d.QueuePaused || queueTexts(d) != "later" {
		t.Fatalf("after steers: state=%s sends=%d paused=%v queue=%s", d.State, len(conv.Sends()), d.QueuePaused, queueTexts(d))
	}
	var kinds []string
	for _, it := range d.Items {
		kinds = append(kinds, string(it.Kind)+":"+it.Delivery)
	}
	if strings.Join(kinds, ",") != "user:steer,notice:" {
		t.Fatalf("items = %v", kinds)
	}
}

func TestQueueFramesFollowEveryChangeAndMoveUpdatedAt(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	sub, snapRaw, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap := parseFrame(t, snapRaw)
	if session := string(snap.data["session"]); !strings.Contains(session, `"queue":[]`) || !strings.Contains(session, `"queue_paused":false`) || !strings.Contains(session, `"queued":0`) {
		t.Fatalf("snapshot session = %s", session)
	}
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.mu.Unlock()

	type queueFrame struct {
		SessionID string         `json:"session_id"`
		Queue     []QueuedPrompt `json:"queue"`
		Paused    bool           `json:"paused"`
	}
	next := func(event string) frame {
		t.Helper()
		f := nextFrame(t, sub)
		if f.event != event {
			t.Fatalf("frame %s, want %s", f.event, event)
		}
		return f
	}
	queueOf := func(f frame) queueFrame {
		var q queueFrame
		if err := json.Unmarshal(mustJSON(t, f.data), &q); err != nil {
			t.Fatal(err)
		}
		return q
	}
	summaryOf := func(f frame) SessionSummary {
		var s SessionSummary
		decodeField(t, f, "session", &s)
		return s
	}

	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "B", rid, ModeQueue, SubmissionQueued)
	qf := next("queue")
	if q := queueOf(qf); q.SessionID != sum.ID || len(q.Queue) != 1 || q.Queue[0].RequestID != rid || q.Queue[0].Text != "B" || !q.Queue[0].QueuedAt.Equal(later) || q.Paused {
		t.Fatalf("queue frame = %+v", q)
	}
	sf := next("session")
	if s := summaryOf(sf); qf.seq <= snap.seq || sf.seq != qf.seq+1 || s.Queued != 1 || !s.UpdatedAt.Equal(later) {
		t.Fatalf("session frame seq %d after queue %d: %+v", sf.seq, qf.seq, s)
	}
	if d := detail(t, m, sum.ID); !d.UpdatedAt.Equal(later) {
		t.Fatalf("updated_at = %s, want %s", d.UpdatedAt, later)
	}

	// Stop turn: timing ends before the pause and state arrive together, in order.
	conv.EmitTurn(agentapi.TurnCancelled, "")
	tf := next("turn_timing")
	var timing TurnTiming
	decodeField(t, tf, "turn_timing", &timing)
	if timing.State != StateCancelled || !timing.EndedAt.Equal(later) || tf.seq != sf.seq+1 {
		t.Fatalf("stop timing frame seq %d after session %d: %+v", tf.seq, sf.seq, timing)
	}
	qf = next("queue")
	sf = next("session")
	if q, s := queueOf(qf), summaryOf(sf); !q.Paused || len(q.Queue) != 1 || s.State != StateCancelled || qf.seq != tf.seq+1 || sf.seq != qf.seq+1 {
		t.Fatalf("stop frames: queue %+v, session %+v", q, s)
	}
	// Resuming is a queue change too, and drains at once.
	evenLater := later.Add(time.Minute)
	m.mu.Lock()
	m.now = func() time.Time { return evenLater }
	m.mu.Unlock()
	if err := m.ResumeQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	if q := queueOf(next("queue")); q.Paused || len(q.Queue) != 1 {
		t.Fatalf("resume frame = %+v", q)
	}
	if s := summaryOf(next("session")); !s.UpdatedAt.Equal(evenLater) {
		t.Fatalf("resume updated_at = %s", s.UpdatedAt)
	}
	if q := queueOf(next("queue")); len(q.Queue) != 0 {
		t.Fatalf("drain frame = %+v", q)
	}
	if s := summaryOf(next("session")); s.Queued != 0 {
		t.Fatalf("drain summary = %+v", s)
	}
	if s := summaryOf(next("session")); s.State != StateWorking {
		t.Fatalf("drained turn summary = %+v", s)
	}
	if f := next("submission"); !strings.Contains(string(f.data["submission"]), rid) {
		t.Fatalf("submission frame = %s", f.data["submission"])
	}
	waitUntil(t, "updated_at stored", func() bool {
		rec, _ := loadRecord(t, st, "fake", sum.ID)
		return rec.Web != nil && rec.Web.UpdatedAt.Equal(evenLater)
	})
}

func mustJSON(t *testing.T, fields map[string]json.RawMessage) []byte {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestServiceStopDropsTheQueue(t *testing.T) {
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum, conv := busySession(t, m, prov)
	mustSubmit(t, m, sum.ID, "B", mustUUID(t), ModeQueue, SubmissionQueued)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(conv.Sends()) != 1 {
		t.Fatalf("stopping sent the queue: %q", conv.Sends())
	}
	m2 := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if d := detail(t, m2, sum.ID); d.State != StateInterrupted || d.Queued != 0 || len(d.Queue) != 0 || d.QueuePaused {
		t.Fatalf("after restart: state=%s queued=%d queue=%+v", d.State, d.Queued, d.Queue)
	}
}
