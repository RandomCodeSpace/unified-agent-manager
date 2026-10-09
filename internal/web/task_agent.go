package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// TaskAgents lists the custom agents a Task of the Project can select with
// provider: native discovery for the Project's directory, user-invocable
// agents only. It is metadata only; authored prompts and tool or MCP
// configuration are never read.
func (m *Manager) TaskAgents(projectID, provider string) ([]agentapi.ConfigurationDefinition, error) {
	m.mu.Lock()
	project := m.projects[projectID]
	var dir string
	if project != nil {
		dir = project.Dir
	}
	m.mu.Unlock()
	if project == nil {
		return nil, newError(http.StatusNotFound, "project not found")
	}
	return m.offeredAgents(provider, dir)
}

func (m *Manager) offeredAgents(provider, workdir string) ([]agentapi.ConfigurationDefinition, error) {
	m.mu.Lock()
	prov := m.providers[provider]
	supported := m.infos[provider].Capabilities.CustomAgents
	m.mu.Unlock()
	if prov == nil {
		return nil, newError(http.StatusBadRequest, msgUnknownProvider, provider)
	}
	discoverer, ok := prov.(agentapi.ConfigurationDiscoverer)
	if !supported || !ok {
		return nil, newError(http.StatusConflict, "this provider cannot select custom agents")
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	catalog, err := discoverer.DiscoverAgents(ctx, []string{workdir})
	if err != nil {
		return nil, newError(http.StatusBadGateway, "could not list custom agents: %s", shortError(err))
	}
	out := []agentapi.ConfigurationDefinition{}
	for _, agent := range catalog.Definitions[:min(len(catalog.Definitions), maxConfigurationFiles)] {
		if agent.UserInvocable == nil || *agent.UserInvocable {
			out = append(out, agent)
		}
	}
	return out, nil
}

// checkAgent refuses an agent the provider does not offer for workdir; ""
// is the provider's default agent.
func (m *Manager) checkAgent(provider, workdir, agent string) error {
	if agent == "" {
		return nil
	}
	offered, err := m.offeredAgents(provider, workdir)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(offered, func(d agentapi.ConfigurationDefinition) bool { return d.ID == agent }) {
		return newError(http.StatusBadRequest, "custom agent %q is not offered for this project", agent)
	}
	return nil
}

// SetAgent selects the Task's custom agent, "" for the provider's default
// agent, for its next turns. It changes only between turns. With the
// conversation open the agent is stored only after the provider selected
// it; otherwise the next open selects it, and fails if it is gone.
func (m *Manager) SetAgent(id, agent string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.stage != StageActive {
		m.mu.Unlock()
		return SessionSummary{}, s.readOnlyLocked()
	}
	provider, workdir, current := s.provider, s.workdir, s.agent
	m.mu.Unlock()
	if agent == current {
		return m.Summary(id)
	}
	if err := m.checkAgent(provider, workdir, agent); err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	if busy(s.state()) {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "the custom agent can change only between turns")
	}
	conv := s.conv
	m.mu.Unlock()
	if conv != nil {
		sel, ok := conv.(agentapi.AgentSelector)
		if !ok {
			return SessionSummary{}, newError(http.StatusConflict, "this provider cannot select custom agents")
		}
		ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
		err := sel.SelectAgent(ctx, agent)
		cancel()
		switch {
		case err == nil, errors.Is(err, agentapi.ErrClosed):
			// The next open selects the agent if the conversation closed.
		case errors.Is(err, agentapi.ErrBusy):
			return SessionSummary{}, newError(http.StatusConflict, "the custom agent can change only between turns")
		case errors.Is(err, agentapi.ErrAgentUnavailable):
			return SessionSummary{}, newError(http.StatusConflict, "%s", shortError(err))
		default:
			log.Warn("web agent selection failed", "session", id, "error", err)
			return SessionSummary{}, newError(http.StatusBadGateway, "could not change the custom agent: %s", shortError(err))
		}
	}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.agent = agent
	m.changedLocked(s, before)
	m.mu.Unlock()
	return m.Summary(id)
}
