package copilot

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// taskTool is the CLI tool that starts a subagent; its model argument picks
// the subagent's model, and without it the CLI picks one from the agent's
// definition, the runtime policy or the session.
const taskTool = "task"

// SetSubagentModels limits the models subagents start on to models, in order
// of preference; an empty list lifts the limit. Open conversations apply it
// from their next subagent.
func (p *webProvider) SetSubagentModels(models []string) {
	p.customMu.Lock()
	defer p.customMu.Unlock()
	p.subagent = slices.Clone(models)
}

func (p *webProvider) subagentModels() []string {
	p.customMu.Lock()
	defer p.customMu.Unlock()
	return slices.Clone(p.subagent)
}

// preToolUse runs uam's checks on every call, a subagent-starting task call
// included, so a refusal (a reached budget, a fuse) also stops new
// subagents. A task call it lets through then always leaves with an allowed
// model argument, because the model the CLI would otherwise pick is not known
// here; the call is refused when no allowed model is usable.
func (c *conversation) preToolUse(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
	var note string
	if c.hooks.Pre != nil {
		args, _ := in.ToolArgs.(map[string]any)
		if patch, ok := in.ToolArgs.(string); ok && in.ToolName == "apply_patch" {
			args = map[string]any{"patch": patch}
		}
		verdict := c.hooks.Pre(context.Background(), agentapi.ToolUse{Tool: in.ToolName, Args: args, Workdir: in.WorkingDirectory, PermissionKind: c.tools.permissionKind(in.ToolName), AgentID: in.AgentID})
		if verdict.Context != "" || verdict.Deny != "" || verdict.Args != nil {
			if strings.HasPrefix(verdict.Context, "uam trail:") || strings.HasPrefix(verdict.Context, "uam thread:") || strings.HasPrefix(verdict.Context, "uam memory:") {
				c.logNotice(verdict.Context)
			}
			out := &copilot.PreToolUseHookOutput{AdditionalContext: verdict.Context}
			if verdict.Args != nil {
				out.ModifiedArgs = verdict.Args
			}
			if verdict.Deny != "" {
				out.PermissionDecision, out.PermissionDecisionReason = "deny", verdict.Deny
			}
			// A task call uam lets through goes on to the model check, with
			// uam's context.
			if in.ToolName != taskTool || verdict.Deny != "" {
				return out, nil
			}
			note = verdict.Context
		}
	}
	allowed := c.p.subagentModels()
	if in.ToolName != taskTool {
		return nil, nil
	}
	if len(allowed) == 0 {
		return contextOnly(note), nil
	}
	args, _ := in.ToolArgs.(map[string]any)
	requested, _ := args["model"].(string)
	c.mu.Lock()
	task := cmp.Or(c.selected, c.turnModel)
	c.mu.Unlock()
	model := c.p.subagentModel(allowed, requested, task)
	if model == "" {
		return &copilot.PreToolUseHookOutput{PermissionDecision: "deny", PermissionDecisionReason: "No model allowed for subagents in UAM Settings is usable: a custom model's API key is missing. Do the work without a subagent."}, nil
	}
	if model == requested {
		return contextOnly(note), nil
	}
	out := maps.Clone(args)
	if out == nil {
		out = map[string]any{}
	}
	out["model"] = model
	// Without the reason an agent sees its arguments changed, takes it for its
	// own mistake and starts the subagent again.
	reason := fmt.Sprintf("UAM started this subagent on %s, because the owner's settings let subagents use only %s. This is expected and final: do not start it again to change the model.", model, strings.Join(allowed, ", "))
	return &copilot.PreToolUseHookOutput{ModifiedArgs: out, AdditionalContext: strings.TrimSpace(note + "\n" + reason)}, nil
}

// logNotice records host context in the transcript: SDK hook context
// reaches the model but is not in hook.end. A durable SDK notification uses
// the existing notice mapper.
func (c *conversation) logNotice(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := c.sess.Log(ctx, text)
	cancel()
	if err != nil {
		log.Warn("persist notice failed", "session", c.id, "error", err)
	}
}

// userPromptSubmitted adds uam's notes for the turn a prompt starts.
func (c *conversation) userPromptSubmitted(copilot.UserPromptSubmittedHookInput, copilot.HookInvocation) (*copilot.UserPromptSubmittedHookOutput, error) {
	if c.hooks.Prompt == nil {
		return nil, nil
	}
	note := c.hooks.Prompt(context.Background())
	if note == "" {
		return nil, nil
	}
	c.logNotice(note)
	return &copilot.UserPromptSubmittedHookOutput{AdditionalContext: note}, nil
}

// contextOnly is a hook answer that only adds note; nil without one.
func contextOnly(note string) *copilot.PreToolUseHookOutput {
	if note == "" {
		return nil
	}
	return &copilot.PreToolUseHookOutput{AdditionalContext: note}
}

// subagentModel is the model a subagent runs on: the requested one, else the
// Task's, else the first in allowed, whichever is allowed and usable first;
// "" when none is. A custom model without its API key is not usable.
func (p *webProvider) subagentModel(allowed []string, requested, task string) string {
	usable := func(id string) bool { return id != "" && slices.Contains(allowed, id) && p.customKeyErr(id) == nil }
	for _, id := range append([]string{requested, task}, allowed...) {
		if usable(id) {
			return id
		}
	}
	return ""
}

// succeededToolUse observes completed calls, including subagent calls. The
// provider result is deliberately not used as proof of filesystem changes.
// Output the CLI saved to a file is kept with the session (keepSpill), and an
// identical repeat view is replaced by a short note (readOnceLocked).
func (c *conversation) succeededToolUse(in copilot.PostToolUseHookInput, _ copilot.HookInvocation) (*copilot.PostToolUseHookOutput, error) {
	if c.hooks.Succeeded != nil {
		args, _ := in.ToolArgs.(map[string]any)
		c.hooks.Succeeded(context.Background(), agentapi.ToolUse{Tool: in.ToolName, Args: args, Workdir: in.WorkingDirectory, PermissionKind: c.tools.permissionKind(in.ToolName)})
	}
	c.mu.Lock()
	repeat := c.readOnceLocked(in)
	c.mu.Unlock()
	if repeat != nil {
		return &copilot.PostToolUseHookOutput{ModifiedResult: repeat}, nil
	}
	if result := c.keepSpill(in.ToolResult); result != nil {
		return &copilot.PostToolUseHookOutput{ModifiedResult: result}, nil
	}
	return nil, nil
}

// failedToolUse receives native execution failures, including subagent calls.
func (c *conversation) failedToolUse(in copilot.PostToolUseFailureHookInput, _ copilot.HookInvocation) (*copilot.PostToolUseFailureHookOutput, error) {
	if c.hooks.Failed == nil {
		return nil, nil
	}
	args, _ := in.ToolArgs.(map[string]any)
	if patch, ok := in.ToolArgs.(string); ok && in.ToolName == "apply_patch" {
		args = map[string]any{"patch": patch}
	}
	note := c.hooks.Failed(context.Background(), agentapi.ToolUse{Tool: in.ToolName, Args: args, Workdir: in.WorkingDirectory, PermissionKind: c.tools.permissionKind(in.ToolName)}, in.Error)
	if note == "" {
		return nil, nil
	}
	return &copilot.PostToolUseFailureHookOutput{AdditionalContext: note}, nil
}
