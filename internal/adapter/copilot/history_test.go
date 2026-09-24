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
