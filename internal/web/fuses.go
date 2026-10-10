package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type fuseKey struct{ kind, key string }
type fuseEntry struct {
	at, until time.Time
	count     int
	status    string
	commands  []string
	warned    bool
}

// preToolUse is shared by the Task and its subagents. Lint never holds mu.
func (m *Manager) preToolUse(ctx context.Context, s *webSession, use agentapi.ToolUse) agentapi.ToolVerdict {
	m.mu.Lock()
	if m.closed || s.removed || s.base == StateClosed || m.sessions[s.id] != s {
		m.mu.Unlock()
		return agentapi.ToolVerdict{}
	}
	out := m.localPreToolUseLocked(s, use)
	m.mu.Unlock()
	if out.Deny == "" {
		glab := m.glabPre(ctx, use)
		out.Context = strings.TrimSpace(out.Context + "\n" + glab.Context)
		out.Deny, out.Args = glab.Deny, glab.Args
	}
	return out
}

func (m *Manager) localPreToolUseLocked(s *webSession, use agentapi.ToolUse) agentapi.ToolVerdict {
	out := agentapi.ToolVerdict{Context: m.trailContextLocked(s, use)}
	kind := use.PermissionKind
	if kind == "" {
		kind = permissionToolKind(use.Tool)
	}
	if e := s.fuses[fuseKey{kind: "permission", key: kind}]; e != nil && e.count >= 2 {
		note := fmt.Sprintf("uam fuse: %s permission was denied twice: %s. Stop retrying this kind of action until permissions change.", kind, strings.Join(e.commands, ", "))
		if !e.warned {
			out.Context = strings.TrimSpace(out.Context + "\n" + note)
			e.warned = true
		}
		out.Deny = note
		return out
	}
	key := fetchFuseKey(use)
	if e := s.fuses[fuseKey{kind: "fetch", key: key}]; key != "" && e != nil {
		if m.now().Before(e.until) {
			out.Deny = fmt.Sprintf("uam fuse: %s refused %s at %s UTC; reopens %s UTC", key, e.status, e.at.UTC().Format("15:04"), e.until.UTC().Format("15:04"))
		} else {
			delete(s.fuses, fuseKey{kind: "fetch", key: key})
		}
	}
	return out
}

func (m *Manager) failedToolUse(_ context.Context, s *webSession, use agentapi.ToolUse, failure string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || s.removed || s.base == StateClosed || m.sessions[s.id] != s {
		return ""
	}
	now := m.now()
	if use.PermissionKind != "" && (failure == "denied-by-permission-request-hook" || failure == "denied-by-rules" || failure == "denied-by-content-exclusion-policy" || failure == "denied-no-approval-rule-and-could-not-request-from-user") {
		e := s.fuseEntry(fuseKey{kind: "permission", key: use.PermissionKind}, now)
		if e.count < 2 {
			e.count++
		}
		command, _ := use.Args["command"].(string)
		if command == "" {
			command = use.Tool
		}
		if len(e.commands) < 3 {
			e.commands = append(e.commands, fmt.Sprintf("%q", clipRunes(command, 80)))
		}
		return ""
	}
	key := fetchFuseKey(use)
	if key == "" {
		return ""
	}
	// The live CLI probe returned precisely this suffix; do not infer status
	// from unrelated digits, a page body, a timeout, or a successful result.
	_, status, ok := strings.Cut(failure, " - status code ")
	if !ok || !strings.HasPrefix(failure, "Error: Failed to fetch ") {
		return ""
	}
	ttl := 6 * time.Hour
	switch status {
	case "401", "403", "451":
	case "429":
		ttl = 10 * time.Minute
	default:
		return ""
	}
	e := s.fuseEntry(fuseKey{kind: "fetch", key: key}, now)
	e.at, e.until, e.status = now, now.Add(ttl), status
	return ""
}

func (s *webSession) fuseEntry(key fuseKey, now time.Time) *fuseEntry {
	if e := s.fuses[key]; e != nil {
		return e
	}
	if s.fuses == nil {
		s.fuses = make(map[fuseKey]*fuseEntry)
	}
	if len(s.fuses) >= 512 {
		var oldest fuseKey
		var first *fuseEntry
		for k, e := range s.fuses {
			if first == nil || e.at.Before(first.at) || e.at.Equal(first.at) && (k.kind+"/"+k.key) < (oldest.kind+"/"+oldest.key) {
				oldest, first = k, e
			}
		}
		delete(s.fuses, oldest)
	}
	e := &fuseEntry{at: now}
	s.fuses[key] = e
	return e
}

func permissionToolKind(tool string) string {
	switch tool {
	case "bash", "powershell", "shell":
		return "shell"
	case "edit", "create", "apply_patch":
		return "write"
	case "view", "read_file":
		return "read"
	case "web_fetch":
		return "url"
	}
	return ""
}

func fetchFuseKey(use agentapi.ToolUse) string {
	if use.Tool != "web_fetch" {
		return ""
	}
	raw, _ := use.Args["url"].(string)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	segment, _, _ := strings.Cut(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	key := strings.ToLower(u.Host) + "/" + segment
	if len(key) > 2048 {
		return ""
	}
	return key
}
