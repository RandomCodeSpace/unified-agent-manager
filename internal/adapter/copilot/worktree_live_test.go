package copilot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Opt-in: a Thread in a git worktree under ~/.uam/worktrees must open as one
// in its Project's folder does: no folder-trust request, the repository's
// skills and MCP servers discovered, and bash running in the worktree.
func TestWorktreeLiveProbe(t *testing.T) {
	if os.Getenv("UAM_WORKTREE_LIVE_PROBE") != "1" {
		t.Skip("set UAM_WORKTREE_LIVE_PROBE=1 for the gpt-6-luna worktree probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	repo := filepath.Join(t.TempDir(), "repo")
	files := map[string]string{
		".github/skills/wt-probe/SKILL.md": "---\nname: wt-probe\ndescription: Probe skill of the worktree test.\n---\nSay probe.\n",
		".mcp.json":                        `{"mcpServers":{"wt-probe-mcp":{"type":"local","command":"true","args":[],"tools":["*"]}}}`,
		".github/mcp.json":                 `{"mcpServers":{"wt-probe-github":{"type":"local","command":"true","args":[],"tools":["*"]}}}`,
		".vscode/mcp.json":                 `{"servers":{"wt-probe-vscode":{"type":"stdio","command":"true","args":[]}}}`,
		"README.md":                        "probe\n",
	}
	for name, body := range files {
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=probe", "-c", "user.email=probe@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(repo, "init", "-q", "-b", "main")
	git(repo, "add", "-A")
	git(repo, "commit", "-q", "-m", "probe")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	tag := hex.EncodeToString(b[:])
	probeRoot := filepath.Join(home, ".uam", "worktrees", "uam-probe-"+tag)
	worktree := filepath.Join(probeRoot, "uam-"+tag)
	if err := os.MkdirAll(probeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(probeRoot) }()
	git(repo, "worktree", "add", "-q", "-b", "uam/"+tag, worktree, "main")
	defer git(repo, "worktree", "remove", "--force", worktree)

	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c := client.(sdkClientAdapter).c
	type result struct {
		trusted     bool
		skills, mcp []string
		permissions []string
		cwd         string
	}
	probe := func(dir string) result {
		var (
			mu  sync.Mutex
			out result
		)
		s, err := c.CreateSession(ctx, &copilot.SessionConfig{
			Model: "gpt-6-luna", WorkingDirectory: dir, AvailableTools: []string{"bash"},
			EnableConfigDiscovery: copilot.Bool(true),
			OnPermissionRequest: func(req copilot.PermissionRequest, _ copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
				mu.Lock()
				out.permissions = append(out.permissions, string(req.Kind()))
				mu.Unlock()
				return &rpc.PermissionDecisionApproveOnce{}, nil
			},
			Hooks: &copilot.SessionHooks{OnPreToolUse: func(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
				mu.Lock()
				out.cwd = in.WorkingDirectory
				mu.Unlock()
				return nil, nil
			}},
		})
		if err != nil {
			t.Fatalf("create session in %s: %v", dir, err)
		}
		defer func() {
			_ = s.Disconnect()
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if err := c.DeleteSession(cleanup, s.SessionID); err != nil {
				t.Errorf("delete probe session: %v", err)
			}
		}()
		if trust, err := s.RPC.Permissions.FolderTrust().IsTrusted(ctx, &rpc.FolderTrustCheckParams{Path: dir}); err == nil {
			out.trusted = trust.Trusted
		} else {
			t.Logf("folder trust check: %v", err)
		}
		if _, err := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: "Use bash exactly once to run `pwd`, then finish. Run nothing else."}); err != nil {
			t.Fatalf("turn in %s: %v", dir, err)
		}
		if list, err := s.RPC.Skills.List(ctx); err == nil {
			for _, sk := range list.Skills {
				out.skills = append(out.skills, sk.Name)
			}
		} else {
			t.Logf("skills list: %v", err)
		}
		if list, err := s.RPC.MCP.List(ctx); err == nil {
			for _, srv := range list.Servers {
				out.mcp = append(out.mcp, srv.Name)
			}
		} else {
			t.Logf("mcp list: %v", err)
		}
		// What Settings → MCP discovers for the folder, as uam asks for it.
		if found, err := c.RPC.MCP.Discover(ctx, &rpc.MCPDiscoverRequest{WorkingDirectory: &dir}); err == nil {
			for _, srv := range found.Servers {
				out.mcp = append(out.mcp, "discovered:"+srv.Name)
			}
		} else {
			t.Logf("mcp discover: %v", err)
		}
		return out
	}
	project, tree := probe(repo), probe(worktree)
	t.Logf("project: trusted=%v cwd-ok=%v skills=%v mcp=%v permissions=%v", project.trusted, project.cwd == repo, slices.Contains(project.skills, "wt-probe"), project.mcp, project.permissions)
	t.Logf("worktree: trusted=%v cwd-ok=%v skills=%v mcp=%v permissions=%v", tree.trusted, tree.cwd == worktree, slices.Contains(tree.skills, "wt-probe"), tree.mcp, tree.permissions)
	if tree.cwd != worktree {
		t.Errorf("bash ran in %q, want the worktree", tree.cwd)
	}
	if slices.Contains(project.skills, "wt-probe") && !slices.Contains(tree.skills, "wt-probe") {
		t.Error("the worktree lost the repository's skill")
	}
	for _, name := range project.mcp {
		if !slices.Contains(tree.mcp, name) {
			t.Errorf("the worktree lost MCP server %s", name)
		}
	}
	if project.trusted != tree.trusted {
		t.Errorf("folder trust differs: project %v, worktree %v", project.trusted, tree.trusted)
	}
	if len(tree.permissions) > len(project.permissions) {
		t.Errorf("the worktree asked more: %v vs %v", tree.permissions, project.permissions)
	}
}
