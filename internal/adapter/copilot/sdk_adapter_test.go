package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// fakeRuntime is a Copilot runtime the SDK reaches over TCP. It answers each
// JSON-RPC request with the result or error set for its method ({} when
// unset), sends the session events set for it afterwards, and records every
// request's params.
type fakeRuntime struct {
	addr string

	mu       sync.Mutex
	conns    []net.Conn
	results  map[string]string
	errs     map[string]string
	events   map[string][]copilot.SessionEvent
	requests map[string][]map[string]any
}

func startFakeRuntime(t *testing.T) *fakeRuntime {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	r := &fakeRuntime{
		addr:     ln.Addr().String(),
		results:  map[string]string{"connect": fmt.Sprintf(`{"protocolVersion":%d}`, copilot.SDKProtocolVersion), "session.detach": `{"success":true}`},
		errs:     map[string]string{},
		events:   map[string][]copilot.SessionEvent{},
		requests: map[string][]map[string]any{},
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			r.mu.Lock()
			r.conns = append(r.conns, conn)
			r.mu.Unlock()
			go r.serve(conn)
		}
	}()
	return r
}

func (r *fakeRuntime) set(method, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.errs, method)
	r.results[method] = result
}

func (r *fakeRuntime) fail(method, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs[method] = message
}

// hangUp ends every connection, as a runtime exiting does.
func (r *fakeRuntime) hangUp() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, conn := range r.conns {
		_ = conn.Close()
	}
}

// last returns the params of the latest request for method.
func (r *fakeRuntime) last(method string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	reqs := r.requests[method]
	if len(reqs) == 0 {
		return nil
	}
	return reqs[len(reqs)-1]
}

func (r *fakeRuntime) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	in := bufio.NewReader(conn)
	for {
		body, err := readFrame(in)
		if err != nil {
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(body, &req) != nil || len(req.ID) == 0 {
			continue
		}
		r.mu.Lock()
		r.requests[req.Method] = append(r.requests[req.Method], req.Params)
		reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if msg, ok := r.errs[req.Method]; ok {
			reply["error"] = map[string]any{"code": -32000, "message": msg}
		} else if res, ok := r.results[req.Method]; ok {
			reply["result"] = json.RawMessage(res)
		} else {
			reply["result"] = json.RawMessage(`{}`)
		}
		events := r.events[req.Method]
		r.mu.Unlock()
		if writeFrame(conn, reply) != nil {
			return
		}
		for _, ev := range events {
			note := map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{"sessionId": req.Params["sessionId"], "event": ev}}
			if writeFrame(conn, note) != nil {
				return
			}
		}
	}
}

func readFrame(in *bufio.Reader) ([]byte, error) {
	size := 0
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			if size, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, err
			}
		}
	}
	body := make([]byte, size)
	_, err := io.ReadFull(in, body)
	return body, err
}

func writeFrame(w io.Writer, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

// startFakeSDK connects the SDK client adapter to rt.
func startFakeSDK(t *testing.T, rt *fakeRuntime) sdkClientAdapter {
	t.Helper()
	a := sdkClientAdapter{copilot.NewClient(&copilot.ClientOptions{Connection: copilot.URIConnection{URL: rt.addr}})}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { rt.hangUp(); a.ForceStop() })
	return a
}

// openFakeSDKSession creates session s-1 on rt through the adapters.
func openFakeSDKSession(t *testing.T, rt *fakeRuntime) sdkSessionAdapter {
	t.Helper()
	a := startFakeSDK(t, rt)
	rt.set("session.create", `{"sessionId":"s-1","workspacePath":"/ws"}`)
	s, err := a.CreateSession(context.Background(), &copilot.SessionConfig{SessionID: "s-1", OnPermissionRequest: copilot.PermissionHandler.ApproveAll})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return s.(sdkSessionAdapter)
}

func TestSDKClientAdapterRequests(t *testing.T) {
	rt := startFakeRuntime(t)
	a := startFakeSDK(t, rt)
	ctx := context.Background()

	rt.set("ping", fmt.Sprintf(`{"message":"pong","timestamp":"2026-09-01T00:00:00Z","protocolVersion":%d}`, copilot.SDKProtocolVersion))
	if err := a.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	rt.fail("ping", "down")
	if err := a.Ping(ctx); err == nil {
		t.Fatal("Ping hid a failed ping")
	}

	rt.set("models.list", `{"models":[{"id":"gpt-5","name":"GPT-5","capabilities":{}}]}`)
	if models, err := a.ListModels(ctx); err != nil || len(models) != 1 || models[0].ID != "gpt-5" {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
	rt.fail("models.list", "no models")
	if _, err := a.ListModels(ctx); err == nil {
		t.Fatal("ListModels hid a failure")
	}

	rt.set("account.getQuota", `{"quotaSnapshots":{"premium_interactions":{"remainingPercentage":42}}}`)
	if quota, err := a.Quota(ctx); err != nil || quota["premium_interactions"].RemainingPercentage != 42 {
		t.Fatalf("Quota = %+v, %v", quota, err)
	}
	rt.fail("account.getQuota", "no quota")
	if _, err := a.Quota(ctx); err == nil {
		t.Fatal("Quota hid a failure")
	}

	// A workdir filters the listing by working directory; none lists all.
	rt.set("session.list", `{"sessions":[{"sessionId":"s-9","startTime":"2026-09-01T00:00:00Z","modifiedTime":"2026-09-01T00:00:00Z","isRemote":false}]}`)
	if listed, err := a.ListSessions(ctx, ""); err != nil || len(listed) != 1 || listed[0].SessionID != "s-9" || rt.last("session.list")["filter"] != nil {
		t.Fatalf("ListSessions all = %+v, %v, params %v", listed, err, rt.last("session.list"))
	}
	if _, err := a.ListSessions(ctx, "/work"); err != nil || !reflect.DeepEqual(rt.last("session.list")["filter"], map[string]any{"cwd": "/work"}) {
		t.Fatalf("ListSessions /work err %v, params %v", err, rt.last("session.list"))
	}

	rt.set("sessions.checkInUse", `{"inUse":["s-9"]}`)
	if inUse, err := a.CheckInUse(ctx, []string{"s-9", "s-8"}); err != nil || !reflect.DeepEqual(inUse, []string{"s-9"}) || !reflect.DeepEqual(rt.last("sessions.checkInUse")["sessionIds"], []any{"s-9", "s-8"}) {
		t.Fatalf("CheckInUse = %v, %v", inUse, err)
	}
	rt.set("sessions.readPersistedEvents", `{"events":[],"hasMore":true,"cursor":"c-2","cursorStatus":"ok"}`)
	if res, err := a.ReadEvents(ctx, &rpc.SessionsReadPersistedEventsRequest{SessionID: "s-9"}); err != nil || !res.HasMore || res.Cursor != "c-2" {
		t.Fatalf("ReadEvents = %+v, %v", res, err)
	}

	// Import needs both probes; a missing journal still proves the method.
	if !a.ImportSupported(ctx) {
		t.Fatal("ImportSupported = false with both methods answering")
	}
	rt.fail("sessions.readPersistedEvents", "journal is unavailable for this session")
	if !a.ImportSupported(ctx) {
		t.Fatal("ImportSupported = false for a missing journal")
	}
	rt.fail("sessions.readPersistedEvents", "unknown method")
	if a.ImportSupported(ctx) {
		t.Fatal("ImportSupported = true without journal reads")
	}
	rt.fail("sessions.checkInUse", "unknown method")
	if _, err := a.CheckInUse(ctx, nil); err == nil || a.ImportSupported(ctx) {
		t.Fatalf("CheckInUse err %v; ImportSupported without in-use checks", err)
	}

	rt.set("session.create", `{"sessionId":"s-1"}`)
	if s, err := a.CreateSession(ctx, &copilot.SessionConfig{SessionID: "s-1", OnPermissionRequest: copilot.PermissionHandler.ApproveAll}); err != nil || s.ID() != "s-1" {
		t.Fatalf("CreateSession = %v, %v", s, err)
	}
	rt.fail("session.create", "refused")
	if s, err := a.CreateSession(ctx, &copilot.SessionConfig{SessionID: "s-2", OnPermissionRequest: copilot.PermissionHandler.ApproveAll}); err == nil || s != nil {
		t.Fatalf("failed CreateSession = %v, %v", s, err)
	}
	rt.set("session.resume", `{"sessionId":"s-3"}`)
	if s, err := a.ResumeSession(ctx, "s-3", &copilot.ResumeSessionConfig{OnPermissionRequest: copilot.PermissionHandler.ApproveAll}); err != nil || s.ID() != "s-3" {
		t.Fatalf("ResumeSession = %v, %v", s, err)
	}
	rt.fail("session.resume", "not found")
	if s, err := a.ResumeSession(ctx, "s-4", &copilot.ResumeSessionConfig{OnPermissionRequest: copilot.PermissionHandler.ApproveAll}); err == nil || s != nil {
		t.Fatalf("failed ResumeSession = %v, %v", s, err)
	}
	rt.set("session.delete", `{"success":true}`)
	if err := a.DeleteSession(ctx, "s-3"); err != nil || rt.last("session.delete")["sessionId"] != "s-3" {
		t.Fatalf("DeleteSession err %v, params %v", err, rt.last("session.delete"))
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestSDKSessionAdapterRequests(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	ctx := context.Background()
	var rejected rejectedError

	if err := s.Abort(ctx); err != nil || rt.last("session.abort")["sessionId"] != "s-1" {
		t.Fatalf("Abort err %v, params %v", err, rt.last("session.abort"))
	}
	rt.set("session.getMessages", `{"events":[{"id":"e-1","timestamp":"2026-09-01T00:00:00Z","parentId":null,"type":"session.idle","data":{}}]}`)
	if evs, err := s.Events(ctx); err != nil || len(evs) != 1 || evs[0].ID != "e-1" {
		t.Fatalf("Events = %+v, %v", evs, err)
	}

	rt.set("session.tasks.cancel", `{"cancelled":true}`)
	if ok, err := s.CancelSubagent(ctx, "agent-1"); !ok || err != nil || rt.last("session.tasks.cancel")["id"] != "agent-1" {
		t.Fatalf("CancelSubagent = %v, %v", ok, err)
	}
	rt.fail("session.tasks.cancel", "gone")
	if ok, err := s.CancelSubagent(ctx, "agent-1"); ok || err == nil {
		t.Fatalf("failed CancelSubagent = %v, %v", ok, err)
	}
	rt.set("session.tasks.list", `{"tasks":[{"type":"shell","id":"sh-1","command":"make","description":"build","status":"running","startedAt":"2026-09-01T00:00:00Z","attachmentMode":"detached"}]}`)
	if tasks, err := s.ListTasks(ctx); err != nil || len(tasks) != 1 || tasks[0].Type() != rpc.TaskInfoTypeShell {
		t.Fatalf("ListTasks = %+v, %v", tasks, err)
	}
	rt.fail("session.tasks.list", "unavailable")
	if _, err := s.ListTasks(ctx); err == nil {
		t.Fatal("ListTasks hid a failure")
	}

	// A JSON-RPC error answer is a refusal: nothing was accepted.
	rt.set("session.tasks.sendMessage", `{"sent":true}`)
	if res, err := s.MessageSubagent(ctx, "agent-1", "more"); err != nil || !res.Sent || rt.last("session.tasks.sendMessage")["message"] != "more" {
		t.Fatalf("MessageSubagent = %+v, %v", res, err)
	}
	rt.fail("session.tasks.sendMessage", "agent is done")
	if _, err := s.MessageSubagent(ctx, "agent-1", "more"); !errors.As(err, &rejected) {
		t.Fatalf("refused MessageSubagent err = %v", err)
	}
	rt.set("session.send", `{"messageId":"m-1"}`)
	if id, err := s.Send(ctx, copilot.MessageOptions{Prompt: "hi"}); err != nil || id != "m-1" || rt.last("session.send")["prompt"] != "hi" {
		t.Fatalf("Send = %q, %v", id, err)
	}
	rt.fail("session.send", "prompt refused")
	if _, err := s.Send(ctx, copilot.MessageOptions{Prompt: "hi"}); !errors.As(err, &rejected) || !strings.Contains(rejected.Error(), "prompt refused") || errors.Unwrap(rejected) == nil {
		t.Fatalf("refused Send err = %v", err)
	}
	if _, err := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: "hi"}); err == nil {
		t.Fatal("SendAndWait hid a refused send")
	}

	// SendAndWait answers with the turn's last assistant message, if any.
	idle := copilot.SessionEvent{ID: "e-3", Timestamp: time.Unix(100, 0), Data: &rpc.SessionIdleData{}}
	rt.set("session.send", `{"messageId":"m-2"}`)
	rt.mu.Lock()
	rt.events["session.send"] = []copilot.SessionEvent{{ID: "e-2", Timestamp: time.Unix(100, 0), Data: &rpc.AssistantMessageData{MessageID: "a-1", Content: "a title"}}, idle}
	rt.mu.Unlock()
	if text, err := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: "name it"}); err != nil || text != "a title" {
		t.Fatalf("SendAndWait = %q, %v", text, err)
	}
	rt.mu.Lock()
	rt.events["session.send"] = []copilot.SessionEvent{idle}
	rt.mu.Unlock()
	if text, err := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: "name it"}); err != nil || text != "" {
		t.Fatalf("SendAndWait without a message = %q, %v", text, err)
	}

	rt.set("session.model.switchTo", `{"modelId":"gpt-5"}`)
	if res, err := s.SwitchModel(ctx, &rpc.ModelSwitchToRequest{ModelID: "gpt-5"}); err != nil || res.ModelID == nil || *res.ModelID != "gpt-5" {
		t.Fatalf("SwitchModel = %+v, %v", res, err)
	}
	if err := s.SetEffort(ctx, "high"); err != nil || rt.last("session.model.setReasoningEffort")["reasoningEffort"] != "high" {
		t.Fatalf("SetEffort err %v, params %v", err, rt.last("session.model.setReasoningEffort"))
	}
	if err := s.SetName(ctx, "Task"); err != nil || rt.last("session.name.set")["name"] != "Task" {
		t.Fatalf("SetName err %v, params %v", err, rt.last("session.name.set"))
	}

	rt.set("session.commands.list", `{"commands":[{"name":"usage","description":"Show usage","kind":"builtin","allowDuringAgentExecution":true}]}`)
	cmds, err := s.ListCommands(ctx)
	if err != nil || len(cmds) != 1 || cmds[0].Name != "usage" {
		t.Fatalf("ListCommands = %+v, %v", cmds, err)
	}
	if p := rt.last("session.commands.list"); p["includeBuiltins"] != true || p["includeSkills"] != true || p["includeClientCommands"] != false {
		t.Fatalf("ListCommands params = %v", p)
	}
	rt.fail("session.commands.list", "unavailable")
	if _, err := s.ListCommands(ctx); err == nil {
		t.Fatal("ListCommands hid a failure")
	}
	rt.set("session.commands.invoke", `{"kind":"text","text":"42 requests"}`)
	if res, err := s.InvokeCommand(ctx, "usage", ""); err != nil || res.(*rpc.SlashCommandTextResult).Text != "42 requests" {
		t.Fatalf("InvokeCommand = %+v, %v", res, err)
	}

	rt.set("session.permissions.handlePendingPermissionRequest", `{"success":true}`)
	if ok, err := s.RespondPermission(ctx, "req-1", rpc.PermissionDecision(&rpc.PermissionDecisionReject{})); !ok || err != nil || rt.last("session.permissions.handlePendingPermissionRequest")["requestId"] != "req-1" {
		t.Fatalf("RespondPermission = %v, %v", ok, err)
	}
	rt.fail("session.permissions.handlePendingPermissionRequest", "not pending")
	if ok, err := s.RespondPermission(ctx, "req-1", rpc.PermissionDecision(&rpc.PermissionDecisionReject{})); ok || err == nil {
		t.Fatalf("failed RespondPermission = %v, %v", ok, err)
	}

	// Custom providers and models go out as one provider.add request.
	err = s.AddProviders(ctx,
		[]copilot.NamedProviderConfig{{Name: "acme", Type: "openai", BaseURL: "https://llm.example/v1", WireAPI: "responses", APIKey: "key"}, {Name: "local", Type: "openai", BaseURL: "http://127.0.0.1:1/v1"}},
		[]copilot.ProviderModelConfig{{ID: "acme-1", Provider: "acme", Name: "Acme One", Capabilities: &rpc.ModelCapabilitiesOverride{Supports: &rpc.ModelCapabilitiesOverrideSupports{Vision: copilot.Bool(true)}}}})
	if err != nil {
		t.Fatalf("AddProviders: %v", err)
	}
	wantProviders := []any{
		map[string]any{"name": "acme", "type": "openai", "baseUrl": "https://llm.example/v1", "wireApi": "responses", "apiKey": "key"},
		map[string]any{"name": "local", "type": "openai", "baseUrl": "http://127.0.0.1:1/v1"},
	}
	wantModels := []any{map[string]any{"id": "acme-1", "provider": "acme", "name": "Acme One", "capabilities": map[string]any{"supports": map[string]any{"vision": true}}}}
	if p := rt.last("session.provider.add"); !reflect.DeepEqual(p["providers"], wantProviders) || !reflect.DeepEqual(p["models"], wantModels) {
		t.Fatalf("provider.add params = %v", p)
	}

	rt.set("session.tools.getCurrentMetadata", `{"tools":[{"name":"uam_show_file","description":"show"}]}`)
	if tools, err := s.ToolCatalog(ctx); err != nil || len(tools) != 1 || tools[0].Name != declarationToolName {
		t.Fatalf("ToolCatalog = %+v, %v", tools, err)
	}
	rt.fail("session.tools.getCurrentMetadata", "unavailable")
	if tools, err := s.ToolCatalog(ctx); err == nil || tools != nil {
		t.Fatalf("failed ToolCatalog = %+v, %v", tools, err)
	}
	rt.fail("session.tools.initializeAndValidate", "invalid tools")
	if _, err := s.ToolCatalog(ctx); err == nil {
		t.Fatal("ToolCatalog hid a failed validation")
	}
	if err := s.SetTools(ctx, []rpc.ProtocolExternalToolDefinition{}); err != nil || !reflect.DeepEqual(rt.last("session.tools.set")["tools"], []any{}) {
		t.Fatalf("SetTools err %v, params %v", err, rt.last("session.tools.set"))
	}

	if err := s.Disconnect(); err != nil || rt.last("session.detach")["sessionId"] != "s-1" {
		t.Fatalf("Disconnect err %v, params %v", err, rt.last("session.detach"))
	}
}

func TestSDKSessionAdapterExecution(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	ctx := context.Background()

	// The objective's text is display text, and its credits are optional.
	rt.set("session.mode.get", `"autopilot"`)
	rt.set("session.autopilotObjective.getState", `{"state":{"id":7,"objective":"ship\u001b[31m it","status":"active","turnCount":2,"creditCountNanoAiu":"0","pauseReason":"waiting\nfor input","creditLimit":{"credits":10,"creditsUsed":3,"creditsUsedNanoAiu":"0"}}}`)
	got, err := s.Execution(ctx)
	limit, used := 10.0, 3.0
	want := &agentapi.ExecutionState{Known: true, Mode: "autopilot", Objective: &agentapi.AutopilotObjective{ID: 7, Objective: "ship it", Status: "active", TurnCount: 2, PauseReason: "waiting for input", CreditLimit: &limit, CreditsUsed: &used}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Execution = %+v (%+v), %v", got, got.Objective, err)
	}
	rt.set("session.autopilotObjective.getState", `{"state":null}`)
	if got, err := s.Execution(ctx); err != nil || !reflect.DeepEqual(got, &agentapi.ExecutionState{Known: true, Mode: "autopilot"}) {
		t.Fatalf("Execution without an objective = %+v, %v", got, err)
	}
	rt.fail("session.autopilotObjective.getState", "unavailable")
	if _, err := s.Execution(ctx); err == nil {
		t.Fatal("Execution hid a failed objective read")
	}
	rt.fail("session.mode.get", "unavailable")
	if _, err := s.Execution(ctx); err == nil {
		t.Fatal("Execution hid a failed mode read")
	}

	// A mode change counts only once the runtime reports the new mode, and
	// never when it needs a confirmation or changes the model.
	rt.set("session.mode.get", `"interactive"`)
	rt.set("session.mode.set", `{"modelChanged":false,"status":"ok"}`)
	if err := s.SetExecutionMode(ctx, rpc.SessionModeInteractive); err != nil || rt.last("session.mode.set")["mode"] != "interactive" {
		t.Fatalf("SetExecutionMode err %v, params %v", err, rt.last("session.mode.set"))
	}
	if err := s.SetExecutionMode(ctx, rpc.SessionModeAutopilot); err == nil || !strings.Contains(err.Error(), "did not apply") {
		t.Fatalf("unapplied SetExecutionMode err = %v", err)
	}
	rt.fail("session.mode.get", "unavailable")
	if err := s.SetExecutionMode(ctx, rpc.SessionModeInteractive); err == nil {
		t.Fatal("SetExecutionMode hid a failed mode read")
	}
	for _, res := range []string{`{"modelChanged":true,"status":"ok"}`, `{"modelChanged":false,"status":"ok","deferImplementation":true}`} {
		rt.set("session.mode.set", res)
		if err := s.SetExecutionMode(ctx, rpc.SessionModeInteractive); err == nil || !strings.Contains(err.Error(), "unsupported confirmation") {
			t.Fatalf("%s: SetExecutionMode err = %v", res, err)
		}
	}
	rt.fail("session.mode.set", "refused")
	if err := s.SetExecutionMode(ctx, rpc.SessionModeInteractive); err == nil {
		t.Fatal("SetExecutionMode hid a refused change")
	}
}

// newSDKClient needs the CLI on PATH; it starts nothing.
func TestNewSDKClientResolvesTheCLI(t *testing.T) {
	t.Setenv("COPILOT_OTEL_ENABLED", "false")
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if c, err := newSDKClient(); err == nil || c != nil {
		t.Fatalf("newSDKClient without a CLI = %v, %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copilot"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c, err := newSDKClient(); err != nil || c == nil {
		t.Fatalf("newSDKClient = %v, %v", c, err)
	}
}
