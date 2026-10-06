package copilot

import (
	"cmp"
	"maps"
	"slices"

	copilot "github.com/github/copilot-sdk/go"
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

// preToolUse keeps every subagent on an allowed model. A task call always
// leaves with an allowed model argument, because the model the CLI would
// otherwise pick is not known here; the call is refused when no allowed model
// is usable.
func (c *conversation) preToolUse(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
	allowed := c.p.subagentModels()
	if in.ToolName != taskTool || len(allowed) == 0 {
		return nil, nil
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
		return nil, nil
	}
	out := maps.Clone(args)
	if out == nil {
		out = map[string]any{}
	}
	out["model"] = model
	return &copilot.PreToolUseHookOutput{ModifiedArgs: out}, nil
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
