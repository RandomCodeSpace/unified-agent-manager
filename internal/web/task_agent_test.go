package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func agentManager(t *testing.T) (*Manager, *configurationProvider, string) {
	t.Helper()
	caps := allCaps
	caps.CustomAgents = true
	subagentOnly := false
	prov := &configurationProvider{Provider: agenttest.NewProvider("fake", caps), agents: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{
		{ID: "reviewer", Name: "reviewer", DisplayName: "Reviewer", Source: "project"},
		{ID: "helper", Name: "helper", Source: "user", UserInvocable: &subagentOnly},
	}}}
	m := startManager(t, openTestStore(t), prov)
	return m, prov, addProject(t, m, t.TempDir())
}

func agentGone(id string) error {
	return fmt.Errorf("%w: %q: Custom agent '%s' not found", agentapi.ErrAgentUnavailable, id, id)
}

// The picker offers the user-invocable agents native discovery finds for
// the Project's directory, never subagent-only ones.
func TestTaskAgentsListsUserInvocableAgents(t *testing.T) {
	m, prov, project := agentManager(t)
	agents, err := m.TaskAgents(project, "fake")
	if err != nil || len(agents) != 1 || agents[0].ID != "reviewer" || !slices.Equal(prov.projects[0], []string{m.projects[project].Dir}) {
		t.Fatalf("agents = %+v, %v; projects %v", agents, err, prov.projects)
	}
	if _, err := m.TaskAgents("missing", "fake"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown project = %v", err)
	}
	prov.agentsErr = errors.New("discovery broke")
	if _, err := m.TaskAgents(project, "fake"); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("failed discovery = %v", err)
	}
	plain := startManager(t, openTestStore(t), agenttest.NewProvider("plain", allCaps))
	if _, err := plain.TaskAgents(addProject(t, plain, t.TempDir()), "plain"); statusOf(err) != http.StatusConflict {
		t.Fatalf("provider without custom agents = %v", err)
	}
}

// A Task created with an agent opens with it, records it, and gets it back
// on every reopen, also after a restart, before anything is sent.
func TestTaskAgentIsAppliedOnCreateAndEveryReopen(t *testing.T) {
	m, prov, project := agentManager(t)
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Agent: "helper"}); statusOf(err) != http.StatusBadRequest || len(prov.Opens()) != 0 {
		t.Fatalf("subagent-only agent = %v; opens %d", err, len(prov.Opens()))
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Agent: "reviewer"})
	if err != nil || sum.Agent != "reviewer" || prov.Opens()[0].Agent != "reviewer" {
		t.Fatalf("create = %+v, %v; open %+v", sum, err, prov.Opens())
	}
	waitUntil(t, "agent persisted", func() bool {
		rec, _ := loadRecord(t, m.store, "fake", sum.ID)
		return rec.Web != nil && rec.Web.Agent == "reviewer"
	})
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if reopened := prov.Last(); reopened.Request().Agent != "reviewer" || len(reopened.Sends()) != 1 {
		t.Fatalf("reopen request %+v, sends %q", reopened.Request(), reopened.Sends())
	}

	// A restarted service reads the agent back from the record.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, m.store, prov)
	if err := restarted.View(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Summary(sum.ID); err != nil || got.Agent != "reviewer" {
		t.Fatalf("restarted summary = %+v, %v", got, err)
	}
	if _, err := restarted.Submit(sum.ID, PromptRequest{Text: "after restart", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if reopened := prov.Last(); reopened.Request().Agent != "reviewer" || len(reopened.Sends()) != 1 {
		t.Fatalf("restarted reopen request %+v, sends %q", reopened.Request(), reopened.Sends())
	}
}

// A Task whose agent is gone does not open with the default agent: the
// open fails with a clear reason and nothing is sent, until the owner
// chooses another agent or the default one.
func TestReopenFailsClosedWhenTheTaskAgentIsGone(t *testing.T) {
	m, prov, project := agentManager(t)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Agent: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	prov.SetOpenError(agentGone("reviewer"))
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend})
	d := detail(t, m, sum.ID)
	if err != nil || sub.Status != SubmissionRejected || d.State != StateFailed || !strings.Contains(d.StateDetail, `"reviewer"`) || !strings.Contains(d.StateDetail, "choose another agent") {
		t.Fatalf("submit = %+v, %v; state %s %q", sub, err, d.State, d.StateDetail)
	}
	if len(prov.Opens()) != opens+1 || prov.Opens()[opens].Agent != "reviewer" {
		t.Fatalf("opens %+v", prov.Opens())
	}
	prov.SetOpenError(nil)
	if got, err := m.SetAgent(sum.ID, ""); err != nil || got.Agent != "" {
		t.Fatalf("back to the default agent = %+v, %v", got, err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "default", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if reopened := prov.Last(); reopened.Request().Agent != "" || len(reopened.Sends()) != 1 {
		t.Fatalf("reopen request %+v, sends %q", reopened.Request(), reopened.Sends())
	}
}

// The agent changes only between turns, only to an offered agent, and is
// stored only once the open conversation selected it.
func TestSetAgentOnlyBetweenTurns(t *testing.T) {
	m, _, project := agentManager(t)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
	if err != nil || sum.Agent != "" {
		t.Fatalf("create = %+v, %v", sum, err)
	}
	conv := m.sessions[sum.ID].conv.(*agenttest.Conversation)
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.SetAgent(sum.ID, "reviewer"); statusOf(err) != http.StatusConflict || len(conv.AgentSelections()) != 0 {
		t.Fatalf("change during a turn = %v; selections %v", err, conv.AgentSelections())
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	for _, agent := range []string{"helper", "missing"} {
		if _, err := m.SetAgent(sum.ID, agent); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s = %v", agent, err)
		}
	}
	got, err := m.SetAgent(sum.ID, "reviewer")
	if err != nil || got.Agent != "reviewer" || !slices.Equal(conv.AgentSelections(), []string{"reviewer"}) {
		t.Fatalf("change = %+v, %v; selections %v", got, err, conv.AgentSelections())
	}
	waitUntil(t, "agent persisted", func() bool {
		rec, _ := loadRecord(t, m.store, "fake", sum.ID)
		return rec.Web != nil && rec.Web.Agent == "reviewer"
	})
	conv.SetSelectAgentError(errors.New("rpc broke"))
	if _, err := m.SetAgent(sum.ID, ""); statusOf(err) != http.StatusBadGateway || detail(t, m, sum.ID).Agent != "reviewer" {
		t.Fatalf("refused change = %v", err)
	}
	conv.SetSelectAgentError(agentGone("reviewer"))
	if _, err := m.SetAgent(sum.ID, ""); statusOf(err) != http.StatusConflict || detail(t, m, sum.ID).Agent != "reviewer" {
		t.Fatalf("unavailable change = %v", err)
	}
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAgent(sum.ID, ""); err == nil {
		t.Fatal("a settled Task changed its agent")
	}
}

// A reload that loses the Task's agent disconnects the Task instead of
// leaving its next turn to the default agent; the next open selects the
// agent again or fails.
func TestReloadThatLosesTheAgentDisconnectsTheTask(t *testing.T) {
	m, prov, sum, conv, wrapped := reloadMCPTask(t)
	m.mu.Lock()
	m.sessions[sum.ID].agent = "reviewer"
	m.mu.Unlock()
	wrapped.reload = func(context.Context) error {
		return fmt.Errorf("reload task configuration: %w", agentGone("reviewer"))
	}
	err := m.ReconnectTaskMCP(sum.ID)
	if statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), `"reviewer"`) {
		t.Fatalf("reconnect = %v", err)
	}
	if d := detail(t, m, sum.ID); d.Open || conv.Closes() != 1 || d.Agent != "reviewer" {
		t.Fatalf("open %t closes %d agent %q", d.Open, conv.Closes(), d.Agent)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if reopened := prov.Last(); reopened == conv || reopened.Request().Agent != "reviewer" {
		t.Fatalf("reopen request %+v", reopened.Request())
	}
}
