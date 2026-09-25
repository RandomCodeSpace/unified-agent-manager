package copilot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Runtime state is provider-owned. Optional here so older/test transports can
// retain the existing conversation interface without pretending to support it.
type executionSession interface {
	Execution(context.Context) (*agentapi.ExecutionState, error)
	SetExecutionMode(context.Context, rpc.SessionMode) error
}

func (a sdkSessionAdapter) Execution(ctx context.Context) (*agentapi.ExecutionState, error) {
	mode, err := a.s.RPC.Mode.Get(ctx)
	if err != nil {
		return nil, err
	}
	objective, err := a.s.RPC.AutopilotObjective.GetState(ctx)
	if err != nil {
		return nil, err
	}
	if mode == nil || objective == nil {
		return nil, errors.New("missing execution state")
	}
	out := &agentapi.ExecutionState{Known: true, Mode: string(*mode)}
	if st := objective.State; st != nil {
		out.Objective = &agentapi.AutopilotObjective{ID: st.ID, Objective: displaytext.Sanitize(st.Objective), Status: string(st.Status), TurnCount: st.TurnCount, PauseReason: cleanPointer(st.PauseReason), CompletionSummary: cleanPointer(st.CompletionSummary)}
		if st.CreditLimit != nil {
			out.Objective.CreditLimit = st.CreditLimit.Credits
			used := st.CreditLimit.CreditsUsed
			out.Objective.CreditsUsed = &used
		}
	}
	return out, nil
}

func (a sdkSessionAdapter) SetExecutionMode(ctx context.Context, mode rpc.SessionMode) error {
	res, err := a.s.RPC.Mode.Set(ctx, &rpc.ModeSetRequest{Mode: mode})
	if err != nil {
		return err
	}
	if res == nil || res.Confirmation != nil || res.DeferImplementation != nil && *res.DeferImplementation || res.ModelChanged {
		return errors.New("mode change needs unsupported confirmation or model synchronization")
	}
	current, err := a.s.RPC.Mode.Get(ctx)
	if err != nil {
		return err
	}
	if current == nil || *current != mode {
		return errors.New("provider did not apply the requested execution mode")
	}
	return nil
}

func cleanPointer(value *string) string {
	if value == nil {
		return ""
	}
	return displaytext.Sanitize(*value)
}

// Caller holds control. Event revisions prevent a read started before a newer
// mode/objective event from overwriting it. The event schedules another read.
func (c *conversation) refreshExecution(ctx context.Context) {
	runtime, ok := c.sess.(executionSession)
	if !ok {
		return
	}
	c.mu.Lock()
	revision := c.executionRevision
	c.mu.Unlock()
	state, err := runtime.Execution(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || revision != c.executionRevision {
		return
	}
	if err != nil {
		state = &agentapi.ExecutionState{}
		if c.execution != nil {
			*state = *c.execution
			state.Known = false
		}
	}
	c.execution = state
	if state != nil && state.Known && state.Mode != "autopilot" {
		c.autopilotTurn = false
		if c.idleUnresolved {
			c.assistantIdleSeen = true
			c.finishTurnLocked(nil, time.Now())
		}
	}
	c.emitLocked(agentapi.Event{Kind: agentapi.EventExecution, Execution: state})
}

func (c *conversation) checkExecutionLocked() {
	if c.sess == nil {
		return
	}
	go func() {
		c.control.Lock()
		defer c.control.Unlock()
		if c.isClosed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.refreshExecution(ctx)
	}()
}

func commandDisabled(name string) string {
	switch name {
	case "allow-all", "permissions", "model", "rename", "context", "usage", "list-dirs", "env", "skills", "autopilot", "init", "review", "blame", "fleet", "research", "security-review":
		return ""
	case "plan":
		return "Plan exit approval is not supported by the web client yet"
	case "every", "after":
		return "Scheduled commands are not supported by the web client"
	case "cwd", "add-dir":
		return "This command changes directories outside the Task's project settings"
	case "share", "remote":
		return "Publishing and remote sessions are not supported by the web client"
	default:
		return "This native command has no supported web handler yet"
	}
}

func (c *conversation) Commands(ctx context.Context) ([]agentapi.Command, error) {
	if c.isClosed() {
		return nil, agentapi.ErrClosed
	}
	listed, err := c.sess.ListCommands(ctx)
	if err != nil {
		return nil, fmt.Errorf("copilot commands: %s", errText(err))
	}
	out := make([]agentapi.Command, 0, len(listed))
	for _, cmd := range listed {
		kind := agentapi.CommandPrompt
		disabled := commandDisabled(cmd.Name)
		if cmd.Kind == rpc.SlashCommandKindSkill {
			kind = agentapi.CommandSkill
			disabled = ""
		}
		if cmd.Kind != rpc.SlashCommandKindBuiltin && cmd.Kind != rpc.SlashCommandKindSkill {
			disabled = "This client command has no web handler"
		}
		item := agentapi.Command{Name: cmd.Name, Description: cmd.Description, Kind: kind, Aliases: cmd.Aliases, AllowDuringTurn: cmd.AllowDuringAgentExecution, DisabledReason: disabled}
		if cmd.Input != nil {
			item.InputHint = cmd.Input.Hint
			item.InputRequired = cmd.Input.Required != nil && *cmd.Input.Required
			for _, choice := range cmd.Input.Choices {
				item.InputChoices = append(item.InputChoices, agentapi.CommandOption{Name: choice.Name, Description: choice.Description})
			}
		}
		switch cmd.Name {
		case "env", "skills":
			item.Description = "Show current " + cmd.Name + " (read-only)"
			item.InputHint = ""
			item.InputChoices = nil
			item.InputRequired = false
		case "model":
			item.Description = "Choose the model for this Task"
			item.InputHint = "model ID (omit to open settings)"
			item.InputChoices = nil
			item.InputRequired = false
		case "rename":
			item.Description = "Rename this web Task"
			item.InputHint = "Task name (omit to open rename)"
			item.InputChoices = nil
			item.InputRequired = false
		case "allow-all":
			item.Description = "Set this Task's permission policy: on (Yolo), off (Safe), show; omit to toggle"
		case "permissions":
			item.Description = "Manage this Task's Safe/Yolo permission policy"
		}
		out = append(out, item)
	}
	return out, nil
}

func (c *conversation) RunCommand(ctx context.Context, name string, args agentapi.Prompt) error {
	_, err := c.ExecuteCommand(ctx, name, args)
	return err
}

// ExecuteCommand validates the live catalog before invoking a potentially
// mutating RPC. Only explicitly supported result paths reach this call.
func (c *conversation) ExecuteCommand(ctx context.Context, name string, args agentapi.Prompt) (*agentapi.CommandResult, error) {
	c.control.Lock()
	defer c.control.Unlock()
	if c.isClosed() {
		return nil, agentapi.ErrClosed
	}
	listed, err := c.Commands(ctx)
	if err != nil {
		return nil, err
	}
	var offered *agentapi.Command
	for i := range listed {
		cmd := &listed[i]
		if cmd.Name == name {
			offered = cmd
			break
		}
		for _, alias := range cmd.Aliases {
			if alias == name {
				offered = cmd
				break
			}
		}
	}
	if offered == nil {
		return nil, fmt.Errorf("unknown command /%s", name)
	}
	if offered.DisabledReason != "" {
		return nil, errors.New(offered.DisabledReason)
	}
	name = offered.Name
	switch name {
	case "model", "rename", "allow-all", "permissions":
		return nil, errors.New("this command is handled by the web Task settings")
	}
	if offered.InputRequired && strings.TrimSpace(args.Text) == "" {
		return nil, errors.New("this command requires arguments")
	}
	promptCommand := offered.Kind == agentapi.CommandSkill || name == "init" || name == "review" || name == "blame" || name == "fleet" || name == "research" || name == "security-review"
	if !promptCommand && (len(args.Files) > 0 || len(args.Attachments) > 0) {
		return nil, errors.New("this command does not accept attachments")
	}
	// Its prompt could not start a turn: refuse before the invocation.
	c.mu.Lock()
	running := c.turnRunning
	c.mu.Unlock()
	if promptCommand && running {
		return nil, agentapi.ErrBusy
	}
	if (name == "context" || name == "usage" || name == "list-dirs" || name == "env" || name == "skills") && strings.TrimSpace(args.Text) != "" {
		return nil, errors.New("this read-only command takes no arguments")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := c.sess.InvokeCommand(ctx, name, args.Text)
	// Invocation can mutate settings before returning an error. Never describe
	// a transport failure as a safe rejection or automatically retry it.
	if err != nil {
		c.refreshExecution(ctx)
		return nil, fmt.Errorf("%w: command outcome unknown: %s", agentapi.ErrSubmissionUncertain, errText(err))
	}
	c.refreshExecution(ctx)
	result := &agentapi.CommandResult{}
	switch value := res.(type) {
	case *rpc.SlashCommandAgentPromptResult:
		if strings.TrimSpace(value.Prompt) == "" {
			return nil, fmt.Errorf("%w: command returned an empty prompt", agentapi.ErrSubmissionUncertain)
		}
		if value.Mode != nil {
			if err := c.ensureExecutionMode(ctx, *value.Mode); err != nil {
				return nil, err
			}
		}
		display := strings.TrimSpace("/" + name + " " + args.Text)
		if err := c.send(ctx, copilot.MessageOptions{Prompt: value.Prompt, DisplayPrompt: display, Attachments: attachments(args)}); err != nil {
			return nil, fmt.Errorf("%w: command resolved but prompt submission failed: %s", agentapi.ErrSubmissionUncertain, errText(err))
		}
		return nil, nil
	case *rpc.SlashCommandTextResult:
		if value.SandboxSessionChange != nil {
			return nil, fmt.Errorf("%w: unexpected sandbox change", agentapi.ErrSubmissionUncertain)
		}
		result.Kind, result.Text = "text", displaytext.Sanitize(value.Text)
		result.Markdown = value.Markdown != nil && *value.Markdown
	case *rpc.SlashCommandCompletedResult:
		if value.Mode != nil {
			if err := c.ensureExecutionMode(ctx, *value.Mode); err != nil {
				return nil, err
			}
		}
		result.Kind, result.Text = "completed", cleanPointer(value.Message)
	case *rpc.SlashCommandSelectSubcommandResult:
		result.Kind, result.Title, result.Command = "select", displaytext.Sanitize(value.Title), name
		for _, option := range value.Options {
			result.Options = append(result.Options, agentapi.CommandOption{Name: displaytext.Sanitize(option.Name), Description: displaytext.Sanitize(option.Description), Group: cleanPointer(option.Group)})
		}
	case *rpc.SlashCommandAddTimelineEntryResult:
		result.Kind, result.Text, result.PrefillInput = "text", displaytext.Sanitize(value.Entry.Text), cleanPointer(value.PrefillInput)
	default:
		return nil, fmt.Errorf("%w: command returned an unsupported result", agentapi.ErrSubmissionUncertain)
	}
	return result, nil
}

func refusePlanExit(copilot.ExitPlanModeRequest, copilot.ExitPlanModeInvocation) (copilot.ExitPlanModeResult, error) {
	return copilot.ExitPlanModeResult{Approved: false, Feedback: "Plan exit approval is not supported by the web client. Use the terminal to review the plan."}, nil
}

func (c *conversation) ensureExecutionMode(ctx context.Context, mode rpc.SessionMode) error {
	if mode != rpc.SessionModeInteractive && mode != rpc.SessionModeAutopilot {
		return fmt.Errorf("%w: unsupported execution mode %s", agentapi.ErrSubmissionUncertain, mode)
	}
	runtime, ok := c.sess.(executionSession)
	if !ok {
		return fmt.Errorf("%w: runtime mode control unavailable", agentapi.ErrSubmissionUncertain)
	}
	c.mu.Lock()
	current := c.execution
	c.mu.Unlock()
	if current != nil && current.Known && current.Mode == string(mode) {
		return nil
	}
	err := runtime.SetExecutionMode(ctx, mode)
	c.refreshExecution(ctx)
	if err != nil {
		return fmt.Errorf("%w: %s", agentapi.ErrSubmissionUncertain, errText(err))
	}
	return nil
}
