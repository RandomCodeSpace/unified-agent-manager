package copilot

import (
	"context"
	"encoding/json"
	"fmt"
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

// Exercise the actual SDK process boundary, including a nested shell, without
// calling a model or touching the user's shell configuration or history.
func TestSDKShellHistoryDisabled(t *testing.T) {
	t.Setenv("COPILOT_OTEL_ENABLED", "false")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	dir := t.TempDir()
	history := filepath.Join(dir, "history")
	const original = "user command\n"
	if err := os.WriteFile(history, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HISTFILE", history)
	t.Setenv("HISTSIZE", "100")
	t.Setenv("UAM_TEST_BASH", bash)
	t.Setenv("UAM_TEST_OUTPUT", filepath.Join(dir, "environment"))
	t.Setenv("UAM_TEST_KEEP", "inherited")
	t.Setenv("PATH", dir)
	// The stub intentionally exits without speaking RPC. Start must fail, but
	// only after both shells have explicitly attempted to persist history.
	const script = `#!/bin/sh
exec "$UAM_TEST_BASH" --noprofile --norc -c '
set -e
printf "%s\n%s\n%s\n" "$HISTFILE" "$HISTSIZE" "$UAM_TEST_KEEP" > "$UAM_TEST_OUTPUT"
set -o history
history -s uam-main-marker
history -a
"$UAM_TEST_BASH" --noprofile --norc -c "set -e; set -o history; history -s uam-child-marker; history -a; history -w"
history -w
'
`
	if err := os.WriteFile(filepath.Join(dir, "copilot"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Start(ctx); err == nil {
		t.Fatal("stub unexpectedly completed the SDK handshake")
	}
	data, err := os.ReadFile(history)
	if err != nil || string(data) != original {
		t.Fatalf("shells changed history: %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(dir, "environment"))
	if want := os.DevNull + "\n0\ninherited\n"; err != nil || string(data) != want {
		t.Fatalf("child environment = %q, %v; want %q", data, err, want)
	}
	if os.Getenv("HISTFILE") != history || os.Getenv("HISTSIZE") != "100" {
		t.Fatal("SDK client changed the parent environment")
	}
}

// Opt-in because this makes real model calls and requires token authentication
// via the CLI's supported environment variables. A disposable HOME protects the
// user's history even if the runtime stops honoring the environment settings.
func TestSDKRealShellHistoryMainAndSubagent(t *testing.T) {
	if os.Getenv("UAM_WEB_REAL_COPILOT_HISTORY") != "1" {
		t.Skip("set UAM_WEB_REAL_COPILOT_HISTORY=1 to test real main/subagent shells")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("COPILOT_HOME", filepath.Join(dir, ".copilot"))
	history := filepath.Join(dir, ".bash_history")
	const original = "user command\n"
	if err := os.WriteFile(history, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HISTFILE", history)
	t.Setenv("HISTSIZE", "100")
	probe := filepath.Join(dir, "probe.bash")
	const script = `set -eu
printf 'outer=%s:%s nested=%s:%s\n' "$2" "$3" "$HISTFILE" "$HISTSIZE"
test "$2" = /dev/null
test "$3" = 0
test "$HISTFILE" = /dev/null
test "$HISTSIZE" = 0
set -o history
history -s "uam-$1-marker"
history -a
history -w
test -z "$(history)"
printf '%s\n' "$HISTFILE:$HISTSIZE" > "${BASH_SOURCE[0]%/*}/$1.result"
`
	if err := os.WriteFile(probe, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{}
	for _, role := range []string{"main", "child"} {
		commands[role] = fmt.Sprintf(`bash --noprofile --norc %q %s "$HISTFILE" "$HISTSIZE"`, probe, role)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	const model = "gpt-6-luna"
	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range models {
		found = found || m.ID == model
	}
	if !found {
		t.Fatalf("required live-test model %s is unavailable", model)
	}
	c := client.(sdkClientAdapter).c
	session, err := c.CreateSession(ctx, &copilot.SessionConfig{
		Model: model, WorkingDirectory: dir,
		EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false),
		EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false),
		AvailableTools: []string{"bash", "task", "read_agent"},
		OnPermissionRequest: func(req copilot.PermissionRequest, invocation copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
			if shell, ok := req.(*copilot.PermissionRequestShell); ok && (shell.FullCommandText == commands["main"] || shell.FullCommandText == commands["child"]) {
				return copilot.PermissionHandler.ApproveAll(req, invocation)
			}
			return &rpc.PermissionDecisionReject{}, nil
		},
		CustomAgents: []copilot.CustomAgentConfig{{
			Name: "history-probe", Description: "Run the supplied shell history probe once",
			Model: model, Tools: []string{"bash"},
			Prompt: "Run the exact supplied command once using bash. Do not change environment variables or files yourself. Report the exit status.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup := sync.OnceFunc(func() {
		if err := session.Disconnect(); err != nil {
			t.Errorf("disconnect test session: %v", err)
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := c.DeleteSession(cleanupCtx, session.SessionID); err != nil {
			t.Errorf("delete test session: %v", err)
		}
		if err := client.Stop(); err != nil {
			t.Errorf("stop test runtime: %v", err)
		}
	})
	defer cleanup()
	var mu sync.Mutex
	seen := map[string]bool{}
	unsubscribe := session.On(func(e copilot.SessionEvent) {
		if d, ok := e.Data.(*rpc.ToolExecutionCompleteData); ok {
			if d.Result != nil {
				t.Logf("tool result: %s", d.Result.Content)
			}
			if d.Error != nil {
				t.Logf("tool error: %s", d.Error.Message)
			}
		}
		if d, ok := e.Data.(*rpc.ToolExecutionStartData); ok && d.ToolName == "bash" {
			var args struct {
				Command string `json:"command"`
			}
			data, _ := json.Marshal(d.Arguments)
			_ = json.Unmarshal(data, &args)
			role := "main"
			if e.AgentID != nil && *e.AgentID != "" {
				role = "child"
			}
			mu.Lock()
			seen[role] = seen[role] || args.Command == commands[role]
			mu.Unlock()
			t.Logf("observed %s bash execution", role)
		}
	})
	defer unsubscribe()
	prompt := fmt.Sprintf("Run this exact command yourself using bash: %s\nThen use task to delegate this exact command to the history-probe custom agent, synchronously: %s\nDo not run the child's command yourself. Do not alter the commands, environment, or files. Wait for the child to finish and report both exit statuses.", commands["main"], commands["child"])
	if _, err := session.SendAndWait(ctx, copilot.MessageOptions{Prompt: prompt}); err != nil {
		t.Fatal(err)
	}
	cleanup()
	mu.Lock()
	defer mu.Unlock()
	for _, role := range []string{"main", "child"} {
		if !seen[role] {
			t.Errorf("no SDK event attributed the exact probe command to %s", role)
		}
		data, err := os.ReadFile(filepath.Join(dir, role+".result"))
		if err != nil || string(data) != "/dev/null:0\n" {
			t.Errorf("%s history probe = %q, %v", role, data, err)
		}
	}
	data, err := os.ReadFile(history)
	if err != nil || string(data) != original {
		t.Fatalf("history changed after shell shutdown: %q, %v", data, err)
	}
}

// The CLI UAM starts gets the AUTO_APPROVAL feature flag that assisted
// permissions need, added to any flags the service's environment sets, and
// the rest of that environment unchanged.
func TestSDKCLIEnvironmentEnablesAutoApproval(t *testing.T) {
	for inherited, want := range map[string]string{
		"":                      "AUTO_APPROVAL",
		"AHP_CLIENT":            "AHP_CLIENT,AUTO_APPROVAL",
		"AUTO_APPROVAL,FOO":     "AUTO_APPROVAL,FOO",
		"FOO, AUTO_APPROVAL ,X": "FOO, AUTO_APPROVAL ,X",
	} {
		env := withAutoApproval([]string{"UAM_TEST_KEEP=inherited", "COPILOT_CLI_ENABLED_FEATURE_FLAGS=" + inherited})
		if !slices.Equal(env, []string{"UAM_TEST_KEEP=inherited", "COPILOT_CLI_ENABLED_FEATURE_FLAGS=" + want}) {
			t.Errorf("flags %q: env = %q", inherited, env)
		}
	}
	if env := withAutoApproval([]string{"UAM_TEST_KEEP=inherited"}); !slices.Equal(env, []string{"UAM_TEST_KEEP=inherited", "COPILOT_CLI_ENABLED_FEATURE_FLAGS=AUTO_APPROVAL"}) {
		t.Errorf("unset flags: env = %q", env)
	}

	// The started process sees it, and the service's own environment keeps
	// what it had.
	t.Setenv("COPILOT_OTEL_ENABLED", "false")
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable")
	}
	dir := t.TempDir()
	t.Setenv("COPILOT_CLI_ENABLED_FEATURE_FLAGS", "AHP_CLIENT")
	t.Setenv("UAM_TEST_OUTPUT", filepath.Join(dir, "environment"))
	t.Setenv("UAM_TEST_KEEP", "inherited")
	t.Setenv("PATH", dir)
	script := "#!" + sh + "\nprintf '%s\\n%s\\n' \"$COPILOT_CLI_ENABLED_FEATURE_FLAGS\" \"$UAM_TEST_KEEP\" > \"$UAM_TEST_OUTPUT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "copilot"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Start(ctx); err == nil {
		t.Fatal("stub unexpectedly completed the SDK handshake")
	}
	data, err := os.ReadFile(filepath.Join(dir, "environment"))
	if want := "AHP_CLIENT,AUTO_APPROVAL\ninherited\n"; err != nil || string(data) != want {
		t.Fatalf("child environment = %q, %v; want %q", data, err, want)
	}
	if os.Getenv("COPILOT_CLI_ENABLED_FEATURE_FLAGS") != "AHP_CLIENT" {
		t.Fatal("SDK client changed the service environment")
	}
}
