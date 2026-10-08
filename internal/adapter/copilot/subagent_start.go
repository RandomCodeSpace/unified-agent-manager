package copilot

import copilot "github.com/github/copilot-sdk/go"

// subagentSystem is what every subagent is told before its first turn: the
// rules of taskSystem a subagent needs too, which it does not otherwise
// receive. Its own row keeps the todo list current without the main agent
// asking for it in the prompt; its commits carry no agent attribution.
const subagentSystem = "You are a subagent in a uam Task. Keep your own row in the session's `todos` table with the sql tool: insert it with status in_progress when you start and set it to done right when you finish (blocked, with the reason in description, when stuck). Write commit messages and pull or merge request titles and descriptions as the owner's own work: no Co-authored-by trailer and no line crediting an AI, agent or tool, unless the owner asks for one."

// subagentStart prepends subagentSystem to each subagent's first prompt.
func subagentStart(copilot.SubagentStartHookInput, copilot.HookInvocation) (*copilot.SubagentStartHookOutput, error) {
	return &copilot.SubagentStartHookOutput{AdditionalContext: subagentSystem}, nil
}
