package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func memoryNotesOf(m *Manager, id string) []store.WebMemoryNote {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.WebMemoryNote{}, m.sessions[id].memoryNotes...)
}

func memoryPinOf(m *Manager, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id].memoryPin
}

// A compaction starts a note job; once it succeeded, the notes its summary
// dropped and uam's records go out with the main agent's next tool call,
// once.
func TestMemoryPinsWhatTheCompactionDropped(t *testing.T) {
	prov := newAssistProvider()
	st := openTestStore(t)
	m, project := assistManager(t, st, prov)
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose != memoryPurpose {
			return "", fmt.Errorf("no %s here", req.Purpose)
		}
		if req.ResponseSchema == nil || !strings.Contains(req.Prompt, `"from":"user"`) {
			return "", fmt.Errorf("unexpected request %+v", req)
		}
		return `{"notes":[
			{"kind":"constraint","item":1,"quote":"Never touch the   vendored folder"},
			{"kind":"fact","item":1,"quote":"use pnpm only, not npm"},
			{"kind":"decision","item":2,"quote":"Always deploy on Fridays, as agreed"},
			{"kind":"wish","item":1,"quote":"Never touch the vendored folder"}]}`, nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	finishTurn(conv, "Never touch the vendored folder; use pnpm only, not npm.", "Understood: pnpm, and the vendored folder stays as it is.")

	conv.Emit(agentapi.Event{Kind: agentapi.EventCompaction, Compacting: true})
	waitUntil(t, "the memory notes", func() bool { return len(memoryNotesOf(m, sum.ID)) == 2 })
	// The invented quote and the unknown kind are left out; spaces are evened.
	if notes := memoryNotesOf(m, sum.ID); notes[0].Kind != "constraint" || notes[0].Quote != "Never touch the vendored folder" || notes[0].ItemID == "" || notes[1].Kind != "fact" {
		t.Fatalf("notes = %+v", notes)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventCompaction, Compacted: true, CompactionSummary: "The user wants pnpm only."})
	waitUntil(t, "the memory pin", func() bool { return memoryPinOf(m, sum.ID) != "" })

	s := budgetSession(t, m, sum.ID)
	if v := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "bash", AgentID: "sub"}); strings.Contains(v.Context, "uam memory") {
		t.Fatalf("a subagent got the pin: %q", v.Context)
	}
	v := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "bash"})
	for _, want := range []string{"uam memory: your conversation was compacted", `"id": "` + sum.ID + `"`, `constraint: "Never touch the vendored folder"`, "uam's records:", "not a git repository"} {
		if !strings.Contains(v.Context, want) {
			t.Errorf("pin lacks %q:\n%s", want, v.Context)
		}
	}
	if strings.Contains(v.Context, "pnpm") || strings.Contains(v.Context, "## Facts") {
		t.Errorf("pin repeats what the summary carries, or the facts' heading:\n%s", v.Context)
	}
	if v := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "bash"}); strings.Contains(v.Context, "uam memory") {
		t.Fatalf("pin given twice: %q", v.Context)
	}

	// The notes are saved with the Thread; the next job reads only newer turns.
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web == nil || len(rec.Web.MemoryNotes) != 2 {
		t.Fatalf("saved notes = %+v", rec.Web)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventCompaction})
	conv.Emit(agentapi.Event{Kind: agentapi.EventCompaction, Compacting: true})
	time.Sleep(50 * time.Millisecond)
	if n := utilityCalls(prov, memoryPurpose); n != 1 {
		t.Fatalf("note jobs without new turns = %d", n)
	}
}

func TestMemoryItemsKeepPromptsFirst(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", maxMemoryItemRunes)
	var items []agentapi.Item
	for i := range 30 {
		items = append(items, agentapi.Item{ID: fmt.Sprint("a", i), Kind: agentapi.ItemAssistant, Text: long, Time: t0.Add(time.Duration(2*i+1) * time.Second)})
		if i == 0 {
			items = append(items, agentapi.Item{ID: "u0", Kind: agentapi.ItemUser, Text: "Only pnpm.", Time: t0.Add(2 * time.Second)})
		}
	}
	items = append(items, agentapi.Item{ID: "old", Kind: agentapi.ItemUser, Text: "before", Time: t0})
	got, through := memoryItems(items, t0)
	if !through.Equal(t0.Add(59*time.Second)) || len(got) == 0 || got[0].Item != 1 {
		t.Fatalf("through %v, %d items", through, len(got))
	}
	size, prompt := 0, false
	for _, it := range got {
		size += len(it.Text)
		prompt = prompt || it.id == "u0"
		if it.id == "old" || it.id == "a0" {
			t.Errorf("kept %s: older than since, or the oldest reply past the bound", it.id)
		}
	}
	if !prompt || size > maxMemoryInput || got[len(got)-1].id != "a29" {
		t.Fatalf("prompt kept %v, %d bytes, last %s", prompt, size, got[len(got)-1].id)
	}
}

func TestMergeMemoryNotesDropsFactsFirst(t *testing.T) {
	var old []store.WebMemoryNote
	for i := range maxMemoryNotes {
		kind := "constraint"
		if i%2 == 0 {
			kind = "fact"
		}
		old = append(old, store.WebMemoryNote{Kind: kind, Quote: fmt.Sprint("note ", i)})
	}
	got := mergeMemoryNotes(old, []store.WebMemoryNote{{Kind: "decision", Quote: "new"}, {Kind: "fact", Quote: "note 1"}})
	if len(got) != maxMemoryNotes || got[len(got)-1].Quote != "new" || got[0].Quote != "note 1" {
		t.Fatalf("merged %d: first %+v, last %+v", len(got), got[0], got[len(got)-1])
	}
}

func TestSummaryCarries(t *testing.T) {
	summary := strings.ToLower("Kept: tests in internal/web/memory_test.go use the Pager; deploy waits for review.")
	for quote, want := range map[string]bool{
		"the tests live in internal/web/memory_test.go":     true,
		"Never touch the vendored folder":                   false,
		"ok, go":                                            true, // nothing distinctive to look for
		"deploy only after the security review is approved": false,
	} {
		if got := summaryCarries(summary, quote); got != want {
			t.Errorf("summaryCarries(%q) = %v, want %v", quote, got, want)
		}
	}
}

// uam search with an id lists that Thread's matching passages, several of
// them, with where each is.
func TestUAMSearchOneThread(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	sessionStore(t,
		[2]string{caller.ConversationID, "We chose pnpm for the redraw build."},
		[2]string{caller.ConversationID, "The redraw needed a second pass."},
		[2]string{"someone-else", "A redraw in a session uam does not know."},
	)
	var page uamTestPage
	uamReadJSON(t, conv, `{"op":"search","args":{"query":"redraw","id":"`+caller.ID+`"}}`, &page)
	if len(page.Rows) != 2 || page.Rows[0]["source"] != "turn" || !strings.Contains(fmt.Sprint(page.Rows[0]["snippet"], page.Rows[1]["snippet"]), "pnpm") {
		t.Fatalf("search one thread: %+v", page)
	}
	if res := uamReadCall(t, conv, `{"op":"search","args":{"query":"redraw","id":"nope"}}`); !res.Failed {
		t.Fatalf("unknown thread: %+v", res)
	}
	if got := searchSource("turn", "abc:turn:12"); got != "turn 12" {
		t.Fatalf("source = %q", got)
	}
	if got := searchSource("checkpoint_overview", "abc:checkpoint:1"); got != "checkpoint overview" {
		t.Fatalf("source = %q", got)
	}
}
