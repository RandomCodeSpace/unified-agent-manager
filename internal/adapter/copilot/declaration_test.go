package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func readyDeclaration(t *testing.T, validate func(context.Context, string) (string, error)) (*declarationTool, copilot.Tool) {
	t.Helper()
	d := newDeclarationTool(validate)
	tool := d.tool()
	if err := d.catalog(context.Background(), &fakeSession{id: "session", catalog: []rpc.CurrentToolMetadata{{Name: declarationToolName}}}, tool); err != nil {
		t.Fatal(err)
	}
	return d, tool
}

func invokeFile(tool copilot.Tool, callID string, args map[string]any) (copilot.ToolResult, error) {
	return tool.Handler(copilot.ToolInvocation{SessionID: "session", ToolCallID: callID, Arguments: args, TraceContext: context.Background()})
}

func TestDeclarationHandlerMetadataCorrectionAndBounds(t *testing.T) {
	workdir := t.TempDir()
	file := filepath.Join(workdir, "report.txt")
	d, tool := readyDeclaration(t, func(_ context.Context, path string) (string, error) {
		if path != "report.txt" {
			return "", errors.New("missing")
		}
		return file, nil
	})
	defer d.stop()
	if _, err := invokeFile(tool, "bad", map[string]any{"path": "missing.txt"}); err == nil || !strings.Contains(err.Error(), "correct the path") {
		t.Fatalf("missing path error = %v", err)
	}
	result, err := invokeFile(tool, "good", map[string]any{"path": "report.txt", "title": "Report", "type_hint": "text"})
	if err != nil || result.ResultType != "success" || result.SessionLog != result.TextResultForLLM || len(result.TextResultForLLM) > maxDeclarationJSON {
		t.Fatalf("result = %+v, %v", result, err)
	}
	var got agentapi.FileDeclaration
	if err := json.Unmarshal([]byte(result.TextResultForLLM), &got); err != nil || got.Path != file || got.Title != "Report" || got.TypeHint != "text" || got.ArtifactID == "" {
		t.Fatalf("metadata = %+v, %v", got, err)
	}
	if duplicate, err := invokeFile(tool, "good", map[string]any{"path": "report.txt", "title": "Report", "type_hint": "text"}); err != nil || duplicate.TextResultForLLM != result.TextResultForLLM {
		t.Fatalf("duplicate = %+v, %v", duplicate, err)
	}
	for _, args := range []map[string]any{{"path": strings.Repeat("p", maxDeclarationPath+1)}, {"path": "report.txt", "title": "x\n"}, {"path": "report.txt", "extra": "x"}} {
		if result, err := invokeFile(tool, "invalid", args); err == nil || result.TextResultForLLM != "" {
			t.Fatalf("invalid args accepted: %+v, %v", result, err)
		}
	}
	if result, err := invokeFile(tool, "good", map[string]any{"path": "missing.txt"}); err == nil || result.TextResultForLLM != "" {
		t.Fatalf("changed duplicate accepted: %+v, %v", result, err)
	}
	if parsed := parseDeclarationResult(result.TextResultForLLM); parsed == nil || *parsed != got {
		t.Fatalf("parse = %+v", parsed)
	}
	for _, raw := range []string{`{"artifact_id":"x","path":"relative"}`, `{"artifact_id":"x","path":"/tmp/a","url":"secret"}`, `{"artifact_id":"x","path":"/tmp/a","title":"x\n"}`, strings.Repeat("x", maxDeclarationJSON+1)} {
		if parseDeclarationResult(raw) != nil {
			t.Fatalf("accepted malformed result %q", raw)
		}
	}
	for i := 0; i < maxDeclarationSuccess-1; i++ {
		if _, err := invokeFile(tool, fmt.Sprintf("more-%d", i), map[string]any{"path": "report.txt"}); err != nil {
			t.Fatalf("success cap entry %d: %v", i, err)
		}
	}
	if _, err := invokeFile(tool, "over-cap", map[string]any{"path": "report.txt"}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("success limit = %v", err)
	}
}

func TestDeclarationEncodedResultFitsEscapedMaximumPath(t *testing.T) {
	path := "/" + strings.Repeat("<", maxDeclarationPath-1)
	d, tool := readyDeclaration(t, func(context.Context, string) (string, error) { return path, nil })
	defer d.stop()
	result, err := invokeFile(tool, "escaped", map[string]any{"path": "report.txt", "title": strings.Repeat("<", maxDeclarationTitle), "type_hint": strings.Repeat("<", maxDeclarationType)})
	if err != nil || len(result.TextResultForLLM) > maxDeclarationJSON || parseDeclarationResult(result.TextResultForLLM) == nil {
		t.Fatalf("escaped maximum result = %d bytes, %v", len(result.TextResultForLLM), err)
	}
}

func TestDeclarationDuplicateConcurrentInvocationUsesOneValidation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var validations atomic.Int32
	d, tool := readyDeclaration(t, func(context.Context, string) (string, error) {
		validations.Add(1)
		close(entered)
		<-release
		return "/tmp/owned-report.txt", nil
	})
	defer d.stop()
	type answer struct {
		result copilot.ToolResult
		err    error
	}
	answers := make(chan answer, 2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := invokeFile(tool, "same", map[string]any{"path": "owned-report.txt"})
		answers <- answer{result, err}
	}()
	<-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := invokeFile(tool, "same", map[string]any{"path": "owned-report.txt"})
		answers <- answer{result, err}
	}()
	close(release)
	wg.Wait()
	a, b := <-answers, <-answers
	if a.err != nil || b.err != nil || a.result.TextResultForLLM != b.result.TextResultForLLM || validations.Load() != 1 || d.successes != 1 || len(d.calls) != 1 {
		t.Fatalf("duplicate results %v %v; validations=%d successes=%d calls=%d", a.err, b.err, validations.Load(), d.successes, len(d.calls))
	}
}

func TestDeclarationCloseCancelsActiveAndDuplicateCalls(t *testing.T) {
	entered := make(chan struct{})
	d, tool := readyDeclaration(t, func(ctx context.Context, _ string) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	})
	errorsOut := make(chan error, 2)
	go func() { _, err := invokeFile(tool, "same", map[string]any{"path": "report.txt"}); errorsOut <- err }()
	<-entered
	go func() { _, err := invokeFile(tool, "same", map[string]any{"path": "report.txt"}); errorsOut <- err }()
	d.stop()
	for range 2 {
		select {
		case err := <-errorsOut:
			if err == nil {
				t.Fatal("closed declaration call succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("closed declaration call did not settle")
		}
	}
}

type changingDeclarationCatalog struct {
	*fakeSession
	declaration *declarationTool
	reads       int
}

func (s *changingDeclarationCatalog) ToolCatalog(ctx context.Context) ([]rpc.CurrentToolMetadata, error) {
	tools, err := s.fakeSession.ToolCatalog(ctx)
	s.reads++
	if s.reads == 1 {
		s.declaration.observe(ev("change", &rpc.MCPToolsListChangedData{}))
	}
	return tools, err
}

func TestDeclarationStartupListChangeInvalidatesCatalog(t *testing.T) {
	d := newDeclarationTool(func(context.Context, string) (string, error) { return "/tmp/report.txt", nil })
	tool := d.tool()
	s := &changingDeclarationCatalog{fakeSession: &fakeSession{id: "session", catalog: []rpc.CurrentToolMetadata{{Name: declarationToolName}}}, declaration: d}
	if err := d.catalog(context.Background(), s, tool); err == nil || len(s.setTools) != 1 {
		t.Fatalf("startup change accepted: %v; tool sets=%d", err, len(s.setTools))
	}
}

func TestDeclarationRegistrationCollisionAndReadiness(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "resume"}[resume], func(t *testing.T) {
			fc := &fakeClient{catalog: []rpc.CurrentToolMetadata{{Name: declarationToolName}}}
			p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			req := agentapi.OpenRequest{SessionID: "session", Workdir: t.TempDir(), Events: &recSink{}, ValidateFile: func(_ context.Context, p string) (string, error) { return filepath.Join("/tmp", p), nil }}
			if resume {
				req.ConversationID = "session"
			}
			conv, err := p.Open(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			fs := fc.sessions[0]
			var tools []copilot.Tool
			if resume {
				tools = fc.resume[0].Tools
			} else {
				tools = fc.create[0].Tools
			}
			if len(tools) != 1 || tools[0].Name != declarationToolName || len(fs.toolCalls) != 4 || fs.disconnected {
				t.Fatalf("registration tools=%+v calls=%v disconnected=%v", tools, fs.toolCalls, fs.disconnected)
			}
			fs.onEvent(ev("changed", &rpc.MCPToolsListChangedData{}))
			if _, err := invokeFile(tools[0], "after-change", map[string]any{"path": "report.txt"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
				t.Fatalf("dynamic tool change accepted: %v", err)
			}
			_ = conv.Close(context.Background())
		})
	}
	fc := &fakeClient{toolCatalogs: []fakeToolCatalog{{tools: []rpc.CurrentToolMetadata{{Name: declarationToolName}}}}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	defer p.Shutdown(context.Background())
	conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "collision", Workdir: t.TempDir(), Events: &recSink{}, ValidateFile: func(context.Context, string) (string, error) { return "/tmp/report.txt", nil }})
	if err == nil || conv != nil || !fc.sessions[0].disconnected || len(fc.sessions[0].setTools) != 1 {
		t.Fatalf("collision = %v, %v; calls=%v", conv, err, fc.sessions[0].toolCalls)
	}
}

func TestDeclarationJournalMapsOnlySuccessfulToolAndChild(t *testing.T) {
	good := `{"artifact_id":"abc","path":"/tmp/report.txt","title":"Report"}`
	events := []copilot.SessionEvent{
		ev("start", &rpc.ToolExecutionStartData{ToolCallID: "call", ToolName: declarationToolName}),
		ev("done", &rpc.ToolExecutionCompleteData{ToolCallID: "call", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: good}}),
		agentEv("child-start", "child", &rpc.ToolExecutionStartData{ToolCallID: "child-call", ToolName: declarationToolName}),
		agentEv("child-done", "child", &rpc.ToolExecutionCompleteData{ToolCallID: "child-call", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: good}}),
		ev("fail-start", &rpc.ToolExecutionStartData{ToolCallID: "failed", ToolName: declarationToolName}),
		ev("fail-done", &rpc.ToolExecutionCompleteData{ToolCallID: "failed", Success: false, Result: &rpc.ToolExecutionCompleteResult{Content: good}}),
		ev("foreign", &rpc.ToolExecutionCompleteData{ToolCallID: "unstarted", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: good}}),
	}
	h := history(events)
	if len(h.Items) != 4 || h.Items[0].Tool.Declaration == nil || h.Items[1].AgentID != "child" || h.Items[1].Tool.Declaration == nil || h.Items[2].Tool.Declaration != nil || h.Items[3].Tool.Declaration != nil {
		t.Fatalf("replay items = %+v", h.Items)
	}
	fc := &fakeClient{journal: events}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	defer p.Shutdown(context.Background())
	read, err := p.ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "session"})
	if err != nil || len(read.Items) != 4 || read.Items[1].Tool.Declaration == nil {
		t.Fatalf("read history = %+v, %v", read, err)
	}
}
