package copilot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// hostCalls records every CallTool call; reply decides the result.
type hostCalls struct {
	mu    sync.Mutex
	calls []agentapi.HostToolCall
	reply func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult
}

func (h *hostCalls) call(ctx context.Context, c agentapi.HostToolCall) agentapi.HostToolResult {
	h.mu.Lock()
	h.calls = append(h.calls, c)
	reply := h.reply
	h.mu.Unlock()
	if reply != nil {
		return reply(ctx, c)
	}
	return agentapi.HostToolResult{Text: "ok " + c.Name, Payload: []byte(`{"entry":1}`)}
}

func (h *hostCalls) recorded() []agentapi.HostToolCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

func notesTools() []agentapi.HostTool {
	object := func(property string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false,
			"properties": map[string]any{property: map[string]any{"type": "string"}}}
	}
	return []agentapi.HostTool{
		{Name: "notes_get", Description: "Read one note", Parameters: object("ref")},
		{Name: "notes_list", Description: "List notes", Parameters: object("query")},
	}
}

func catalogOf(names ...string) []rpc.CurrentToolMetadata {
	out := make([]rpc.CurrentToolMetadata, 0, len(names))
	for _, name := range names {
		out = append(out, rpc.CurrentToolMetadata{Name: name})
	}
	return out
}

func toolNames(tools []copilot.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}

func callTool(tool copilot.Tool, session, callID string, args any) (copilot.ToolResult, error) {
	return tool.Handler(copilot.ToolInvocation{SessionID: session, ToolCallID: callID, ToolName: tool.Name, Arguments: args, TraceContext: context.Background()})
}

// readyHostTools returns the sample tools with their catalog verified for
// the session "session".
func readyHostTools(t *testing.T, call func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult) (*toolGate, []copilot.Tool) {
	t.Helper()
	gate, tools, err := sessionTools("task", nil, notesTools(), call)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.catalog(context.Background(), &fakeSession{id: "session", catalog: catalogOf("notes_get", "notes_list")}, tools...); err != nil {
		t.Fatal(err)
	}
	return gate, tools
}

// Two host tools register beside uam_show_file on create and resume, are
// verified together and forward their calls; a tool list change and Close
// disable all of them.
func TestHostToolsRegisterBesideDeclarationAndAnswer(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "resume"}[resume], func(t *testing.T) {
			fc := &fakeClient{catalog: catalogOf(declarationToolName, "notes_get", "notes_list")}
			p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			calls := &hostCalls{}
			defs := notesTools()
			req := agentapi.OpenRequest{SessionID: "session", Workdir: t.TempDir(), Events: &recSink{}, Tools: defs, CallTool: calls.call,
				ValidateFile: func(_ context.Context, p string) (string, error) { return filepath.Join("/tmp", p), nil }}
			if resume {
				req.ConversationID = "session"
			}
			conv, err := p.Open(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			var registered []copilot.Tool
			if resume {
				registered = fc.resume[0].Tools
			} else {
				registered = fc.create[0].Tools
			}
			if got := toolNames(registered); !slices.Equal(got, []string{declarationToolName, "notes_get", "notes_list"}) {
				t.Fatalf("registered tools = %v", got)
			}
			for _, tool := range registered[1:] {
				if !tool.SkipPermission || tool.Defer != copilot.ToolDeferNever || tool.IsTerminal || tool.OverridesBuiltInTool || tool.Parameters["additionalProperties"] != false || tool.Description == "" {
					t.Fatalf("host tool %+v", tool)
				}
			}
			// The registered schema is a copy of the web service's.
			defs[0].Parameters["additionalProperties"] = true
			if registered[1].Parameters["additionalProperties"] != false {
				t.Fatal("the registered schema follows the caller's map")
			}
			fs := fc.sessions[0]
			if !slices.Equal(fs.toolCalls, []string{"clear", "catalog", "restore", "catalog"}) || len(fs.setTools[1]) != 3 {
				t.Fatalf("catalog calls %v, restored %+v", fs.toolCalls, fs.setTools)
			}
			for _, definition := range fs.setTools[1] {
				if definition.SkipPermission == nil || !*definition.SkipPermission || definition.Defer == nil || *definition.Defer != rpc.ProtocolExternalToolDeferNever {
					t.Fatalf("restored definition %+v", definition)
				}
			}

			result, err := callTool(registered[1], "session", "call-1", map[string]any{"ref": "#12"})
			if err != nil || result.ResultType != "success" || result.TextResultForLLM != "ok notes_get" || result.SessionLog != result.TextResultForLLM {
				t.Fatalf("notes_get = %+v, %v", result, err)
			}
			got := calls.recorded()
			if len(got) != 1 || got[0].Name != "notes_get" || got[0].CallID != "call-1" || got[0].TaskID != "session" || got[0].AgentID != "" || string(got[0].Arguments) != `{"ref":"#12"}` {
				t.Fatalf("forwarded calls = %+v", got)
			}
			if strings.Contains(result.TextResultForLLM, "entry") {
				t.Fatal("the payload reached the model")
			}
			if _, err := invokeFile(registered[0], "show", map[string]any{"path": "report.txt"}); err != nil {
				t.Fatalf("uam_show_file beside host tools = %v", err)
			}
			calls.reply = func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult {
				return agentapi.HostToolResult{Text: "no note #99", Failed: true}
			}
			if result, err := callTool(registered[2], "session", "call-2", map[string]any{"query": "x"}); err != nil || result.ResultType != "failure" || result.TextResultForLLM != "no note #99" {
				t.Fatalf("failed call = %+v, %v", result, err)
			}
			calls.reply = func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult { return agentapi.HostToolResult{} }
			if result, err := callTool(registered[2], "session", "call-3", nil); err != nil || result.TextResultForLLM == "" || string(calls.recorded()[2].Arguments) != "{}" {
				t.Fatalf("empty result or arguments = %+v, %v", result, err)
			}

			fs.onEvent(ev("changed", &rpc.MCPToolsListChangedData{}))
			if _, err := callTool(registered[1], "session", "after-change", map[string]any{}); err == nil || !strings.Contains(err.Error(), "unavailable") {
				t.Fatalf("host tool after a tool list change = %v", err)
			}
			if _, err := invokeFile(registered[0], "after-change", map[string]any{"path": "report.txt"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
				t.Fatalf("uam_show_file after a tool list change = %v", err)
			}
			if err := conv.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n := len(calls.recorded()); n != 3 {
				t.Fatalf("refused calls reached CallTool: %d calls", n)
			}
		})
	}
}

// Open fails, and leaves no session connected, unless every uam tool is
// uam's own in the catalog.
func TestHostToolCatalogRefusals(t *testing.T) {
	server, name := "notes", "notes_list"
	mcpNotes := rpc.CurrentToolMetadata{Name: "notes_list", MCPServerName: &server, MCPToolName: &name}
	own := catalogOf(declarationToolName, "notes_get", "notes_list")
	for label, tc := range map[string]struct {
		catalogs []fakeToolCatalog
		want     string
	}{
		"name clash":    {[]fakeToolCatalog{{tools: catalogOf("notes_list")}, {tools: catalogOf("notes_list")}}, "notes_list already exists in the unshadowed tool catalog"},
		"from MCP":      {[]fakeToolCatalog{{tools: []rpc.CurrentToolMetadata{}}, {tools: append(catalogOf(declarationToolName, "notes_get"), mcpNotes)}}, "notes_list has ambiguous tool origin"},
		"one missing":   {[]fakeToolCatalog{{tools: []rpc.CurrentToolMetadata{}}, {tools: own[:2]}}, "changed or is ambiguous"},
		"one repeated":  {[]fakeToolCatalog{{tools: []rpc.CurrentToolMetadata{}}, {tools: append(slices.Clone(own), own[1])}}, "changed or is ambiguous"},
		"other changed": {[]fakeToolCatalog{{tools: []rpc.CurrentToolMetadata{}}, {tools: append(slices.Clone(own), rpc.CurrentToolMetadata{Name: "bash"})}}, "changed or is ambiguous"},
	} {
		t.Run(label, func(t *testing.T) {
			fc := &fakeClient{toolCatalogs: tc.catalogs}
			p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			calls := &hostCalls{}
			conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "session", Workdir: t.TempDir(), Events: &recSink{}, Tools: notesTools(), CallTool: calls.call,
				ValidateFile: func(context.Context, string) (string, error) { return "/tmp/report.txt", nil }})
			if err == nil || conv != nil || !strings.Contains(err.Error(), tc.want) || !fc.sessions[0].disconnected {
				t.Fatalf("open = %v, %v; calls %v", conv, err, fc.sessions[0].toolCalls)
			}
			if _, err := callTool(fc.create[0].Tools[1], "session", "call", map[string]any{}); err == nil || len(calls.recorded()) != 0 {
				t.Fatalf("an unverified host tool answered: %v", err)
			}
		})
	}
}

// Bad definitions fail before any session is created.
func TestHostToolDefinitionRefusals(t *testing.T) {
	calls := &hostCalls{}
	good := notesTools()
	for label, tc := range map[string]struct {
		tools []agentapi.HostTool
		call  func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult
	}{
		"no CallTool":           {good, nil},
		"bad name":              {[]agentapi.HostTool{{Name: "notes get"}}, calls.call},
		"repeated name":         {[]agentapi.HostTool{good[0], good[0]}, calls.call},
		"declaration name":      {[]agentapi.HostTool{{Name: declarationToolName}}, calls.call},
		"unencodable parameter": {[]agentapi.HostTool{{Name: "notes_get", Parameters: map[string]any{"x": func() {}}}}, calls.call},
	} {
		fc := &fakeClient{}
		p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
		if _, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "session", Events: &recSink{}, Tools: tc.tools, CallTool: tc.call}); err == nil || len(fc.create) != 0 {
			t.Errorf("%s: open = %v, sessions %d", label, err, len(fc.create))
		}
		if _, err := p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Purpose: "summary", Tools: tc.tools, CallTool: tc.call}); err == nil || len(fc.create) != 0 {
			t.Errorf("%s: utility run = %v, sessions %d", label, err, len(fc.create))
		}
		_ = p.Shutdown(context.Background())
	}
	// A tool without parameters is registered without a schema.
	gate, tools, err := sessionTools("task", nil, []agentapi.HostTool{{Name: "notes_ping"}}, calls.call)
	if err != nil || gate == nil || len(tools) != 1 || tools[0].Parameters != nil {
		t.Fatalf("tool without parameters = %+v, %v", tools, err)
	}
	gate.stop()
	if gate, tools, err := sessionTools("task", nil, nil, nil); err != nil || gate != nil || tools != nil {
		t.Fatalf("no tools = %v, %v, %v", gate, tools, err)
	}
}

// Malformed, oversized, unidentified or foreign calls never reach CallTool.
func TestHostToolHandlerGuards(t *testing.T) {
	calls := &hostCalls{}
	gate, tools := readyHostTools(t, calls.call)
	defer gate.stop()
	get := tools[0]
	for label, inv := range map[string]copilot.ToolInvocation{
		"not an object":  {SessionID: "session", ToolCallID: "a", Arguments: "ref"},
		"too large":      {SessionID: "session", ToolCallID: "b", Arguments: map[string]any{"ref": strings.Repeat("x", agentapi.MaxHostToolArguments)}},
		"unencodable":    {SessionID: "session", ToolCallID: "c", Arguments: map[string]any{"ref": func() {}}},
		"no call id":     {SessionID: "session", Arguments: map[string]any{}},
		"long call id":   {SessionID: "session", ToolCallID: strings.Repeat("i", 257), Arguments: map[string]any{}},
		"no session":     {ToolCallID: "d", Arguments: map[string]any{}},
		"other session":  {SessionID: "other", ToolCallID: "e", Arguments: map[string]any{}},
		"untraced other": {SessionID: "other", ToolCallID: "f"},
	} {
		if result, err := get.Handler(inv); err == nil || result.TextResultForLLM != "" {
			t.Errorf("%s: %+v, %v", label, result, err)
		}
	}
	if _, err := get.Handler(copilot.ToolInvocation{SessionID: "session", ToolCallID: "big", Arguments: map[string]any{"ref": strings.Repeat("x", agentapi.MaxHostToolArguments)}}); err == nil || !strings.Contains(err.Error(), fmt.Sprint(agentapi.MaxHostToolArguments)) {
		t.Fatalf("oversized error = %v", err)
	}
	if n := len(calls.recorded()); n != 0 {
		t.Fatalf("refused calls reached CallTool %d times", n)
	}
	// Tools answer only once their catalog is verified.
	_, unverified, err := sessionTools("task", nil, notesTools(), calls.call)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(unverified[0], "session", "early", map[string]any{}); err == nil || len(calls.recorded()) != 0 {
		t.Fatalf("an unverified tool answered: %v", err)
	}
}

// A repeated call ID gets the first call's result without a second CallTool;
// the same ID with other arguments or another tool is refused.
func TestHostToolRepeatedCallReturnsTheFirstResult(t *testing.T) {
	calls := &hostCalls{}
	n := 0
	calls.reply = func(_ context.Context, c agentapi.HostToolCall) agentapi.HostToolResult {
		n++
		return agentapi.HostToolResult{Text: fmt.Sprintf("%s result %d", c.Name, n)}
	}
	gate, tools := readyHostTools(t, calls.call)
	defer gate.stop()
	first, err := callTool(tools[0], "session", "same", map[string]any{"ref": "#1"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := callTool(tools[0], "session", "same", map[string]any{"ref": "#1"})
	if err != nil || again.TextResultForLLM != first.TextResultForLLM || again.ResultType != "success" || len(calls.recorded()) != 1 {
		t.Fatalf("repeat = %+v, %v; calls %d", again, err, len(calls.recorded()))
	}
	if _, err := callTool(tools[0], "session", "same", map[string]any{"ref": "#2"}); err == nil || !strings.Contains(err.Error(), "different arguments") {
		t.Fatalf("changed arguments = %v", err)
	}
	if _, err := callTool(tools[1], "session", "same", map[string]any{"ref": "#1"}); err == nil {
		t.Fatal("another tool reused the call ID")
	}
	// A repeat waiting for the first stops waiting when cancelled.
	entered, release := make(chan struct{}), make(chan struct{})
	calls.reply = func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult {
		close(entered)
		<-release
		return agentapi.HostToolResult{Text: "slow"}
	}
	done := make(chan error, 1)
	go func() { _, err := callTool(tools[1], "session", "slow", map[string]any{}); done <- err }()
	<-entered
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tools[1].Handler(copilot.ToolInvocation{SessionID: "session", ToolCallID: "slow", Arguments: map[string]any{}, TraceContext: cancelled}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled repeat = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first call = %v", err)
	}
}

// Stop cancels a running call and a repeat waiting for it, promptly; later
// calls are refused.
func TestHostToolStopCancelsActiveAndRepeatedCalls(t *testing.T) {
	entered := make(chan struct{})
	calls := &hostCalls{reply: func(ctx context.Context, _ agentapi.HostToolCall) agentapi.HostToolResult {
		close(entered)
		<-ctx.Done()
		return agentapi.HostToolResult{Text: "stopped: " + ctx.Err().Error(), Failed: true}
	}}
	gate, tools := readyHostTools(t, calls.call)
	results := make(chan copilot.ToolResult, 2)
	go func() { r, _ := callTool(tools[0], "session", "same", map[string]any{}); results <- r }()
	<-entered
	go func() { r, _ := callTool(tools[0], "session", "same", map[string]any{}); results <- r }()
	gate.stop()
	for range 2 {
		select {
		case r := <-results:
			if r.ResultType == "success" {
				t.Fatalf("a stopped call succeeded: %+v", r)
			}
		case <-time.After(time.Second):
			t.Fatal("a stopped call did not return")
		}
	}
	if _, err := callTool(tools[1], "session", "later", map[string]any{}); err == nil || len(calls.recorded()) != 1 {
		t.Fatalf("call after stop = %v", err)
	}
}

// The session remembers a bounded number of calls, forgetting the oldest
// finished one; when every remembered call still runs, a new one is refused.
func TestHostToolCallsForgetTheOldestFinished(t *testing.T) {
	calls := &hostCalls{}
	gate, tools := readyHostTools(t, calls.call)
	defer gate.stop()
	for i := range maxHostToolCalls + 1 {
		if _, err := callTool(tools[0], "session", fmt.Sprint("call-", i), map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := callTool(tools[0], "session", "call-0", map[string]any{}); err != nil || len(calls.recorded()) != maxHostToolCalls+2 {
		t.Fatalf("forgotten call = %v; calls %d", err, len(calls.recorded()))
	}
	h := &hostTools{calls: map[string]*hostToolCall{}}
	for i := range maxHostToolCalls {
		key := fmt.Sprint(i)
		h.calls[key], h.order = &hostToolCall{done: make(chan struct{})}, append(h.order, key)
	}
	if h.makeRoomLocked() {
		t.Fatal("room made while every call runs")
	}
	close(h.calls["5"].done)
	if !h.makeRoomLocked() || h.calls["5"] != nil || len(h.order) != maxHostToolCalls-1 || slices.Contains(h.order, "5") {
		t.Fatalf("finished call kept: %d remembered", len(h.order))
	}
}

// A Utility run registers only its host tools, verifies them, answers the
// model's calls without a TaskID and deletes the session; titles afterwards
// still run without tools.
func TestRunUtilityRegistersOnlyItsHostTools(t *testing.T) {
	calls := &hostCalls{}
	fc := &fakeClient{catalog: catalogOf("notes_get", "notes_list"), models: []rpc.Model{{ID: "gpt-6-luna", SupportedReasoningEfforts: []string{"none", "low"}}}}
	var deadline bool
	fc.reply = func(ctx context.Context, msg copilot.MessageOptions) (string, error) {
		_, deadline = ctx.Deadline()
		cfg := fc.create[len(fc.create)-1]
		if len(cfg.Tools) == 0 {
			return "A title", nil
		}
		result, err := cfg.Tools[0].Handler(copilot.ToolInvocation{SessionID: "created-1", ToolCallID: "call-1", Arguments: map[string]any{"ref": "#3"}, TraceContext: ctx})
		if err != nil {
			return "", err
		}
		return msg.Prompt + " after " + result.TextResultForLLM, nil
	}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	var _ agentapi.UtilityRunner = p
	if !p.Capabilities().HostTools {
		t.Fatal("copilot does not report host tools")
	}
	reply, err := p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Workdir: "/work", Purpose: "summary",
		System: "Summarize the notes.", Prompt: "Split it", Tools: notesTools(), CallTool: calls.call, Timeout: time.Minute})
	if err != nil || reply != "Split it after ok notes_get" || !deadline {
		t.Fatalf("reply = %q, %v; deadline %v", reply, err, deadline)
	}
	if got := calls.recorded(); len(got) != 1 || got[0].Name != "notes_get" || got[0].CallID != "call-1" || got[0].TaskID != "" || string(got[0].Arguments) != `{"ref":"#3"}` {
		t.Fatalf("utility calls = %+v", got)
	}
	cfg := fc.create[0]
	off := func(b *bool) bool { return b != nil && !*b }
	if cfg.ClientName != "uam-summary" || cfg.Model != "gpt-6-luna" || cfg.ReasoningEffort != "none" || cfg.WorkingDirectory != "/work" || cfg.SessionID != "" || cfg.OnEvent == nil {
		t.Fatalf("utility session = %+v", cfg)
	}
	if !slices.Equal(cfg.AvailableTools, []string{"notes_get", "notes_list"}) || !slices.Equal(toolNames(cfg.Tools), cfg.AvailableTools) {
		t.Fatalf("available %v, tools %v", cfg.AvailableTools, toolNames(cfg.Tools))
	}
	if !off(cfg.EnableConfigDiscovery) || !off(cfg.EnableSessionStore) || !off(cfg.EnableSkills) || !off(cfg.EnableFileHooks) || !off(cfg.EnableOnDemandInstructionDiscovery) ||
		cfg.SystemMessage == nil || cfg.SystemMessage.Mode != "replace" || cfg.SystemMessage.Content != "Summarize the notes." {
		t.Fatalf("utility configuration = %+v", cfg)
	}
	if d, err := cfg.OnPermissionRequest(&rpc.PermissionRequestShell{FullCommandText: "ls"}, copilot.PermissionInvocation{}); err != nil {
		t.Fatal(err)
	} else if _, ok := d.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("permission decision = %T", d)
	}
	fs := fc.sessions[0]
	if !slices.Equal(fs.toolCalls, []string{"clear", "catalog", "restore", "catalog", "disconnect"}) || !slices.Equal(fc.deleted, []string{"created-1"}) {
		t.Fatalf("tool calls %v, deleted %v", fs.toolCalls, fc.deleted)
	}
	if _, err := callTool(cfg.Tools[1], "created-1", "late", map[string]any{}); err == nil || len(calls.recorded()) != 1 {
		t.Fatalf("a tool answered after its run: %v", err)
	}

	if title, err := p.Title(context.Background(), agentapi.TitleRequest{Model: "gpt-6-luna", Text: "x"}); err != nil || title != "A title" {
		t.Fatalf("title = %q, %v", title, err)
	}
	if cfg := fc.create[1]; cfg.Tools != nil || cfg.AvailableTools == nil || len(cfg.AvailableTools) != 0 || cfg.OnEvent != nil || len(fc.sessions[1].toolCalls) != 1 {
		t.Fatalf("title session tools %v / %v, calls %v", cfg.Tools, cfg.AvailableTools, fc.sessions[1].toolCalls)
	}
}

// A Utility run whose tools fail verification sends nothing and still
// deletes its session.
func TestRunUtilityRefusesAClashingTool(t *testing.T) {
	calls := &hostCalls{}
	clash := fakeToolCatalog{tools: catalogOf("notes_get")}
	fc := &fakeClient{toolCatalogs: []fakeToolCatalog{clash, clash}, reply: func(context.Context, copilot.MessageOptions) (string, error) { return "sent", nil }}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	_, err := p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Purpose: "summary", Prompt: "Split it", Tools: notesTools(), CallTool: calls.call})
	if err == nil || !strings.Contains(err.Error(), "notes_get already exists") || len(fc.sessions[0].sent) != 0 || !slices.Equal(fc.deleted, []string{"created-1"}) {
		t.Fatalf("clash = %v; sent %v, deleted %v", err, fc.sessions[0].sent, fc.deleted)
	}
	fc.createErr = errors.New("no session")
	if _, err := p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Purpose: "summary", Tools: notesTools(), CallTool: calls.call}); err == nil || len(fc.deleted) != 1 {
		t.Fatalf("failed create = %v, deleted %v", err, fc.deleted)
	}
}
