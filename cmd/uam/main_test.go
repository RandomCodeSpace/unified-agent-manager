package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randomcodespace/unified-agent-manager/internal/store"
)

func TestRunHelpListDispatchAndErrors(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfg"))
	if out := captureStderr(t, func() {
		if err := run(context.Background(), []string{"help"}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "uam") {
		t.Fatalf("help=%q", out)
	}
	if out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"ls", "--json"}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "[") {
		t.Fatalf("ls=%q", out)
	}
	out := captureStdout(t, func() {
		if err := run(context.Background(), []string{"dispatch", "claude", "hello world"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty dispatch id")
	}
	noPromptOut := captureStdout(t, func() {
		if err := run(context.Background(), []string{"dispatch", "claude"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(noPromptOut) == "" {
		t.Fatal("empty no-prompt dispatch id")
	}
	namedOut := captureStdout(t, func() {
		if err := run(context.Background(), []string{"dispatch", "claude", "#bugfix", "fix", "thing"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(namedOut) == "" {
		t.Fatal("empty named dispatch id")
	}
	if text := captureStdout(t, func() {
		if err := run(context.Background(), []string{"ls"}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(text, "bugfix") {
		t.Fatalf("named session missing from list: %q", text)
	}
	if err := run(context.Background(), []string{"unknown"}); err == nil {
		t.Fatal("want unknown error")
	}
	if err := run(context.Background(), []string{"peek"}); err == nil {
		t.Fatal("want peek arg error")
	}
	if err := run(context.Background(), []string{"stop"}); err == nil {
		t.Fatal("want stop arg error")
	}
	if err := run(context.Background(), []string{"rm"}); err == nil {
		t.Fatal("want rm arg error")
	}
	if err := run(context.Background(), []string{"attach"}); err == nil {
		t.Fatal("want attach arg error")
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
	_, _ = w.WriteString("claude\n/tmp\nfrom wizard\n")
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

func TestRunNewAllowsEmptyPrompt(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfg-empty-prompt"))
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("claude\n/tmp\n\n")
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

func mustTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "explicit", "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestUsageAndNewService(t *testing.T) {
	dir := setupFakeCLIEnv(t)
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(dir, "cfg3"))
	out := captureStderr(t, usage)
	if !strings.Contains(out, "dispatch") {
		t.Fatalf("usage=%q", out)
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
	writeFileMode(t, filepath.Join(dir, "tmux"), `#!/bin/sh
case "$*" in
  *"list-sessions"*) exit 0 ;;
  *"capture-pane"*) echo "pane" ;;
esac
exit 0
`, 0o755)
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
	fn()
	_ = w.Close()
	*target = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}
