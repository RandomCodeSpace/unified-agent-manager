package copilot

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
)

// maxHostToolCalls bounds the calls a session remembers for idempotence; the
// oldest finished one makes room for a new one.
const maxHostToolCalls = 256

var hostToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type hostToolCall struct {
	name, args string
	done       chan struct{}
	result     copilot.ToolResult
}

// hostTools forwards the calls of the web service's host tools to its
// CallTool. It shares the session's gate with uam_show_file, whose guards
// it keeps: bounded arguments, a call identity from the verified session,
// one CallTool per (session, call ID), and cancellation on stop.
type hostTools struct {
	*toolGate
	call  func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult
	task  string
	calls map[string]*hostToolCall
	// order holds the keys of calls oldest first.
	order []string
}

// sessionTools builds one session's uam tools behind one gate: uam_show_file
// when validate is set, then tools, whose calls go to call with task as
// their TaskID. The gate is nil when there are no tools.
func sessionTools(task string, validate func(context.Context, string) (string, error), tools []agentapi.HostTool,
	call func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult) (*toolGate, []copilot.Tool, error) {
	if len(tools) > 0 && call == nil {
		return nil, nil, errors.New("copilot: host tools need a CallTool")
	}
	seen := map[string]bool{declarationToolName: true}
	parameters := make([]map[string]any, len(tools))
	for i, tool := range tools {
		if !hostToolName.MatchString(tool.Name) || seen[tool.Name] {
			return nil, nil, fmt.Errorf("copilot: host tool name %q is invalid or repeated", tool.Name)
		}
		seen[tool.Name] = true
		// A deep copy, so the caller changing its map later cannot race the
		// SDK encoding the schema.
		if tool.Parameters != nil {
			data, err := json.Marshal(tool.Parameters)
			if err != nil || json.Unmarshal(data, &parameters[i]) != nil {
				return nil, nil, fmt.Errorf("copilot: host tool %s parameters cannot be encoded as JSON", tool.Name)
			}
		}
	}
	var gate *toolGate
	var out []copilot.Tool
	if d := newDeclarationTool(validate); d != nil {
		gate, out = d.toolGate, append(out, d.tool())
	}
	if len(tools) == 0 {
		return gate, out, nil
	}
	if gate == nil {
		gate = newToolGate()
	}
	h := &hostTools{toolGate: gate, call: call, task: task, calls: make(map[string]*hostToolCall)}
	for i, tool := range tools {
		name := tool.Name
		out = append(out, copilot.Tool{
			Name: name, Description: tool.Description, Parameters: parameters[i],
			SkipPermission: true, Defer: copilot.ToolDeferNever,
			Handler: func(inv copilot.ToolInvocation) (copilot.ToolResult, error) { return h.invoke(name, inv) },
		})
	}
	return gate, out, nil
}

func (h *hostTools) invoke(name string, inv copilot.ToolInvocation) (copilot.ToolResult, error) {
	ctx := inv.TraceContext
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.stopCtx, cancel)
	defer func() { stop(); cancel() }()
	args := inv.Arguments
	if args == nil {
		args = map[string]any{}
	}
	if _, ok := args.(map[string]any); !ok {
		return copilot.ToolResult{}, fmt.Errorf("%s arguments must be a JSON object", name)
	}
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) > agentapi.MaxHostToolArguments {
		return copilot.ToolResult{}, fmt.Errorf("%s arguments must be at most %d bytes of JSON", name, agentapi.MaxHostToolArguments)
	}
	if inv.ToolCallID == "" || len(inv.ToolCallID) > 256 || inv.SessionID == "" {
		return copilot.ToolResult{}, fmt.Errorf("%s call identity is unavailable", name)
	}
	key := inv.SessionID + "\x00" + inv.ToolCallID
	h.mu.Lock()
	if !h.ready || h.closed || inv.SessionID != h.session {
		h.mu.Unlock()
		return copilot.ToolResult{}, fmt.Errorf("%s is unavailable; continue without it", name)
	}
	if previous := h.calls[key]; previous != nil {
		h.mu.Unlock()
		if previous.name != name || previous.args != string(encoded) {
			return copilot.ToolResult{}, fmt.Errorf("the %s call was repeated with different arguments", name)
		}
		select {
		case <-previous.done:
			return previous.result, nil
		case <-ctx.Done():
			return copilot.ToolResult{}, ctx.Err()
		}
	}
	if !h.makeRoomLocked() {
		h.mu.Unlock()
		return copilot.ToolResult{}, fmt.Errorf("too many uam tool calls are running; retry %s later", name)
	}
	call := &hostToolCall{name: name, args: string(encoded), done: make(chan struct{})}
	h.calls[key] = call
	h.order = append(h.order, key)
	h.mu.Unlock()
	r := h.call(ctx, agentapi.HostToolCall{Name: name, CallID: inv.ToolCallID, TaskID: h.task, Arguments: encoded})
	// The SDK would send the result's Go representation for an empty text.
	text := cmp.Or(r.Text, "The tool returned no text.")
	result := copilot.ToolResult{ResultType: "success", TextResultForLLM: text, SessionLog: text}
	if r.Failed {
		result.ResultType = "failure"
	}
	h.mu.Lock()
	call.result = result
	close(call.done)
	h.mu.Unlock()
	return result, nil
}

// makeRoomLocked forgets the oldest finished call when the session remembers
// as many as it may, and reports whether a new one fits.
func (h *hostTools) makeRoomLocked() bool {
	if len(h.order) < maxHostToolCalls {
		return true
	}
	for i, key := range h.order {
		select {
		case <-h.calls[key].done:
			delete(h.calls, key)
			h.order = slices.Delete(h.order, i, i+1)
			return true
		default:
		}
	}
	return false
}
