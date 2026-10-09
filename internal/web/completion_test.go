package web

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestCompletionOutcomeUsesOnlyMatchingAcceptedSummary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		completion *agentapi.TaskCompletion
		accepted   bool
	}{
		{"accepted", &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, UserItemID: "native-message", Summary: "  Fixed the parser\x1b[31m  "}, true},
		{"rejected", &agentapi.TaskCompletion{Decision: agentapi.CompletionRejected, UserItemID: "native-message", Summary: "Wrong shortcut"}, false},
		{"blocked", &agentapi.TaskCompletion{Decision: agentapi.CompletionBlocked, UserItemID: "native-message", Summary: "Wrong shortcut"}, false},
		{"unknown", &agentapi.TaskCompletion{Decision: agentapi.CompletionUnknown, UserItemID: "native-message", Summary: "Wrong shortcut"}, false},
		{"empty summary", &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, UserItemID: "native-message", Summary: " \x1b[31m "}, false},
		{"wrong message", &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, UserItemID: "native-event", Summary: "Wrong shortcut"}, false},
		{"missing correlation", &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, Summary: "Wrong shortcut"}, false},
		{"missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := newAssistProvider()
			st := openTestStore(t)
			m, project := assistManager(t, st, prov)
			prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) { return "Utility fallback", nil })
			sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
			if err != nil {
				t.Fatal(err)
			}
			conv := prov.Last()
			conv.EmitTurn(agentapi.TurnWorking, "")
			conv.EmitItem(agentapi.Item{ID: "native-message", Kind: agentapi.ItemUser, Text: "fix parser"})
			conv.EmitItem(agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Input: `{"path":"a.go"}`, Status: agentapi.ToolCompleted}})
			conv.EmitItem(agentapi.Item{ID: "answer", Kind: agentapi.ItemAssistant, Text: "Fixed the parser."})
			if tc.completion != nil {
				conv.EmitItem(agentapi.Item{ID: "receipt", Kind: agentapi.ItemNotice, Text: "Native completion", Completion: tc.completion})
			}
			conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCompleted, Completion: tc.completion}})
			want := "Utility fallback; 1 file changed"
			if tc.accepted {
				want = "Fixed the parser; 1 file changed"
			}
			waitUntil(t, "the native or Utility outcome", func() bool { return summaryOf(t, m, sum.ID).Outcome == want })
			calls := utilityCalls(prov, "outcome")
			if (tc.accepted && calls != 0) || (!tc.accepted && calls != 1) {
				t.Fatalf("Utility calls=%d", calls)
			}
			// The existing stored outcome survives a cold Manager reload.
			if tc.accepted {
				if err := m.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				m2 := startManager(t, st, prov)
				if got := summaryOf(t, m2, sum.ID).Outcome; got != want {
					t.Fatalf("stored native outcome=%q", got)
				}
			}
		})
	}
}

func TestCompletionMetadataIsClonedBoundedAndAccounted(t *testing.T) {
	c := &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, UserItemID: strings.Repeat("u", 257), Summary: strings.Repeat("界", 300), Reason: strings.Repeat("r", 300), Blocker: &agentapi.CompletionBlocker{Kind: strings.Repeat("k", 100), Reason: strings.Repeat("b", 100), Resumable: true}}
	input := agentapi.Item{ID: "receipt", Kind: agentapi.ItemNotice, Completion: c}
	got := checkItem(input, time.Unix(1, 0))
	if got.Completion == c || got.Completion.Blocker == c.Blocker {
		t.Fatal("mutable completion aliases provider memory")
	}
	if got.Completion.UserItemID != "" || len(got.Completion.Summary) > 512 || len(got.Completion.Reason) > 256 || len(got.Completion.Blocker.Kind) > 64 || len(got.Completion.Blocker.Reason) > 64 {
		t.Fatalf("unbounded metadata=%+v", got.Completion)
	}
	if c.UserItemID == "" || len(c.Summary) != 900 {
		t.Fatal("provider metadata mutated")
	}
	n := len(got.ID) + len(got.Completion.Decision) + len(got.Completion.Summary) + len(got.Completion.Reason) + len(got.Completion.Blocker.Kind) + len(got.Completion.Blocker.Reason)
	if size := itemSize(got); size != n {
		t.Fatalf("retained bytes=%d want=%d", size, n)
	}
	clone := cloneBody(got)
	clone.Completion.Blocker.Kind = "changed"
	if got.Completion.Blocker.Kind == "changed" {
		t.Fatal("body snapshot aliases completion blocker")
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
				return checkCompletion(&agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, Summary: large, Reason: large, Blocker: &agentapi.CompletionBlocker{Kind: large, Reason: large}})
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

func TestCompletionNoticePreservesSuggestedReplies(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		return "Run the focused check", nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u", Kind: agentapi.ItemUser, Text: "fix it"})
	conv.EmitItem(agentapi.Item{ID: "a", Kind: agentapi.ItemAssistant, Text: "Fixed it."})
	c := &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, UserItemID: "u", Summary: "Fixed it"}
	conv.EmitItem(agentapi.Item{ID: "receipt", Kind: agentapi.ItemNotice, Text: "Completion accepted: Fixed it", Completion: c})
	conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCompleted, Completion: c}})
	got, err := m.SuggestReplies(context.Background(), sum.ID)
	if err != nil || got.ItemID != "a" || len(got.Replies) != 1 || got.Replies[0] != "Run the focused check" {
		t.Fatalf("completion hid suggestions: %+v %v", got, err)
	}
}

func TestCompletionPreviousUserCannotShortcutLatestTiming(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		return "Latest Utility fallback", nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	finish := func(userID, summary string, completion *agentapi.TaskCompletion) {
		conv.EmitTurn(agentapi.TurnWorking, "")
		conv.EmitItem(agentapi.Item{ID: userID, Kind: agentapi.ItemUser, Text: userID})
		conv.EmitItem(agentapi.Item{ID: "a-" + userID, Kind: agentapi.ItemAssistant, Text: summary})
		conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCompleted, Completion: completion}})
	}
	old := &agentapi.TaskCompletion{Decision: agentapi.CompletionAccepted, UserItemID: "message-1", Summary: "First summary"}
	finish("message-1", "First reply", old)
	if got := summaryOf(t, m, sum.ID).Outcome; got != "First summary" {
		t.Fatalf("first outcome=%q", got)
	}
	finish("message-2", "Second reply", old)
	waitUntil(t, "the latest timing's Utility fallback", func() bool { return summaryOf(t, m, sum.ID).Outcome == "Latest Utility fallback" })
	if calls := utilityCalls(prov, "outcome"); calls != 1 {
		t.Fatalf("latest fallback calls=%d", calls)
	}
}
