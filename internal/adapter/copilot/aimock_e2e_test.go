package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// The aimock suite runs the real provider against the real Copilot CLI with
// the model replaced by aimock (web/node_modules/@copilotkit/aimock), a local
// mock of the OpenAI API: no sign-in, no AI credits. It is opt-in, and once
// opted in a missing CLI, node or aimock fails instead of skipping.

// aimockModel is the custom model every aimock test selects.
var aimockModel = agentapi.CustomModel{Name: "mock", DisplayName: "Mock", ModelID: "mock-1", APIKey: "k"}

// mockFixture is one aimock fixture: {"match": ..., "response": ...} plus
// optional timing keys.
type mockFixture map[string]any

type aimockServer struct {
	url string
	mu  sync.Mutex
	out strings.Builder
}

// aimockRequest is the part of a journal entry the tests read.
type aimockRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   struct {
		Model    string           `json:"model"`
		Messages []map[string]any `json:"messages"`
	} `json:"body"`
	Response struct {
		Status int `json:"status"`
	} `json:"response"`
}

// requireAimockE2E skips without UAM_AIMOCK_E2E=1, fails when a tool the
// suite needs is missing, and isolates the CLI from the developer's login,
// configuration and sessions: HOME is a fresh directory and no token or XDG
// directory is inherited.
func requireAimockE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("UAM_AIMOCK_E2E") != "1" {
		t.Skip("set UAM_AIMOCK_E2E=1 to run the real CLI against aimock")
	}
	for _, tool := range []string{"copilot", "node"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("UAM_AIMOCK_E2E=1 needs %s on PATH: %v", tool, err)
		}
	}
	if _, err := os.Stat(aimockCLI()); err != nil {
		t.Fatalf("UAM_AIMOCK_E2E=1 needs aimock (npm --prefix web ci, or UAM_AIMOCK_CLI): %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "COPILOT_HOME"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func aimockCLI() string {
	if path := os.Getenv("UAM_AIMOCK_CLI"); path != "" {
		return path
	}
	return filepath.Join("..", "..", "..", "web", "node_modules", "@copilotkit", "aimock", "dist", "cli.js")
}

// startAimock serves fixtures on a free localhost port until the test ends.
// The info log is what reports the port.
// Strict mode answers an unmatched request with an error, so a request no
// fixture expects fails the turn. A failed test logs the journal and output.
func startAimock(t *testing.T, fixtures []mockFixture) *aimockServer {
	t.Helper()
	data, err := json.Marshal(map[string]any{"fixtures": fixtures})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "fixtures.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &aimockServer{}
	cmd := exec.Command("node", aimockCLI(), "-h", "127.0.0.1", "-p", "0", "-f", file, "--strict", "--log-level", "info") // #nosec G204 -- the test's own aimock and fixture file.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start aimock: %v", err)
	}
	listening := make(chan string, 1)
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			s.mu.Lock()
			s.out.WriteString(line + "\n")
			s.mu.Unlock()
			if _, url, ok := strings.Cut(line, "listening on "); ok {
				select {
				case listening <- strings.TrimSpace(url):
				default:
				}
			}
		}
	}()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("aimock journal:\n%s", s.rawJournal())
		}
		_ = cmd.Process.Kill()
		<-copied
		_ = cmd.Wait()
		if t.Failed() {
			s.mu.Lock()
			t.Logf("aimock output:\n%s", s.out.String())
			s.mu.Unlock()
		}
	})
	select {
	case s.url = <-listening:
	case <-copied:
		t.Fatal("aimock exited before it listened")
	case <-time.After(20 * time.Second):
		t.Fatal("aimock did not listen within 20s")
	}
	return s
}

func (s *aimockServer) rawJournal() string {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(s.url + "/__aimock/journal")
	if err != nil {
		return err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

// chats returns the chat completion requests aimock received, oldest first.
func (s *aimockServer) chats(t *testing.T) []aimockRequest {
	t.Helper()
	var all []aimockRequest
	if err := json.Unmarshal([]byte(s.rawJournal()), &all); err != nil {
		t.Fatalf("aimock journal: %v", err)
	}
	var out []aimockRequest
	for _, r := range all {
		if r.Method == http.MethodPost && r.Path == "/v1/chat/completions" {
			out = append(out, r)
		}
	}
	return out
}

// aimockThread is one conversation of a real provider whose only model is
// aimockModel served by mock.
type aimockThread struct {
	p    *webProvider
	conv agentapi.Conversation
	sink *recSink
	dir  string
}

// openAimock starts a provider on the real CLI and opens a new conversation
// in a fresh project directory. req fills in what the test needs beyond that.
func openAimock(t *testing.T, ctx context.Context, mock *aimockServer, req agentapi.OpenRequest) aimockThread {
	t.Helper()
	p := newWebProvider(newSDKClient, webPingInterval)
	// Never sweep CLIs this test did not start.
	p.swept = true
	m := aimockModel
	m.BaseURL = mock.url + "/v1"
	p.SetCustomModels([]agentapi.CustomModel{m})
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := p.Shutdown(sctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	th := aimockThread{p: p, sink: &recSink{}, dir: req.Workdir}
	if th.dir == "" {
		th.dir = t.TempDir()
	}
	req.SessionID = uuid.NewString()
	req.Workdir, req.Model, req.Events = th.dir, aimockModel.SelectionID(), th.sink
	conv, err := p.Open(ctx, req)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	th.conv = conv
	return th
}

// send sends text and returns the turn it ends with.
func (th aimockThread) send(t *testing.T, ctx context.Context, text string) agentapi.Turn {
	t.Helper()
	from := len(th.sink.all())
	if err := th.conv.Send(ctx, agentapi.Prompt{Text: text}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return th.turnEnd(t, ctx, from)
}

// turnEnd waits for the first turn end emitted after event from.
func (th aimockThread) turnEnd(t *testing.T, ctx context.Context, from int) agentapi.Turn {
	t.Helper()
	e := awaitEvent(t, ctx, th.sink, from, "the turn to end", func(e agentapi.Event) bool {
		return e.Kind == agentapi.EventTurn && e.Turn.State != agentapi.TurnWorking
	})
	return *e.Turn
}

// awaitEvent polls sink until an event after from satisfies match or ctx ends.
func awaitEvent(t *testing.T, ctx context.Context, sink *recSink, from int, what string, match func(agentapi.Event) bool) agentapi.Event {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		evs := sink.all()
		for _, e := range evs[min(from, len(evs)):] {
			if match(e) {
				return e
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

// replies returns the main agent's assistant text in evs: the final items,
// the deltas streamed for them and how many deltas there were.
func replies(evs []agentapi.Event) (items, deltas string, chunks int) {
	for _, e := range evs {
		switch {
		case e.Kind == agentapi.EventItem && e.Item.Kind == agentapi.ItemAssistant && e.Item.AgentID == "":
			items += e.Item.Text
		case e.Kind == agentapi.EventDelta && e.Delta.Kind == agentapi.ItemAssistant:
			deltas += e.Delta.Text
			chunks++
		}
	}
	return items, deltas, chunks
}

// reply answers a prompt containing prompt with content.
func reply(prompt, content string) mockFixture {
	return mockFixture{"match": map[string]any{"userMessage": prompt}, "response": map[string]any{"content": content}}
}

// toolCall is one tool call of a mock model response.
func toolCall(id, name string, args map[string]any) map[string]any {
	return map[string]any{"id": id, "name": name, "arguments": args}
}

// toolRound answers prompt with calls, then the result of the call with id
// last with final. The result fixture comes first: the prompt is still the
// last user message when the results come back, and would match again.
func toolRound(prompt, last, final string, calls ...map[string]any) []mockFixture {
	return []mockFixture{
		{"match": map[string]any{"toolCallId": last}, "response": map[string]any{"content": final}},
		{"match": map[string]any{"userMessage": prompt}, "response": map[string]any{"toolCalls": calls}},
	}
}

// awaitPermission waits for the first pending permission emitted after from.
func (th aimockThread) awaitPermission(t *testing.T, ctx context.Context, from int) agentapi.Interaction {
	t.Helper()
	e := awaitEvent(t, ctx, th.sink, from, "a permission request", func(e agentapi.Event) bool {
		return e.Kind == agentapi.EventInteraction && e.Interaction.Kind == agentapi.InteractionPermission && e.Interaction.State == agentapi.InteractionPending
	})
	return *e.Interaction
}

// tool returns the latest state of tool call id.
func (th aimockThread) tool(id string) *agentapi.ToolCall {
	var got *agentapi.ToolCall
	for _, e := range th.sink.all() {
		if e.Kind == agentapi.EventItem && e.Item.ID == id && e.Item.Tool != nil {
			got = e.Item.Tool
		}
	}
	return got
}

func TestAimockPlainTurn(t *testing.T) {
	requireAimockE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const text = "Plain reply from the mock model, streamed in several chunks."
	mock := startAimock(t, []mockFixture{reply("say plain", text)})
	th := openAimock(t, ctx, mock, agentapi.OpenRequest{})
	turn := th.send(t, ctx, "say plain")
	if turn.State != agentapi.TurnCompleted || turn.Model != aimockModel.SelectionID() {
		t.Fatalf("turn = %+v, want completed on %s", turn, aimockModel.SelectionID())
	}
	if items, deltas, chunks := replies(th.sink.all()); items != text || deltas != text || chunks < 2 {
		t.Fatalf("reply item %q, %d deltas %q; want %q streamed", items, chunks, deltas, text)
	}
	chats := mock.chats(t)
	if len(chats) != 1 || chats[0].Body.Model != aimockModel.ModelID || chats[0].Response.Status != http.StatusOK {
		t.Fatalf("model calls = %+v, want one to %s", chats, aimockModel.ModelID)
	}
}

func TestAimockShellPermission(t *testing.T) {
	for _, tc := range []struct {
		decision string
		state    agentapi.InteractionState
		ran      bool
	}{
		{"approve_once", agentapi.InteractionAnswered, true},
		{"reject", agentapi.InteractionRejected, false},
	} {
		t.Run(tc.decision, func(t *testing.T) {
			requireAimockE2E(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran.txt")
			command := "printf ran > " + marker + " && echo wrote-marker"
			mock := startAimock(t, toolRound("run the shell", "call_shell", "Shell round finished.",
				toolCall("call_shell", "bash", map[string]any{"command": command, "description": "Write the marker file"})))
			th := openAimock(t, ctx, mock, agentapi.OpenRequest{Workdir: dir})
			from := len(th.sink.all())
			if err := th.conv.Send(ctx, agentapi.Prompt{Text: "run the shell"}); err != nil {
				t.Fatal(err)
			}
			perm := th.awaitPermission(t, ctx, from)
			if !strings.Contains(perm.Detail, command) || perm.ToolCallID != "call_shell" {
				t.Fatalf("permission = %+v, want the command of call_shell", perm)
			}
			if err := th.conv.Respond(ctx, perm.ID, agentapi.Answer{Decision: tc.decision}); err != nil {
				t.Fatal(err)
			}
			if turn := th.turnEnd(t, ctx, from); turn.State != agentapi.TurnCompleted {
				t.Fatalf("turn = %+v", turn)
			}
			if got := th.sink.interaction(perm.ID); got.State != tc.state {
				t.Fatalf("permission ended %+v, want %s", got, tc.state)
			}
			_, err := os.Stat(marker)
			if ran := err == nil; ran != tc.ran {
				t.Fatalf("command ran = %v, want %v", ran, tc.ran)
			}
			tool := th.tool("call_shell")
			items, _, _ := replies(th.sink.all())
			calls := len(mock.chats(t))
			if tc.ran {
				if tool == nil || tool.Status != agentapi.ToolCompleted || !strings.Contains(tool.Output, "wrote-marker") || items != "Shell round finished." || calls != 2 {
					t.Fatalf("approved: tool %+v, reply %q, %d model calls", tool, items, calls)
				}
				return
			}
			// The CLI ends the turn on a denial without asking the model again.
			if tool == nil || tool.Status != agentapi.ToolFailed || items != "" || calls != 1 {
				t.Fatalf("denied: tool %+v, reply %q, %d model calls", tool, items, calls)
			}
		})
	}
}

func TestAimockFileEditIsTracked(t *testing.T) {
	requireAimockE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	mock := startAimock(t, toolRound("create the notes", "call_create", "Created the notes.",
		toolCall("call_create", "create", map[string]any{"path": path, "file_text": "first line\nsecond line\n"})))
	th := openAimock(t, ctx, mock, agentapi.OpenRequest{Workdir: dir})
	from := len(th.sink.all())
	if err := th.conv.Send(ctx, agentapi.Prompt{Text: "create the notes"}); err != nil {
		t.Fatal(err)
	}
	perm := th.awaitPermission(t, ctx, from)
	if perm.ToolCallID != "call_create" {
		t.Fatalf("permission = %+v, want call_create's", perm)
	}
	if err := th.conv.Respond(ctx, perm.ID, agentapi.Answer{Decision: "approve_once"}); err != nil {
		t.Fatal(err)
	}
	if turn := th.turnEnd(t, ctx, from); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn = %+v", turn)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "first line\nsecond line\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
	tool := th.tool("call_create")
	if tool == nil || tool.Status != agentapi.ToolCompleted || tool.EditEventID == "" ||
		len(tool.FileEdits) != 1 || tool.FileEdits[0].Path != path || tool.FileEdits[0].Kind != "create" || tool.FileEdits[0].Additions != 2 {
		t.Fatalf("create tool = %+v", tool)
	}
	diffs, err := th.conv.Diff(ctx)
	if err != nil || len(diffs) != 1 || diffs[0].Path != "notes.txt" || diffs[0].Status != "added" ||
		!strings.Contains(diffs[0].Patch, "+first line\n+second line") {
		t.Fatalf("session diff = %+v, %v", diffs, err)
	}
}

func TestAimockHostTools(t *testing.T) {
	requireAimockE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	shown := filepath.Join(dir, "report.md")
	if err := os.WriteFile(shown, []byte("# Report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []agentapi.HostToolCall
	// uam_show_file is the adapter's own tool; uam_echo stands for the web
	// service's host tools (form, surface, ...), which CallTool runs.
	mock := startAimock(t, toolRound("use the uam tools", "call_echo", "Both uam tools answered.",
		toolCall("call_show", declarationToolName, map[string]any{"path": shown, "title": "Report"}),
		toolCall("call_echo", "uam_echo", map[string]any{"word": "teal"})))
	th := openAimock(t, ctx, mock, agentapi.OpenRequest{
		Workdir:      dir,
		ValidateFile: func(_ context.Context, p string) (string, error) { return p, nil },
		Tools: []agentapi.HostTool{{Name: "uam_echo", Description: "Echo a word.", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"word": map[string]any{"type": "string"}}, "required": []string{"word"},
		}}},
		CallTool: func(_ context.Context, call agentapi.HostToolCall) agentapi.HostToolResult {
			mu.Lock()
			calls = append(calls, call)
			mu.Unlock()
			return agentapi.HostToolResult{Text: "echo-result-teal"}
		},
	})
	if turn := th.send(t, ctx, "use the uam tools"); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn = %+v", turn)
	}
	if show := th.tool("call_show"); show == nil || show.Status != agentapi.ToolCompleted || show.Declaration == nil ||
		show.Declaration.Path != shown || show.Declaration.Title != "Report" {
		t.Fatalf("uam_show_file = %+v", show)
	}
	mu.Lock()
	got := slices.Clone(calls)
	mu.Unlock()
	if len(got) != 1 || got[0].Name != "uam_echo" || got[0].CallID != "call_echo" || got[0].TaskID == "" || string(got[0].Arguments) != `{"word":"teal"}` {
		t.Fatalf("CallTool calls = %+v", got)
	}
	if echo := th.tool("call_echo"); echo == nil || echo.Status != agentapi.ToolCompleted || echo.Output != "echo-result-teal" {
		t.Fatalf("uam_echo = %+v", echo)
	}
	// Both results reached the model.
	chats := mock.chats(t)
	if len(chats) != 2 {
		t.Fatalf("model calls = %d, want 2", len(chats))
	}
	results := map[string]string{}
	for _, m := range chats[1].Body.Messages {
		if m["role"] == "tool" {
			id, _ := m["tool_call_id"].(string)
			results[id], _ = m["content"].(string)
		}
	}
	if !strings.Contains(results["call_show"], shown) || results["call_echo"] != "echo-result-teal" {
		t.Fatalf("tool results sent to the model = %q", results)
	}
	if items, _, _ := replies(th.sink.all()); items != "Both uam tools answered." {
		t.Fatalf("final reply = %q", items)
	}
}

func TestAimockResume(t *testing.T) {
	requireAimockE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	mock := startAimock(t, []mockFixture{reply("remember amber", "Amber noted."), reply("what colour", "It was amber.")})
	th := openAimock(t, ctx, mock, agentapi.OpenRequest{})
	if turn := th.send(t, ctx, "remember amber"); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn = %+v", turn)
	}
	id := th.conv.ID()
	if err := th.conv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened := aimockThread{p: th.p, sink: &recSink{}, dir: th.dir}
	conv, err := th.p.Open(ctx, agentapi.OpenRequest{SessionID: uuid.NewString(), ConversationID: id, Workdir: th.dir, Events: reopened.sink})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened.conv = conv
	h, err := conv.History(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range h.Items {
		got = append(got, string(it.Kind)+": "+it.Text)
	}
	if want := []string{"user: remember amber", "assistant: Amber noted."}; !slices.Equal(got, want) || h.Model != aimockModel.SelectionID() {
		t.Fatalf("history = %q on %q, want %q on %s", got, h.Model, want, aimockModel.SelectionID())
	}
	// The reopened conversation sends the earlier turn to the model.
	if turn := reopened.send(t, ctx, "what colour"); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn after reopen = %+v", turn)
	}
	chats := mock.chats(t)
	if len(chats) != 2 {
		t.Fatalf("model calls = %d, want 2", len(chats))
	}
	earlier := false
	for _, m := range chats[1].Body.Messages {
		earlier = earlier || m["role"] == "assistant" && m["content"] == "Amber noted."
	}
	if !earlier {
		t.Fatalf("the model call after reopen lacks the earlier reply: %+v", chats[1].Body.Messages)
	}
}

func TestAimockCancelMidTurn(t *testing.T) {
	requireAimockE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Streaming the slow reply would take over a minute.
	slow := reply("slow answer", strings.Repeat("slow words ", 50))
	slow["latency"], slow["chunkSize"] = 200, 2
	mock := startAimock(t, []mockFixture{slow, reply("quick answer", "Quick.")})
	th := openAimock(t, ctx, mock, agentapi.OpenRequest{})
	from := len(th.sink.all())
	if err := th.conv.Send(ctx, agentapi.Prompt{Text: "slow answer"}); err != nil {
		t.Fatal(err)
	}
	awaitEvent(t, ctx, th.sink, from, "the slow reply to stream", func(e agentapi.Event) bool { return e.Kind == agentapi.EventDelta })
	if err := th.conv.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if turn := th.turnEnd(t, ctx, from); turn.State != agentapi.TurnCancelled {
		t.Fatalf("cancelled turn = %+v", turn)
	}
	if turn := th.send(t, ctx, "quick answer"); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn after cancel = %+v", turn)
	}
	if items, _, _ := replies(th.sink.all()); !strings.HasSuffix(items, "Quick.") {
		t.Fatalf("replies = %q", items)
	}
}

func TestAimockUpstreamFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture mockFixture
		status  int
		err     string
	}{
		{"500", mockFixture{"response": map[string]any{"error": map[string]any{"message": "mock outage", "type": "server_error"}, "status": 500}}, 500, "mock outage"},
		{"429", mockFixture{"response": map[string]any{"error": map[string]any{"message": "mock rate limit", "type": "rate_limit_error"}, "status": 429}}, 429, "mock rate limit"},
		// The stream ends after two chunks, without its finish.
		{"disconnect", mockFixture{"response": map[string]any{"content": strings.Repeat("cut ", 50)}, "truncateAfterChunks": 2}, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireAimockE2E(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			tc.fixture["match"] = map[string]any{"userMessage": "break please"}
			mock := startAimock(t, []mockFixture{tc.fixture, reply("recover please", "Recovered.")})
			th := openAimock(t, ctx, mock, agentapi.OpenRequest{})
			turn := th.send(t, ctx, "break please")
			if turn.State != agentapi.TurnFailed || turn.Error == "" || !strings.Contains(turn.Error, tc.err) {
				t.Fatalf("turn = %+v, want failed with %q", turn, tc.err)
			}
			failed := mock.chats(t)
			if len(failed) == 0 {
				t.Fatal("the turn failed without a model call")
			}
			for _, c := range failed {
				if c.Response.Status != tc.status {
					t.Fatalf("model call answered %d, want %d", c.Response.Status, tc.status)
				}
			}
			if turn := th.send(t, ctx, "recover please"); turn.State != agentapi.TurnCompleted {
				t.Fatalf("turn after the failure = %+v", turn)
			}
			if chats := mock.chats(t); len(chats) != len(failed)+1 || chats[len(failed)].Response.Status != http.StatusOK {
				t.Fatalf("model calls after recovery = %+v", chats[len(failed):])
			}
		})
	}
}
