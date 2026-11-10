package copilot

import (
	"context"
	"errors"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// sdkClient is the part of the Copilot SDK client the web provider drives.
// Tests replace it with a fake so no CLI or model is involved.
type sdkClient interface {
	Start(ctx context.Context) error
	Stop() error
	ForceStop()
	Ping(ctx context.Context) error
	ListModels(ctx context.Context) ([]rpc.Model, error)
	// Quota reads the signed-in account's quota snapshots by type.
	Quota(ctx context.Context) (map[string]rpc.AccountQuotaSnapshot, error)
	CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error)
	ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error)
	// DeleteSession deletes a session and its stored data for good.
	DeleteSession(ctx context.Context, id string) error
	// ReadEvents reads one page of a session's persisted journal without
	// creating, resuming or activating the session.
	ReadEvents(ctx context.Context, req *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error)
	// ListSessions lists the sessions whose working directory is exactly
	// workdir.
	ListSessions(ctx context.Context, workdir string) ([]copilot.SessionMetadata, error)
	// CheckInUse returns the ids another process holds by a live in-use lock.
	CheckInUse(ctx context.Context, ids []string) ([]string, error)
}

// sdkSession is the part of a Copilot SDK session the web provider drives.
type sdkSession interface {
	ID() string
	// WorkspacePath is the session's folder in the CLI's session state.
	WorkspacePath() string
	// Send submits a message and returns the message ID the CLI assigned to
	// it.
	Send(ctx context.Context, msg copilot.MessageOptions) (string, error)
	// SendAndWait submits a message, waits for the session to go idle and
	// returns the text of the last assistant message.
	SendAndWait(ctx context.Context, msg copilot.MessageOptions) (string, error)
	// SetName names the session through the experimental session.name.set,
	// which also stops the CLI from naming it.
	SetName(ctx context.Context, name string) error
	// Log persists a notification in the provider timeline.
	Log(ctx context.Context, text string) error
	// ListCommands lists the session's built-in commands and skills.
	ListCommands(ctx context.Context) ([]rpc.SlashCommandInfo, error)
	// InvokeCommand resolves a command; it starts no turn.
	InvokeCommand(ctx context.Context, name, input string) (rpc.SlashCommandInvocationResult, error)
	SwitchModel(ctx context.Context, req *rpc.ModelSwitchToRequest) (*rpc.ModelSwitchToResult, error)
	// AddProviders registers custom providers and models on the open
	// session through the experimental session.provider.add.
	AddProviders(ctx context.Context, providers []copilot.NamedProviderConfig, models []copilot.ProviderModelConfig) error
	SetEffort(ctx context.Context, effort string) error
	// CurrentModel reads the session's model selection.
	CurrentModel(ctx context.Context) (*rpc.CurrentModel, error)
	// ToolCatalog and SetTools verify the declaration's visible name at open.
	ToolCatalog(ctx context.Context) ([]rpc.CurrentToolMetadata, error)
	SetTools(ctx context.Context, tools []rpc.ProtocolExternalToolDefinition) error
	// CurrentAgent returns the selected custom agent, nil for the default
	// agent. SelectAgent selects one by ID and returns it; DeselectAgent
	// returns to the default agent. Either change builds the session's tool
	// set again (rebuildTools).
	CurrentAgent(ctx context.Context) (*rpc.AgentInfo, error)
	SelectAgent(ctx context.Context, id string) (*rpc.AgentInfo, error)
	DeselectAgent(ctx context.Context) error
	Abort(ctx context.Context) error
	CancelSubagent(ctx context.Context, agentID string) (bool, error)
	// ListTasks returns the tasks the CLI tracks, subagents included.
	ListTasks(ctx context.Context) ([]rpc.TaskInfo, error)
	// ReadTaskOutput reads a detached shell's output after cursor, from the
	// start when cursor is empty (background_output.go).
	ReadTaskOutput(ctx context.Context, id, cursor string) (*rpc.TasksReadOutputResult, error)
	// ReadTodos reads the rows of the session's todo list (todos.go).
	ReadTodos(ctx context.Context) (*rpc.PlanReadSQLTodosWithDependenciesResult, error)
	// MessageSubagent sends a follow-up to one agent task.
	MessageSubagent(ctx context.Context, agentID, message string) (*rpc.TasksSendMessageResult, error)
	Events(ctx context.Context) ([]copilot.SessionEvent, error)
	// RespondPermission answers a pending permission request. It reports false
	// when the CLI no longer considered the request pending.
	RespondPermission(ctx context.Context, requestID string, decision rpc.PermissionDecision) (bool, error)
	Disconnect() error
}

// rejectedError marks a request the CLI answered with a JSON-RPC error: it
// reached the CLI and was refused, so nothing was accepted.
type rejectedError struct{ err error }

func (e rejectedError) Error() string { return e.err.Error() }
func (e rejectedError) Unwrap() error { return e.err }

type sdkClientAdapter struct {
	c *copilot.Client
	// export is the CLI's usage export file, "" for none. One the CLI never
	// wrote to is removed once the CLI has stopped.
	export string
}

// ImportSupported probes optional CLI methods without opening a session.
func (a sdkClientAdapter) ImportSupported(ctx context.Context) bool {
	if _, err := a.CheckInUse(ctx, []string{}); err != nil {
		return false
	}
	_, err := a.ReadEvents(ctx, &rpc.SessionsReadPersistedEventsRequest{SessionID: "00000000-0000-4000-8000-000000000000"})
	return err == nil || strings.Contains(err.Error(), "journal is unavailable")
}

func (a sdkClientAdapter) Start(ctx context.Context) error { return a.c.Start(ctx) }
func (a sdkClientAdapter) Stop() error {
	err := a.c.Stop()
	if err == nil {
		removeEmptyExport(a.export)
	}
	return err
}
func (a sdkClientAdapter) ForceStop() { a.c.ForceStop(); removeEmptyExport(a.export) }

func (a sdkClientAdapter) Ping(ctx context.Context) error {
	_, err := a.c.Ping(ctx, "")
	return err
}

// ListModels sends the same models.list request as Client.ListModels, which
// caches its answer until the CLI stops; an entitlement change must show up
// while the service runs.
func (a sdkClientAdapter) ListModels(ctx context.Context) ([]rpc.Model, error) {
	res, err := a.c.RPC.Models.List(ctx, &rpc.ModelsListRequest{})
	if err != nil {
		return nil, err
	}
	return res.Models, nil
}

// Quota sends the experimental account.getQuota request.
func (a sdkClientAdapter) Quota(ctx context.Context) (map[string]rpc.AccountQuotaSnapshot, error) {
	res, err := a.c.RPC.Account.GetQuota(ctx, &rpc.AccountGetQuotaRequest{})
	if err != nil {
		return nil, err
	}
	return res.QuotaSnapshots, nil
}

func (a sdkClientAdapter) CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	s, err := a.c.CreateSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return sdkSessionAdapter{s}, nil
}

func (a sdkClientAdapter) DeleteSession(ctx context.Context, id string) error {
	return a.c.DeleteSession(ctx, id)
}

func (a sdkClientAdapter) ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error) {
	s, err := a.c.ResumeSession(ctx, id, cfg)
	if err != nil {
		return nil, err
	}
	return sdkSessionAdapter{s}, nil
}

func (a sdkClientAdapter) ReadEvents(ctx context.Context, req *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error) {
	return a.c.RPC.Sessions.ReadPersistedEvents(ctx, req)
}

func (a sdkClientAdapter) ListSessions(ctx context.Context, workdir string) ([]copilot.SessionMetadata, error) {
	if workdir == "" {
		return a.c.ListSessions(ctx, nil)
	}
	return a.c.ListSessions(ctx, &copilot.SessionListFilter{WorkingDirectory: workdir})
}

func (a sdkClientAdapter) CheckInUse(ctx context.Context, ids []string) ([]string, error) {
	res, err := a.c.RPC.Sessions.CheckInUse(ctx, &rpc.SessionsCheckInUseRequest{SessionIDs: ids})
	if err != nil {
		return nil, err
	}
	return res.InUse, nil
}

type sdkSessionAdapter struct{ s *copilot.Session }

func (a sdkSessionAdapter) ID() string                      { return a.s.SessionID }
func (a sdkSessionAdapter) WorkspacePath() string           { return a.s.WorkspacePath() }
func (a sdkSessionAdapter) Abort(ctx context.Context) error { return a.s.Abort(ctx) }
func (a sdkSessionAdapter) Disconnect() error               { return a.s.Disconnect() }
func (a sdkSessionAdapter) Events(ctx context.Context) ([]copilot.SessionEvent, error) {
	return a.s.GetEvents(ctx)
}

func (a sdkSessionAdapter) CancelSubagent(ctx context.Context, agentID string) (bool, error) {
	result, err := a.s.RPC.Tasks.Cancel(ctx, &rpc.TasksCancelRequest{ID: agentID})
	if err != nil {
		return false, err
	}
	return result.Cancelled, nil
}

func (a sdkSessionAdapter) ListTasks(ctx context.Context) ([]rpc.TaskInfo, error) {
	if raw, ok, err := listTasksRaw(ctx, a.s.RPC.Tasks, a.s.SessionID); ok {
		if err != nil {
			return nil, err
		}
		return decodeTaskList(raw)
	}
	result, err := a.s.RPC.Tasks.List(ctx)
	if err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

func (a sdkSessionAdapter) MessageSubagent(ctx context.Context, agentID, message string) (*rpc.TasksSendMessageResult, error) {
	result, err := a.s.RPC.Tasks.SendMessage(ctx, &rpc.TasksSendMessageRequest{ID: agentID, Message: message})
	if err != nil && isRPCError(err) {
		return nil, rejectedError{err}
	}
	return result, err
}

func (a sdkSessionAdapter) SwitchModel(ctx context.Context, req *rpc.ModelSwitchToRequest) (*rpc.ModelSwitchToResult, error) {
	return a.s.RPC.Model.SwitchTo(ctx, req)
}

func (a sdkSessionAdapter) AddProviders(ctx context.Context, providers []copilot.NamedProviderConfig, models []copilot.ProviderModelConfig) error {
	_, err := a.s.RPC.Provider.Add(ctx, addProviders(providers, models))
	return err
}

func (a sdkSessionAdapter) CurrentModel(ctx context.Context) (*rpc.CurrentModel, error) {
	return a.s.RPC.Model.GetCurrent(ctx)
}

func (a sdkSessionAdapter) SetEffort(ctx context.Context, effort string) error {
	_, err := a.s.RPC.Model.SetReasoningEffort(ctx, &rpc.ModelSetReasoningEffortRequest{ReasoningEffort: effort})
	return err
}

func (a sdkSessionAdapter) Send(ctx context.Context, msg copilot.MessageOptions) (string, error) {
	id, err := a.s.Send(ctx, msg)
	if err != nil && isRPCError(err) {
		return "", rejectedError{err}
	}
	return id, err
}

func (a sdkSessionAdapter) SendAndWait(ctx context.Context, msg copilot.MessageOptions) (string, error) {
	ev, err := a.s.SendAndWait(ctx, msg)
	if err != nil || ev == nil {
		return "", err
	}
	if d, ok := ev.Data.(*copilot.AssistantMessageData); ok {
		return d.Content, nil
	}
	return "", nil
}

func (a sdkSessionAdapter) Log(ctx context.Context, text string) error {
	return a.s.Log(ctx, text, &copilot.LogOptions{Ephemeral: copilot.Bool(false)})
}

// QuotaInfo reads the session's quota and account projection through the
// experimental session.quota.get, which makes no network request.
func (a sdkSessionAdapter) QuotaInfo(ctx context.Context) (*rpc.SessionQuotaGetResult, error) {
	return a.s.RPC.Quota.Get(ctx)
}

func (a sdkSessionAdapter) SetName(ctx context.Context, name string) error {
	_, err := a.s.RPC.Name.Set(ctx, &rpc.NameSetRequest{Name: name})
	return err
}

func (a sdkSessionAdapter) ListCommands(ctx context.Context) ([]rpc.SlashCommandInfo, error) {
	res, err := a.s.RPC.Commands.List(ctx, &rpc.SessionCommandsListRequest{
		IncludeBuiltins: copilot.Bool(true), IncludeSkills: copilot.Bool(true), IncludeClientCommands: copilot.Bool(false),
	})
	if err != nil {
		return nil, err
	}
	return res.Commands, nil
}

func (a sdkSessionAdapter) InvokeCommand(ctx context.Context, name, input string) (rpc.SlashCommandInvocationResult, error) {
	return a.s.RPC.Commands.Invoke(ctx, &rpc.CommandsInvokeRequest{Name: name, Input: &input})
}

func (a sdkSessionAdapter) RespondPermission(ctx context.Context, requestID string, decision rpc.PermissionDecision) (bool, error) {
	res, err := a.s.RPC.Permissions.HandlePendingPermissionRequest(ctx, &rpc.PermissionDecisionRequest{RequestID: requestID, Result: decision})
	if err != nil {
		return false, err
	}
	return res.Success, nil
}

// isRPCError reports whether err carries the SDK's JSON-RPC error response.
func isRPCError(err error) bool {
	var rpcErr *copilot.RPCError
	return errors.As(err, &rpcErr)
}
