package web

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func uamReadCall(t *testing.T, conv *agenttest.Conversation, args string) agentapi.HostToolResult {
	t.Helper()
	res, err := conv.CallTool(t.Context(), agentapi.HostToolCall{Name: uamToolName, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func uamReadJSON(t *testing.T, conv *agenttest.Conversation, args string, value any) {
	t.Helper()
	res := uamReadCall(t, conv, args)
	if res.Failed || !strings.HasPrefix(res.Text, uamDataPrefix) || len(res.Text) > uamMaxBytes {
		t.Fatalf("read %s: %+v", args, res)
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(res.Text, uamDataPrefix)), value); err != nil {
		t.Fatal(err)
	}
}

type uamTestPage struct {
	Rows []map[string]any `json:"rows"`
	More int              `json:"more"`
	Next string           `json:"next"`
	Note map[string]any   `json:"note"`
}

func TestUAMReadProjectScope(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	other, _ := createSession(t, m, prov)
	var page uamTestPage
	uamReadJSON(t, conv, `{"op":"projects"}`, &page)
	if len(page.Rows) != 1 || page.Rows[0]["id"] != caller.ProjectID {
		t.Fatalf("projects: %+v", page)
	}
	for _, mode := range []store.Mode{store.ModeSafe, store.ModeAssisted} {
		setSession(m, caller.ID, func(s *webSession) { s.mode = mode })
		for _, args := range []string{
			fmt.Sprintf(`{"op":"tasks","args":{"project":%q}}`, other.ProjectID),
			fmt.Sprintf(`{"op":"task","args":{"id":%q}}`, other.ID),
			fmt.Sprintf(`{"op":"changes","args":{"id":%q}}`, other.ID),
			fmt.Sprintf(`{"op":"history","args":{"id":%q}}`, other.ID),
			fmt.Sprintf(`{"op":"search","args":{"query":"x","project":%q}}`, other.ProjectID),
		} {
			if res := uamReadCall(t, conv, args); !res.Failed || !strings.Contains(res.Text, "requires Yolo") {
				t.Fatalf("%s read escaped project: %+v", mode, res)
			}
		}
	}
	setSession(m, caller.ID, func(s *webSession) { s.mode = store.ModeYolo })
	uamReadJSON(t, conv, `{"op":"projects"}`, &page)
	if len(page.Rows) != 2 {
		t.Fatalf("Yolo projects: %+v", page)
	}
	var row uamTaskRow
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"task","args":{"id":%q}}`, other.ID), &row)
	if row.ID != other.ID {
		t.Fatalf("Yolo task: %+v", row)
	}
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"tasks","args":{"project":%q}}`, other.ProjectID), &page)
	if len(page.Rows) != 1 || page.Rows[0]["id"] != other.ID {
		t.Fatalf("Yolo tasks: %+v", page)
	}
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"changes","args":{"id":%q}}`, other.ID), &page)
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"history","args":{"id":%q}}`, other.ID), &page)
	setSession(m, caller.ID, func(s *webSession) { s.modeUnknown = true })
	if res := uamReadCall(t, conv, fmt.Sprintf(`{"op":"task","args":{"id":%q}}`, other.ID)); !res.Failed {
		t.Fatal("unknown permission mode allowed cross-project read")
	}
}

func TestUAMReadStrictArguments(t *testing.T) {
	m, prov, _ := newTestManager(t)
	_, conv := createSession(t, m, prov)
	for _, tc := range []struct{ args, want string }{
		{`{"op":"send"}`, "history {id, cursor?}"},
		{`{"op":"search","args":{"query":" "}}`, "query is required"},
		{`{"op":"search","args":{"q":"x"}}`, `"query": string`},
		{`{"op":"history","args":{}}`, "id is required"},
		{`{"op":"history","args":[]}`, "JSON object"},
		{`{"op":"sense","oops":true}`, `unknown field "oops"`},
		{`{"op":"sense","args":{"project":"elsewhere"}}`, `args: {}`},
		{`{"op":"projects","args":{"limit":200}}`, `args: {"cursor"?: string}`},
		{`{"op":"tasks","args":{"projet":"elsewhere"}}`, `"project"?`},
		{`{"op":"tasks","args":{"stage":"deleted"}}`, "stage must be"},
		{`{"op":"tasks","args":{"state":"finished"}}`, "state must be"},
		{`{"op":"task","args":{"id":"a","cursor":"b"}}`, `args: {"id": "thread ID"}`},
		{`{"op":"changes","args":{}}`, "id is required"},
		{`{"op":"who_touched","args":{}}`, "path is required"},
		{`{"op":"sense","args":null}`, "JSON object"},
		{`{"op":"sense","args":[]}`, "JSON object"},
		{`{"op":"sense"} {"op":"projects"}`, "one JSON object"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			res := uamReadCall(t, conv, tc.args)
			if !res.Failed || !strings.Contains(res.Text, tc.want) {
				t.Fatalf("result %+v; want %q", res, tc.want)
			}
		})
	}
}

func TestUAMReadSenseFiltersAndPaging(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	at := time.Now()
	m.mu.Lock()
	for i := 0; i < 57; i++ {
		s := newSession(fmt.Sprintf("row-%03d", i), prov.Name(), fmt.Sprintf("Task %d", i), caller.Workdir, "", at)
		s.projectID = caller.ProjectID
		s.stage = StageArchived
		s.base = StateCompleted
		m.sessions[s.id] = s
	}
	own := m.sessions[caller.ID]
	own.context = &agentapi.Context{Used: 150, Limit: 1000}
	own.model = "model-a"
	m.mu.Unlock()
	var sense struct {
		Model    string
		Context  agentapi.Context
		Siblings int
	}
	uamReadJSON(t, conv, `{"op":"sense"}`, &sense)
	if sense.Model != "model-a" || sense.Context.Used != 150 || sense.Siblings != 0 {
		t.Fatalf("sense: %+v", sense)
	}
	var page uamTestPage
	args := `{"op":"tasks","args":{"stage":"archived","state":"completed"}}`
	uamReadJSON(t, conv, args, &page)
	if len(page.Rows) != 50 || page.More != 7 || page.Next != "row-049" {
		t.Fatalf("first page: rows %d more %d next %s", len(page.Rows), page.More, page.Next)
	}
	next := page.Next
	page = uamTestPage{}
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"tasks","args":{"stage":"archived","state":"completed","cursor":%q}}`, next), &page)
	if len(page.Rows) != 7 || page.More != 0 || page.Next != "" || page.Rows[0]["id"] != "row-050" {
		t.Fatalf("last page: %+v", page)
	}
}

func TestUAMReadBytePagingKeepsAllRows(t *testing.T) {
	// Valid paths with JSON escapes hit the byte limit before the row limit.
	var rows []uamRow
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("file-%02d-%s", i, strings.Repeat("<", 1000))
		rows = append(rows, uamRow{key, map[string]any{"path": key}})
	}
	cursor := ""
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		text, err := uamPage(rows, cursor, nil)
		if err != nil || len(text) > uamMaxBytes {
			t.Fatalf("page: %d bytes, %v", len(text), err)
		}
		var page uamTestPage
		if err := json.Unmarshal([]byte(strings.TrimPrefix(text, uamDataPrefix)), &page); err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Rows {
			key := r["path"].(string)
			if seen[key] {
				t.Fatal("repeated row")
			}
			seen[key] = true
		}
		if page.More == 0 {
			break
		}
		if page.Next == cursor || page.Next == "" {
			t.Fatal("paging made no progress")
		}
		cursor = page.Next
	}
	if len(seen) != len(rows) {
		t.Fatalf("read %d of %d rows", len(seen), len(rows))
	}
}

func TestUAMReadWhoTouched(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	sibling, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: caller.ProjectID, Name: "Earlier task"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := createSession(t, m, prov)
	at := time.Now().Add(-time.Minute).UTC()
	setSession(m, caller.ID, func(s *webSession) { s.edits = map[string]time.Time{"README.md": at}; s.editsKnown = true })
	setSession(m, sibling.ID, func(s *webSession) {
		s.edits = map[string]time.Time{filepath.Join(caller.Workdir, "README.md"): at}
		s.stage = StageSettled
		s.editsKnown = false
		s.outcome = "Updated README.md"
	})
	setSession(m, other.ID, func(s *webSession) { s.edits = map[string]time.Time{filepath.Join(caller.Workdir, "README.md"): at} })
	var page uamTestPage
	uamReadJSON(t, conv, `{"op":"who_touched","args":{"path":"sub/../README.md"}}`, &page)
	if len(page.Rows) != 2 || page.Note["partial"] != true || !strings.Contains(page.Note["source"].(string), "shell") {
		t.Fatalf("who_touched: %+v", page)
	}
	for _, row := range page.Rows {
		if row["id"] == other.ID {
			t.Fatal("other project leaked")
		}
		if row["id"] == sibling.ID && (row["outcome"] != "Updated README.md" || row["stage"] != "settled" || row["last_edit"] != at.Format(time.RFC3339Nano)) {
			t.Fatalf("sibling: %+v", row)
		}
	}
	setSession(m, sibling.ID, func(s *webSession) { s.editsKnown = true })
	uamReadJSON(t, conv, `{"op":"who_touched","args":{"path":"missing.md"}}`, &page)
	if len(page.Rows) != 0 || page.Note["partial"] != false {
		t.Fatalf("empty result: %+v", page)
	}
}

func TestUAMReadChanges(t *testing.T) {
	repo := gitRepoFixture(t)
	m, prov, _ := newTestManager(t)
	caller, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, repo)})
	if err != nil {
		t.Fatal(err)
	}
	setSession(m, caller.ID, func(s *webSession) { s.edits = map[string]time.Time{"tracked.txt": time.Now()}; s.editsKnown = true })
	var page uamTestPage
	uamReadJSON(t, prov.Last(), fmt.Sprintf(`{"op":"changes","args":{"id":%q}}`, caller.ID), &page)
	if len(page.Rows) != 1 || page.Rows[0]["path"] != "tracked.txt" || page.Rows[0]["additions"] != float64(2) || page.Note["scope"] != ScopeTask {
		t.Fatalf("changes: %+v", page)
	}
}

func TestUAMReadLifecycleAndRegistration(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	if !slices.Contains(toolNames(conv.Request().Tools), uamToolName) {
		t.Fatal("fresh task has no read tool")
	}
	if _, err := m.Close(caller.ID); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, caller.ID, "resume", mustUUID(t), ModeSend, SubmissionAccepted)
	conv = prov.Last()
	if !slices.Contains(toolNames(conv.Request().Tools), uamToolName) {
		t.Fatal("resumed task has no read tool")
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if _, err := m.Settle(caller.ID); err != nil {
		t.Fatal(err)
	}
	call := agentapi.HostToolCall{TaskID: caller.ID, Name: uamToolName, Arguments: json.RawMessage(`{"op":"sense"}`)}
	if res := m.uamCall(t.Context(), caller.ID, call); !res.Failed || !strings.Contains(res.Text, "settled") {
		t.Fatalf("settled: %+v", res)
	}
	call.TaskID = "different"
	if res := m.uamCall(t.Context(), caller.ID, call); !res.Failed || !strings.Contains(res.Text, "does not belong") {
		t.Fatalf("identity: %+v", res)
	}
	active, activeConv := createSession(t, m, prov)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call.TaskID = active.ID
	if res := m.uamCall(ctx, active.ID, call); !res.Failed {
		t.Fatal("cancelled read returned data")
	}
	var sense map[string]any
	uamReadJSON(t, activeConv, `{"op":"sense"}`, &sense)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) != 0 {
		t.Fatal("read call leaked lifecycle registration")
	}
}

func TestUAMReadLongInvalidArgumentsStayBounded(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	setSession(m, caller.ID, func(s *webSession) { s.mode = store.ModeYolo })
	long := strings.Repeat("界", 7000)
	for _, tc := range []struct{ args, shape string }{
		{fmt.Sprintf(`{"op":"tasks",%q:true}`, long), ""},
		{fmt.Sprintf(`{"op":"tasks","args":{%q:true}}`, long), uamShapes["tasks"]},
		{fmt.Sprintf(`{"op":"tasks","args":{"project":%q}}`, long), uamShapes["tasks"]},
	} {
		result := uamReadCall(t, conv, tc.args)
		if !result.Failed || len(result.Text) > uamMaxBytes || !utf8.ValidString(result.Text) || !strings.Contains(result.Text, truncatedMarker) {
			t.Fatalf("invalid argument result: failed=%v, bytes=%d, valid UTF-8=%v", result.Failed, len(result.Text), utf8.ValidString(result.Text))
		}
		if tc.shape != "" && !strings.HasSuffix(result.Text, "; args: "+tc.shape) {
			t.Fatal("bounded error lost its argument shape")
		}
	}
}

func TestUAMReadSearch(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	sibling, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: caller.ProjectID, Name: "Sibling"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := createSession(t, m, prov)
	t.Setenv("COPILOT_HOME", t.TempDir())
	var page uamTestPage
	uamReadJSON(t, conv, `{"op":"search","args":{"query":"redraw"}}`, &page)
	if len(page.Rows) != 0 || !strings.Contains(page.Note["reason"].(string), "unavailable") {
		t.Fatalf("no store: %+v", page)
	}
	sessionStore(t,
		[2]string{caller.ConversationID, "The redraw needed a second pass."},
		[2]string{sibling.ConversationID, "We fixed the redraw by replaying focus events."},
		[2]string{other.ConversationID, "A redraw in another project."},
	)
	page = uamTestPage{}
	uamReadJSON(t, conv, `{"op":"search","args":{"query":"redraw"}}`, &page)
	if len(page.Rows) != 2 || page.Note != nil {
		t.Fatalf("search: %+v", page)
	}
	for _, row := range page.Rows {
		if row["project"] != caller.ProjectID || !strings.Contains(row["snippet"].(string), "redraw") || row["name"] == "" || row["updated"] == "" {
			t.Fatalf("row: %+v", row)
		}
	}
	setSession(m, caller.ID, func(s *webSession) { s.mode = store.ModeYolo })
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"search","args":{"query":"redraw","project":%q}}`, other.ProjectID), &page)
	if len(page.Rows) != 1 || page.Rows[0]["id"] != other.ID {
		t.Fatalf("Yolo search: %+v", page)
	}
	m.mu.Lock()
	m.searchSlot = make(chan struct{}, 1)
	m.searchSlot <- struct{}{}
	m.mu.Unlock()
	if res := uamReadCall(t, conv, `{"op":"search","args":{"query":"redraw"}}`); !res.Failed || !strings.Contains(res.Text, "another search") {
		t.Fatalf("busy search: %+v", res)
	}
}

func TestUAMReadHistory(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	sibling, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: caller.ProjectID, Name: "Sibling"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	items := []agentapi.Item{
		{ID: "u0", Kind: agentapi.ItemUser, Text: "Fix the redraw", Time: at},
		{ID: "r0", Kind: agentapi.ItemReasoning, Text: "thinking"},
		{ID: "t0", Kind: agentapi.ItemTool, Text: "tool output"},
		{ID: "s0", Kind: agentapi.ItemAssistant, Text: "subagent reply", AgentID: "sub"},
		{ID: "a0", Kind: agentapi.ItemAssistant, Text: strings.Repeat("x", 3000)},
	}
	for i := 0; i < 55; i++ {
		items = append(items, agentapi.Item{ID: fmt.Sprintf("a%d", i+1), Kind: agentapi.ItemAssistant, Text: fmt.Sprintf("reply %d", i)})
	}
	setSession(m, sibling.ID, func(s *webSession) { s.items = items })
	var page uamTestPage
	args := fmt.Sprintf(`{"op":"history","args":{"id":%q}}`, sibling.ID)
	uamReadJSON(t, conv, args, &page)
	if len(page.Rows) != 50 || page.More != 7 || page.Note["earlier_missing"] != false || page.Note["loading"] != false || page.Note["unavailable"] != nil {
		t.Fatalf("first page: rows %d more %d note %+v", len(page.Rows), page.More, page.Note)
	}
	long := page.Rows[1]["text"].(string)
	if page.Rows[0]["kind"] != "user" || page.Rows[0]["text"] != "Fix the redraw" || page.Rows[0]["time"] != at.Format(time.RFC3339Nano) ||
		page.Rows[1]["kind"] != "assistant" || len(long) > uamHistoryEntryBytes+len(truncatedMarker) || !strings.HasSuffix(long, truncatedMarker) {
		t.Fatalf("entries: %+v", page.Rows[:2])
	}
	next := page.Next
	page = uamTestPage{}
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"history","args":{"id":%q,"cursor":%q}}`, sibling.ID, next), &page)
	if len(page.Rows) != 7 || page.More != 0 || page.Rows[6]["text"] != "reply 54" {
		t.Fatalf("last page: %+v", page)
	}
	// The caller may read its own conversation, as with task and changes.
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"history","args":{"id":%q}}`, caller.ID), &page)
	// An active Thread whose record is not in memory starts reading it.
	setSession(m, sibling.ID, func(s *webSession) { s.conv, s.items, s.history, s.historyRead = nil, nil, "", false })
	page = uamTestPage{}
	uamReadJSON(t, conv, args, &page)
	if len(page.Rows) != 0 || page.Note["loading"] != true {
		t.Fatalf("loading history: %+v", page)
	}
	var done <-chan struct{}
	setSession(m, sibling.ID, func(s *webSession) { done = s.historyDone })
	<-done
	setSession(m, sibling.ID, func(s *webSession) { s.convID, s.items, s.history, s.historyRead = "", nil, "", false })
	uamReadJSON(t, conv, args, &page)
	if !strings.Contains(fmt.Sprint(page.Note["unavailable"]), "no provider conversation") {
		t.Fatalf("unavailable active history: %+v", page)
	}
	// A closed Thread without a provider conversation reports why its history is unavailable.
	setSession(m, sibling.ID, func(s *webSession) {
		s.conv, s.items, s.convID, s.base, s.history, s.historyRead = nil, nil, "", StateClosed, "", false
	})
	if res := uamReadCall(t, conv, args); !res.Failed || !strings.Contains(res.Text, "no provider conversation") {
		t.Fatalf("unavailable history: %+v", res)
	}
}
