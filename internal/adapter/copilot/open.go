package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// taskSystem is appended to Copilot's own system message in every Task
// session, created or resumed, so results land in the reply rather than the
// folded tool calls, the agent loads the built-in uam skill before using what
// uam renders or provides (an agent rarely loads it from its description
// alone), replies use the Markdown shapes the web UI renders as rich blocks
// (web/packages/ui/src/lib/richMarkdown.ts; the stored text stays Markdown),
// a question's options arrive as choices the composer lists and
// pre-selects, and work of several steps keeps the todo list uam shows
// (todos.go). The owner's git rules follow it (git_rules.go). Utility
// sessions replace the message. Subagents do not receive it: subagentStart
// gives them the todo and git rules.
const taskSystem = `This conversation is a uam Thread: the owner reads it in a browser, often on a phone, where your tool calls and their output are folded away. Put what the owner needs in your reply: results, the errors that matter, decisions and next steps. Load the uam skill before you show the owner a file, image, diagram or chart, start another Thread, or start subagents.

Make replies easy to scan: uam turns these Markdown shapes into rich blocks, so use them where they fit instead of long prose. A GitHub alert (` + "`> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]` or `[!CAUTION]`" + `) for what the owner must not miss: put risks, caveats and what you did not verify in a ` + "`> [!WARNING]`" + `, not under a label; bullets or paragraphs that each start with ` + "`**Label:**`" + ` for facts, which show as a grid, and as stat tiles when two to four items are each one short figure (` + "`**Tests:** 742`" + `); a ` + "`> [!QUESTION]`" + ` when you need the owner's decision and a ` + "`> [!BLOCKER]`" + ` for what stops you until the owner acts; a numbered list where every item starts with bold text for steps; a task list (` + "`- [x]`" + `) for progress. A task item or a step's bold lead with the same title as a todo row ticks off live as the row changes, so plan with your todo titles; a table for comparisons; a ` + "`diff`" + ` fence for changes; a ` + "`mermaid`" + ` fence for flows. Write Markdown only, never HTML.

When you ask the owner a question with ask_user, pass each answer option as its own entry in choices rather than listing the options in the question text. Put the option you recommend first and end its label with " (Recommended)". When several options may apply together, end the question itself with " (Choose any that apply)".

For work with more than two steps, keep a todo list in the session's ` + "`todos`" + ` table with the sql tool: insert the steps first, then set each to in_progress when you start it and done right when it is finished (blocked, with the reason in description, when stuck). In the same sql call that inserts the steps, insert a ` + "`todo_deps`" + ` row (todo_id, depends_on) for every step that has to wait for another; a summary or wrap-up step depends on each step it draws on. When you start a subagent, tell it in its prompt to insert its own row in the same ` + "`todos`" + ` table, keep it current and set it done when it finishes. Keep plans and step lists there, not in Markdown files in the project, unless the owner asks for a file.

Narrow broad shell output with grep, head or tail, or a command's own filters, before it reaches you. Output over 8 KB is saved to a file and you get a preview and the file's path; read that file in parts rather than all at once.`

func (p *webProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	if req.Events == nil {
		return nil, errors.New("copilot: OpenRequest.Events is required")
	}
	if req.ConversationID == "" {
		if err := p.customKeyErr(req.Model); err != nil {
			return nil, err
		}
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	c := &conversation{
		p: p, client: client, id: req.ConversationID, sink: req.Events, hooks: req.Hooks, skillDirs: req.SkillDirectories, pending: map[string]*interaction{}, tr: newTranscript(), subs: newSubagentLog(),
		seen: map[string]bool{}, watch: map[string]time.Time{},
		reportEmptyTasks: req.ConversationID != "",
	}
	gate, uamTools, err := sessionTools(req.SessionID, req.ValidateFile, req.Tools, req.CallTool)
	if err != nil {
		return nil, err
	}
	c.tools, c.uamTools = gate, uamTools
	if gate != nil {
		gate.rebuild = c.rebuildTools
	}
	if req.ConversationID == "" {
		c.selected = req.Model
	}
	// The Task's selection, kept across agent changes; on reopen the caller
	// also applies it with SetModel.
	if req.Model != "" {
		c.selection = &modelSelection{model: req.Model, effort: req.Effort, contextSize: cmp.Or(req.ContextSize, "default")}
	}
	// A resumed session keeps its selected model but not its custom
	// models, so every open supplies them.
	custom := p.customModels()
	providers, models := byom(custom)
	c.byom = newRegistered(custom)
	// Permission requests are answered through the pending-permission RPC
	// with the request id from the permission.requested event; the SDK
	// callback only registers this client as the one that decides.
	deferPermission := func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		return &rpc.PermissionDecisionNoResult{}, nil
	}
	compaction := infiniteSessions(req.CompactionThreshold)
	disabled := p.disabledMCPServers()
	rules, coauthor := p.git()
	system := taskSystem + "\n\n" + rules
	if req.Instructions != "" {
		system += "\n\n" + req.Instructions
	}
	var sess sdkSession
	if req.ConversationID == "" {
		sess, err = client.CreateSession(ctx, &copilot.SessionConfig{
			SessionID:                req.SessionID,
			WorkingDirectory:         req.Workdir,
			Model:                    req.Model,
			ReasoningEffort:          req.Effort,
			ContextTier:              copilot.ContextTier(req.ContextSize),
			Providers:                providers,
			Models:                   models,
			Tools:                    uamTools,
			SystemMessage:            systemMessage(system, req.OutputStyle),
			CoauthorEnabled:          copilot.Bool(coauthor),
			Streaming:                copilot.Bool(true),
			EnableFileChangeTracking: copilot.Bool(true),
			OnPermissionRequest:      deferPermission,
			OnUserInputRequest:       c.askUser,
			OnAutoModeSwitchRequest:  c.confirmAuto,
			AskUserVariant:           copilot.AskUserVariantLegacy, // forms and links come from elicitations only
			OnElicitationRequest:     c.elicit,
			OnExitPlanModeRequest:    c.reviewPlan,
			OnEvent:                  c.onEvent,
			Hooks:                    &copilot.SessionHooks{OnPreToolUse: c.preToolUse, OnPostToolUse: c.succeededToolUse, OnPostToolUseFailure: c.failedToolUse, OnUserPromptSubmitted: c.userPromptSubmitted, OnSubagentStart: p.subagentStart},
			// Discovery loads what the terminal CLI loads for this directory:
			// skills, project agents, custom instructions, MCP servers and
			// hooks. The owner turned it on for web Tasks (#176).
			EnableConfigDiscovery: copilot.Bool(true),
			SkillDirectories:      req.SkillDirectories,
			DisabledMCPServers:    disabled,
			InfiniteSessions:      compaction,
			LargeOutput:           largeOutput(),
		})
	} else {
		p.awaitDetach(ctx, req.ConversationID)
		sess, err = client.ResumeSession(ctx, req.ConversationID, &copilot.ResumeSessionConfig{
			WorkingDirectory: req.Workdir,
			Providers:        providers,
			Models:           models,
			Tools:            uamTools,
			SystemMessage:    systemMessage(system, req.OutputStyle),
			CoauthorEnabled:  copilot.Bool(coauthor),
			Streaming:        copilot.Bool(true),
			// Native capture survives only when the resumed session has a valid
			// baseline; this option cannot reconstruct earlier untracked edits.
			EnableFileChangeTracking: copilot.Bool(true),
			// Explicit false: nil keeps the runtime default, false treats tool
			// calls and prompts pending at the last suspend as interrupted.
			// Keep false in every mode, Yolo included: a 2026-11-04 probe
			// (CLI 1.0.105) with true after a kill -9 did not bring back the
			// pending permission, left the session busy and swallowed the
			// next prompt until Cancel. With false, a new prompt works.
			ContinuePendingWork:     copilot.Bool(false),
			OnPermissionRequest:     deferPermission,
			OnUserInputRequest:      c.askUser,
			AskUserVariant:          copilot.AskUserVariantLegacy,
			OnElicitationRequest:    c.elicit,
			OnAutoModeSwitchRequest: c.confirmAuto,
			OnExitPlanModeRequest:   c.reviewPlan,
			OnEvent:                 c.onEvent,
			Hooks:                   &copilot.SessionHooks{OnPreToolUse: c.preToolUse, OnPostToolUse: c.succeededToolUse, OnPostToolUseFailure: c.failedToolUse, OnUserPromptSubmitted: c.userPromptSubmitted, OnSubagentStart: p.subagentStart},
			// Resumed Tasks discover the same configuration as new ones.
			EnableConfigDiscovery: copilot.Bool(true),
			SkillDirectories:      req.SkillDirectories,
			DisabledMCPServers:    disabled,
			InfiniteSessions:      compaction,
			LargeOutput:           largeOutput(),
		})
	}
	if err != nil {
		c.mu.Lock()
		c.expirePlansLocked()
		c.closed = true
		c.mu.Unlock()
		if c.tools != nil {
			c.tools.stop()
		}
		if req.ConversationID != "" && strings.Contains(err.Error(), "Session not found") {
			return nil, fmt.Errorf("%w: %s", agentapi.ErrConversationNotFound, req.ConversationID)
		}
		p.poke()
		return nil, fmt.Errorf("open copilot conversation: %s", errText(err))
	}
	if err := p.recordUsageSession(client, sess.ID(), true); err != nil {
		c.mu.Lock()
		c.expirePlansLocked()
		c.closed = true
		c.mu.Unlock()
		if c.tools != nil {
			c.tools.stop()
		}
		p.disconnectBounded(ctx, sess)
		return nil, fmt.Errorf("record copilot usage ownership: %w", err)
	}
	// Under mu: a subagent event may already start a task-list read.
	c.mu.Lock()
	c.sess, c.id = sess, sess.ID()
	c.checkSchedulesLocked()
	if req.ConversationID != "" || c.relist {
		c.relist = false
		c.checkTasksLocked()
	}
	if req.ConversationID != "" {
		c.checkTodosLocked()
	}
	c.checkPlanLocked()
	c.mu.Unlock()
	if !p.track(c) {
		return nil, errors.Join(agentapi.ErrClosed, c.Close(ctx))
	}
	// Before anything is sent, so no request is decided under another mode.
	if req.AssistedApprovalModel != "" {
		if err := c.SetAssistedPermissions(ctx, req.AssistedApprovalModel); err != nil {
			p.abandonOpen(ctx, client, c, req.ConversationID == "")
			return nil, fmt.Errorf("apply assisted permissions: %w", err)
		}
	}
	// The Task's custom agent is selected, with the Task's model kept,
	// before anything is sent and before the first tool proof, which then
	// proves the tools it has.
	if req.Agent != "" {
		if err := c.selectAgentKeepingModel(ctx, req.Agent); err != nil {
			p.abandonOpen(ctx, client, c, req.ConversationID == "")
			return nil, err
		}
	}
	// SetGitHubMCP does not see a session it changes during the open.
	if !slices.Equal(p.disabledMCPServers(), disabled) {
		go c.syncGitHubMCP(ctx)
	}
	// The first proof waits for the runtime to start its tools, which
	// includes connecting the built-in GitHub MCP server when it is on (over
	// a second on CLI 1.0.93), so the open does not wait for it. The tools
	// refuse calls until it passes, and a message waits for it.
	if c.tools != nil {
		c.tools.proveFirst(sess, uamTools)
	}
	c.control.Lock()
	c.refreshExecution(ctx)
	c.control.Unlock()
	c.mu.Lock()
	if _, ok := sess.(mcpSession); ok {
		snapshot := copyMCPStatus(c.mcpStatus)
		snapshot.Supported = true
		c.publishMCPStatusLocked(snapshot)
	}
	c.mu.Unlock()
	return c, nil
}
