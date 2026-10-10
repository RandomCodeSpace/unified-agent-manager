package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestFuseDenialsAndTaskScope(t *testing.T) {
	now := time.Date(2026, 10, 10, 14, 2, 0, 0, time.UTC)
	s := &webSession{id: "s"}
	other := &webSession{id: "other"}
	m := &Manager{now: func() time.Time { return now }, sessions: map[string]*webSession{"s": s, "other": other}}
	denied := agentapi.ToolUse{Tool: "bash", PermissionKind: "shell", Args: map[string]any{"command": "ls /etc/ssl"}}
	use := agentapi.ToolUse{Tool: "bash"}
	m.failedToolUse(t.Context(), s, denied, "denied-by-rules")
	if got := m.preToolUse(t.Context(), s, use); got.Context != "" || got.Deny != "" {
		t.Fatalf("first: %+v", got)
	}
	m.failedToolUse(t.Context(), s, denied, "denied-by-rules")
	got := m.preToolUse(t.Context(), s, use)
	if !strings.Contains(got.Context, `"ls /etc/ssl"`) || got.Deny == "" {
		t.Fatalf("third: %+v", got)
	}
	if got := m.preToolUse(t.Context(), s, use); got.Context != "" || got.Deny == "" {
		t.Fatalf("repeat: %+v", got)
	}
	if got := m.preToolUse(t.Context(), other, use); got.Deny != "" {
		t.Fatal("cross-task refusal")
	}
	if got := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "web_fetch"}); got.Deny != "" {
		t.Fatal("cross-kind refusal")
	}
	for range 5 {
		m.failedToolUse(t.Context(), s, denied, "denied-by-rules")
	}
	if n := len(s.fuses[fuseKey{kind: "permission", key: "shell"}].commands); n > 3 {
		t.Fatalf("evidence=%d", n)
	}
	// Different MCP names share their verified native permission kind.
	denied = agentapi.ToolUse{Tool: "forge-read_issue", PermissionKind: "mcp"}
	m.failedToolUse(t.Context(), s, denied, "denied-by-rules")
	denied.Tool = "other-get_file"
	m.failedToolUse(t.Context(), s, denied, "denied-by-rules")
	if got := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "third-read_config", PermissionKind: "mcp"}); got.Deny == "" {
		t.Fatal("observed MCP tool not refused")
	}
}

func TestFuseFetchStatusSegmentAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 10, 14, 2, 0, 0, time.UTC)
	s := &webSession{id: "s"}
	m := &Manager{now: func() time.Time { return now }, sessions: map[string]*webSession{"s": s}}
	fetch := func(raw string) agentapi.ToolUse {
		return agentapi.ToolUse{Tool: "web_fetch", Args: map[string]any{"url": raw}}
	}
	for _, status := range []string{"401", "403", "451", "429"} {
		t.Run(status, func(t *testing.T) {
			s.fuses = nil
			use := fetch("https://EXAMPLE.com/a/one?q=1")
			m.failedToolUse(t.Context(), s, use, "Error: Failed to fetch https://EXAMPLE.com/a/one?q=1 - status code "+status)
			got := m.preToolUse(t.Context(), s, fetch("https://example.com/a/two"))
			reopen := "20:02"
			ttl := 6 * time.Hour
			if status == "429" {
				reopen = "14:12"
				ttl = 10 * time.Minute
			}
			if !strings.Contains(got.Deny, "refused "+status+" at 14:02 UTC; reopens "+reopen+" UTC") {
				t.Fatalf("reason=%q", got.Deny)
			}
			for _, raw := range []string{"https://example.com/b/one", "https://other.com/a/one"} {
				if m.preToolUse(t.Context(), s, fetch(raw)).Deny != "" {
					t.Fatalf("wrong scope: %s", raw)
				}
			}
			now = now.Add(ttl)
			if m.preToolUse(t.Context(), s, use).Deny != "" {
				t.Fatal("did not expire")
			}
			now = now.Add(-ttl)
		})
	}
	for _, failure := range []string{"Error: Failed to fetch x - status code 404", "body mentions 403", "Error: Failed to fetch x - status code 4030", "timeout"} {
		s.fuses = nil
		m.failedToolUse(t.Context(), s, fetch("https://example.com/a"), failure)
		if len(s.fuses) != 0 {
			t.Fatalf("unexpected fuse: %q", failure)
		}
	}
}

func TestFuseCap(t *testing.T) {
	s := &webSession{}
	for i := range 520 {
		s.fuseEntry(fuseKey{kind: "fetch", key: fmt.Sprint(i)}, time.Unix(int64(i+1), 0))
	}
	if len(s.fuses) != 512 || s.fuses[fuseKey{kind: "fetch", key: "0"}] != nil || s.fuses[fuseKey{kind: "fetch", key: "519"}] == nil {
		t.Fatalf("cap/eviction: %d", len(s.fuses))
	}
}

func TestFuseResetOnModeRuleAndClose(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	s := m.sessions[sum.ID]
	seed := func() {
		m.mu.Lock()
		s.fuseEntry(fuseKey{kind: "permission", key: "shell"}, m.now()).count = 2
		m.mu.Unlock()
	}
	assertClear := func() {
		t.Helper()
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(s.fuses) != 0 {
			t.Fatal("fuses retained")
		}
	}
	seed()
	if _, err := m.SetMode(sum.ID, "yolo"); err != nil {
		t.Fatal(err)
	}
	assertClear()
	if _, err := m.SetMode(sum.ID, "safe"); err != nil {
		t.Fatal(err)
	}
	seed()
	ix := permissionRequest("rule")
	ix.Options = append(ix.Options, agentapi.Option{ID: "approve_session", Label: "Allow for session"})
	conv.EmitInteraction(ix)
	if _, err := m.Answer(sum.ID, ix.ID, agentapi.Answer{Decision: "approve_session"}); err != nil {
		t.Fatal(err)
	}
	assertClear()
	seed()
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	assertClear()
	m.failedToolUse(t.Context(), s, agentapi.ToolUse{Tool: "bash", PermissionKind: "shell"}, "denied-by-rules")
	assertClear()
}
