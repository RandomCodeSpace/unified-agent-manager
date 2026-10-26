package copilot

import copilot "github.com/github/copilot-sdk/go"

// subagentSystem is what every subagent is told before its first turn: the
// rules of taskSystem a subagent needs too, which it does not otherwise
// receive. Its own row keeps the todo list current without the main agent
// asking for it in the prompt. The owner's git rules follow it.
const subagentSystem = "You are a subagent in a uam Thread. Keep your own row in the session's `todos` table with the sql tool: insert it with status in_progress when you start and set it to done right when you finish (blocked, with the reason in description, when stuck). Keep plans and step lists there, not in Markdown files in the project, unless the owner asks for a file."

// subagentStart prepends subagentSystem and the git rules to each
// subagent's first prompt.
func (p *webProvider) subagentStart(copilot.SubagentStartHookInput, copilot.HookInvocation) (*copilot.SubagentStartHookOutput, error) {
	rules, _ := p.git()
	return &copilot.SubagentStartHookOutput{AdditionalContext: subagentSystem + "\n\n" + rules}, nil
}
