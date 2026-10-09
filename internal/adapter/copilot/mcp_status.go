package copilot

import (
	"context"
	"fmt"
	"slices"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

func copyMCPStatus(snapshot agentapi.MCPStatusSnapshot) agentapi.MCPStatusSnapshot {
	snapshot.Servers = append([]agentapi.MCPServerStatus{}, snapshot.Servers...)
	return snapshot
}

func (c *conversation) publishMCPStatusLocked(snapshot agentapi.MCPStatusSnapshot) {
	before := c.mcpStatus
	if before.Supported == snapshot.Supported && before.Ready == snapshot.Ready && before.Truncated == snapshot.Truncated && slices.Equal(before.Servers, snapshot.Servers) {
		return
	}
	c.mcpStatus = copyMCPStatus(snapshot)
	c.mcpRevision++
	snapshot = copyMCPStatus(snapshot)
	c.emitLocked(agentapi.Event{Kind: agentapi.EventMCPStatus, MCPStatus: &snapshot})
}

func boundedMCPStatus(snapshot *agentapi.MCPStatusSnapshot, row agentapi.MCPServerStatus) {
	if row.Name == "" || len(row.Name) > 256 {
		snapshot.Truncated = true
		return
	}
	row.Error = clip(displaytext.Sanitize(row.Error), maxErrorText)
	row.Status = clip(displaytext.Sanitize(row.Status), 128)
	row.Source = clip(displaytext.Sanitize(row.Source), 128)
	i := slices.IndexFunc(snapshot.Servers, func(s agentapi.MCPServerStatus) bool { return s.Name == row.Name })
	if i >= 0 {
		snapshot.Servers[i] = row
	} else if len(snapshot.Servers) < agentapi.MaxMCPStatusServers {
		snapshot.Servers = append(snapshot.Servers, row)
	} else {
		snapshot.Truncated = true
	}
}

func (c *conversation) observeMCPStatusLocked(ev copilot.SessionEvent, agentID string) bool {
	// Streaming text and other unrelated events do not copy the server list.
	switch ev.Data.(type) {
	case *rpc.SessionMCPServersLoadedData, *rpc.SessionMCPServerStatusChangedData, *rpc.SessionMCPServerNeedsReconnectData, *rpc.SessionMCPServerRemovedData:
		if agentID != "" {
			return true
		}
	default:
		return false
	}
	snapshot := copyMCPStatus(c.mcpStatus)
	snapshot.Supported = true
	switch d := ev.Data.(type) {
	case *rpc.SessionMCPServersLoadedData:
		if agentID != "" {
			return true
		}
		snapshot = agentapi.MCPStatusSnapshot{Supported: true, Ready: true, Servers: []agentapi.MCPServerStatus{}}
		for _, s := range d.Servers {
			auth := s.Status == rpc.MCPServerStatusNeedsAuth
			row := agentapi.MCPServerStatus{Name: s.Name, Status: mcpStatus(s.Status), Error: deref(s.Error), Remote: auth, SignIn: auth}
			if s.Source != nil {
				row.Source = string(*s.Source)
			}
			if s.Transport != nil {
				row.Remote = row.Remote || *s.Transport == "http" || *s.Transport == "sse"
			}
			boundedMCPStatus(&snapshot, row)
		}
	case *rpc.SessionMCPServerStatusChangedData:
		if agentID != "" {
			return true
		}
		row := agentapi.MCPServerStatus{Name: d.ServerName}
		if i := slices.IndexFunc(snapshot.Servers, func(s agentapi.MCPServerStatus) bool { return s.Name == d.ServerName }); i >= 0 {
			row = snapshot.Servers[i]
		}
		row.Status, row.Error, row.NeedsReconnect = mcpStatus(d.Status), deref(d.Error), false
		row.Remote = row.Remote || d.Status == rpc.MCPServerStatusNeedsAuth
		row.SignIn = row.SignIn || d.Status == rpc.MCPServerStatusNeedsAuth
		if d.ConfigSource != nil {
			row.Source = *d.ConfigSource
		}
		boundedMCPStatus(&snapshot, row)
	case *rpc.SessionMCPServerNeedsReconnectData:
		if agentID != "" {
			return true
		}
		row := agentapi.MCPServerStatus{Name: d.ServerName, Status: agentapi.MCPNotConfigured}
		if i := slices.IndexFunc(snapshot.Servers, func(s agentapi.MCPServerStatus) bool { return s.Name == d.ServerName }); i >= 0 {
			row = snapshot.Servers[i]
		}
		row.NeedsReconnect = true
		boundedMCPStatus(&snapshot, row)
	case *rpc.SessionMCPServerRemovedData:
		if agentID != "" {
			return true
		}
		snapshot.Servers = slices.DeleteFunc(snapshot.Servers, func(s agentapi.MCPServerStatus) bool { return s.Name == d.ServerName })
	default:
		return false
	}
	slices.SortFunc(snapshot.Servers, func(a, b agentapi.MCPServerStatus) int { return strings.Compare(a.Name, b.Name) })
	c.publishMCPStatusLocked(snapshot)
	return true
}

func (c *conversation) MCPStatusSnapshot(ctx context.Context) (agentapi.MCPStatusSnapshot, error) {
	ms, err := c.mcp()
	if err != nil {
		return agentapi.MCPStatusSnapshot{}, err
	}
	c.mu.Lock()
	revision := c.mcpRevision
	c.mu.Unlock()
	servers, err := ms.MCPList(ctx)
	if err != nil {
		return agentapi.MCPStatusSnapshot{}, fmt.Errorf("list the task's MCP servers: %s", rpcText(err))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return agentapi.MCPStatusSnapshot{}, agentapi.ErrClosed
	}
	// A status event received while List ran is newer than the response.
	if c.mcpRevision != revision {
		return copyMCPStatus(c.mcpStatus), nil
	}
	snapshot := agentapi.MCPStatusSnapshot{Supported: true, Ready: true, Servers: []agentapi.MCPServerStatus{}}
	for _, s := range servers {
		row := agentapi.MCPServerStatus{Name: s.Name, Status: mcpStatus(s.Status), Error: deref(s.Error), Remote: s.URL != nil || s.Status == rpc.MCPServerStatusNeedsAuth, SignIn: s.Status == rpc.MCPServerStatusNeedsAuth}
		// List has no reconnect flag; only a native state update clears it.
		if i := slices.IndexFunc(c.mcpStatus.Servers, func(before agentapi.MCPServerStatus) bool { return before.Name == s.Name }); i >= 0 {
			row.NeedsReconnect = c.mcpStatus.Servers[i].NeedsReconnect
			row.Remote = row.Remote || c.mcpStatus.Servers[i].Remote
			row.SignIn = row.SignIn || c.mcpStatus.Servers[i].SignIn
		}
		if s.Source != nil {
			row.Source = string(*s.Source)
		}
		boundedMCPStatus(&snapshot, row)
	}
	slices.SortFunc(snapshot.Servers, func(a, b agentapi.MCPServerStatus) int { return strings.Compare(a.Name, b.Name) })
	c.publishMCPStatusLocked(snapshot)
	return copyMCPStatus(snapshot), nil
}

func (c *conversation) MCPTools(ctx context.Context, name string) ([]agentapi.MCPTool, error) {
	ms, err := c.mcp()
	if err != nil {
		return nil, err
	}
	tools, err := ms.MCPTools(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("list the MCP server's tools: %s", rpcText(err))
	}
	out := make([]agentapi.MCPTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, agentapi.MCPTool{Name: t.Name, Description: clip(displaytext.Sanitize(deref(t.Description)), maxErrorText)})
	}
	return out, nil
}
