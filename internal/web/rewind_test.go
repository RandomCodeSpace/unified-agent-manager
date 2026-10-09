package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type rewindConversation struct {
	*agenttest.Conversation
	mu       sync.Mutex
	previews int
	calls    []string
	preview  func(string) (agentapi.RewindPreview, error)
	rewind   func(event, mode string) (agentapi.RewindResult, error)
}

func (c *rewindConversation) PreviewRewind(_ context.Context, user string) (agentapi.RewindPreview, error) {
	c.mu.Lock()
	c.previews++
	read := c.preview
	c.mu.Unlock()
	return read(user)
}

func (c *rewindConversation) Rewind(_ context.Context, event, mode string) (agentapi.RewindResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, event+" "+mode)
	run := c.rewind
	c.mu.Unlock()
	return run(event, mode)
}

func (c *rewindConversation) rewinds() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.calls) }

func secondTurnPreview(string) (agentapi.RewindPreview, error) {
	return agentapi.RewindPreview{UserEventID: "native-second", TailEventID: "native-tail", Turns: 2, Discarded: []string{"u-second", "u-third"}, FilesAvailable: true,
		Files: agentapi.NativeTurnChanges{Status: "available", EventID: "native-second", Files: 1, Additions: 4, Deletions: 1, Entries: []agentapi.NativeTurnFile{{Path: "/work/a.txt", Kind: "modified", Additions: 4, Deletions: 1}}}}, nil
}

// rewindManager has one open Task with three finished owner turns; the
// provider's record after a rewind keeps only the first.
func rewindManager(t *testing.T) (*Manager, *taskForkProvider, *store.Store, SessionSummary, *rewindConversation) {
	t.Helper()
	caps := allCaps
	caps.Fork, caps.Rewind = true, true
	p := &taskForkProvider{Pager: agenttest.NewPager("fake", caps)}
	st := openTestStore(t)
	m := startManager(t, st, p)
	sum, err := m.Create(CreateRequest{ProjectID: addProject(t, m, t.TempDir()), Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Last()
	for _, turn := range []string{"first", "second", "third"} {
		finishTurn(c, turn, "reply "+turn)
	}
	rc := &rewindConversation{Conversation: c, preview: secondTurnPreview}
	p.SetHistory(sum.ConversationID, agentapi.History{Items: []agentapi.Item{{ID: "u-first", Kind: agentapi.ItemUser, Text: "first"}, {ID: "a-first", Kind: agentapi.ItemAssistant, Text: "reply first"}}})
	m.mu.Lock()
	m.sessions[sum.ID].conv = rc
	m.mu.Unlock()
	return m, p, st, sum, rc
}

func previewToken(t *testing.T, m *Manager, id string) string {
	t.Helper()
	p, err := m.PreviewRewind(id, "u-second")
	if err != nil {
		t.Fatal(err)
	}
	if p.Turns != 2 || !p.FilesAvailable || p.Files.Files != 1 || p.Token == "" {
		t.Fatalf("preview = %+v", p)
	}
	return p.Token
}

func timingUsers(m *Manager, id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, t := range m.sessions[id].turnTimings {
		out = append(out, t.UserItemID)
	}
	return out
}

func storedRewind(t *testing.T, st *store.Store, id string) *store.WebRewind {
	t.Helper()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Sessions[store.Key("fake", id)].Web.Rewind
}

func TestRewindPreservesEveryNativeOutcome(t *testing.T) {
	text := func(s string) *string { return &s }
	removed := func(n int64) *int64 { return &n }
	for _, tc := range []struct {
		result    agentapi.RewindResult
		state     string
		truncated bool
	}{
		{agentapi.RewindResult{Outcome: "success", EventsRemoved: removed(9), RestoredFiles: []string{"/work/a.txt"}, SkippedFiles: []agentapi.RewindSkip{}}, rewindApplied, true},
		{agentapi.RewindResult{Outcome: "checkpoint-cleanup-failed", Error: text("cleanup"), EventsRemoved: removed(9), RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, rewindApplied, true},
		{agentapi.RewindResult{Outcome: "snapshot-prune-failed", Error: text("prune"), EventsRemoved: removed(0), RestoredFiles: []string{"/work/a.txt"}, SkippedFiles: []agentapi.RewindSkip{}}, rewindApplied, true},
		{agentapi.RewindResult{Outcome: "truncation-failed", Error: text("truncate"), RestoredFiles: []string{"/work/a.txt"}, SkippedFiles: []agentapi.RewindSkip{{Path: "/work/b.txt", Reason: "user-modified"}, {Path: "/work/c.bin", Reason: "skipped-capture"}}}, rewindApplied, false},
		{agentapi.RewindResult{Outcome: "rollback-incomplete", Error: text("rollback"), RestoredFiles: []string{"/work/a.txt"}, SkippedFiles: []agentapi.RewindSkip{}}, rewindApplied, false},
		{agentapi.RewindResult{Outcome: "files-rolled-back", Error: text("restore"), RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, rewindDone, false},
		{agentapi.RewindResult{Outcome: "session-busy", RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, rewindDone, false},
		{agentapi.RewindResult{Outcome: "file-change-tracking-disabled", RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, rewindDone, false},
		{agentapi.RewindResult{Outcome: "unsupported-remote-session", RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, rewindDone, false},
	} {
		t.Run(tc.result.Outcome, func(t *testing.T) {
			m, _, st, sum, rc := rewindManager(t)
			req := RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversationAndFiles, Token: previewToken(t, m, sum.ID), RequestID: mustUUID(t)}
			rc.rewind = func(event, mode string) (agentapi.RewindResult, error) {
				if r := storedRewind(t, st, sum.ID); r == nil || r.State != rewindPending || r.RequestID != req.RequestID || r.UserEventID != event || r.Mode != mode {
					return agentapi.RewindResult{}, errors.New("native rewind had no durable receipt")
				}
				return tc.result, nil
			}
			got, err := m.Rewind(sum.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.state || got.Result == nil || !reflect.DeepEqual(*got.Result, tc.result) || rc.calls[0] != "native-second conversation-and-files" {
				t.Fatalf("receipt = %+v %+v, calls %v", got, got.Result, rc.calls)
			}
			want := []string{"u-first", "u-second", "u-third"}
			if tc.truncated {
				want = []string{"u-first"}
			}
			if users := timingUsers(m, sum.ID); !reflect.DeepEqual(users, want) {
				t.Fatalf("timings = %v, want %v", users, want)
			}
			if tc.state == rewindDone {
				if s := detail(t, m, sum.ID); s.Rewind != (RewindStatus{}) || !s.Open || len(s.Items) != 6 || rc.Closes() != 0 {
					t.Fatalf("unapplied outcome changed the task: %+v closes %d", s.SessionSummary, rc.Closes())
				}
				return
			}
			if rc.Closes() != 1 {
				t.Fatal("applied rewind kept the old conversation open")
			}
			waitUntil(t, "authoritative reread", func() bool {
				d, _ := m.Detail(sum.ID)
				return d.History == HistoryLoaded && len(d.Items) == 2 && d.Rewind == (RewindStatus{})
			})
			if r := storedRewind(t, st, sum.ID); r == nil || r.State != rewindDone && r.State != rewindApplied {
				t.Fatalf("stored = %+v", r)
			}
		})
	}
}

func TestRewindRefusesStalePreviewWithoutNativeRequest(t *testing.T) {
	m, _, st, sum, rc := rewindManager(t)
	token := previewToken(t, m, sum.ID)
	rc.rewind = func(string, string) (agentapi.RewindResult, error) {
		t.Fatal("stale rewind reached the provider")
		return agentapi.RewindResult{}, nil
	}
	rc.preview = func(user string) (agentapi.RewindPreview, error) {
		p, _ := secondTurnPreview(user)
		p.Files.Entries[0].Additions, p.Files.Additions = 5, 5
		return p, nil
	}
	if _, err := m.Rewind(sum.ID, RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversationAndFiles, Token: token, RequestID: mustUUID(t)}); !errors.Is(err, errRewindStale) {
		t.Fatalf("changed files = %v", err)
	}
	rc.preview = secondTurnPreview
	token = previewToken(t, m, sum.ID)
	m.mu.Lock()
	m.sessions[sum.ID].gen++
	m.mu.Unlock()
	if _, err := m.Rewind(sum.ID, RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversation, Token: token, RequestID: mustUUID(t)}); !errors.Is(err, errRewindStale) {
		t.Fatalf("new generation = %v", err)
	}
	if rc.rewinds() != 0 || storedRewind(t, st, sum.ID) != nil {
		t.Fatal("stale preview left a receipt or reached the provider")
	}
}

func TestRewindDuplicateSubmitRunsOnce(t *testing.T) {
	m, _, _, sum, rc := rewindManager(t)
	release := make(chan struct{})
	rc.rewind = func(string, string) (agentapi.RewindResult, error) {
		<-release
		n := int64(4)
		return agentapi.RewindResult{Outcome: "success", EventsRemoved: &n, RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, nil
	}
	req := RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversation, Token: previewToken(t, m, sum.ID), RequestID: mustUUID(t)}
	const clients = 6
	got := make([]RewindReceipt, clients)
	errs := make([]error, clients)
	var wg sync.WaitGroup
	for i := range clients {
		wg.Go(func() { got[i], errs[i] = m.Rewind(sum.ID, req) })
	}
	waitUntil(t, "native rewind started", func() bool { return rc.rewinds() == 1 })
	close(release)
	wg.Wait()
	for i := range clients {
		if errs[i] != nil || got[i].RequestID != req.RequestID || got[i].Result == nil || got[i].Result.Outcome != "success" {
			t.Fatalf("client %d = %+v, %v", i, got[i], errs[i])
		}
	}
	if rc.rewinds() != 1 {
		t.Fatalf("native rewinds = %d", rc.rewinds())
	}
	req.Mode = agentapi.RewindConversationAndFiles
	if _, err := m.Rewind(sum.ID, req); statusOf(err) != http.StatusConflict || rc.rewinds() != 1 {
		t.Fatalf("reused request ID = %v", err)
	}
}

func TestRewindLostResultHoldsTaskUntilReconciled(t *testing.T) {
	m, p, st, sum, rc := rewindManager(t)
	rc.rewind = func(string, string) (agentapi.RewindResult, error) {
		return agentapi.RewindResult{}, fmt.Errorf("%w: connection lost", agentapi.ErrRewindUncertain)
	}
	req := RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversationAndFiles, Token: previewToken(t, m, sum.ID), RequestID: mustUUID(t)}
	got, err := m.Rewind(sum.ID, req)
	if err != nil || got.State != rewindUncertain || got.Result != nil {
		t.Fatalf("lost result = %+v, %v", got, err)
	}
	if again, err := m.Rewind(sum.ID, req); err != nil || again.State != rewindUncertain || rc.rewinds() != 1 {
		t.Fatalf("retry = %+v, %v, rewinds %d", again, err, rc.rewinds())
	}
	waitUntil(t, "reread after uncertain result", func() bool { d, _ := m.Detail(sum.ID); return d.History == HistoryLoaded && len(d.Items) == 2 })
	if d := detail(t, m, sum.ID); d.Rewind.State != rewindUncertain || d.Rewind.RequestID != req.RequestID || d.Open {
		t.Fatalf("summary = %+v", d.SessionSummary)
	}
	if users := timingUsers(m, sum.ID); len(users) != 3 {
		t.Fatalf("uncertain rewind pruned timings: %v", users)
	}
	_, err = m.Submit(sum.ID, PromptRequest{Text: "edited", RequestID: mustUUID(t), Mode: ModeSend})
	var webErr *Error
	if !errors.As(err, &webErr) || webErr.Code != "rewind_unreconciled" {
		t.Fatalf("send while uncertain = %v", err)
	}
	if _, err := m.Fork(sum.ID, ForkRequest{UserItemID: "u-first", RequestID: mustUUID(t)}); !errors.Is(err, errRewindUnreconciled) {
		t.Fatalf("fork while uncertain = %v", err)
	}
	if _, err := m.PreviewRewind(sum.ID, "u-first"); !errors.Is(err, errRewindUnreconciled) {
		t.Fatalf("rewind while uncertain = %v", err)
	}
	if len(p.Opens()) != 1 || rc.rewinds() != 1 {
		t.Fatal("uncertain state resumed or rewound again")
	}
	if r := storedRewind(t, st, sum.ID); r == nil || r.State != rewindUncertain || !reflect.DeepEqual(r.Discarded, []string{"u-second", "u-third"}) {
		t.Fatalf("stored = %+v", r)
	}

	// Explicit reconcile proves the boundary is gone before pruning, rereads,
	// and only then releases the Task.
	p.readHook = func(req agentapi.ForkBoundaryRequest) error {
		if req.UserItemID != "u-second" || req.ConversationID != sum.ConversationID {
			return errors.New("wrong boundary check")
		}
		return agentapi.ErrItemNotFound
	}
	if _, err := m.ReconcileRewind(sum.ID, mustUUID(t)); statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown request = %v", err)
	}
	done, err := m.ReconcileRewind(sum.ID, req.RequestID)
	if err != nil || done.State != rewindDone {
		t.Fatalf("reconcile = %+v, %v", done, err)
	}
	if users := timingUsers(m, sum.ID); !reflect.DeepEqual(users, []string{"u-first"}) {
		t.Fatalf("timings = %v", users)
	}
	if rc.rewinds() != 1 {
		t.Fatal("reconcile repeated the rewind")
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "edited", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatalf("send after reconcile = %v", err)
	}
}

func TestRewindPendingReceiptLoadsUncertainAfterRestart(t *testing.T) {
	m, p, st, sum, _ := rewindManager(t)
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[store.Key("fake", sum.ID)]
		rec.Web.Rewind = &store.WebRewind{RequestID: "11111111-1111-4111-8111-111111111111", UserItemID: "u-second", State: rewindPending, Mode: agentapi.RewindConversation}
		cfg.Sessions[store.Key("fake", sum.ID)] = rec
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, p)
	got, err := restarted.Summary(sum.ID)
	if err != nil || got.Rewind.State != rewindUncertain {
		t.Fatalf("restart = %+v, %v", got.Rewind, err)
	}
}

func TestRewindRefusalsChangeNothing(t *testing.T) {
	m, _, st, sum, rc := rewindManager(t)
	rc.rewind = func(string, string) (agentapi.RewindResult, error) { return agentapi.RewindResult{}, agentapi.ErrBusy }
	req := RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversation, Token: previewToken(t, m, sum.ID), RequestID: mustUUID(t)}
	if _, err := m.Rewind(sum.ID, req); statusOf(err) != http.StatusConflict || rc.rewinds() != 1 {
		t.Fatalf("definite refusal = %v", err)
	}
	if r := storedRewind(t, st, sum.ID); r != nil || detail(t, m, sum.ID).Rewind != (RewindStatus{}) {
		t.Fatalf("refused rewind kept its receipt: %+v", r)
	}
	rc.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.PreviewRewind(sum.ID, "u-second"); statusOf(err) != http.StatusConflict {
		t.Fatalf("busy = %v", err)
	}
	rc.EmitTurn(agentapi.TurnCompleted, "")
	rc.EmitInteraction(agentapi.Interaction{ID: "ask", Kind: agentapi.InteractionQuestion, State: agentapi.InteractionPending, Title: "?"})
	if _, err := m.PreviewRewind(sum.ID, "u-second"); statusOf(err) != http.StatusConflict {
		t.Fatalf("pending interaction = %v", err)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	previews := rc.previews
	if _, err := m.PreviewRewind(sum.ID, "u-second"); statusOf(err) != http.StatusConflict || rc.previews != previews {
		t.Fatalf("closed conversation = %v", err)
	}
	if _, err := m.PreviewRewind(sum.ID, "bad\nid"); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("invalid owner = %v", err)
	}
}

func TestRewindHoldRefusesCommandsAndSubagentFollowUps(t *testing.T) {
	m, _, _, sum, conv, wrapped := commandManager(t)
	var calls int
	wrapped.execute = func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls++
		return &agentapi.CommandResult{Kind: "text", Text: "ran"}, nil
	}
	conv.EmitSubagent(agentapi.Subagent{ID: "sub", Status: agentapi.SubagentIdle})
	waitUntil(t, "idle subagent", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		sa := m.sessions[sum.ID].subIdx["sub"]
		return sa != nil && sa.Status == agentapi.SubagentIdle
	})
	m.mu.Lock()
	m.sessions[sum.ID].rewind = &store.WebRewind{RequestID: "11111111-1111-4111-8111-111111111111", UserItemID: "u", Mode: agentapi.RewindConversation, State: rewindUncertain}
	m.mu.Unlock()
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review"}); !errors.Is(err, errRewindUnreconciled) || calls != 0 {
		t.Fatalf("command while uncertain = %v, calls %d", err, calls)
	}
	if _, err := m.PromptSubagent(sum.ID, "sub", "go", mustUUID(t)); !errors.Is(err, errRewindUnreconciled) || len(conv.SubagentPrompts()) != 0 {
		t.Fatalf("subagent follow-up while uncertain = %v", err)
	}
}

func TestRewindRefusesAConversationHeldElsewhere(t *testing.T) {
	caps := allCaps
	caps.Fork, caps.Rewind, caps.Import = true, true, true
	p := &taskForkProvider{Pager: agenttest.NewPager("fake", caps)}
	st := openTestStore(t)
	m := startManager(t, st, p)
	sum, err := m.Create(CreateRequest{ProjectID: addProject(t, m, t.TempDir()), Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Last()
	for _, turn := range []string{"first", "second", "third"} {
		finishTurn(c, turn, "reply "+turn)
	}
	rc := &rewindConversation{Conversation: c, preview: secondTurnPreview, rewind: func(string, string) (agentapi.RewindResult, error) {
		return agentapi.RewindResult{Outcome: "success", RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}, nil
	}}
	m.mu.Lock()
	m.sessions[sum.ID].conv, m.sessions[sum.ID].imported = rc, true
	m.mu.Unlock()
	req := RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversation, Token: previewToken(t, m, sum.ID), RequestID: mustUUID(t)}
	p.SetInUse([]string{sum.ConversationID}, nil)
	if _, err := m.Rewind(sum.ID, req); !errors.Is(err, errHeldElsewhere) {
		t.Fatalf("held rewind = %v", err)
	}
	if rc.rewinds() != 0 || storedRewind(t, st, sum.ID) != nil {
		t.Fatal("a conversation held elsewhere was rewound")
	}
}

func TestRewindReleaseOnlyAfterAFailedReconcile(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"ambiguous boundary", errors.New("the recorded branch boundary is ambiguous")},
		{"missing conversation", agentapi.ErrConversationNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p, st, sum, rc := rewindManager(t)
			rc.rewind = func(string, string) (agentapi.RewindResult, error) {
				return agentapi.RewindResult{}, fmt.Errorf("%w: lost", agentapi.ErrRewindUncertain)
			}
			req := RewindRequest{UserItemID: "u-second", Mode: agentapi.RewindConversationAndFiles, Token: previewToken(t, m, sum.ID), RequestID: mustUUID(t)}
			if got, err := m.Rewind(sum.ID, req); err != nil || got.State != rewindUncertain {
				t.Fatalf("rewind = %+v, %v", got, err)
			}
			if _, err := m.ReleaseRewind(sum.ID, req.RequestID); statusOf(err) != http.StatusConflict {
				t.Fatalf("release before a failed reconcile = %v", err)
			}
			p.readHook = func(agentapi.ForkBoundaryRequest) error { return tc.err }
			if _, err := m.ReconcileRewind(sum.ID, req.RequestID); err == nil {
				t.Fatal("reconcile established an unreadable boundary")
			}
			if d := detail(t, m, sum.ID); d.Rewind.State != rewindUncertain || !d.Rewind.ReconcileFailed {
				t.Fatalf("after failed reconcile = %+v", d.Rewind)
			}
			if err := m.flush(); err != nil {
				t.Fatal(err)
			}
			if r := storedRewind(t, st, sum.ID); r == nil || !r.ReconcileFailed {
				t.Fatalf("stored = %+v", r)
			}
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend}); !errors.Is(err, errRewindUnreconciled) {
				t.Fatalf("send while held = %v", err)
			}
			if _, err := m.ReleaseRewind(sum.ID, mustUUID(t)); statusOf(err) != http.StatusNotFound {
				t.Fatalf("other request = %v", err)
			}
			got, err := m.ReleaseRewind(sum.ID, req.RequestID)
			if err != nil || got.State != rewindDone || !got.Released || got.Result != nil {
				t.Fatalf("release = %+v, %v", got, err)
			}
			if rc.rewinds() != 1 {
				t.Fatal("release repeated the native rewind")
			}
			if users := timingUsers(m, sum.ID); len(users) != 3 {
				t.Fatalf("release pruned timings without proof: %v", users)
			}
			waitUntil(t, "history refreshed after release", func() bool {
				d, _ := m.Detail(sum.ID)
				return d.History == HistoryLoaded && d.Rewind == (RewindStatus{})
			})
			if r := storedRewind(t, st, sum.ID); r == nil || !r.Released || r.State != rewindDone {
				t.Fatalf("stored = %+v", r)
			}
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatalf("send after release = %v", err)
			}
		})
	}
}
