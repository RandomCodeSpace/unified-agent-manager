package copilot

import (
	"context"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func completeData(success *bool, outcome string, summary string) *rpc.SessionTaskCompleteData {
	d := &rpc.SessionTaskCompleteData{Success: success, Summary: copilot.String(summary)}
	if outcome != "" {
		v := rpc.TaskCompletionOutcome(outcome)
		d.Outcome = &v
	}
	return d
}

func completionEvent(id, parent string, data rpc.SessionEventData) copilot.SessionEvent {
	e := ev(id, data)
	if parent != "" {
		e.ParentID = copilot.String(parent)
	}
	return e
}

func TestCompletionDecisionsPreserveTypedNativeFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success *bool
		outcome string
		want    agentapi.CompletionDecision
	}{
		{"success", copilot.Bool(true), "", agentapi.CompletionAccepted},
		{"completed", nil, "completed", agentapi.CompletionAccepted},
		{"accepted", copilot.Bool(true), "completed", agentapi.CompletionAccepted},
		{"rejected", copilot.Bool(false), "", agentapi.CompletionRejected},
		{"continue", nil, "continue", agentapi.CompletionRejected},
		{"blocked", copilot.Bool(false), "blocked", agentapi.CompletionBlocked},
		{"contradictory completed", copilot.Bool(false), "completed", agentapi.CompletionUnknown},
		{"contradictory continue", copilot.Bool(true), "continue", agentapi.CompletionUnknown},
		{"contradictory blocked", copilot.Bool(true), "blocked", agentapi.CompletionUnknown},
		{"unknown", copilot.Bool(true), "future", agentapi.CompletionUnknown},
		{"legacy summary", nil, "", agentapi.CompletionUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTranscript()
			tr.items(ev("user-event", userMessage("user-message", rpc.UserMessageDeliveryIdle, "root")))
			d := completeData(tc.success, tc.outcome, "  Native summary\x1b[31m  ")
			d.Reason = copilot.String("label-safe reason")
			d.Blocker = &rpc.TaskBlocker{Kind: "permission", Reason: "denied", Resumable: true}
			// Exercise the SDK union decoder, rather than a parallel event shape.
			b, err := json.Marshal(completionEvent("receipt", "user-event", d))
			if err != nil {
				t.Fatal(err)
			}
			var native copilot.SessionEvent
			if err := json.Unmarshal(b, &native); err != nil {
				t.Fatal(err)
			}
			got := tr.items(native)
			if len(got) != 1 || got[0].Kind != agentapi.ItemNotice || got[0].ID != "receipt" || got[0].Time != native.Timestamp {
				t.Fatalf("receipt=%+v", got)
			}
			c := got[0].Completion
			if c == nil || c.Decision != tc.want || c.UserItemID != "user-message" || c.Summary != "Native summary" || c.Reason != "label-safe reason" || c.Blocker == nil || c.Blocker.Kind != "permission" || c.Blocker.Reason != "denied" || !c.Blocker.Resumable {
				t.Fatalf("completion=%+v", c)
			}
			for _, fact := range []string{"Summary: ` Native summary `", "Reason: ` label-safe reason `", "Blocker kind: ` permission `", "Blocker reason: ` denied `", "Resumable: yes"} {
				if !strings.Contains(got[0].Text, fact) {
					t.Fatalf("notice hides %q: %s", fact, got[0].Text)
				}
			}
		})
	}
	if got := newTranscript().items(ev("legacy", &rpc.SessionTaskCompleteData{})); len(got) != 0 {
		t.Fatalf("empty legacy receipt added noise: %+v", got)
	}
}

func TestCompletionBoundsAndReplay(t *testing.T) {
	main := userMessage("exact-message-id", rpc.UserMessageDeliveryIdle, "root")
	steer := userMessage("steer-id", rpc.UserMessageDeliverySteering, "steer")
	auto := userMessage("auto-id", rpc.UserMessageDeliveryIdle, "")
	auto.IsAutopilotContinuation = copilot.Bool(true)
	d := completeData(copilot.Bool(true), "completed", strings.Repeat("界", 500))
	d.Reason = copilot.String(strings.Repeat("r", 500))
	d.Blocker = &rpc.TaskBlocker{Kind: rpc.TaskBlockerKind(strings.Repeat("k", 100)), Reason: rpc.PermissionRecoveryReason(strings.Repeat("r", 100))}
	journal := []copilot.SessionEvent{ev("user-native", main), completionEvent("steer", "user-native", steer), completionEvent("auto", "steer", auto), agentEv("child-user", "child", userMessage("child-message", rpc.UserMessageDeliveryIdle, "child")), agentEv("child-complete", "child", d), completionEvent("main-complete", "auto", d)}
	var live []agentapi.Item
	tr := newTranscript()
	for _, e := range journal {
		live = append(live, tr.items(e)...)
	}
	if len(live) != 6 {
		t.Fatalf("completion notices missing: %+v", live)
	}
	for _, idx := range []int{4, 5} {
		c := live[idx].Completion
		want := "exact-message-id"
		if idx == 4 {
			want = "child-message"
		}
		if c == nil || c.UserItemID != want || len(c.Summary) > 512 || !utf8.ValidString(c.Summary) || len(c.Reason) > 256 || len(c.Blocker.Kind) > 64 || len(c.Blocker.Reason) > 64 {
			t.Fatalf("bounded receipt=%+v", c)
		}
	}
	if got := history(journal).Items; !reflect.DeepEqual(got, live) {
		t.Fatalf("replay differs: %+v", got)
	}
	fc := &fakeClient{journal: journal, pageSize: 2}
	p := readerProvider(fc)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	h, err := p.ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "s", Workdir: "/work"})
	if err != nil || !reflect.DeepEqual(h.Items, live) {
		t.Fatalf("cold history=%+v %v", h.Items, err)
	}
	w, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: agentapi.ReadRequest{ConversationID: "s", Workdir: "/work"}, ItemID: "main-complete", Before: 10, After: 10})
	if err != nil || len(w.Items) == 0 {
		t.Fatalf("window=%+v %v", w, err)
	}
	for _, it := range w.Items {
		if it.Completion != nil && len(it.Completion.Summary) > 512 {
			t.Fatal("whole window escaped metadata bound")
		}
	}
	tr = newTranscript()
	tr.items(ev("event-id", &rpc.UserMessageData{Content: "no message ID"}))
	if got := tr.items(completionEvent("done", "event-id", d))[0].Completion.UserItemID; got != "event-id" {
		t.Fatalf("event fallback=%q", got)
	}
	tr.items(ev("too-long", userMessage(strings.Repeat("u", 257), rpc.UserMessageDeliveryIdle, "root")))
	if got := tr.items(ev("unsafe", d))[0].Completion.UserItemID; got != "" {
		t.Fatal("identity was truncated or retained")
	}
}

func TestCompletionBoundsReleaseOversizedBacking(t *testing.T) {
	for _, tc := range []struct {
		name, unit string
	}{
		{"visible text clipped", "x"},
		{"control sequences removed", "\x1b[31m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			c := func() *agentapi.TaskCompletion {
				large := "fact" + strings.Repeat(tc.unit, 1<<20)
				d := completeData(copilot.Bool(true), "completed", large)
				d.Reason = copilot.String(large)
				d.Blocker = &rpc.TaskBlocker{Kind: rpc.TaskBlockerKind(large), Reason: rpc.PermissionRecoveryReason(large)}
				return newTranscript().completion(ev("receipt", d), d)
			}()
			runtime.GC()
			runtime.ReadMemStats(&after)
			for _, field := range []struct {
				text  string
				limit int
			}{{c.Summary, 512}, {c.Reason, 256}, {c.Blocker.Kind, 64}, {c.Blocker.Reason, 64}} {
				if !strings.HasPrefix(field.text, "fact") || len(field.text) > field.limit || !utf8.ValidString(field.text) {
					t.Fatalf("bounded facts changed: length=%d limit=%d", len(field.text), field.limit)
				}
			}
			runtime.KeepAlive(c)
			retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			t.Logf("completion retained heap delta: %d bytes", retained)
			if retained > 1<<20 {
				t.Fatalf("compact completion retained oversized backing: %d bytes", retained)
			}
		})
	}
}

func TestCompletionForegroundGeneration(t *testing.T) {
	for _, scenario := range []string{"current", "child", "unresolved", "delayed", "duplicate", "rejected", "synchronous send", "between send and boundary", "continuation before boundary"} {
		t.Run(scenario, func(t *testing.T) {
			h := openWeb(t)
			c := h.conv.(*conversation)
			d := completeData(copilot.Bool(true), "completed", "Finished native work")
			user := completionEvent("user-event", "", userMessage("same-root", rpc.UserMessageDeliveryIdle, "root"))
			start := completionEvent("start-1", "user-event", &rpc.AssistantTurnStartData{TurnID: "0"})
			answer := completionEvent("answer-1", "start-1", &rpc.AssistantMessageData{MessageID: "answer", Content: "Done"})
			if scenario == "synchronous send" {
				h.fs.beforeReturn = func(string) {
					h.fs.onEvent(user)
					h.fs.onEvent(start)
					h.fs.onEvent(answer)
					h.fs.onEvent(completionEvent("receipt", "answer-1", d))
				}
				if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "root"}); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, e := range []copilot.SessionEvent{user, start, answer} {
					h.fs.onEvent(e)
				}
				if scenario == "continuation before boundary" {
					continuation := userMessage("auto", rpc.UserMessageDeliveryIdle, "")
					continuation.IsAutopilotContinuation = copilot.Bool(true)
					h.fs.onEvent(completionEvent("continue", "answer-1", continuation))
				}
				if scenario == "between send and boundary" {
					h.fs.onEvent(completionEvent("idle-1", "answer-1", &rpc.SessionIdleData{}))
					h.fs.beforeReturn = func(string) { h.fs.onEvent(completionEvent("receipt", "answer-1", d)) }
					if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "next prompt"}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "duplicate" {
					h.fs.onEvent(completionEvent("receipt", "answer-1", d))
				}
				if scenario == "delayed" || scenario == "duplicate" {
					// Autopilot's new model call reuses the same root user item and
					// the same provider TurnID. Timestamps also stay identical.
					h.fs.onEvent(completionEvent("start-2", "answer-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
				}
				receipt := completionEvent("receipt", "answer-1", d)
				if scenario == "child" {
					receipt.AgentID = copilot.String("child")
				}
				if scenario == "unresolved" {
					receipt.ParentID = copilot.String("unseen")
				}
				if scenario == "rejected" {
					receipt.Data = completeData(copilot.Bool(false), "continue", "More work")
				}
				h.fs.onEvent(receipt)
			}
			h.fs.onEvent(completionEvent("idle", "receipt", &rpc.SessionIdleData{}))
			var turn *agentapi.Turn
			for _, e := range h.sink.all() {
				if e.Turn != nil {
					turn = e.Turn
				}
			}
			if turn == nil || turn.State != agentapi.TurnCompleted {
				t.Fatalf("turn=%+v", turn)
			}
			want := scenario == "current" || scenario == "synchronous send" || scenario == "rejected"
			if (turn.Completion != nil) != want {
				t.Fatalf("receipt crossed foreground boundary: %+v", turn.Completion)
			}
			if want && turn.Completion.UserItemID != "same-root" {
				t.Fatalf("anchor=%+v", turn.Completion)
			}
			if scenario == "rejected" && c.taskCompleted {
				t.Fatal("rejected modern receipt ended autopilot")
			}
		})
	}
}

func TestCompletionRejectedOrConflictingReceiptKeepsAutopilotRunning(t *testing.T) {
	for _, d := range []*rpc.SessionTaskCompleteData{
		completeData(copilot.Bool(false), "continue", "More work"),
		completeData(copilot.Bool(false), "blocked", "Needs intervention"),
		completeData(copilot.Bool(false), "completed", "Conflict"),
		completeData(copilot.Bool(true), "continue", "Conflict"),
	} {
		h, runtime := runtimeHarness(t)
		runtime.state.Mode = "autopilot"
		h.conv.(*conversation).refreshExecution(context.Background())
		h.fs.onEvent(ev("user", userMessage("root", rpc.UserMessageDeliveryIdle, "root")))
		h.fs.onEvent(completionEvent("start", "user", &rpc.AssistantTurnStartData{TurnID: "0"}))
		h.fs.onEvent(completionEvent("receipt", "start", d))
		mode := rpc.SessionModeAutopilot
		h.fs.onEvent(completionEvent("idle", "receipt", &rpc.SessionIdleData{Mode: &mode}))
		for _, e := range h.sink.all() {
			if e.Turn != nil && e.Turn.State != agentapi.TurnWorking {
				t.Fatalf("nonaccepted receipt ended autopilot: %+v", e.Turn)
			}
		}
	}
}

func TestCompletionCreateAndResumeLiveReceiptsReplayWithoutDuplicates(t *testing.T) {
	journal := []copilot.SessionEvent{
		ev("user", userMessage("native-message", rpc.UserMessageDeliveryIdle, "root")),
		completionEvent("start", "user", &rpc.AssistantTurnStartData{TurnID: "0"}),
		completionEvent("accepted-1", "start", completeData(copilot.Bool(true), "completed", "First")),
		completionEvent("accepted-2", "accepted-1", completeData(copilot.Bool(true), "completed", "Second")),
	}
	journal = append(journal, journal[2])
	for _, resumed := range []bool{false, true} {
		fc := &fakeClient{}
		p := readerProvider(fc)
		t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
		sink := &recSink{}
		req := agentapi.OpenRequest{SessionID: "s", Workdir: "/work", Events: sink}
		if resumed {
			req.ConversationID = "previous"
		}
		conv, err := p.Open(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range journal {
			fc.sessions[0].onEvent(e)
		}
		fc.sessions[0].events = journal
		got, err := conv.History(context.Background())
		if err != nil || len(got.Items) != 3 || got.Items[1].Completion == nil || got.Items[2].Completion == nil || got.Items[1].Completion.Summary != "First" || got.Items[2].Completion.Summary != "Second" {
			t.Fatalf("create/resume=%v history=%+v %v", resumed, got.Items, err)
		}
		var receipts []agentapi.Item
		for _, e := range sink.all() {
			if e.Item != nil && e.Item.Completion != nil {
				receipts = append(receipts, *e.Item)
			}
		}
		if len(receipts) != 3 || receipts[0].ID != "accepted-1" || receipts[1].ID != "accepted-2" || !reflect.DeepEqual(receipts[0], receipts[2]) {
			t.Fatalf("live order/identity=%+v", receipts)
		}
		fc.sessions[0].onEvent(completionEvent("idle", "accepted-2", &rpc.SessionIdleData{}))
		var ended *agentapi.Turn
		waitFor(t, "the resumed todo snapshot and ending turn", func() bool {
			for _, e := range sink.all() {
				if e.Turn != nil {
					ended = e.Turn
				}
			}
			return ended != nil && ended.State == agentapi.TurnCompleted
		})
		if ended == nil || ended.Completion == nil || ended.Completion.Summary != "Second" {
			t.Fatalf("duplicate replaced latest receipt: %+v", ended)
		}
	}
}

func TestCompletionDelayedReceiptPreservesItsOriginalUserAnchor(t *testing.T) {
	tr := newTranscript()
	journal := []copilot.SessionEvent{
		ev("user-1", userMessage("message-1", rpc.UserMessageDeliveryIdle, "old root")),
		completionEvent("start-1", "user-1", &rpc.AssistantTurnStartData{TurnID: "0"}),
		completionEvent("answer-1", "start-1", &rpc.AssistantMessageData{MessageID: "a1", Content: "Old reply"}),
		completionEvent("user-2", "answer-1", userMessage("message-2", rpc.UserMessageDeliveryIdle, "new root")),
		completionEvent("start-2", "user-2", &rpc.AssistantTurnStartData{TurnID: "0"}),
		completionEvent("late", "answer-1", completeData(copilot.Bool(true), "completed", "Old summary")),
		completionEvent("current", "start-2", completeData(copilot.Bool(true), "completed", "Current summary")),
	}
	var items []agentapi.Item
	for _, e := range journal {
		items = append(items, tr.items(e)...)
	}
	if items[3].Completion.UserItemID != "message-1" || items[4].Completion.UserItemID != "message-2" {
		t.Fatalf("anchors=%+v", items)
	}
}

func TestCompletionDelayedDistinctStartCannotBorrowNewUserAnchor(t *testing.T) {
	h := openWeb(t)
	events := []copilot.SessionEvent{
		ev("u1", userMessage("message-1", rpc.UserMessageDeliveryIdle, "first")),
		completionEvent("start1", "u1", &rpc.AssistantTurnStartData{TurnID: "0"}),
		completionEvent("answer1", "start1", &rpc.AssistantMessageData{MessageID: "a1", Content: "First reply"}),
		completionEvent("u2", "answer1", userMessage("message-2", rpc.UserMessageDeliveryIdle, "second")),
		completionEvent("start2", "u2", &rpc.AssistantTurnStartData{TurnID: "0"}),
		completionEvent("start3", "answer1", &rpc.AssistantTurnStartData{TurnID: "0"}),
		completionEvent("receipt", "start3", completeData(copilot.Bool(true), "completed", "First summary")),
		completionEvent("idle", "receipt", &rpc.SessionIdleData{}),
	}
	for _, e := range events {
		h.fs.onEvent(e)
	}
	var ended *agentapi.Turn
	var receipt *agentapi.Item
	for _, e := range h.sink.all() {
		if e.Turn != nil {
			ended = e.Turn
		}
		if e.Item != nil && e.Item.ID == "receipt" {
			receipt = e.Item
		}
	}
	if receipt == nil || receipt.Completion.UserItemID != "message-1" {
		t.Fatalf("parent anchor was replaced: %+v", receipt)
	}
	if ended == nil || ended.State != agentapi.TurnCompleted || ended.Completion != nil {
		t.Fatalf("conflicting start authorized latest outcome: %+v", ended)
	}
}

func TestCompletionNoticeFactsAreLiteralAndBounded(t *testing.T) {
	d := completeData(copilot.Bool(false), "blocked", "[summary](https://example.com) `code`")
	d.Reason = copilot.String("**reason**")
	d.Blocker = &rpc.TaskBlocker{Kind: "permission", Reason: "denied", Resumable: false}
	it := newTranscript().items(ev("receipt", d))[0]
	if !strings.Contains(it.Text, "Summary: `` [summary](https://example.com) `code` ``") || !strings.Contains(it.Text, "Reason: ` **reason** `") || !strings.Contains(it.Text, "Blocker kind: ` permission ` · Blocker reason: ` denied ` · Resumable: no") {
		t.Fatalf("literal facts=%q", it.Text)
	}
	d.Summary, d.Reason = copilot.String(strings.Repeat("`", 1000)), copilot.String(strings.Repeat("`", 1000))
	d.Blocker.Kind, d.Blocker.Reason = rpc.TaskBlockerKind(strings.Repeat("`", 1000)), rpc.PermissionRecoveryReason(strings.Repeat("`", 1000))
	if text := newTranscript().items(ev("max", d))[0].Text; len(text) > 3072 {
		t.Fatalf("notice bytes=%d", len(text))
	}
}
