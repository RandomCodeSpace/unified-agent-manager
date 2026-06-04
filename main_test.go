package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

func TestRunHelpAndList(t *testing.T) {
	dir := setupFakeCLIConfig(t, "cfg")
	if out := runStderr(t, []string{"help"}); !strings.Contains(out, "uam") {
		t.Fatalf("help=%q", out)
	}
	if out := runStdout(t, []string{"ls", "--json"}); !strings.Contains(out, "[") {
		t.Fatalf("ls=%q", out)
	}
	_ = dir
}

func TestRunDispatchVariants(t *testing.T) {
	setupFakeCLIConfig(t, "cfg-dispatch")
	assertNonEmptyRunOutput(t, []string{"dispatch", "claude", "hello world"}, "empty dispatch id")
	assertNonEmptyRunOutput(t, []string{"dispatch", "claude"}, "empty no-prompt dispatch id")
	assertNonEmptyRunOutput(t, []string{"dispatch", "claude", "#bugfix", "fix", "thing"}, "empty named dispatch id")
	if text := runStdout(t, []string{"ls"}); !strings.Contains(text, "bugfix") {
		t.Fatalf("named session missing from list: %q", text)
	}
}

func TestRunArgumentErrors(t *testing.T) {
	setupFakeCLIConfig(t, "cfg-errors")
	cases := []struct {
		args []string
		msg  string
	}{
		{[]string{"unknown"}, "want unknown error"},
		{[]string{"peek"}, "want peek arg error"},
		{[]string{"stop"}, "want stop arg error"},
		{[]string{"rm"}, "want rm arg error"},
		{[]string{"attach"}, "want attach arg error"},
	}
	for _, tc := range cases {
		if err := run(context.Background(), tc.args); err == nil {
			t.Fatal(tc.msg)
		}
	}
}

func setupFakeCLIConfig(t *testing.T, name string) string {
	t.Helper()
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, name))
	return dir
}

func runStdout(t *testing.T, args []string) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	})
}

func runStderr(t *testing.T, args []string) string {
	t.Helper()
	return captureStderr(t, func() {
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	})
}

func assertNonEmptyRunOutput(t *testing.T, args []string, message string) {
	t.Helper()
	if strings.TrimSpace(runStdout(t, args)) == "" {
		t.Fatal(message)
	}
}

func TestRunNew(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfg2"))
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("claude\n\n/tmp\nfrom wizard\n")
	_ = w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"new"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "dispatched") {
		t.Fatalf("out=%q", out)
	}
}

// F54 — `uam new` keeps the prompt optional: a blank final prompt dispatches a
// prompt-less session and exits 0.
func TestRunNewAllowsEmptyPrompt(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	cfgDir := filepath.Join(dir, "cfg-empty-prompt")
	t.Setenv("UAM_CONFIG_DIR", cfgDir)
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("claude\n\n/tmp\n\n")
	_ = w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"new"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "dispatched") {
		t.Fatalf("new should dispatch without a prompt, out=%q", out)
	}
	st, err := store.Open(filepath.Join(cfgDir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(cfg.Sessions))
	}
	for _, rec := range cfg.Sessions {
		if rec.Prompt != "" {
			t.Fatalf("prompt = %q, want empty", rec.Prompt)
		}
	}
}

func TestRunMoreCLIPaths(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfgmore"))
	out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"dispatch", "--safe", "--cwd", "/tmp", "claude", "safe prompt"}); err != nil {
			t.Fatal(err)
		}
	})
	id := strings.TrimSpace(out)
	if id == "" {
		t.Fatal("empty id")
	}
	if text := captureStdout(t, func() {
		if err := run(context.Background(), []string{"ls"}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(text, "claude") {
		t.Fatalf("ls text=%q", text)
	}
	if text := captureStdout(t, func() {
		if err := run(context.Background(), []string{"peek", id}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(text, "pane") {
		t.Fatalf("peek=%q", text)
	}
	if err := run(context.Background(), []string{"stop", id}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"rm", id}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"last"}); err == nil {
		t.Fatal("want no sessions after rm")
	}
	if err := runDispatch(context.Background(), newService(mustTestStore(t, dir)), []string{"--bad"}); err == nil {
		t.Fatal("want bad flag error")
	}
}

func TestAttachReturnsToTUI(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfg-attach"))
	out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"dispatch", "claude", "attach me"}); err != nil {
			t.Fatal(err)
		}
	})
	id := strings.TrimSpace(out)
	if id == "" {
		t.Fatal("empty id")
	}

	oldRunTUI := runTUIFn
	defer func() { runTUIFn = oldRunTUI }()
	returnedToTUI := false
	runTUIFn = func(ctx context.Context, model tea.Model) error {
		returnedToTUI = true
		return nil
	}

	if err := run(context.Background(), []string{"attach", id}); err != nil {
		t.Fatal(err)
	}
	if !returnedToTUI {
		t.Fatal("attach should return to UAM TUI after the session exits")
	}
}

func mustTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "explicit", "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestUsageAndNewService(t *testing.T) {
	oldVersion := version.Override
	version.Override = "v9.9.9"
	t.Cleanup(func() { version.Override = oldVersion })

	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfg3"))
	out := captureStderr(t, usage)
	if !strings.Contains(out, "dispatch") {
		t.Fatalf("usage=%q", out)
	}
	if out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"version"}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "v9.9.9") {
		t.Fatalf("version=%q", out)
	}
	st, err := store.Open(filepath.Join(dir, "direct", "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(st)
	if svc == nil || svc.Registry == nil {
		t.Fatal("nil svc")
	}
}

func setupFakeCLIEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFileMode(t, filepath.Join(dir, "claude"), "#!/bin/sh\nexit 0\n", 0o755)
	tmuxPath := filepath.Join(dir, "tmux")
	writeFileMode(t, tmuxPath, `#!/bin/sh
case "$*" in
  *"list-sessions"*) exit 0 ;;
  *"capture-pane"*) echo "pane" ;;
esac
exit 0
`, 0o755)
	t.Setenv("UAM_TMUX_BIN", tmuxPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func writeFileMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureFile(t, &os.Stdout, fn)
}
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureFile(t, &os.Stderr, fn)
}
func captureFile(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*target = w
	defer func() {
		_ = w.Close()
		*target = old
	}()
	fn()
	_ = w.Close()
	*target = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}
