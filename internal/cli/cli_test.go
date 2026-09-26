package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

func TestRunDefaultsToHelpWithoutCreatingState(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"UAM_CONFIG_DIR", "UAM_SESSION_DIR", "UAM_CACHE_DIR"} {
		t.Setenv(name, filepath.Join(root, name))
	}
	output := captureCLIStderr(t, func() { must(t, Run(context.Background(), nil)) })
	if !strings.Contains(output, "uam web") || strings.Contains(output, "open the TUI") {
		t.Fatalf("default output = %q", output)
	}
	entries, err := os.ReadDir(root)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("help created runtime state: %v", entries)
	}
}

func TestTerminalCommandsAreRetiredWithoutChangingSavedData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("UAM_CONFIG_DIR", root)
	t.Setenv("UAM_SESSION_DIR", filepath.Join(root, "run"))
	path := filepath.Join(root, "sessions.json")
	legacy := []byte(`{"schema_version":4,"default_agent":"claude","sessions":{"copilot:old":{"id":"old","agent":"copilot"}},"profiles":{"saved":{"provider":"codex"}}}`)
	must(t, os.WriteFile(path, legacy, 0o600))
	for _, command := range []string{"new", "dispatch", "attach", "last", "ls", "list", "stop", "restart", "rm", "kill-all", "profile", "doctor", "notify-closed", "__host", "__attach", "__opencode"} {
		if err := Run(context.Background(), []string{command}); err == nil || !strings.Contains(err.Error(), "terminal support has been removed") {
			t.Fatalf("%s = %v, want retirement error", command, err)
		}
	}
	got, err := os.ReadFile(path)
	must(t, err)
	if !bytes.Equal(got, legacy) {
		t.Fatal("retired command changed saved data")
	}
	if _, err := os.Stat(filepath.Join(root, "run")); !os.IsNotExist(err) {
		t.Fatalf("retired command created runtime state: %v", err)
	}
}

func TestOnlyCopilotIsRegistered(t *testing.T) {
	providers := webProviders()
	if len(providers) != 1 || providers[0].Name() != "copilot" {
		t.Fatalf("providers = %v, want Copilot only", providers)
	}
}

func TestMainRetiredCommandFails(t *testing.T) {
	cmd := cliMainSubprocess(t, "dispatch", t.TempDir(), t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "terminal support has been removed") {
		t.Fatalf("dispatch = %v, %s", err, out)
	}
}

func TestMainWebStatusFallsBackToStderrWhenLoggerFails(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	must(t, os.WriteFile(blocked, []byte("x"), 0o600))
	cmd := cliMainSubprocess(t, "web", blocked, t.TempDir())
	cmd.Env = append(cmd.Env, `UAM_TEST_MAIN_ARGS=["web","status"]`, "UAM_SESSION_DIR="+secureSessionDir(t))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "uam web is not running") || strings.Count(string(out), "failed to initialize logger") != 1 {
		t.Fatalf("web status with blocked logger = %v, %s", err, out)
	}
}

func TestMainStatelessCommandsSkipLoggerAndStore(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"help", "version"} {
		cmd := cliMainSubprocess(t, command, blocked, blocked)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("uam %s: %v\n%s", command, err, output)
		}
		if strings.Contains(string(output), "failed to initialize logger") {
			t.Fatalf("uam %s initialized the logger: %s", command, output)
		}
	}
}

func TestMainVersionFlagsExitZeroWithVersionOnStdout(t *testing.T) {
	for _, arg := range []string{"--version", "-v"} {
		cmd := cliMainSubprocess(t, arg, t.TempDir(), t.TempDir())
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		if err != nil {
			t.Fatalf("uam %s: %v\nstderr=%s", arg, err, stderr.String())
		}
		if got := strings.TrimSpace(string(stdout)); got != version.String() {
			t.Fatalf("uam %s stdout = %q, want %q", arg, got, version.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("uam %s wrote to stderr: %q", arg, stderr.String())
		}
	}
}

func TestCLIMainHelperProcess(t *testing.T) {
	command := os.Getenv("UAM_TEST_MAIN_COMMAND")
	if command == "" {
		return
	}
	args := []string{command}
	if encoded := os.Getenv("UAM_TEST_MAIN_ARGS"); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &args); err != nil {
			os.Exit(97)
		}
	}
	flag.CommandLine = flag.NewFlagSet("uam", flag.ContinueOnError)
	os.Args = append([]string{"uam"}, args...)
	Main()
	os.Exit(0)
}

func cliMainSubprocess(t *testing.T, command, cacheDir, configDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIMainHelperProcess$")
	cmd.Env = append(os.Environ(),
		"UAM_TEST_MAIN_COMMAND="+command,
		"UAM_CACHE_DIR="+cacheDir,
		"UAM_CONFIG_DIR="+configDir,
	)
	return cmd
}

func captureCLIStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureCLIFile(t, &os.Stdout, fn)
}

func captureCLIStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureCLIFile(t, &os.Stderr, fn)
}

func captureCLIFile(t *testing.T, target **os.File, fn func()) string {
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

func withCLIStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(input)
	_ = w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	fn()
}

func secureSessionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
