package copilot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// titleDeleteTimeout bounds deleting a title session, whatever ended it.
const titleDeleteTimeout = 5 * time.Second

// titleSystem replaces Copilot's system prompt in a title session.
const titleSystem = `You write a title for a coding task from the user's first message, like the subject line of a ticket.
The message may be or include attached images. When the agent's first reply follows it, use the reply only to understand what was asked.
Rules:
- 2 to 6 words, sentence case, at most 60 characters.
- A noun phrase or an imperative that names what the user asks about, e.g. "Why teams write changelogs" or "Fix flaky login test".
- Never answer, explain or carry out the message: the title names the request, not its answer.
- No quotes, no ending punctuation, no emoji.
- Output only the title on one line.`

// Title asks req.Model for a title in a throwaway session with no tools,
// no discovered configuration, no session store and a fixed system message.
// Once created, the session is always disconnected and deleted, with a fresh
// deadline, so neither its directory nor a session-store row outlives it.
func (p *webProvider) Title(ctx context.Context, req agentapi.TitleRequest) (string, error) {
	return p.RunUtility(ctx, agentapi.UtilityRequest{Model: req.Model, Workdir: req.Workdir, Purpose: "title", System: titleSystem,
		Prompt: titlePrompt(req), Attachments: req.Images, OnUsage: req.OnUsage})
}

// titlePrompt is the first message, with a note of its attached images and
// the agent's reply when the request has them.
func titlePrompt(req agentapi.TitleRequest) string {
	text := req.Text
	if n := len(req.Images); n > 0 {
		note := fmt.Sprintf("[%d attached images]", n)
		if n == 1 {
			note = "[1 attached image]"
		}
		text = strings.TrimSpace(note + "\n" + text)
	}
	prompt := "<user_message>\n" + text + "\n</user_message>"
	if req.Reply != "" {
		prompt += "\n<agent_reply>\n" + req.Reply + "\n</agent_reply>"
	}
	return prompt
}

// RunUtility shares the same client and restricted throwaway-session
// configuration for every utility prompt. The only tools allowed are
// req.Tools, verified like a Task's before the prompt is sent.
func (p *webProvider) RunUtility(ctx context.Context, req agentapi.UtilityRequest) (string, error) {
	if err := p.customKeyErr(req.Model); err != nil {
		return "", err
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	start := time.Now()
	client, done, err := p.startUtility(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("the Copilot CLI did not start within %s: %w", seconds(time.Since(start)), ctx.Err())
		}
		return "", err
	}
	defer done()
	model, purpose := req.Model, req.Purpose
	gate, tools, err := sessionTools("", nil, req.Tools, req.CallTool)
	if err != nil {
		return "", err
	}
	available := make([]string, 0, len(tools))
	for _, tool := range tools {
		available = append(available, tool.Name)
	}
	var observe copilot.SessionEventHandler
	if gate != nil {
		observe = gate.observe
		defer gate.stop()
	}
	if req.OnUsage != nil {
		usage := &utilityUsage{provider: p, selected: req.Model}
		observe = func(ev copilot.SessionEvent) {
			if gate != nil {
				gate.observe(ev)
			}
			usage.add(ev)
		}
		defer usage.report(req.OnUsage)
	}
	providers, models := byom(p.customModels())
	sess, err := client.CreateSession(ctx, &copilot.SessionConfig{
		ClientName:                         "uam-" + purpose,
		Providers:                          providers,
		Models:                             models,
		Model:                              model,
		ReasoningEffort:                    titleEffort(ctx, client, model),
		ReasoningSummary:                   copilot.ReasoningSummaryNone,
		WorkingDirectory:                   req.Workdir,
		AvailableTools:                     available,
		Tools:                              tools,
		OnEvent:                            observe,
		EnableConfigDiscovery:              copilot.Bool(false),
		SkipCustomInstructions:             copilot.Bool(true),
		EnableOnDemandInstructionDiscovery: copilot.Bool(false),
		EnableFileHooks:                    copilot.Bool(false),
		EnableHostGitOperations:            copilot.Bool(false),
		EnableSessionStore:                 copilot.Bool(false),
		EnableSkills:                       copilot.Bool(false),
		SkipEmbeddingRetrieval:             copilot.Bool(true),
		DisabledMCPServers:                 p.disabledMCPServers(),
		InfiniteSessions:                   &copilot.InfiniteSessionConfig{Enabled: copilot.Bool(false)},
		Memory:                             &copilot.MemoryConfiguration{Enabled: false},
		SystemMessage:                      &copilot.SystemMessageConfig{Mode: "replace", Content: req.System},
		Streaming:                          copilot.Bool(false),
		OnPermissionRequest: func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
			return &rpc.PermissionDecisionReject{}, nil
		},
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("the Copilot CLI did not open the Utility session within %s: %w", seconds(time.Since(start)), ctx.Err())
		}
		return "", fmt.Errorf("create copilot %s session: %s", purpose, errText(err))
	}
	ready := time.Since(start)
	if err := p.recordUsageSession(client, sess.ID(), true); err != nil {
		p.disconnectBounded(ctx, sess)
		return "", fmt.Errorf("record copilot %s usage ownership: %w", purpose, err)
	}
	defer func() {
		id := sess.ID()
		// The SDK's detach has no deadline: a CLI that never answers it must
		// not hold the call, its slot and the CLI restart forever.
		detached := make(chan error, 1)
		go func() {
			defer log.Recover("copilot utility disconnect", &p.mu)
			detached <- sess.Disconnect()
		}()
		select {
		case err := <-detached:
			if err != nil {
				log.Info("disconnect copilot utility session failed", "purpose", purpose, "conversation", id, "error", err)
			} else if err := p.recordUsageSession(client, id, false); err != nil {
				log.Warn("release copilot utility usage ownership failed", "purpose", purpose, "conversation", id, "error", err)
			}
		case <-time.After(titleDeleteTimeout):
			log.Info("disconnect copilot utility session timed out", "purpose", purpose, "conversation", id)
		}
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), titleDeleteTimeout)
		defer cancel()
		if err := client.DeleteSession(dctx, id); err != nil {
			log.Warn("delete copilot utility session failed", "purpose", purpose, "conversation", id, "error", err)
		}
	}()
	if gate != nil {
		if err := gate.catalog(ctx, sess, tools...); err != nil {
			return "", fmt.Errorf("copilot %s: %w", purpose, err)
		}
	}
	schema := req.ResponseSchema
	if _, ok := p.customModel(model); ok {
		// shortcut: BYOM endpoints were not probed for json_schema in the 2026-10 probe; probe them on a CLI bump
		schema = nil
	}
	reply, err := sess.SendAndWait(ctx, copilot.MessageOptions{Prompt: req.Prompt, Attachments: attachments(agentapi.Prompt{Attachments: req.Attachments}), ResponseSchema: schema})
	if err != nil {
		if ctx.Err() != nil {
			// Stop the model request before the session goes, so it does
			// not run on in the CLI beside the conversations.
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), titleDeleteTimeout)
			if aerr := sess.Abort(actx); aerr != nil {
				log.Info("abort copilot utility session failed", "purpose", purpose, "conversation", sess.ID(), "error", aerr)
			}
			cancel()
			return "", fmt.Errorf("the Utility model did not answer within %s (its session was open after %s): %w", seconds(time.Since(start)), seconds(ready), ctx.Err())
		}
		return "", fmt.Errorf("copilot %s: %s", purpose, errText(err))
	}
	return reply, nil
}

// utilityUsage sums a Utility session's assistant.usage events. Events
// reach the handler one at a time and each comes before the idle event
// SendAndWait returns on, so the sum is complete when it returns; mu covers
// a call ended early by its context.
type utilityUsage struct {
	provider *webProvider
	selected string
	mu       sync.Mutex
	sum      agentapi.UtilityUsage
	reported bool
	seen     map[string]bool
}

func (u *utilityUsage) add(ev copilot.SessionEvent) {
	d, ok := ev.Data.(*rpc.AssistantUsageData)
	if !ok {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if ev.ID != "" {
		if u.seen[ev.ID] {
			return
		}
		if u.seen == nil {
			u.seen = map[string]bool{}
		}
		u.seen[ev.ID] = true
	}
	u.reported = true
	if tokens := modelTokens(ev, d); tokens != nil {
		if u.provider != nil && d.IsByok != nil && *d.IsByok {
			tokens.Model = u.provider.customSelection(d.Model, u.selected)
		}
		u.sum.Tokens = append(u.sum.Tokens, *tokens)
	}
	if d.InputTokens != nil && *d.InputTokens > 0 {
		u.sum.InputTokens += *d.InputTokens
	}
	if d.OutputTokens != nil && *d.OutputTokens > 0 {
		u.sum.OutputTokens += *d.OutputTokens
	}
	if d.CopilotUsage != nil && d.CopilotUsage.TotalNanoAiu > 0 {
		u.sum.Credits += d.CopilotUsage.TotalNanoAiu / nanoPerUnit
	}
}

// report passes the sum to fn when any event reported usage.
func (u *utilityUsage) report(fn func(agentapi.UtilityUsage)) {
	u.mu.Lock()
	sum, reported := u.sum, u.reported
	sum.Tokens = slices.Clone(sum.Tokens)
	u.mu.Unlock()
	if reported {
		fn(sum)
	}
}

// titleEffort is the lowest reasoning effort model supports, or "" for its
// default when it lists none or the catalog cannot be read.
func titleEffort(ctx context.Context, client sdkClient, model string) string {
	models, err := client.ListModels(ctx)
	if err != nil {
		return ""
	}
	for _, m := range models {
		if m.ID != model {
			continue
		}
		for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			if slices.Contains(m.SupportedReasoningEfforts, effort) {
				return effort
			}
		}
	}
	return ""
}
