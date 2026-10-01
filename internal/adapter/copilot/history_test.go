package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// settledJournal is a recorded events.jsonl of a Task settled by uam web
// (CLI 1.0.88): two turns, one synchronous explore subagent, then the
// disconnect's cancelled second completion and the shutdown. The system
// prompt and opaque fields were dropped and the directory renamed.
func settledJournal(t *testing.T) []copilot.SessionEvent {
	t.Helper()
	f, err := os.Open("testdata/settled-subagent.events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var evs []copilot.SessionEvent
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		var ev copilot.SessionEvent
		if err := json.Unmarshal(lines.Bytes(), &ev); err != nil {
			t.Fatalf("recorded event: %v", err)
		}
		evs = append(evs, ev)
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return evs
}

func readerProvider(fc *fakeClient) *webProvider {
	return newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
}

func TestReadHistoryReadsTheRecordedJournalWithoutResuming(t *testing.T) {
	fc := &fakeClient{journal: settledJournal(t), pageSize: 7}
	p := readerProvider(fc)
	const id = "e5cc1da2-d847-4205-8e3c-c27a133f0127"
	h, err := p.ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: id, Workdir: "/work/project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.resume) != 0 || len(fc.create) != 0 {
		t.Fatalf("reading opened a session: resumed %d, created %d", len(fc.resume), len(fc.create))
	}
	// 25 recorded events in pages of 7, read newest first.
	if len(fc.reads) != 4 || fc.reads[0].Cursor != nil || *fc.reads[0].Direction != rpc.EventsReadDirectionBackward || fc.reads[0].SessionID != id {
		t.Fatalf("reads = %+v", fc.reads)
	}
	var got []string
	for _, it := range h.Items {
		got = append(got, fmt.Sprintf("%s|%s|%.8s", it.Kind, it.AgentID, it.ID))
	}
	const sub = "7f2ffe9e-6e06-4459-8957-1bfdecf93edd"
	want := []string{
		"user||1646b4db",
		"tool||toolu_01",
		"user|" + sub + "|10d60772",
		"assistant|" + sub + "|0791bac1",
		"assistant||00965ab7",
		"user||a99d019b",
		"assistant||5d5e3d8e",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("items =\n%v\nwant\n%v", got, want)
	}
	if tool := h.Items[1].Tool; tool.Name != "task" || tool.Status != agentapi.ToolCompleted || tool.Output != "alpha.txt  \nbeta.md" {
		t.Fatalf("tool = %+v", tool)
	}
	// The first end wins: the disconnect's cancelled completion does not
	// turn a finished subagent into a stopped one.
	if len(h.Subagents) != 1 || h.Subagents[0].ID != sub || h.Subagents[0].Status != agentapi.SubagentCompleted || h.Subagents[0].Name != "list-files" {
		t.Fatalf("subagents = %+v", h.Subagents)
	}
	// The subagent's own model change is not the conversation's model.
	if h.Model != "claude-sonnet-5" || h.Truncated {
		t.Fatalf("model = %q, truncated = %v", h.Model, h.Truncated)
	}
}

func TestReadHistoryKeepsTheNewestPagesOfALargeJournal(t *testing.T) {
	var journal []copilot.SessionEvent
	for i := range webReadPages + 6 {
		id := fmt.Sprintf("m%03d", i)
		journal = append(journal, ev(id, userMessage(id, rpc.UserMessageDeliveryIdle, "prompt")))
	}
	fc := &fakeClient{journal: journal, pageSize: 1}
	h, err := readerProvider(fc).ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.reads) != webReadPages+6 || !h.Truncated || len(h.Items) != webReadPages || h.Items[0].ID != "m006" || h.Items[len(h.Items)-1].ID != fmt.Sprintf("m%03d", webReadPages+5) {
		t.Fatalf("reads %d, truncated %v, items %d from %s", len(fc.reads), h.Truncated, len(h.Items), h.Items[0].ID)
	}
}

func TestReadHistoryErrors(t *testing.T) {
	// Recorded from CLI 1.0.88 for an ID with no journal.
	missing := errors.New("JSON-RPC Error -32603: Request sessions.readPersistedEvents failed with message: Persisted event journal is unavailable for session 00000000-1111-4222-8333-444444444444")
	_, err := readerProvider(&fakeClient{readErr: missing}).ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "00000000-1111-4222-8333-444444444444"})
	if !errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("missing journal = %v", err)
	}
	_, err = readerProvider(&fakeClient{readErr: errors.New("broken pipe")}).ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "c1"})
	if err == nil || errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("read failure = %v", err)
	}
	if _, err := readerProvider(&fakeClient{}).ReadHistory(context.Background(), agentapi.ReadRequest{}); err == nil {
		t.Fatal("reading without an ID must fail")
	}
}

func TestReadHistoryCapsBytesAndFinishesTheSnapshot(t *testing.T) {
	journal := []copilot.SessionEvent{ev("model", &rpc.SessionModelChangeData{NewModel: "recorded-model"})}
	for i := range 8 {
		journal = append(journal, ev(fmt.Sprint(i), userMessage(fmt.Sprint(i), rpc.UserMessageDeliveryIdle, strings.Repeat("x", 3<<20))))
	}
	fc := &fakeClient{journal: journal, pageSize: 1}
	p := readerProvider(fc)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	h, err := p.ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Truncated || len(h.Items) != 5 || h.Items[0].ID != "3" || h.Items[4].ID != "7" || len(fc.reads) != 9 || h.Model != "recorded-model" {
		t.Fatalf("truncated %v; items %d; pages %d; model %q", h.Truncated, len(h.Items), len(fc.reads), h.Model)
	}
}

func TestReadHistoryListsSubagentsBegunBeforeItsPart(t *testing.T) {
	journal := []copilot.SessionEvent{
		ev("e0", &rpc.ToolExecutionStartData{ToolCallID: "task-1", ToolName: "task"}),
		agentEv("e1", "early", &rpc.SubagentStartedData{ToolCallID: "task-1", AgentName: "explore", AgentDisplayName: "Explore"}),
		agentEv("e2", "early", &rpc.AssistantMessageData{MessageID: "m1", Content: "found it"}),
		agentEv("e3", "early", &rpc.SubagentCompletedData{ToolCallID: "task-1", AgentName: "explore"}),
		ev("e4", &rpc.ToolExecutionCompleteData{ToolCallID: "task-1", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: strings.Repeat("r", 1000)}}),
	}
	for i := range 6 {
		journal = append(journal, ev(fmt.Sprint(i), userMessage(fmt.Sprint(i), rpc.UserMessageDeliveryIdle, strings.Repeat("x", 3<<20))))
	}
	h, err := readerProvider(&fakeClient{journal: journal, pageSize: 1}).ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Truncated || slices.ContainsFunc(h.Items, func(it agentapi.Item) bool { return it.AgentID != "" || it.ID == "task-1" }) {
		t.Fatalf("truncated %v, items %v", h.Truncated, itemIDs(h.Items))
	}
	if len(h.Subagents) != 1 {
		t.Fatalf("subagents = %+v", h.Subagents)
	}
	if sa := h.Subagents[0]; sa.ID != "early" || sa.Name != "Explore" || sa.Status != agentapi.SubagentCompleted || sa.ParentToolCallID != "task-1" || sa.Result != strings.Repeat("r", webResultBytes) {
		t.Fatalf("subagent = %+v", sa)
	}
}

// windowJournal records n main-agent turns: a prompt, a bash call and an
// answer, with a subagent's message between them. Turn 3's output is 100 KiB.
func windowJournal(n int) []copilot.SessionEvent {
	var journal []copilot.SessionEvent
	for i := range n {
		id := fmt.Sprintf("%03d", i)
		output := "ok " + id
		if i == 3 {
			output = strings.Repeat("y", 100<<10)
		}
		journal = append(journal,
			ev("e-u"+id, userMessage("u"+id, rpc.UserMessageDeliveryIdle, "prompt "+id)),
			ev("e-ts"+id, &rpc.ToolExecutionStartData{ToolCallID: "t" + id, ToolName: "bash", Arguments: map[string]any{"command": "ls"}}),
			agentEv("e-s"+id, "sub", &rpc.AssistantMessageData{MessageID: "s" + id, Content: "sub " + id}),
			ev("e-tc"+id, &rpc.ToolExecutionCompleteData{ToolCallID: "t" + id, Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: output}}),
			ev("e-a"+id, &rpc.AssistantMessageData{MessageID: "a" + id, Content: "answer " + id}))
	}
	return journal
}

func itemIDs(items []agentapi.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestReadHistoryWindowReadsOneAgentWholeAroundAnItem(t *testing.T) {
	fc := &fakeClient{journal: windowJournal(8), pageSize: 7}
	p := readerProvider(fc)
	read := agentapi.ReadRequest{ConversationID: "c1"}
	w, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, ItemID: "u004", Before: 4, After: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(w.Items); !slices.Equal(got, []string{"a002", "u003", "t003", "a003", "u004", "t004", "a004"}) || w.At != 4 || w.Start || w.Next != "u005" {
		t.Fatalf("window = %v at %d, start %v, next %q", got, w.At, w.Start, w.Next)
	}
	// Whole: ReadHistory clips this 100 KiB output.
	if it := w.Items[2]; it.Tool == nil || it.Tool.Status != agentapi.ToolCompleted || len(it.Tool.Output) != 100<<10 || it.Clipped {
		t.Fatalf("tool = %+v", it.Tool)
	}
	// 40 events in pages of 7, all read forward: the snapshot is released.
	if len(fc.reads) != 6 {
		t.Fatalf("reads = %d", len(fc.reads))
	}
	for _, r := range fc.reads {
		if r.Direction == nil || *r.Direction != rpc.EventsReadDirectionForward {
			t.Fatalf("read %+v is not forward", r)
		}
	}

	h, err := p.ReadHistory(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range h.Items {
		if it.ID == "t003" && (len(it.Tool.Output) != maxToolText || !it.Clipped) || it.ID != "t003" && it.Clipped {
			t.Fatalf("history item %s: %+v, clipped %v", it.ID, it.Tool, it.Clipped)
		}
	}

	w, err = p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, ItemID: "u001", Before: 10})
	if err != nil || !slices.Equal(itemIDs(w.Items), []string{"u000", "t000", "a000", "u001"}) || !w.Start || w.At != 3 || w.Next != "t001" {
		t.Fatalf("first window = %v, %+v", err, w)
	}
	// The end of the record, of the subagent only.
	w, err = p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, AgentID: "sub", Before: 3})
	if err != nil || !slices.Equal(itemIDs(w.Items), []string{"s005", "s006", "s007"}) || w.Start || w.At != 3 || w.Next != "" {
		t.Fatalf("subagent tail = %v, %+v", err, w)
	}
	if _, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, ItemID: "s001"}); !errors.Is(err, agentapi.ErrItemNotFound) {
		t.Fatalf("a subagent's item is not the main agent's: %v", err)
	}
}

func TestReadHistoryWindowStartsOverWhenTheJournalIsReplaced(t *testing.T) {
	grown := windowJournal(3)
	fc := &fakeClient{journal: windowJournal(2), pageSize: 4, expireRead: 2, grow: grown[10:]}
	p := readerProvider(fc)
	read := agentapi.ReadRequest{ConversationID: "c1"}
	w, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, ItemID: "a001", After: 10})
	if err != nil {
		t.Fatal(err)
	}
	// The second pass starts over, reads the grown journal and finds the
	// item again.
	if !slices.Equal(itemIDs(w.Items), []string{"a001", "u002", "t002", "a002"}) || w.At != 0 || fc.reads[2].Cursor != nil {
		t.Fatalf("window = %v at %d", itemIDs(w.Items), w.At)
	}
	if _, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, ItemID: "zzz"}); !errors.Is(err, agentapi.ErrItemNotFound) {
		t.Fatalf("missing item = %v", err)
	}
}

func TestReadHistoryLeavesToolCallsBegunBeforeItsPartToTheWindow(t *testing.T) {
	journal := []copilot.SessionEvent{ev("e0", &rpc.ToolExecutionStartData{ToolCallID: "early", ToolName: "bash", Arguments: map[string]any{"command": "sleep"}})}
	for i := range 6 {
		journal = append(journal, ev(fmt.Sprint(i), userMessage(fmt.Sprint(i), rpc.UserMessageDeliveryIdle, strings.Repeat("x", 3<<20))))
	}
	journal = append(journal, ev("e9", &rpc.ToolExecutionCompleteData{ToolCallID: "early", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "slept"}}))
	fc := &fakeClient{journal: journal, pageSize: 1}
	p := readerProvider(fc)
	read := agentapi.ReadRequest{ConversationID: "c1"}
	h, err := p.ReadHistory(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Truncated || slices.Contains(itemIDs(h.Items), "early") || h.Items[0].ID != "1" {
		t.Fatalf("truncated %v, items %v", h.Truncated, itemIDs(h.Items))
	}
	w, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: read, ItemID: "1", Before: 10})
	if err != nil || !slices.Equal(itemIDs(w.Items), []string{"early", "0", "1"}) || !w.Start || w.Items[0].Tool.Output != "slept" || w.Items[0].Tool.Status != agentapi.ToolCompleted {
		t.Fatalf("window = %v, %v", err, itemIDs(w.Items))
	}
}

// subagentJournal records n subagents, each spawned by a task tool call
// whose output is its result. Subagent 3 ends only after subagent 5 began,
// and subagent 1 fails.
func subagentJournal(n int) []copilot.SessionEvent {
	end := func(i int) []copilot.SessionEvent {
		id, call := fmt.Sprintf("sa%03d", i), fmt.Sprintf("task%03d", i)
		last := ev("e-c"+id, &rpc.SubagentCompletedData{ToolCallID: call, AgentName: "explore"})
		if i == 1 {
			last = ev("e-f"+id, &rpc.SubagentFailedData{ToolCallID: call, AgentName: "explore", Error: "boom"})
		}
		return []copilot.SessionEvent{last, ev("e-tc"+id, &rpc.ToolExecutionCompleteData{ToolCallID: call, Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "result " + id + strings.Repeat("r", 1000)}})}
	}
	var journal []copilot.SessionEvent
	for i := range n {
		id, call := fmt.Sprintf("sa%03d", i), fmt.Sprintf("task%03d", i)
		journal = append(journal,
			ev("e-ts"+id, &rpc.ToolExecutionStartData{ToolCallID: call, ToolName: "task"}),
			agentEv("e-s"+id, id, &rpc.SubagentStartedData{ToolCallID: call, AgentName: "explore", AgentDisplayName: "Explore " + id}),
			agentEv("e-m"+id, id, &rpc.AssistantMessageData{MessageID: "m" + id, Content: "done"}))
		if i != 3 {
			journal = append(journal, end(i)...)
		}
		if i == 5 {
			journal = append(journal, end(3)...)
		}
	}
	return journal
}

func subagentIDs(subs []agentapi.Subagent) []string {
	out := make([]string, len(subs))
	for i, sa := range subs {
		out[i] = sa.ID
	}
	return out
}

func TestReadSubagentsReadsTheRecordsUpToOne(t *testing.T) {
	fc := &fakeClient{journal: subagentJournal(8), pageSize: 5}
	p := readerProvider(fc)
	read := agentapi.ReadRequest{ConversationID: "c1"}
	w, err := p.ReadSubagents(context.Background(), agentapi.SubagentRequest{ReadRequest: read, AgentID: "sa005", Before: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := subagentIDs(w.Subagents); !slices.Equal(got, []string{"sa002", "sa003", "sa004", "sa005"}) || w.Start {
		t.Fatalf("window = %v, start %v", got, w.Start)
	}
	// One forward pass to the end of the journal releases its snapshot.
	if len(fc.reads) != 8 {
		t.Fatalf("reads = %d", len(fc.reads))
	}
	for _, r := range fc.reads {
		if r.Direction == nil || *r.Direction != rpc.EventsReadDirectionForward {
			t.Fatalf("read %+v is not forward", r)
		}
	}
	// Records are as ReadHistory reports them, updated after the requested
	// one began.
	h, err := p.ReadHistory(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	for _, sa := range w.Subagents {
		want := h.Subagents[slices.IndexFunc(h.Subagents, func(r agentapi.Subagent) bool { return r.ID == sa.ID })]
		if sa != want || sa.Status != agentapi.SubagentCompleted || len(sa.Result) != webResultBytes {
			t.Fatalf("record %+v, want %+v", sa, want)
		}
	}
	w, err = p.ReadSubagents(context.Background(), agentapi.SubagentRequest{ReadRequest: read, AgentID: "sa002", Before: 10})
	if err != nil || !slices.Equal(subagentIDs(w.Subagents), []string{"sa000", "sa001", "sa002"}) || !w.Start || w.Subagents[1].Status != agentapi.SubagentFailed || w.Subagents[1].Error != "boom" {
		t.Fatalf("first window = %v, %+v", err, w)
	}
	if _, err := p.ReadSubagents(context.Background(), agentapi.SubagentRequest{ReadRequest: read, AgentID: "zzz"}); !errors.Is(err, agentapi.ErrItemNotFound) {
		t.Fatalf("unknown subagent = %v", err)
	}
	// The records kept before the requested one hold at most webWindowBytes.
	long := subagentJournal(6)
	for i := range long {
		if d, ok := long[i].Data.(*rpc.SubagentStartedData); ok {
			d.AgentDescription = strings.Repeat("d", 1500<<10)
		}
	}
	w, err = readerProvider(&fakeClient{journal: long}).ReadSubagents(context.Background(), agentapi.SubagentRequest{ReadRequest: read, AgentID: "sa005", Before: 10})
	if err != nil || !slices.Equal(subagentIDs(w.Subagents), []string{"sa003", "sa004", "sa005"}) || w.Start {
		t.Fatalf("long window = %v, %v", err, subagentIDs(w.Subagents))
	}
}

func TestReadHistoryKeepsShellExitCodes(t *testing.T) {
	journal := []copilot.SessionEvent{
		ev("e0", &rpc.ToolExecutionStartData{ToolCallID: "sh", ToolName: "bash", Arguments: map[string]any{"command": "go test ./..."}}),
		ev("e1", &rpc.ToolExecutionCompleteData{ToolCallID: "sh", Success: true, ShellExecution: &rpc.ToolExecutionCompleteShellExecution{ExitCode: 2}, Result: &rpc.ToolExecutionCompleteResult{Content: "FAIL"}}),
		ev("e2", &rpc.ToolExecutionStartData{ToolCallID: "view", ToolName: "view"}),
		ev("e3", &rpc.ToolExecutionCompleteData{ToolCallID: "view", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "text"}}),
	}
	h, err := readerProvider(&fakeClient{journal: journal}).ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Items) != 2 || h.Items[0].Tool.ExitCode == nil || *h.Items[0].Tool.ExitCode != 2 || h.Items[1].Tool.ExitCode != nil {
		t.Fatalf("items = %+v", h.Items)
	}
}
