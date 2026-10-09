package copilot

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

var reviewerAgent = rpc.AgentInfo{ID: "reviewer", Name: "reviewer", DisplayName: "Reviewer"}

func openAgent(t *testing.T, fc *fakeClient, convID, agent string) (agentapi.Conversation, error) {
	t.Helper()
	return openAgentWith(t, fc, agentapi.OpenRequest{ConversationID: convID, Agent: agent})
}

func openAgentWith(t *testing.T, fc *fakeClient, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	t.Helper()
	if fc.catalog == nil {
		fc.catalog = catalogOf(declarationToolName, "notes_get", "notes_list")
	}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	req.SessionID, req.Workdir, req.Events = "session", t.TempDir(), &recSink{}
	req.Tools, req.CallTool = notesTools(), (&hostCalls{}).call
	req.ValidateFile = func(_ context.Context, p string) (string, error) { return filepath.Join("/tmp", p), nil }
	return p.Open(context.Background(), req)
}

// Create and every resume select the Task's agent before the first tool
// proof and before anything is sent, so the proof sees that agent's tools.
func TestOpenSelectsTheTaskAgentFirst(t *testing.T) {
	for name, convID := range map[string]string{"create": "", "resume": "conv"} {
		t.Run(name, func(t *testing.T) {
			fc := &fakeClient{agents: []rpc.AgentInfo{reviewerAgent}}
			conv, err := openAgent(t, fc, convID, "reviewer")
			if err != nil {
				t.Fatal(err)
			}
			firstProof(conv)
			fs := fc.sessions[0]
			if want := slices.Concat([]string{"select reviewer"}, catalogProof); !slices.Equal(fs.toolCalls, want) || fs.agent == nil || fs.agent.ID != "reviewer" {
				t.Fatalf("tool and agent RPCs = %v, agent %v", fs.toolCalls, fs.agent)
			}
			if conv.(*conversation).agent != "reviewer" || len(fc.deleted) != 0 {
				t.Fatalf("agent %q, deleted %v", conv.(*conversation).agent, fc.deleted)
			}
		})
	}
	// No agent: the session stays on the default agent, as before.
	fc := &fakeClient{agents: []rpc.AgentInfo{reviewerAgent}}
	conv, err := openAgent(t, fc, "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	firstProof(conv)
	if fs := fc.sessions[0]; !slices.Equal(fs.toolCalls, catalogProof) || fs.agent != nil {
		t.Fatalf("default open RPCs = %v, agent %v", fs.toolCalls, fs.agent)
	}
}

// An agent that no longer exists fails the open: the conversation is not
// used with the default agent instead, and a session the open created is
// deleted rather than left behind.
func TestOpenFailsClosedWhenTheTaskAgentIsGone(t *testing.T) {
	for name, convID := range map[string]string{"create": "", "resume": "conv"} {
		t.Run(name, func(t *testing.T) {
			fc := &fakeClient{}
			conv, err := openAgent(t, fc, convID, "reviewer")
			if conv != nil || !errors.Is(err, agentapi.ErrAgentUnavailable) || !strings.Contains(err.Error(), `"reviewer"`) || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("open = %t, %v", conv != nil, err)
			}
			fs := fc.sessions[0]
			if !fs.disconnected || len(fs.sent) != 0 || !slices.Equal(fs.toolCalls, []string{"select reviewer", "disconnect"}) {
				t.Fatalf("disconnected %t, sent %v, RPCs %v", fs.disconnected, fs.sent, fs.toolCalls)
			}
			if created := convID == ""; created != slices.Equal(fc.deleted, []string{"session"}) || (!created && len(fc.deleted) != 0) {
				t.Fatalf("deleted %v", fc.deleted)
			}
		})
	}
}

// After a turn the CLI's catalog keeps cleared tools until the tool set is
// built again. With a custom agent selected, the proof builds it again by
// selecting that agent again, never by returning to the default agent, so
// the uam host tools stay available and the agent stays selected.
func TestSelectedAgentKeepsHostToolsAfterAStaleCatalog(t *testing.T) {
	fc := &fakeClient{agents: []rpc.AgentInfo{reviewerAgent}}
	conv, err := openAgent(t, fc, "", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	firstProof(conv)
	fs := fc.sessions[0]
	tools := fc.create[0].Tools
	fs.staleTools = true
	ctx := context.Background()
	if err := conv.Send(ctx, agentapi.Prompt{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	toolsChanged(fs, "mid-turn")
	fs.onEvent(ev("idle", &rpc.SessionIdleData{}))
	if err := conv.Send(ctx, agentapi.Prompt{Text: "next"}); err != nil {
		t.Fatal(err)
	}
	want := slices.Concat([]string{"select reviewer"}, catalogProof, []string{"clear", "catalog", "select reviewer", "catalog", "restore", "catalog"})
	if !slices.Equal(fs.toolCalls, want) || !toolsAnswer(t, tools, "next") || fs.agent == nil || fs.agent.ID != "reviewer" {
		t.Fatalf("tool and agent RPCs %v, agent %v", fs.toolCalls, fs.agent)
	}
}

// The agent changes only between turns. A change proves the uam tools
// again before the next message; an agent that is gone changes nothing.
func TestSelectAgentBetweenTurns(t *testing.T) {
	conv, fs, tools := openProved(t, &fakeClient{agents: []rpc.AgentInfo{reviewerAgent}})
	sel := conv.(agentapi.AgentSelector)
	ctx := context.Background()
	if err := conv.Send(ctx, agentapi.Prompt{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := sel.SelectAgent(ctx, "reviewer"); !errors.Is(err, agentapi.ErrBusy) || fs.agent != nil {
		t.Fatalf("select during a turn = %v, agent %v", err, fs.agent)
	}
	fs.onEvent(ev("idle", &rpc.SessionIdleData{}))
	if err := sel.SelectAgent(ctx, "reviewer"); err != nil || fs.agent == nil || conv.(*conversation).agent != "reviewer" {
		t.Fatalf("select = %v, agent %v", err, fs.agent)
	}
	calls := len(fs.toolCalls)
	if err := conv.Send(ctx, agentapi.Prompt{Text: "under the agent"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fs.toolCalls[calls:], catalogProof) || !toolsAnswer(t, tools, "agent") {
		t.Fatalf("after the change: RPCs %v", fs.toolCalls[calls:])
	}
	fs.onEvent(ev("idle-2", &rpc.SessionIdleData{}))
	if err := sel.SelectAgent(ctx, "gone"); !errors.Is(err, agentapi.ErrAgentUnavailable) || fs.agent.ID != "reviewer" || conv.(*conversation).agent != "reviewer" {
		t.Fatalf("missing agent = %v, agent %v", err, fs.agent)
	}
	if err := sel.SelectAgent(ctx, ""); err != nil || fs.agent != nil || conv.(*conversation).agent != "" || fs.toolCalls[len(fs.toolCalls)-1] != "deselect" {
		t.Fatalf("default = %v, agent %v, RPCs %v", err, fs.agent, fs.toolCalls)
	}
	_ = conv.Close(ctx)
	if err := sel.SelectAgent(ctx, "reviewer"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed = %v", err)
	}
}

// A configuration reload reads the agent definitions again; the Task's
// agent is selected again, and one that is gone fails the reload.
func TestReloadSelectsTheTaskAgentAgain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agents []rpc.AgentInfo
		err    error
	}{
		{name: "kept", agents: []rpc.AgentInfo{reviewerAgent}},
		{name: "kept after a partial reload", agents: []rpc.AgentInfo{reviewerAgent}, err: errors.New("MCP failed")},
		{name: "gone"},
		{name: "gone after a partial reload", err: errors.New("MCP failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{agents: []rpc.AgentInfo{reviewerAgent}}
			conv, err := openAgent(t, fc, "conv", "reviewer")
			if err != nil {
				t.Fatal(err)
			}
			firstProof(conv)
			c := conv.(*conversation)
			fs := &reloadSession{fakeSession: fc.sessions[0], result: &rpc.CustomizationsReloadResult{}, err: tc.err}
			fs.agents = tc.agents
			c.sess = fs
			err = c.ReloadCustomizations(context.Background())
			gone := len(tc.agents) == 0
			if errors.Is(err, agentapi.ErrAgentUnavailable) != gone || (err == nil) != (tc.err == nil && !gone) || fs.toolCalls[len(fs.toolCalls)-1] != "select reviewer" {
				t.Fatalf("reload = %v; RPCs %v", err, fs.toolCalls)
			}
		})
	}
}

// The Copilot adapter offers agent selection on its conversations.
func TestCustomAgentsCapability(t *testing.T) {
	p := newWebProvider(func() (sdkClient, error) { return &fakeClient{}, nil }, time.Hour)
	if !p.Capabilities().CustomAgents {
		t.Fatal("custom agents capability is off")
	}
	var _ agentapi.AgentSelector = (*conversation)(nil)
}

// pinnedAgent authors its own model and effort, which selecting it applies.
var pinnedAgent = rpc.AgentInfo{ID: "pinned", Name: "pinned", Model: copilot.String("gpt-6-luna"), ReasoningEffort: copilot.String("high")}

const taskModel, taskEffort = "ollama/deepseek-v4.1-flash", "low"

func keptTaskModel(t *testing.T, fs *fakeSession, when string) {
	t.Helper()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if deref(fs.current.ModelID) != taskModel || deref(fs.current.ReasoningEffort) != taskEffort {
		t.Fatalf("%s: model %q effort %q; RPCs %v", when, deref(fs.current.ModelID), deref(fs.current.ReasoningEffort), fs.toolCalls)
	}
}

func openPinned(t *testing.T, convID string) (agentapi.Conversation, *fakeClient, *fakeSession) {
	t.Helper()
	fc := &fakeClient{agents: []rpc.AgentInfo{pinnedAgent, reviewerAgent}}
	conv, err := openAgentWith(t, fc, agentapi.OpenRequest{ConversationID: convID, Agent: "pinned", Model: taskModel, Effort: taskEffort, ContextSize: "default"})
	if err != nil {
		t.Fatal(err)
	}
	firstProof(conv)
	return conv, fc, fc.sessions[0]
}

// An agent's authored model and effort never replace the Task's: create and
// resume restore the Task's selection right after selecting the agent and
// before the first tool proof.
func TestAgentModelDoesNotReplaceTheTaskModelOnOpen(t *testing.T) {
	for name, convID := range map[string]string{"create": "", "resume": "conv"} {
		t.Run(name, func(t *testing.T) {
			_, _, fs := openPinned(t, convID)
			if want := slices.Concat([]string{"select pinned", "model " + taskModel}, catalogProof); !slices.Equal(fs.toolCalls, want) {
				t.Fatalf("RPCs %v, want %v", fs.toolCalls, want)
			}
			keptTaskModel(t, fs, name)
		})
	}
	// Without a known Task selection, the runtime's model from before the
	// selection is restored.
	fc := &fakeClient{agents: []rpc.AgentInfo{pinnedAgent}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	fc.catalog = catalogOf(declarationToolName, "notes_get", "notes_list")
	conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "session", ConversationID: "conv", Workdir: t.TempDir(), Events: &recSink{}})
	if err != nil {
		t.Fatal(err)
	}
	fs := fc.sessions[0]
	fs.current = rpc.CurrentModel{ModelID: copilot.String(taskModel), ReasoningEffort: copilot.String(taskEffort)}
	if err := conv.(agentapi.AgentSelector).SelectAgent(context.Background(), "pinned"); err != nil {
		t.Fatal(err)
	}
	keptTaskModel(t, fs, "untracked")
}

// A change between turns, the stale-catalog rebuild and a reload select the
// agent again; each restores the Task's model before anything is sent.
func TestAgentModelDoesNotReplaceTheTaskModelLater(t *testing.T) {
	ctx := context.Background()
	t.Run("change", func(t *testing.T) {
		fc := &fakeClient{agents: []rpc.AgentInfo{pinnedAgent}}
		conv, err := openAgentWith(t, fc, agentapi.OpenRequest{Model: taskModel, Effort: taskEffort, ContextSize: "default"})
		if err != nil {
			t.Fatal(err)
		}
		firstProof(conv)
		fs := fc.sessions[0]
		calls := len(fs.toolCalls)
		if err := conv.(agentapi.AgentSelector).SelectAgent(ctx, "pinned"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(fs.toolCalls[calls:], []string{"select pinned", "model " + taskModel}) {
			t.Fatalf("RPCs %v", fs.toolCalls[calls:])
		}
		keptTaskModel(t, fs, "change")
		// A later SetModel is the selection the next agent change keeps.
		if err := conv.SetModel(ctx, "gpt-6-luna", "", "default"); err != nil {
			t.Fatal(err)
		}
		if err := conv.(agentapi.AgentSelector).SelectAgent(ctx, ""); err != nil || deref(fs.current.ModelID) != "gpt-6-luna" {
			t.Fatalf("default agent = %v, model %q", err, deref(fs.current.ModelID))
		}
	})
	t.Run("rebuild", func(t *testing.T) {
		conv, fc, fs := openPinned(t, "")
		fs.staleTools = true
		if err := conv.Send(ctx, agentapi.Prompt{Text: "first"}); err != nil {
			t.Fatal(err)
		}
		toolsChanged(fs, "mid-turn")
		fs.onEvent(ev("idle", &rpc.SessionIdleData{}))
		calls := len(fs.toolCalls)
		if err := conv.Send(ctx, agentapi.Prompt{Text: "next"}); err != nil {
			t.Fatal(err)
		}
		want := []string{"clear", "catalog", "select pinned", "model " + taskModel, "catalog", "restore", "catalog"}
		if !slices.Equal(fs.toolCalls[calls:], want) || !toolsAnswer(t, fc.create[0].Tools, "next") {
			t.Fatalf("RPCs %v, want %v", fs.toolCalls[calls:], want)
		}
		keptTaskModel(t, fs, "rebuild")
	})
	t.Run("reload", func(t *testing.T) {
		conv, _, fs := openPinned(t, "conv")
		c := conv.(*conversation)
		rs := &reloadSession{fakeSession: fs, result: &rpc.CustomizationsReloadResult{}}
		c.sess = rs
		calls := len(fs.toolCalls)
		if err := c.ReloadCustomizations(ctx); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(fs.toolCalls[calls:], []string{"select pinned", "model " + taskModel}) {
			t.Fatalf("RPCs %v", fs.toolCalls[calls:])
		}
		keptTaskModel(t, fs, "reload")
	})
}

// A Task model that cannot be restored after an agent change fails closed:
// the open fails, and an open conversation ends, so no turn runs on the
// agent's model.
func TestAgentChangeFailsClosedWhenTheTaskModelCannotBeRestored(t *testing.T) {
	for name, convID := range map[string]string{"create": "", "resume": "conv"} {
		t.Run(name, func(t *testing.T) {
			fc := &fakeClient{agents: []rpc.AgentInfo{pinnedAgent}, switchErr: errors.New("switch broke")}
			conv, err := openAgentWith(t, fc, agentapi.OpenRequest{ConversationID: convID, Agent: "pinned", Model: taskModel, Effort: taskEffort, ContextSize: "default"})
			if conv != nil || !errors.Is(err, agentapi.ErrAgentUnavailable) || !errors.Is(err, errModelNotKept) {
				t.Fatalf("open = %t, %v", conv != nil, err)
			}
			fs := fc.sessions[0]
			if !fs.disconnected || len(fs.sent) != 0 || slices.Contains(fs.toolCalls, "clear") {
				t.Fatalf("disconnected %t, sent %v, RPCs %v", fs.disconnected, fs.sent, fs.toolCalls)
			}
			if created := convID == ""; created != slices.Equal(fc.deleted, []string{"session"}) {
				t.Fatalf("deleted %v", fc.deleted)
			}
		})
	}
	t.Run("change", func(t *testing.T) {
		fc := &fakeClient{agents: []rpc.AgentInfo{pinnedAgent}}
		conv, err := openAgentWith(t, fc, agentapi.OpenRequest{Model: taskModel, Effort: taskEffort, ContextSize: "default"})
		if err != nil {
			t.Fatal(err)
		}
		firstProof(conv)
		fs := fc.sessions[0]
		fs.modelErr = errors.New("switch broke")
		ctx := context.Background()
		if err := conv.(agentapi.AgentSelector).SelectAgent(ctx, "pinned"); !errors.Is(err, agentapi.ErrAgentUnavailable) || !errors.Is(err, errModelNotKept) {
			t.Fatalf("select = %v", err)
		}
		if err := conv.Send(ctx, agentapi.Prompt{Text: "on the agent's model"}); !errors.Is(err, agentapi.ErrClosed) || len(fs.sent) != 0 {
			t.Fatalf("send after a failed restore = %v, sent %v", err, fs.sent)
		}
	})
}
