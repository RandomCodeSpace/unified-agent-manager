package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var allCaps = agentapi.Capabilities{Cancel: true, Permissions: true, Questions: true, SessionDiff: true, History: true}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func startManager(t *testing.T, st *store.Store, providers ...agentapi.Provider) *Manager {
	t.Helper()
	m := NewManager(st, providers)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func newTestManager(t *testing.T) (*Manager, *agenttest.Provider, *store.Store) {
	t.Helper()
	prov := agenttest.NewProvider("fake", allCaps)
	st := openTestStore(t)
	return startManager(t, st, prov), prov, st
}

// addProject adds dir as a Project and returns its ID.
func addProject(t *testing.T, m *Manager, dir string) string {
	t.Helper()
	p, err := m.AddProject(dir, "", nil)
	if err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return p.ID
}

func createSession(t *testing.T, m *Manager, prov *agenttest.Provider) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir()), Name: "task"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	conv := prov.Last()
	if conv == nil || conv.ID() != sum.ConversationID {
		t.Fatalf("conversation = %v, summary = %+v", conv, sum)
	}
	return sum, conv
}

func mustUUID(t *testing.T) string {
	t.Helper()
	id, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func statusOf(err error) int {
	status, _ := errorStatus(err)
	return status
}

func detail(t *testing.T, m *Manager, id string) SessionDetail {
	t.Helper()
	d, err := m.Detail(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type frame struct {
	event string
	data  map[string]json.RawMessage
	seq   uint64
}

func parseFrame(t *testing.T, raw []byte) frame {
	t.Helper()
	text := strings.TrimPrefix(string(raw), "retry: 2000\n\n")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
		t.Fatalf("malformed frame %q", raw)
	}
	f := frame{event: strings.TrimPrefix(lines[0], "event: ")}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &f.data); err != nil {
		t.Fatalf("frame data: %v", err)
	}
	if err := json.Unmarshal(f.data["seq"], &f.seq); err != nil {
		t.Fatalf("frame seq: %v", err)
	}
	return f
}

func TestViewerDisconnectMidTurnLeavesProviderRunning(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "do it", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitDelta("a1", agentapi.ItemAssistant, "hel")
	m.Unsubscribe(sub) // the browser tab closes mid-turn
	conv.EmitDelta("a1", agentapi.ItemAssistant, "lo")
	conv.EmitItem(agentapi.Item{ID: "t1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted}})
	conv.EmitTurn(agentapi.TurnCompleted, "")

	d := detail(t, m, sum.ID)
	if d.State != StateCompleted || len(d.Items) != 2 || d.Items[0].Text != "hello" {
		t.Fatalf("state advanced wrongly after disconnect: %+v", d)
	}
	if conv.Cancels() != 0 || conv.Closes() != 0 || len(conv.Sends()) != 1 {
		t.Fatalf("viewer disconnect reached the provider: cancels=%d closes=%d sends=%d", conv.Cancels(), conv.Closes(), len(conv.Sends()))
	}
}

func TestEmitNeverBlocksOnAbsentOrStalledSubscribers(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)

	start := time.Now()
	for i := range 20000 {
		conv.EmitDelta("a1", agentapi.ItemAssistant, fmt.Sprintf("tok%d ", i))
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("emitting with no subscribers took %s", elapsed)
	}

	stalled, _, err := m.Subscribe(sum.ID) // never read
	if err != nil {
		t.Fatal(err)
	}
	global, _, err := m.Subscribe("") // never read either
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 1024)
	start = time.Now()
	for i := range 5000 {
		conv.EmitItem(agentapi.Item{ID: fmt.Sprintf("i%d", i), Kind: agentapi.ItemAssistant, Text: payload})
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("emitting with a stalled subscriber took %s", elapsed)
	}
	select {
	case <-stalled.Gone():
	default:
		t.Fatal("a subscriber that never reads must be dropped, not waited on")
	}
	select {
	case <-global.Gone():
		t.Fatal("the summary-only subscriber got few events and must still be connected")
	default:
	}
	d := detail(t, m, sum.ID)
	if d.State != StateWorking || len(d.Items) == 0 || len(d.Items) > maxItems {
		t.Fatalf("events not processed: state=%s items=%d", d.State, len(d.Items))
	}
}

func TestSnapshotHasAccumulatedItemsOnceAndLaterEventsHaveGreaterSeq(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	first, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	conv.EmitItem(agentapi.Item{ID: "a", Kind: agentapi.ItemAssistant, Text: "one"})
	conv.EmitItem(agentapi.Item{ID: "b", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolRunning}})
	conv.EmitItem(agentapi.Item{ID: "b", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted}})
	conv.EmitDelta("a", agentapi.ItemAssistant, " two")
	m.Unsubscribe(first)

	sub, snapRaw, err := m.Subscribe(sum.ID) // browser reconnects
	if err != nil {
		t.Fatal(err)
	}
	snap := parseFrame(t, snapRaw)
	if snap.event != "snapshot" {
		t.Fatalf("first frame = %s", snap.event)
	}
	var session SessionDetail
	if err := json.Unmarshal(snap.data["session"], &session); err != nil {
		t.Fatal(err)
	}
	if len(session.Items) != 2 || session.Items[0].Text != "one two" || session.Items[1].Tool.Status != agentapi.ToolCompleted {
		t.Fatalf("snapshot items = %+v", session.Items)
	}
	conv.EmitItem(agentapi.Item{ID: "c", Kind: agentapi.ItemAssistant, Text: "three"})
	next := parseFrame(t, <-sub.Frames())
	if next.event != "item" || next.seq <= snap.seq {
		t.Fatalf("event %s seq %d after snapshot seq %d", next.event, next.seq, snap.seq)
	}
}

func TestDuplicateRequestIDSendsOnce(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	rid := mustUUID(t)
	first, err := m.Submit(sum.ID, PromptRequest{Text: "hello", RequestID: rid, Mode: ModeSend})
	if err != nil || first.Status != SubmissionAccepted {
		t.Fatalf("first submit = %+v, %v", first, err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	again, err := m.Submit(sum.ID, PromptRequest{Text: "hello", RequestID: rid, Mode: ModeSend})
	if err != nil || again != first {
		t.Fatalf("repeat = %+v, %v; want %+v", again, err, first)
	}
	if got := len(conv.Sends()); got != 1 {
		t.Fatalf("Send called %d times, want 1", got)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "hello", RequestID: "not-a-uuid", Mode: ModeSend}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("bad request id error = %v", err)
	}
}

func TestConcurrentSubmitsOneAcceptedOneConflict(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	release := make(chan struct{})
	conv.SetSendHook(func(context.Context, string) error { <-release; return nil })
	type result struct {
		sub Submission
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, text := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, err := m.Submit(sum.ID, PromptRequest{Text: text, RequestID: mustUUID(t), Mode: ModeSend})
			results <- result{sub, err}
		}()
	}
	waitUntil(t, "one send in flight", func() bool { return len(conv.Sends()) == 1 })
	close(release)
	wg.Wait()
	close(results)
	accepted, conflicts := 0, 0
	for r := range results {
		switch {
		case r.err == nil && r.sub.Status == SubmissionAccepted:
			accepted++
		case statusOf(r.err) == http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected result %+v %v", r.sub, r.err)
		}
	}
	if accepted != 1 || conflicts != 1 || len(conv.Sends()) != 1 {
		t.Fatalf("accepted=%d conflicts=%d sends=%d", accepted, conflicts, len(conv.Sends()))
	}
}

func TestSubmissionOutcomes(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)

	conv.SetSendHook(func(context.Context, string) error {
		return fmt.Errorf("stream reset: %w", agentapi.ErrSubmissionUncertain)
	})
	rid := mustUUID(t)
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "maybe", RequestID: rid, Mode: ModeSend})
	if err != nil || sub.Status != SubmissionUncertain || !strings.Contains(sub.Error, "did not resend") {
		t.Fatalf("uncertain submission = %+v, %v", sub, err)
	}
	if again, _ := m.Submit(sum.ID, PromptRequest{Text: "maybe", RequestID: rid, Mode: ModeSend}); again != sub || len(conv.Sends()) != 1 {
		t.Fatalf("uncertain prompt was resubmitted: %+v sends=%d", again, len(conv.Sends()))
	}
	if st := detail(t, m, sum.ID).State; st != StateIdle {
		t.Fatalf("state after uncertain send = %s", st)
	}

	conv.SetSendHook(func(context.Context, string) error { return agentapi.ErrBusy })
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "busy", RequestID: mustUUID(t), Mode: ModeSend}); statusOf(err) != http.StatusConflict {
		t.Fatalf("ErrBusy = %v, want 409", err)
	}

	conv.SetSendHook(func(context.Context, string) error { return errors.New("quota \x1b[31mexceeded") })
	sub, err = m.Submit(sum.ID, PromptRequest{Text: "rejected", RequestID: mustUUID(t), Mode: ModeSend})
	if err != nil || sub.Status != SubmissionRejected || sub.Error != "quota exceeded" {
		t.Fatalf("rejected submission = %+v, %v", sub, err)
	}

	conv.SetSendHook(nil)
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "while working", RequestID: mustUUID(t), Mode: ModeSend}); statusOf(err) != http.StatusConflict {
		t.Fatalf("prompt while working = %v, want 409", err)
	}
}

func permissionRequest(id string) agentapi.Interaction {
	return agentapi.Interaction{
		ID: id, Kind: agentapi.InteractionPermission, Title: "Run rm -rf build?",
		Options: []agentapi.Option{{ID: "allow", Label: "Allow"}, {ID: "deny", Label: "Deny", Reject: true}},
		State:   agentapi.InteractionPending,
	}
}

func TestInteractionPendingWithoutViewersAndFirstAnswerWins(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(permissionRequest("p1"))
	time.Sleep(20 * time.Millisecond)
	d := detail(t, m, sum.ID)
	if d.State != StateAwaitingPermission || d.Pending != 1 || d.Interactions[0].State != agentapi.InteractionPending {
		t.Fatalf("interaction without viewers = %+v", d)
	}
	if len(conv.Responds()) != 0 {
		t.Fatal("the service answered on the user's behalf")
	}

	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "always"}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("invalid decision = %v, want 400", err)
	}
	release := make(chan struct{})
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { <-release; return nil })
	done := make(chan error, 1)
	go func() {
		_, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "allow"})
		done <- err
	}()
	waitUntil(t, "first answer at the provider", func() bool { return len(conv.Responds()) == 1 })
	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "deny"}); statusOf(err) != http.StatusConflict {
		t.Fatalf("concurrent second answer = %v, want 409", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "deny"}); statusOf(err) != http.StatusConflict {
		t.Fatalf("answer after resolution = %v, want 409", err)
	}
	if n := len(conv.Responds()); n != 1 {
		t.Fatalf("Respond called %d times, want 1", n)
	}
	d = detail(t, m, sum.ID)
	if d.Interactions[0].State != agentapi.InteractionAnswered || d.Interactions[0].Resolution != "Allow" || d.State != StateWorking {
		t.Fatalf("after answer: %+v", d)
	}
}

func TestInteractionExpiredAndQuestionValidation(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitInteraction(agentapi.Interaction{
		ID: "q1", Kind: agentapi.InteractionQuestion, Title: "Pick",
		Questions: []agentapi.Question{{Text: "Color?", Choices: []string{"red", "blue"}}, {Text: "Why?", Custom: true}},
	})
	if st := detail(t, m, sum.ID).State; st != StateAwaitingAnswer {
		t.Fatalf("state = %s", st)
	}
	for _, bad := range []agentapi.Answer{
		{Answers: [][]string{{"red"}}},
		{Answers: [][]string{{"green"}, {"because"}}},
		{Answers: [][]string{{"red", "blue"}, {"because"}}},
		{Decision: "allow"},
	} {
		if _, err := m.Answer(sum.ID, "q1", bad); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("answer %+v = %v, want 400", bad, err)
		}
	}
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { return agentapi.ErrInteractionGone })
	if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Answers: [][]string{{"red"}, {"free text"}}}); statusOf(err) != http.StatusGone {
		t.Fatalf("gone interaction = %v, want 410", err)
	}
	if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Answers: [][]string{{"red"}, {"free text"}}}); statusOf(err) != http.StatusGone {
		t.Fatalf("expired interaction = %v, want 410", err)
	}
	if n := len(conv.Responds()); n != 1 {
		t.Fatalf("Respond called %d times, want 1", n)
	}
	if d := detail(t, m, sum.ID); d.Interactions[0].State != agentapi.InteractionExpired || d.State != StateIdle {
		t.Fatalf("after expiry: %+v", d)
	}
}

// An interaction's tool call ID reaches the stream and the detail as
// tool_call_id; one unfit to match a tool item's ID is dropped.
func TestInteractionToolCallIDReachesStreamAndDetail(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	want := map[string]string{
		"linked": "call_1", "none": "", "long": "", "control": "", "spaced": "", "invalid": "",
	}
	for id, toolCallID := range map[string]string{
		"linked":  "call_1",
		"none":    "",
		"long":    strings.Repeat("c", maxToolCallID+1),
		"control": "call\x1b[2J",
		"spaced":  "call 1",
		"invalid": "call\xff",
	} {
		ix := permissionRequest(id)
		ix.ToolCallID = toolCallID
		conv.EmitInteraction(ix)
	}
	for range want {
		f := frameOf(t, sub, "interaction")
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(f.data["interaction"], &fields); err != nil {
			t.Fatal(err)
		}
		var id, got string
		_ = json.Unmarshal(fields["id"], &id)
		raw, present := fields["tool_call_id"]
		if present {
			_ = json.Unmarshal(raw, &got)
		}
		if got != want[id] || present != (want[id] != "") {
			t.Fatalf("%s: stream tool_call_id = %s (present %v), want %q", id, raw, present, want[id])
		}
	}
	d := detail(t, m, sum.ID)
	if len(d.Interactions) != len(want) {
		t.Fatalf("detail interactions = %d, want %d", len(d.Interactions), len(want))
	}
	for _, ix := range d.Interactions {
		if ix.ToolCallID != want[ix.ID] {
			t.Fatalf("%s: detail ToolCallID = %q, want %q", ix.ID, ix.ToolCallID, want[ix.ID])
		}
	}
	body, err := json.Marshal(d)
	if err != nil || !strings.Contains(string(body), `"tool_call_id":"call_1"`) {
		t.Fatalf("detail JSON lacks tool_call_id: %s %v", body, err)
	}
	answered, err := m.Answer(sum.ID, "linked", agentapi.Answer{Decision: "allow"})
	if err != nil || answered.ToolCallID != "call_1" {
		t.Fatalf("answered = %+v %v", answered, err)
	}
}

func TestCancelCallsCancelOnly(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.Cancel(sum.ID); statusOf(err) != http.StatusConflict {
		t.Fatalf("cancel with no turn = %v, want 409", err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if conv.Cancels() != 1 || conv.Closes() != 0 || len(conv.Sends()) != 1 {
		t.Fatalf("cancel side effects: cancels=%d closes=%d sends=%d", conv.Cancels(), conv.Closes(), len(conv.Sends()))
	}
	if d := detail(t, m, sum.ID); !d.Open {
		t.Fatal("cancel must keep the conversation open")
	}
}

func TestCloseKeepsRecordAndExpiresInteractions(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitInteraction(permissionRequest("p1"))
	closed, err := m.Close(sum.ID)
	if err != nil || closed.State != StateClosed || closed.Open {
		t.Fatalf("Close = %+v, %v", closed, err)
	}
	if conv.Closes() != 1 || conv.Cancels() != 0 {
		t.Fatalf("close side effects: closes=%d cancels=%d", conv.Closes(), conv.Cancels())
	}
	if d := detail(t, m, sum.ID); d.Interactions[0].State != agentapi.InteractionExpired {
		t.Fatalf("pending interaction after close: %+v", d.Interactions[0])
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", sum.ID)]
	if rec.ID != sum.ID || rec.Web == nil || rec.Web.Turn != StateClosed {
		t.Fatalf("closed record = %+v", rec)
	}
	// Viewing a closed session does not reopen it.
	if err := m.View(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(prov.Opens()); n != 1 {
		t.Fatalf("viewing a closed session opened a conversation (%d opens)", n)
	}
}

func TestRuntimeExitFailsWithoutReplayOrReplacement(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(permissionRequest("p1"))
	conv.Exit("copilot exited with status 1")

	d := detail(t, m, sum.ID)
	if d.State != StateFailed || !strings.Contains(d.StateDetail, "copilot exited with status 1") || d.Open {
		t.Fatalf("after exit: state=%s detail=%q open=%v", d.State, d.StateDetail, d.Open)
	}
	if d.Interactions[0].State != agentapi.InteractionExpired {
		t.Fatalf("pending interaction after exit: %+v", d.Interactions[0])
	}
	if err := m.View(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if len(prov.Opens()) != 1 || len(conv.Sends()) != 1 {
		t.Fatalf("exit triggered replay or replacement: opens=%d sends=%d", len(prov.Opens()), len(conv.Sends()))
	}
	// Late events from the dead conversation are ignored.
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if st := detail(t, m, sum.ID).State; st != StateFailed {
		t.Fatalf("stale event changed state to %s", st)
	}
}

func seedWebRecord(t *testing.T, st *store.Store, id, convID, turn string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", id)] = store.SessionRecord{
			ID: id, Agent: "fake", Name: "old", Mode: store.ModeSafe, Workdir: t.TempDir(), CreatedAt: now, LastSeenAt: now,
			Status: store.StatusActive, Surface: store.SurfaceWeb, ProviderSessionID: convID,
			Web: &store.WebState{Turn: turn, UpdatedAt: now},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRestartMarksRunningTurnsInterrupted(t *testing.T) {
	st := openTestStore(t)
	working, waiting, done := mustUUID(t), mustUUID(t), mustUUID(t)
	seedWebRecord(t, st, working, "conv_a", StateWorking)
	seedWebRecord(t, st, waiting, "conv_b", StateAwaitingPermission)
	seedWebRecord(t, st, done, "conv_c", StateCompleted)
	prov := agenttest.NewProvider("fake", allCaps)
	m := startManager(t, st, prov)
	for id, want := range map[string]string{working: StateInterrupted, waiting: StateInterrupted, done: StateCompleted} {
		if got := detail(t, m, id).State; got != want {
			t.Fatalf("session %s state = %s, want %s", id, got, want)
		}
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rec := cfg.Sessions[store.Key("fake", working)]; rec.Web == nil || rec.Web.Turn != StateInterrupted {
		t.Fatalf("interrupted state not persisted: %+v", rec.Web)
	}
	if len(prov.Opens()) != 0 {
		t.Fatal("loading records must not open conversations")
	}
}

func TestShutdownInterruptsClosesAndShutsDownProviders(t *testing.T) {
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum, conv := createSession(t, m, prov)
	rid := mustUUID(t)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: rid, Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if conv.Closes() != 1 || prov.ShutdownCalls() != 1 {
		t.Fatalf("closes=%d shutdowns=%d", conv.Closes(), prov.ShutdownCalls())
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", sum.ID)]
	if rec.Web == nil || rec.Web.Turn != StateInterrupted || rec.Web.RequestID != rid || rec.Web.RequestStatus != SubmissionAccepted {
		t.Fatalf("persisted after shutdown: %+v", rec.Web)
	}

	// The next service recognizes the persisted request ID without sending.
	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(sum.ConversationID, nil)
	m2 := startManager(t, st, prov2)
	again, err := m2.Submit(sum.ID, PromptRequest{Text: "work", RequestID: rid, Mode: ModeSend})
	if err != nil || again.RequestID != rid || again.Status != SubmissionAccepted {
		t.Fatalf("repeat after restart = %+v, %v", again, err)
	}
	if len(prov2.Opens()) != 0 {
		t.Fatal("a recorded request must not reach the provider")
	}
}

func TestReopenUsesExactConversationAndHistory(t *testing.T) {
	st := openTestStore(t)
	id, gone := mustUUID(t), mustUUID(t)
	seedWebRecord(t, st, id, "conv_known", StateCompleted)
	seedWebRecord(t, st, gone, "conv_gone", StateCompleted)
	prov := agenttest.NewProvider("fake", allCaps)
	prov.AddConversation("conv_known", []agentapi.Item{
		{ID: "u1", Kind: agentapi.ItemUser, Text: "earlier prompt"},
		{ID: "a1", Kind: agentapi.ItemAssistant, Text: "earlier answer"},
	})
	m := startManager(t, st, prov)

	if err := m.View(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	d := detail(t, m, id)
	if !d.Open || d.State != StateCompleted || len(d.Items) != 2 || d.Items[1].Text != "earlier answer" {
		t.Fatalf("reopened detail = %+v", d)
	}
	if err := m.View(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	d = detail(t, m, gone)
	if d.State != StateFailed || !strings.Contains(d.StateDetail, "no longer exists") || d.Open {
		t.Fatalf("missing conversation detail = %+v", d.SessionSummary)
	}
	opens := prov.Opens()
	if len(opens) != 2 {
		t.Fatalf("opens = %+v", opens)
	}
	for _, req := range opens {
		if req.ConversationID == "" {
			t.Fatal("a missing conversation was replaced by a new one")
		}
	}
	// Viewing again does not retry or replace.
	_ = m.View(context.Background(), gone)
	if len(prov.Opens()) != 2 {
		t.Fatal("viewing a failed session reopened it")
	}
}

func TestConcurrentViewersShareOneConversation(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_known", StateIdle)
	prov := agenttest.NewProvider("fake", allCaps)
	prov.AddConversation("conv_known", nil)
	m := startManager(t, st, prov)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = m.View(context.Background(), id) }()
	}
	wg.Wait()
	if n := len(prov.Opens()); n != 1 {
		t.Fatalf("%d opens for one session, want 1", n)
	}
}

func TestItemAndTextBounds(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	for i := range maxItems + 100 {
		conv.EmitItem(agentapi.Item{ID: fmt.Sprintf("i%d", i), Kind: agentapi.ItemAssistant, Text: "x"})
	}
	conv.EmitItem(agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "cat", Output: strings.Repeat("y", maxToolText*2)}})
	d := detail(t, m, sum.ID)
	if len(d.Items) > maxItems || !d.HistoryTruncated {
		t.Fatalf("items=%d truncated=%v", len(d.Items), d.HistoryTruncated)
	}
	last := d.Items[len(d.Items)-1]
	if last.ID != "tool" || len(last.Tool.Output) > maxToolText+len(truncatedMarker) || !strings.HasSuffix(last.Tool.Output, truncatedMarker) {
		t.Fatalf("tool output not bounded: %d bytes", len(last.Tool.Output))
	}
}

func TestCreateValidatesProjectModelAndProvider(t *testing.T) {
	m, prov, _ := newTestManager(t)
	for _, dir := range []string{"", "relative/path", "/definitely/not/here"} {
		if _, err := m.AddProject(dir, "", nil); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("AddProject(%q) = %v, want 400", dir, err)
		}
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	project := addProject(t, m, link)
	for _, req := range []CreateRequest{
		{Provider: "fake"},
		{Provider: "fake", ProjectID: "nope"},
		{Provider: "nope", ProjectID: project},
		{Provider: "fake", ProjectID: project, Model: "not-offered"},
		{Provider: "fake", ProjectID: project, Name: strings.Repeat("n", maxNameRunes+1)},
	} {
		if _, err := m.Create(req); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("Create(%+v) = %v, want 400", req, err)
		}
	}
	if len(prov.Opens()) != 0 {
		t.Fatal("an invalid create reached the provider")
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "start", RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(real)
	if sum.Workdir != canonical || sum.ProjectID != project || sum.Name != "" || sum.Model != "" || prov.Last().Request().Workdir != canonical || prov.Last().Request().Model != "" {
		t.Fatalf("created task = %+v, open %+v", sum, prov.Last().Request())
	}
	if sends := prov.Last().Sends(); len(sends) != 1 || sends[0] != "start" {
		t.Fatalf("create prompt sends = %q", sends)
	}
	unavailable := agenttest.NewProvider("down", allCaps)
	unavailable.SetCheckError(errors.New("not installed"))
	m2 := startManager(t, openTestStore(t), unavailable)
	if _, err := m2.Create(CreateRequest{Provider: "down", ProjectID: addProject(t, m2, t.TempDir())}); statusOf(err) != http.StatusConflict {
		t.Fatalf("unavailable provider = %v, want 409", err)
	}
	if info := m2.Providers()[0]; info.Available || info.Reason != "not installed" {
		t.Fatalf("provider info = %+v", info)
	}
	gone := t.TempDir()
	goneProject := addProject(t, m, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: goneProject}); statusOf(err) != http.StatusConflict {
		t.Fatalf("project directory removed = %v, want 409", err)
	}
}

func TestCreateWithRepeatedRequestIDReturnsSameSession(t *testing.T) {
	m, prov, _ := newTestManager(t)
	rid := mustUUID(t)
	project := addProject(t, m, t.TempDir())
	first, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "go", RequestID: rid})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "go", RequestID: rid})
	if err != nil || second.ID != first.ID {
		t.Fatalf("repeat create = %+v, %v", second, err)
	}
	if len(prov.Opens()) != 1 || len(prov.Last().Sends()) != 1 {
		t.Fatalf("repeat create reached the provider: opens=%d", len(prov.Opens()))
	}
}
