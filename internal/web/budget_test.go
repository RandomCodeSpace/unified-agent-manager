package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func budgetSession(t *testing.T, m *Manager, id string) *webSession {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// budgetThread is a Thread in dir whose first prompt ("work") runs, with
// 5 AI credits reported before its budget was set to 2.
func budgetThread(t *testing.T, m *Manager, prov *agenttest.Provider, dir string) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, dir), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	reportUnits(conv, 5)
	if got, err := m.SetBudget(sum.ID, 2, 0); err != nil || got.Budget == nil || got.Budget.Credits != 2 || got.Budget.Spent != 0 || got.Budget.Reached {
		t.Fatalf("SetBudget = %+v, %v", got.Budget, err)
	}
	return sum, conv
}

func reportUnits(conv *agenttest.Conversation, units float64) {
	conv.Emit(agentapi.Event{Kind: agentapi.EventUsage, Usage: &agentapi.Usage{AIUnits: units}})
}

// wrapModelCall reports one model call's tokens, as the provider does after each.
func wrapModelCall(conv *agenttest.Conversation) {
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "m", Input: 10, Output: 5}})
}

func emitItem(conv *agenttest.Conversation, it agentapi.Item) {
	conv.Emit(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
}

func reply(conv *agenttest.Conversation, id, text string) {
	emitItem(conv, agentapi.Item{ID: id, Kind: agentapi.ItemAssistant, Text: text, Time: usageT0})
}

// wrapping waits for the Thread to wrap up, not stop.
func wrapping(t *testing.T, m *Manager, conv *agenttest.Conversation, id string) {
	t.Helper()
	d := detail(t, m, id)
	if d.Budget == nil || !d.Budget.Reached || !d.Budget.WrappingUp || d.State != StateWorking || conv.Cancels() != 0 {
		t.Fatalf("at the limit = budget %+v state %s cancels %d", d.Budget, d.State, conv.Cancels())
	}
}

func handoffOf(t *testing.T, m *Manager, id string) store.WebHandoff {
	t.Helper()
	waitUntil(t, "the handoff", func() bool { return detail(t, m, id).Handoff != nil })
	d := detail(t, m, id)
	if d.Budget == nil || d.Budget.WrappingUp || !d.Budget.Reached {
		t.Fatalf("budget after the handoff = %+v", d.Budget)
	}
	return *d.Handoff
}

func TestBudgetLimitMidToolWrapsUpWithTheHandoffReply(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, gitRepoFixture(t))
	s := budgetSession(t, m, sum.ID)
	use := agentapi.ToolUse{Tool: "bash"}
	if v := m.preToolUse(t.Context(), s, use); v.Deny != "" || v.Context != "" {
		t.Fatalf("tool under budget = %+v", v)
	}
	reportUnits(conv, 6.7) // 1.7 of 2: past the warning share
	if v := m.preToolUse(t.Context(), s, use); v.Deny != "" || !strings.Contains(v.Context, "uam budget") {
		t.Fatalf("tool near budget = %+v", v)
	}
	if v := m.preToolUse(t.Context(), s, use); v.Context != "" {
		t.Fatalf("warning repeated: %+v", v)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventTodos, Todos: &agentapi.TodoList{Known: true, Todos: []agentapi.Todo{
		{ID: "1", Title: "Write the parser", Status: agentapi.TodoDone}, {ID: "2", Title: "Add the tests", Status: agentapi.TodoInProgress}, {ID: "3", Title: "Update the docs", Status: agentapi.TodoPending},
	}}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Intent: "Running the tests"}})
	one := 1
	emitItem(conv, agentapi.Item{ID: "fail", Kind: agentapi.ItemTool, Time: usageT0, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolFailed, Input: `{"command":"go test ./..."}`, ExitCode: &one}})
	reply(conv, "r1", "Running the tests now.")
	emitItem(conv, agentapi.Item{ID: "run", Kind: agentapi.ItemTool, Time: usageT0, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolRunning, Input: `{"command":"make check"}`}})
	reportUnits(conv, 7.1) // the call that asked for the tool passes the limit
	wrapping(t, m, conv, sum.ID)

	// The running tool finishes; the next is denied with the instruction.
	emitItem(conv, agentapi.Item{ID: "run", Kind: agentapi.ItemTool, Time: usageT0, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"make check"}`}})
	if v := m.preToolUse(t.Context(), s, use); !strings.Contains(v.Deny, "Reply now with a handoff") || !strings.Contains(v.Deny, "## How to resume") {
		t.Fatalf("tool while wrapping up = %+v", v)
	}
	if v := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "task"}); v.Deny != wrapDeny {
		t.Fatalf("subagent start while wrapping up = %+v", v)
	}
	denied(conv, "d1", "")
	_, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend})
	var coded *Error
	if !errors.As(err, &coded) || coded.Status != http.StatusConflict || coded.Code != "budget_reached" {
		t.Fatalf("send while wrapping up = %v", err)
	}
	wrapModelCall(conv)
	reply(conv, "r2", "## Done\nThe parser.\n## In progress\nTests.\n## Next steps\nDocs.\n## How to resume\nmake check")
	conv.EmitTurn(agentapi.TurnCompleted, "")

	h := handoffOf(t, m, sum.ID)
	if h.ReplyID != "r2" || h.NoReply != "" {
		t.Fatalf("handoff = %+v", h)
	}
	for _, want := range []string{"## Facts", "**AI credits:** 2.1 spent; budget 2.0", "**Last intent:** Running the tests", "1 done, 1 in progress, 1 to do", "[x] Write the parser", "[ ] Add the tests (in progress)",
		"`bash`: `go test ./...` (exit 1)", "none of its edits differ from `HEAD`", "**Other changes in the folder:** 2 files differ from `HEAD`"} {
		if !strings.Contains(h.Facts, want) {
			t.Errorf("facts lack %q:\n%s", want, h.Facts)
		}
	}
	if strings.Contains(h.Facts, "make check") {
		t.Errorf("facts name a call that did not fail:\n%s", h.Facts)
	}
	if sends := conv.Sends(); len(sends) != 1 || conv.Cancels() != 0 {
		t.Fatalf("sends %q, cancels %d: the handoff needed no prompt and no stop", sends, conv.Cancels())
	}
	if d := detail(t, m, sum.ID); d.State != StateCompleted {
		t.Fatalf("state after the handoff = %s", d.State)
	}
	if v := m.preToolUse(t.Context(), s, use); v.Deny == "" {
		t.Fatalf("tool after the handoff = %+v", v)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, m.store, "fake", sum.ID); rec.Web == nil || rec.Web.Handoff == nil || rec.Web.Handoff.ReplyID != "r2" || rec.Web.Budget == nil || !rec.Web.Budget.Reached || rec.Web.Budget.WrapUp || rec.Web.Budget.From != 5 {
		t.Fatalf("stored = %+v", rec.Web)
	}

	// Raising the budget keeps the handoff until the Thread works again.
	if got, err := m.SetBudget(sum.ID, 3, 0); err != nil || got.Budget.Reached || got.Handoff == nil {
		t.Fatalf("raised budget = %+v %+v, %v", got.Budget, got.Handoff, err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatalf("send after raising = %v", err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if d := detail(t, m, sum.ID); d.Handoff != nil {
		t.Fatalf("handoff after the next turn started = %+v", d.Handoff)
	}
	if got, err := m.SetBudget(sum.ID, 0, 0); err != nil || got.Budget != nil {
		t.Fatalf("removed budget = %+v, %v", got.Budget, err)
	}
	if _, err := m.SetBudget(sum.ID, -1, 0); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("negative budget = %v", err)
	}
}

func TestBudgetLimitMidReplySendsOneWrapUpPrompt(t *testing.T) {
	for _, handoff := range []bool{true, false} {
		m, prov := startUsageManager(t, nil, nil)
		sum, conv := budgetThread(t, m, prov, t.TempDir())
		reply(conv, "r1", "Here is the result.")
		reportUnits(conv, 7.5)
		wrapping(t, m, conv, sum.ID)
		conv.EmitTurn(agentapi.TurnCompleted, "")
		waitUntil(t, "the wrap-up prompt", func() bool { return len(conv.Sends()) == 2 })
		if got := conv.Sends()[1]; got != wrapPrompt {
			t.Fatalf("wrap-up prompt = %q", got)
		}
		conv.EmitTurn(agentapi.TurnWorking, "")
		wrapping(t, m, conv, sum.ID)
		if v := m.preToolUse(t.Context(), budgetSession(t, m, sum.ID), agentapi.ToolUse{Tool: "view"}); v.Deny != wrapDeny {
			t.Fatalf("tool in the wrap-up turn = %+v", v)
		}
		wrapModelCall(conv)
		if handoff {
			reply(conv, "r2", "## Done\nAll of it.")
		}
		conv.EmitTurn(agentapi.TurnCompleted, "")
		h := handoffOf(t, m, sum.ID)
		switch {
		case handoff && (h.ReplyID != "r2" || h.NoReply != ""):
			t.Fatalf("handoff = %+v", h)
		case !handoff && (h.ReplyID != "" || h.NoReply != "the agent wrote no handoff" || !strings.Contains(h.Facts, "the agent wrote none (the agent wrote no handoff)") || !strings.Contains(h.Facts, "not a git repository")):
			t.Fatalf("facts-only handoff = %+v", h)
		}
		if sends := conv.Sends(); len(sends) != 2 || conv.Cancels() != 0 {
			t.Fatalf("sends %q, cancels %d: one prompt, no stop", sends, conv.Cancels())
		}
	}
}

func TestBudgetWrapUpStopsAtItsThirdModelCall(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	s := budgetSession(t, m, sum.ID)
	for range wrapCalls {
		_ = m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "bash"})
		wrapModelCall(conv)
	}
	if conv.Cancels() != 0 {
		t.Fatalf("stopped within %d calls", wrapCalls)
	}
	wrapModelCall(conv)
	waitUntil(t, "the backstop stop", func() bool { return conv.Cancels() == 1 })
	conv.EmitTurn(agentapi.TurnCancelled, "")
	h := handoffOf(t, m, sum.ID)
	if h.ReplyID != "" || h.NoReply != "the wrap-up used its 2 model calls" || !strings.Contains(h.Facts, "**AI credits:** 2.1 spent; budget 2.0") {
		t.Fatalf("handoff = %+v", h)
	}
	if d := detail(t, m, sum.ID); d.StopReason != stopBudget || d.State != StateCancelled || len(conv.Sends()) != 1 {
		t.Fatalf("after the backstop = %s %s, sends %q", d.State, d.StopReason, conv.Sends())
	}
}

func TestBudgetQuotaLimitWrapsUpAfterAQuotaRead(t *testing.T) {
	m, prov := startUsageManager(t, []agentapi.Quota{premium(600)}, nil) // 40% used
	sum, conv := createSession(t, m, prov)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	got, err := m.SetBudget(sum.ID, 0, 80)
	if err != nil || got.Budget == nil || got.Budget.Reached || got.Budget.QuotaUsed == nil || *got.Budget.QuotaUsed != 40 {
		t.Fatalf("SetBudget = %+v, %v", got.Budget, err)
	}
	prov.SetQuota([]agentapi.Quota{premium(1260)}, nil) // 84% used
	m.refreshQuota()
	wrapping(t, m, conv, sum.ID)
	if v := m.preToolUse(t.Context(), budgetSession(t, m, sum.ID), agentapi.ToolUse{Tool: "bash"}); v.Deny != wrapDeny {
		t.Fatalf("tool at the quota limit = %+v", v)
	}
	denied(conv, "d1", "")
	reply(conv, "r1", "## Done\nHalf.")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	h := handoffOf(t, m, sum.ID)
	if h.ReplyID != "r1" || !strings.Contains(h.Facts, "**Account quota:** 84% used, limit 80%") {
		t.Fatalf("handoff = %+v", h)
	}
}

func TestBudgetRaisedDuringTheWrapUpLetsTheTurnGoOn(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	got, err := m.SetBudget(sum.ID, 10, 0)
	if err != nil || got.Budget == nil || got.Budget.Reached || got.Budget.WrappingUp {
		t.Fatalf("raised budget = %+v, %v", got.Budget, err)
	}
	if v := m.preToolUse(t.Context(), budgetSession(t, m, sum.ID), agentapi.ToolUse{Tool: "bash"}); v.Deny != "" {
		t.Fatalf("tool after raising = %+v", v)
	}
	for range wrapCalls + 1 {
		wrapModelCall(conv)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if d := detail(t, m, sum.ID); d.Handoff != nil || d.State != StateCompleted || len(conv.Sends()) != 1 || conv.Cancels() != 0 {
		t.Fatalf("after raising = %+v %s, sends %q, cancels %d", d.Handoff, d.State, conv.Sends(), conv.Cancels())
	}
}

func TestBudgetStopDuringTheWrapUpLeavesTheFacts(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	reply(conv, "r1", "Partial")
	conv.EmitTurn(agentapi.TurnCancelled, "")
	h := handoffOf(t, m, sum.ID)
	if h.ReplyID != "" || h.NoReply != "you stopped the wrap-up" {
		t.Fatalf("handoff = %+v", h)
	}
	if d := detail(t, m, sum.ID); d.StopReason != stopOwner || len(conv.Sends()) != 1 {
		t.Fatalf("after Stop = %s, sends %q", d.StopReason, conv.Sends())
	}
}

func TestBudgetRestartDuringTheWrapUpLeavesTheFacts(t *testing.T) {
	caps := allCaps
	caps.Usage = true
	st := openTestStore(t)
	start := func() (*Manager, *agenttest.Provider) {
		prov := agenttest.NewProvider("fake", caps)
		m := NewManager(st, []agentapi.Provider{prov})
		m.now = func() time.Time { return usageT0 }
		m.quotaTick = time.Hour
		if err := m.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
		return m, prov
	}
	m, prov := start()
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web == nil || rec.Web.Budget == nil || !rec.Web.Budget.WrapUp {
		t.Fatalf("stored while wrapping up = %+v", rec.Web)
	}

	m, _ = start()
	h := handoffOf(t, m, sum.ID)
	if h.ReplyID != "" || h.NoReply != "uam restarted during the wrap-up" || !strings.Contains(h.Facts, "**AI credits:** spend not reported yet; budget 2.0") {
		t.Fatalf("handoff after the restart = %+v", h)
	}
	if d := detail(t, m, sum.ID); d.State != StateInterrupted {
		t.Fatalf("state after the restart = %s", d.State)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web == nil || rec.Web.Handoff == nil || rec.Web.Budget.WrapUp {
		t.Fatalf("stored after the restart = %+v", rec.Web)
	}
}

// denied reports a tool call uam's hook denied, as Copilot CLI 1.0.98 does:
// a failed call whose output starts "Denied by preToolUse hook: ".
func denied(conv *agenttest.Conversation, id, agent string) {
	emitItem(conv, agentapi.Item{ID: id, Kind: agentapi.ItemTool, AgentID: agent, Time: usageT0, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolFailed, Output: "Denied by preToolUse hook: " + wrapDeny}})
}

// A subagent's denied call does not tell the main agent: its reply is not
// the handoff, and the wrap-up prompt goes out.
func TestBudgetSubagentDenialDoesNotTellTheMainAgent(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	_ = m.preToolUse(t.Context(), budgetSession(t, m, sum.ID), agentapi.ToolUse{Tool: "bash"})
	denied(conv, "sub-call", "agent-1")
	reply(conv, "r1", "Summary of the work so far.")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the wrap-up prompt", func() bool { return len(conv.Sends()) == 2 })
	if d := detail(t, m, sum.ID); d.Handoff != nil || !d.Budget.WrappingUp {
		t.Fatalf("after a subagent's denial = %+v %+v", d.Handoff, d.Budget)
	}
}

// Commands that call the model are refused at the budget; read-only and
// settings commands still run.
func TestBudgetRefusesModelCommands(t *testing.T) {
	m, _, _, sum, conv, wrapped := commandManager(t)
	var ran []string
	wrapped.execute = func(_ context.Context, name string, _ agentapi.Prompt) (*agentapi.CommandResult, error) {
		ran = append(ran, name)
		return &agentapi.CommandResult{Kind: "text", Text: "ok"}, nil
	}
	m.mu.Lock()
	m.sessions[sum.ID].budget = store.Budget{Credits: 1, Reached: true}
	m.mu.Unlock()
	_, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review"})
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "budget_reached" {
		t.Fatalf("/review at the budget = %v", err)
	}
	if sub, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "autopilot"}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("/autopilot at the budget = %+v, %v", sub, err)
	}
	if len(ran) != 1 || ran[0] != "autopilot" || len(conv.Sends()) != 0 {
		t.Fatalf("ran %v, sends %q", ran, conv.Sends())
	}
}

func TestBudgetCloseDuringTheWrapUpLeavesTheFacts(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if h := handoffOf(t, m, sum.ID); h.ReplyID != "" || h.NoReply != "the conversation was closed" {
		t.Fatalf("handoff = %+v", h)
	}
}

func TestBudgetUncertainWrapUpPromptLapses(t *testing.T) {
	wait := wrapPromptWait
	wrapPromptWait = 50 * time.Millisecond
	t.Cleanup(func() { wrapPromptWait = wait })
	for _, turnStarts := range []bool{false, true} {
		m, prov := startUsageManager(t, nil, nil)
		sum, conv := budgetThread(t, m, prov, t.TempDir())
		conv.SetSendHook(func(_ context.Context, prompt string) error {
			if prompt == wrapPrompt {
				return agentapi.ErrSubmissionUncertain
			}
			return nil
		})
		reportUnits(conv, 7.1)
		wrapping(t, m, conv, sum.ID)
		conv.EmitTurn(agentapi.TurnCompleted, "")
		waitUntil(t, "the wrap-up prompt", func() bool { return len(conv.Sends()) == 2 })
		if !turnStarts {
			h := handoffOf(t, m, sum.ID)
			if h.ReplyID != "" || h.NoReply != "the wrap-up prompt was not confirmed and no turn started" {
				t.Fatalf("lapsed handoff = %+v", h)
			}
			continue
		}
		// The provider had it after all: its turn keeps the wrap-up going past
		// the deadline, and its reply is the handoff.
		conv.EmitTurn(agentapi.TurnWorking, "")
		time.Sleep(4 * wrapPromptWait)
		if d := detail(t, m, sum.ID); d.Handoff != nil || !d.Budget.WrappingUp {
			t.Fatalf("lapsed during its turn = %+v %+v", d.Handoff, d.Budget)
		}
		reply(conv, "r2", "## Done\nAll.")
		conv.EmitTurn(agentapi.TurnCompleted, "")
		if h := handoffOf(t, m, sum.ID); h.ReplyID != "r2" {
			t.Fatalf("handoff = %+v", h)
		}
	}
}

func TestBudgetBackstopStopRetriesAfterARefusedStop(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	var refused atomic.Bool
	conv.SetCancelHook(func(context.Context) error {
		if refused.CompareAndSwap(false, true) {
			return fmt.Errorf("%w: another operation is in progress", agentapi.ErrBusy)
		}
		return nil
	})
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	for range wrapCalls + 1 {
		wrapModelCall(conv)
	}
	waitUntil(t, "the refused stop", func() bool { return conv.Cancels() == 1 && !budgetStopping(m, sum.ID) })
	wrapModelCall(conv)
	waitUntil(t, "the stop again", func() bool { return conv.Cancels() == 2 })
	conv.EmitTurn(agentapi.TurnCancelled, "")
	if h := handoffOf(t, m, sum.ID); h.NoReply != "the wrap-up used its 2 model calls" {
		t.Fatalf("handoff = %+v", h)
	}
}

func budgetStopping(m *Manager, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id].budgetStopping
}

// A budget changed while the facts are read drops the handoff on its way.
func TestBudgetRaisedWhileTheFactsAreReadDropsTheHandoff(t *testing.T) {
	reading, release := make(chan struct{}), make(chan struct{})
	files := handoffFiles
	handoffFiles = func(ctx context.Context, workdir string, edits map[string]time.Time, start time.Time) (taskListing, error) {
		close(reading)
		<-release
		return files(ctx, workdir, edits, start)
	}
	t.Cleanup(func() { handoffFiles = files })
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := budgetThread(t, m, prov, t.TempDir())
	reportUnits(conv, 7.1)
	wrapping(t, m, conv, sum.ID)
	_ = m.preToolUse(t.Context(), budgetSession(t, m, sum.ID), agentapi.ToolUse{Tool: "bash"})
	denied(conv, "d1", "")
	reply(conv, "r1", "## Done\nAll.")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	<-reading
	if got, err := m.SetBudget(sum.ID, 10, 0); err != nil || got.Budget.Reached || got.Budget.WrappingUp {
		t.Fatalf("raised = %+v, %v", got.Budget, err)
	}
	close(release)
	if err := m.Shutdown(context.Background()); err != nil { // waits for the facts
		t.Fatal(err)
	}
	if d := detail(t, m, sum.ID); d.Handoff != nil {
		t.Fatalf("stale handoff = %+v", d.Handoff)
	}
}

func TestBudgetCreditTextMatchesTheWebUI(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 0.0001: "<0.01", 0.0196: "0.02", 2: "2.0", 2.14: "2.1", 84: "84", 1234.6: "1235"} {
		if got := creditText(v); got != want {
			t.Errorf("creditText(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestHandoffFactsListOnlyTheThreadsFiles(t *testing.T) {
	mine := ChangedFile{Path: "a.go", Status: "M", Additions: 3, Deletions: 1}
	other := ChangedFile{Path: "b.go", Status: "M", Additions: 9}
	cases := []struct {
		name      string
		files     taskListing
		want, not []string
	}{
		{"own and others", taskListing{repo: &gitRepo{}, all: Changes{Files: []ChangedFile{mine, other, {Path: "c.go", Status: "A"}}}, task: []ChangedFile{mine}},
			[]string{"**Files changed by this Thread** against `HEAD`: 1", "`a.go` M +3 −1", "**Other changes in the folder:** 2 files differ from"}, []string{"b.go", "c.go"}},
		{"others only", taskListing{repo: &gitRepo{}, all: Changes{Files: []ChangedFile{other}}},
			[]string{"none of its edits differ from `HEAD`", "**Other changes in the folder:** 1 file differs from"}, []string{"b.go"}},
		{"own only", taskListing{repo: &gitRepo{}, all: Changes{Files: []ChangedFile{mine}}, task: []ChangedFile{mine}},
			[]string{"`a.go` M +3 −1"}, []string{"Other changes"}},
	}
	for _, c := range cases {
		facts := handoffNotes{}.markdown(c.files, nil, nil)
		for _, w := range c.want {
			if !strings.Contains(facts, w) {
				t.Errorf("%s: facts lack %q:\n%s", c.name, w, facts)
			}
		}
		for _, n := range c.not {
			if strings.Contains(facts, n) {
				t.Errorf("%s: facts carry %q:\n%s", c.name, n, facts)
			}
		}
	}
}
