package web

import (
	"context"
	"net/http"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

func (m *Manager) mcpStatusLocked(s *webSession, snapshot agentapi.MCPStatusSnapshot) {
	if len(snapshot.Servers) > agentapi.MaxMCPStatusServers {
		snapshot.Servers = snapshot.Servers[:agentapi.MaxMCPStatusServers]
		snapshot.Truncated = true
	}
	rows := snapshot.Servers
	snapshot.Servers = []agentapi.MCPServerStatus{}
	for _, row := range rows {
		if !listedMCPName(row.Name) {
			snapshot.Truncated = true
			continue
		}
		row.Error = clipRunes(displaytext.Sanitize(row.Error), maxDetailRunes)
		row.Status = clipRunes(displaytext.Sanitize(row.Status), maxNameRunes)
		row.Source = clipRunes(displaytext.Sanitize(row.Source), maxNameRunes)
		if i := slices.IndexFunc(snapshot.Servers, func(before agentapi.MCPServerStatus) bool { return before.Name == row.Name }); i >= 0 {
			snapshot.Servers[i] = row
		} else {
			snapshot.Servers = append(snapshot.Servers, row)
		}
	}
	before := s.mcpStatus
	if before != nil && before.Supported == snapshot.Supported && before.Ready == snapshot.Ready && before.Truncated == snapshot.Truncated && slices.Equal(before.Servers, snapshot.Servers) {
		return
	}
	s.mcpStatus = &snapshot
	s.mcpRevision++
	m.broadcastLocked("mcp_status", s.id, func(seq uint64) any { return mcpStatusEvent{Seq: seq, SessionID: s.id, MCPStatus: snapshot} })
}

func (m *Manager) forgetMCPStatusLocked(s *webSession) {
	if s.mcpStatus != nil {
		m.mcpStatusLocked(s, agentapi.MCPStatusSnapshot{Supported: s.mcpStatus.Supported, Servers: []agentapi.MCPServerStatus{}})
	}
}

func (m *Manager) TaskMCPStatus(id string) (agentapi.MCPStatusSnapshot, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.MCPStatusSnapshot{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	c, err := m.taskMCPLocked(s)
	if err != nil {
		return agentapi.MCPStatusSnapshot{}, err
	}
	m.mu.Lock()
	gen, revision := s.gen, s.mcpRevision
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	var snapshot agentapi.MCPStatusSnapshot
	if reader, ok := c.(agentapi.MCPStatusReader); ok {
		snapshot, err = reader.MCPStatusSnapshot(ctx)
	} else {
		var servers []agentapi.MCPStatus
		servers, err = c.MCPStatus(ctx)
		snapshot = agentapi.MCPStatusSnapshot{Ready: true, Servers: []agentapi.MCPServerStatus{}}
		for _, row := range servers {
			snapshot.Servers = append(snapshot.Servers, agentapi.MCPServerStatus{Name: row.Name, Status: row.Status, Error: row.Error, Source: row.Source, Remote: row.Remote, SignIn: row.SignIn})
		}
	}
	if err != nil {
		return agentapi.MCPStatusSnapshot{}, mcpFailure(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.gen != gen {
		return agentapi.MCPStatusSnapshot{}, mcpFailure(agentapi.ErrClosed)
	}
	if s.mcpRevision == revision {
		m.mcpStatusLocked(s, snapshot)
	}
	snapshot = *s.mcpStatus
	snapshot.Servers = slices.Clone(snapshot.Servers)
	return snapshot, nil
}

func (m *Manager) TaskMCPTools(id, name string) ([]agentapi.MCPTool, error) {
	if !listedMCPName(name) {
		return nil, newError(http.StatusNotFound, "MCP server not found")
	}
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	c, err := m.taskMCPLocked(s)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	gen := s.gen
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	var tools []agentapi.MCPTool
	if reader, ok := c.(agentapi.MCPStatusReader); ok {
		tools, err = reader.MCPTools(ctx, name)
	} else {
		var servers []agentapi.MCPStatus
		servers, err = c.MCPStatus(ctx)
		if err == nil {
			i := slices.IndexFunc(servers, func(row agentapi.MCPStatus) bool { return row.Name == name })
			if i < 0 {
				err = newError(http.StatusNotFound, "MCP server not found")
			} else {
				tools = servers[i].Tools
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.gen != gen {
		return nil, mcpFailure(agentapi.ErrClosed)
	}
	if err != nil {
		return nil, mcpFailure(err)
	}
	if tools == nil {
		tools = []agentapi.MCPTool{}
	}
	return tools, nil
}
