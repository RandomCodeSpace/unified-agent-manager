package web

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// fetchFusesFile keeps the web_fetch refusals beside the store, so a host the
// network blocks stays skipped across restarts.
const fetchFusesFile = "fetch-fuses.json"

// fetchBlockTTL is how long a host that refused a fetch (401, 403, 407, 451)
// is skipped by every Thread (owner, 2026-10-28: 3–4 days). The first fetch
// is always made; each refusal after the block lapses starts a new one.
const fetchBlockTTL = 4 * 24 * time.Hour

// maxFetchFuses bounds the shared refusals; the oldest goes first.
const maxFetchFuses = 512

// fetchFuse is a web_fetch refusal shared by every Thread on this server, for
// "host/first-segment", or "host/*" once two segments of the host refused.
type fetchFuse struct {
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
	Status string    `json:"status"`
}

type fuseKey struct{ kind, key string }
type fuseEntry struct {
	at       time.Time
	count    int
	commands []string
	warned   bool
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
		glab := m.glabPre(ctx, s, use)
		out.Context = strings.TrimSpace(out.Context + "\n" + glab.Context)
		out.Deny, out.Args = glab.Deny, glab.Args
	}
	if out.Deny == "" {
		if out.Args != nil {
			use.Args = out.Args
		}
		if args := m.stampGitLabCommit(ctx, s, use); args != nil {
			out.Args = args
		}
	}
	if out.Deny != "" {
		m.count("tool_refusals")
	}
	return out
}

func (m *Manager) localPreToolUseLocked(s *webSession, use agentapi.ToolUse) agentapi.ToolVerdict {
	out := agentapi.ToolVerdict{Context: m.trailContextLocked(s, use)}
	if use.AgentID == "" {
		// First, so the adapter records it as a notice (uam thread:).
		out.Context = strings.TrimSpace(m.takeChildNotesLocked(s) + "\n" + m.takeMemoryPinLocked(s) + "\n" + out.Context)
	}
	deny, note := m.budgetToolLocked(s)
	out.Context = strings.TrimSpace(out.Context + "\n" + note)
	if deny != "" {
		out.Deny = deny
		return out
	}
	if deny := m.editScopeDenyLocked(s, use); deny != "" {
		out.Deny = deny
		return out
	}
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
		m.count("fuse_permission")
		return out
	}
	key := fetchFuseKey(use)
	if key == "" {
		return out
	}
	host, _, _ := strings.Cut(key, "/")
	fuses := m.fetchFusesLocked()
	for _, k := range []string{key, host + "/*"} {
		if e := fuses[k]; e != nil && m.now().Before(e.Until) {
			m.count("fuse_fetch")
			out.Deny = fmt.Sprintf("uam fuse: %s refused %s at %s UTC; every thread skips it until %s UTC. Do not retry it: use another source, or say it is unreachable.", k, e.Status, e.At.UTC().Format("2006-01-02 15:04"), e.Until.UTC().Format("2006-01-02 15:04"))
			return out
		}
	}
	return out
}

// fetchFusesLocked is the shared web_fetch refusals, read from their file on
// first use; a missing or unreadable file starts empty.
func (m *Manager) fetchFusesLocked() map[string]*fetchFuse {
	if m.fetchFuses != nil {
		return m.fetchFuses
	}
	m.fetchFuses = map[string]*fetchFuse{}
	if path := m.fetchFusesPath(); path != "" {
		var saved map[string]*fetchFuse
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &saved) == nil { // #nosec G304 -- uam's own file beside the store.
			for k, e := range saved {
				if e != nil && m.now().Before(e.Until) {
					m.fetchFuses[k] = e
				}
			}
		}
	}
	return m.fetchFuses
}

func (m *Manager) fetchFusesPath() string {
	if m.store == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(m.store.Path()), fetchFusesFile)
}

// blockFetchLocked records that key refused with status until until, and the
// whole host once two of its segments refused; then saves the live ones.
func (m *Manager) blockFetchLocked(key, status string, until time.Time) {
	now := m.now()
	fuses := m.fetchFusesLocked()
	maps.DeleteFunc(fuses, func(_ string, e *fetchFuse) bool { return !now.Before(e.Until) })
	fuses[key] = &fetchFuse{At: now, Until: until, Status: status}
	host, _, _ := strings.Cut(key, "/")
	if status != "429" {
		segments := 0
		for k, e := range fuses {
			segments += b2i(e.Status != "429" && strings.HasPrefix(k, host+"/") && k != host+"/*")
		}
		if segments >= 2 {
			fuses[host+"/*"] = &fetchFuse{At: now, Until: until, Status: status}
		}
	}
	for len(fuses) > maxFetchFuses {
		oldest := ""
		for k, e := range fuses {
			if oldest == "" || e.At.Before(fuses[oldest].At) || e.At.Equal(fuses[oldest].At) && k < oldest {
				oldest = k
			}
		}
		delete(fuses, oldest)
	}
	if path := m.fetchFusesPath(); path != "" {
		if b, err := json.Marshal(fuses); err == nil {
			_ = writeFileAtomic(path, ".fetch-fuses-*", b)
		}
	}
}

func (m *Manager) failedToolUse(ctx context.Context, s *webSession, use agentapi.ToolUse, failure string) string {
	// A compound shell call may commit successfully before a later command fails.
	m.succeededToolUse(ctx, s, use)
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
	ttl := fetchBlockTTL
	switch status {
	case "401", "403", "407", "451":
	case "429":
		ttl = 10 * time.Minute
	default:
		return ""
	}
	m.blockFetchLocked(key, status, now.Add(ttl))
	m.count("fuse_fetch_blocks")
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
