package tmux

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func TestClientCommandsWithFakeTmux(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if c.Socket != "uam" {
		t.Fatal(c.Socket)
	}
	assertCreateSessionCommand(t, c, logPath)
	assertClientReadCommands(t, c)
	assertClientWriteCommands(t, c)
	assertClientHelpers(t, c)
}

func setupFakeTmuxClient(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-a|1|0|1|/tmp|bash" ;;
  *"capture-pane"*) echo "pane text" ;;
  *"has-session"*) exit 0 ;;
esac
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "log")
	t.Setenv("TMUX_LOG", logPath)
	c := New("uam")
	c.Executable = script
	return c, logPath
}

func assertCreateSessionCommand(t *testing.T, c *Client, logPath string) {
	t.Helper()
	if err := c.CreateSession(context.Background(), "uam-a-deadbeef", "/tmp", map[string]string{"A": "B"}, []string{"cmd", "arg with space"}); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if strings.Contains(logText, " -e ") {
		t.Fatalf("CreateSession should not rely on tmux new-session -e because older tmux rejects it: %s", logText)
	}
	if !strings.Contains(logText, "env A=B cmd 'arg with space'") {
		t.Fatalf("CreateSession should prefix the shell command with env assignments: %s", logText)
	}
}

func assertClientReadCommands(t *testing.T, c *Client) {
	t.Helper()
	list, err := c.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	cap, err := c.Capture(context.Background(), "uam-a", 0)
	if err != nil || !strings.Contains(cap, "pane text") {
		t.Fatalf("cap=%q err=%v", cap, err)
	}
}

func assertClientWriteCommands(t *testing.T, c *Client) {
	t.Helper()
	for _, action := range []func() error{
		func() error { return c.SendLine(context.Background(), "uam-a", "hello") },
		func() error { return c.Kill(context.Background(), "uam-a") },
	} {
		if err := action(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertClientHelpers(t *testing.T, c *Client) {
	t.Helper()
	if !c.HasSession(context.Background(), "uam-a") {
		t.Fatal("expected session")
	}
	argv, err := c.AttachArgv("uam-a")
	if err != nil {
		t.Fatalf("AttachArgv: %v", err)
	}
	if len(argv) != 6 || argv[0] != c.Executable || argv[1] != "-L" {
		t.Fatalf("attach argv: %v", argv)
	}
	if got := c.AttachArgs("uam-a"); len(got) != 5 || got[0] != "-L" {
		t.Fatalf("attach args: %v", got)
	}
	if !PaneAlive(os.Getpid()) {
		t.Fatal("current process should be alive")
	}
	if PaneAlive(-1) {
		t.Fatal("negative pid should not be alive")
	}
	joined := ShellJoin([]string{"abc", "two words"})
	if !strings.Contains(joined, "two words") {
		t.Fatalf("join=%s", joined)
	}
}

func TestEnsureServerConfigInstallsSessionClosedHook(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("EnsureServerConfig: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "set-hook -g session-closed") {
		t.Fatalf("session-closed hook not installed: %s", data)
	}
	if !strings.Contains(string(data), "notify-closed") {
		t.Fatalf("hook command should reference notify-closed: %s", data)
	}
	// #{hook_session_name} must reach tmux verbatim so it can substitute
	// the dying session's name at fire time.
	if !strings.Contains(string(data), "hook_session_name") {
		t.Fatalf("hook command must pass through tmux format variable: %s", data)
	}
}

// SetSessionLabel must write the user-facing label to the @uam_label session
// option and rename the window, so tmux's display shows the user's name while
// the canonical uam-<agent>-<id> session name (#S) is left untouched.
func TestSetSessionLabelWritesLabelAndRenamesWindow(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.SetSessionLabel(context.Background(), "uam-opencode-deadbeef", "tracker · opencode", "tracker"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	if !strings.Contains(logText, "set-option -t uam-opencode-deadbeef @uam_label tracker · opencode") {
		t.Fatalf("expected @uam_label set-option: %s", logText)
	}
	if !strings.Contains(logText, "rename-window -t uam-opencode-deadbeef tracker") {
		t.Fatalf("expected rename-window: %s", logText)
	}
}

// The private-server config must point tmux's status line and terminal title
// at @uam_label (with a #S fallback) and disable automatic-rename so the agent
// can't clobber the user-facing window name.
func TestEnsureServerConfigSetsDisplayName(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("EnsureServerConfig: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, want := range []string{
		"set-option -g automatic-rename off",
		"set-option -g status-left",
		"@uam_label",
		"set-option -g set-titles-string",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("EnsureServerConfig missing %q: %s", want, logText)
		}
	}
}

// The private-server config must let wheel events scroll tmux pane history
// instead of leaking to full-screen agent prompts as history navigation, while
// keeping tmux-side OSC 52 clipboard sync enabled. tmux 3.4 ships no default
// WheelUpPane/WheelDownPane binding (only the status-line wheel), so `mouse on`
// alone captures wheel events without scrolling anything; the pane wheel must be
// bound explicitly to drive copy-mode scrollback.
func TestEnsureServerConfigEnablesMousePaneScrollback(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("EnsureServerConfig: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, want := range []string{
		"set-option -g mouse on",
		"set-option -g set-clipboard on",
		// Wheel over a pane enters copy-mode and scrolls history (forwarding to
		// the app only when it grabbed the mouse or the pane is already in copy
		// mode), instead of leaking to the agent prompt as input navigation.
		"bind-key -T root WheelUpPane",
		"copy-mode -e",
		"bind-key -T root WheelDownPane send-keys -M",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("EnsureServerConfig missing %q: %s", want, logText)
		}
	}
	for _, unwanted := range []string{"MouseDrag1Pane", "MouseDown3Pane", "copy-pipe-and-cancel"} {
		if strings.Contains(logText, unwanted) {
			t.Fatalf("EnsureServerConfig should not install custom mouse clipboard binding %q: %s", unwanted, logText)
		}
	}
}

// F25 — EnsureServerConfig must not latch a failure. On first dispatch the
// server doesn't exist yet, so set-option fails; the next call (after the
// server is up) must retry and succeed rather than caching the earlier error.
func TestEnsureServerConfigRetriesAfterServerlessFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	// set-option fails until a sentinel file exists (simulating "server up").
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"set-option"*)
    if [ ! -f "$TMUX_UP" ]; then
      echo "no server running on /tmp/tmux" >&2
      exit 1
    fi
    ;;
esac
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", filepath.Join(dir, "log"))
	upFile := filepath.Join(dir, "up")
	t.Setenv("TMUX_UP", upFile)

	c := New("uam")
	c.Executable = script

	if err := c.EnsureServerConfig(context.Background()); err == nil {
		t.Fatal("expected serverless EnsureServerConfig to fail")
	}
	// Server comes up.
	if err := os.WriteFile(upFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("EnsureServerConfig must retry and succeed once the server is up, got: %v", err)
	}
	// A third call after success must be a no-op (success-latched): the log
	// length must not grow because applyServerConfig is not re-run.
	before, _ := os.ReadFile(filepath.Join(dir, "log"))
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("post-success EnsureServerConfig: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "log"))
	if len(after) != len(before) {
		t.Fatalf("EnsureServerConfig must not re-apply after a successful latch:\nbefore=%s\nafter=%s", before, after)
	}
}

// F56 — a failing session-closed hook install must be logged (best-effort,
// non-fatal) rather than silently discarded.
func TestApplyServerConfigLogsHookInstallFailure(t *testing.T) {
	if sessionClosedHookCommand() == "" {
		t.Skip("test binary path rejected as unsafe — no hook command to install")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	// Everything succeeds except set-hook, which fails.
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"set-hook"*) echo "hook boom" >&2; exit 1 ;;
esac
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", filepath.Join(dir, "log"))
	c := New("uam")
	c.Executable = script

	var buf bytes.Buffer
	prev := log.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer log.SetLogger(prev)

	// Hook failure is non-fatal: EnsureServerConfig still returns nil.
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("hook install failure must stay non-fatal, got: %v", err)
	}
	if !strings.Contains(buf.String(), "session-closed hook") {
		t.Fatalf("hook install failure should be logged, got: %q", buf.String())
	}
}

// F51 — hookCommandForExe is the testable seam: it takes the binary path
// explicitly so the rejection branch (shell metacharacters, relative paths)
// can be table-tested without faking os.Executable. The old test could only
// exercise the real test-binary path and had to t.Skip the rejection branch.
func TestHookCommandForExe(t *testing.T) {
	cases := []struct {
		name      string
		exe       string
		wantEmpty bool
	}{
		{"clean absolute path", "/usr/local/bin/uam", false},
		{"relative path rejected", "uam", true},
		{"dot-relative path rejected", "./uam", true},
		{"double-quote rejected", `/usr/local/bin/u"am`, true},
		{"single-quote rejected", "/usr/local/bin/u'am", true},
		{"backslash rejected", `/usr/local/bin/u\am`, true},
		{"dollar rejected", "/usr/local/bin/u$am", true},
		{"backtick rejected", "/usr/local/bin/u`am`", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := hookCommandForExe(tc.exe)
			if tc.wantEmpty {
				if cmd != "" {
					t.Fatalf("hookCommandForExe(%q) = %q, want empty (rejected)", tc.exe, cmd)
				}
				return
			}
			if cmd == "" {
				t.Fatalf("hookCommandForExe(%q) was rejected, want a command", tc.exe)
			}
			if !strings.Contains(cmd, "run-shell") {
				t.Fatalf("hook command must use run-shell: %q", cmd)
			}
			if !strings.Contains(cmd, "notify-closed") {
				t.Fatalf("hook command must reference notify-closed: %q", cmd)
			}
			if !strings.Contains(cmd, "'#{hook_session_name}'") {
				t.Fatalf("session name must be single-quoted for the inner shell: %q", cmd)
			}
			if !strings.Contains(cmd, tc.exe) {
				t.Fatalf("hook command must embed the binary path: %q", cmd)
			}
		})
	}
}

// TestSessionClosedHookCommandUsesRealBinary verifies the wrapper resolves the
// running binary and delegates to hookCommandForExe. The deterministic
// rejection-branch coverage lives in TestHookCommandForExe above.
func TestSessionClosedHookCommandUsesRealBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	if got, want := sessionClosedHookCommand(), hookCommandForExe(exe); got != want {
		t.Fatalf("sessionClosedHookCommand() = %q, want %q (from real binary path)", got, want)
	}
}

// TestShellQuoteIsInertUnderSh proves that values flowing through ShellJoin
// are passed literally to /bin/sh and cannot trigger command substitution,
// variable expansion, or word-splitting. /bin/sh is the faithful sink that
// tmux's `new-session <command>` ultimately feeds, so exercising sh directly
// keeps the test CI/air-gap portable (no real tmux required).
func TestShellQuoteIsInertUnderSh(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh unavailable: %v", err)
	}
	dangerous := []struct {
		name  string
		value string
	}{
		{"command substitution", "$(touch SENTINEL)"},
		{"backtick substitution", "`touch SENTINEL`"},
		{"variable expansion", "$HOME"},
		{"embedded single quote", "a'b"},
		{"embedded newline", "line1\nline2"},
	}
	for _, tc := range dangerous {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "SENTINEL")
			// Mirror the real call site: env-prefixed command joined for sh.
			joined := ShellJoin(commandWithEnv(map[string]string{"X": tc.value}, []string{"printf", "%s", tc.value}))
			// Run with cwd=dir so a relative `touch SENTINEL` (if substitution
			// fired) would land where we check for it.
			cmd := exec.Command("/bin/sh", "-c", joined)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sh -c %q failed: %v (out=%q)", joined, err, out)
			}
			if _, statErr := os.Stat(sentinel); statErr == nil {
				t.Fatalf("sentinel created — value was NOT inert: joined=%q", joined)
			}
			// The literal value must survive into argv (the env=VALUE prefix
			// also contains it). The first printf token echoes it back verbatim.
			if !strings.Contains(string(out), tc.value) {
				t.Fatalf("value not passed literally: want substring %q in out=%q (joined=%q)", tc.value, out, joined)
			}
		})
	}
}

func TestShellQuoteFormByInput(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"safe-token.v1", "safe-token.v1"},
		{"a'b", `'a'\''b'`},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Fatalf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCreateSessionRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{
		"uam-claude-deadbeef'; touch x",
		"uam-claude-$(touch x)",
		"uam-claude-deadbeef; rm -rf /",
		"`touch x`",
	} {
		c, logPath := setupFakeTmuxClient(t)
		err := c.CreateSession(context.Background(), name, "/tmp", nil, []string{"cmd"})
		if err == nil {
			t.Fatalf("CreateSession accepted unsafe name %q", name)
		}
		data, readErr := os.ReadFile(logPath)
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if strings.Contains(string(data), name) {
			t.Fatalf("fake tmux was invoked with unsafe name %q: %s", name, data)
		}
	}
}

func TestCreateSessionAcceptsValidUamNames(t *testing.T) {
	// Canonical shape produced by tmux_adapter.go: uam-<provider>-<id8hex>.
	c, logPath := setupFakeTmuxClient(t)
	if err := c.CreateSession(context.Background(), "uam-claude-deadbeef", "/tmp", nil, []string{"cmd"}); err != nil {
		t.Fatalf("CreateSession rejected a valid name: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "uam-claude-deadbeef") {
		t.Fatalf("valid session not created: %s", data)
	}
}

// newSendKeysRecorder returns a Client whose fake tmux records, for each
// invocation, one marker line "INVOKE <type> <rawlast>" where <type> is the
// keystroke kind ("literal" for send-keys -l, "enter" for send-keys Enter,
// "other" otherwise). Using $@ positionally is newline-safe: an embedded
// newline in the literal text cannot inflate the invocation count.
func newSendKeysRecorder(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
# Drop "-L <socket>" so $1 is the subcommand.
shift 2
kind=other
case " $* " in
  *" -l --"*) kind=literal ;;
  *" Enter"*) kind=enter ;;
esac
# Record exactly one marker line per invocation (newline-safe count).
printf 'INVOKE %s\n' "$kind" >> "$TMUX_LOG"
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "log")
	t.Setenv("TMUX_LOG", logPath)
	c := New("uam")
	c.Executable = script
	return c, logPath
}

// F13 characterization — a single-line prompt must keep the byte-identical
// sequence: one literal send-keys carrying the text, then exactly one Enter.
func TestSendLineSingleLineSequence(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.SendLine(context.Background(), "uam-a", "hello world"); err != nil {
		t.Fatalf("SendLine: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	logText := string(data)
	if !strings.Contains(logText, "send-keys -t uam-a -l -- hello world") {
		t.Fatalf("single-line literal sequence changed: %s", logText)
	}
	if strings.Count(logText, "Enter") != 1 {
		t.Fatalf("single-line prompt must emit exactly one Enter: %s", logText)
	}
}

// F13 — a multi-line prompt must submit ONCE: exactly one Enter keystroke (the
// final submit) and no interior Enter events, regardless of how many literal
// segments are sent.
func TestSendLineDoesNotEmitLiteralEmbeddedNewline(t *testing.T) {
	c, logPath := newSendKeysRecorder(t)
	if err := c.SendLine(context.Background(), "uam-a", "line1\nline2\nline3"); err != nil {
		t.Fatalf("SendLine: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	enters := strings.Count(string(data), "INVOKE enter")
	if enters != 1 {
		t.Fatalf("multi-line prompt must emit exactly one trailing Enter, got %d: %s", enters, data)
	}
	// More than one literal invocation proves the newlines were split into
	// separate keystrokes rather than bundled into one submit-on-newline blob.
	if literals := strings.Count(string(data), "INVOKE literal"); literals < 3 {
		t.Fatalf("interior newlines must be split into separate literal keystrokes, got %d: %s", literals, data)
	}
}

// F13 — a trailing newline is trimmed so a normal "text\n" stays a single
// submit rather than emitting a spurious extra Enter or an empty trailing
// segment.
func TestSendLineTrimsTrailingNewline(t *testing.T) {
	c, logPath := newSendKeysRecorder(t)
	if err := c.SendLine(context.Background(), "uam-a", "solo\n"); err != nil {
		t.Fatalf("SendLine: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if enters := strings.Count(string(data), "INVOKE enter"); enters != 1 {
		t.Fatalf("trailing-newline prompt must emit exactly one Enter, got %d: %s", enters, data)
	}
	if literals := strings.Count(string(data), "INVOKE literal"); literals != 1 {
		t.Fatalf("trailing-newline prompt should send one literal segment, got %d: %s", literals, data)
	}
}

// newScriptedClient builds a Client whose fake tmux runs the given /bin/sh body.
func newScriptedClient(t *testing.T, body string) *Client {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New("uam")
	c.Executable = script
	return c
}

// F10 — tmux 3.4 emits "(No such file or directory)" instead of the legacy
// "no server running" string when listing sessions against a fresh socket.
// List must treat that as an empty server, not an error.
func TestListReturnsEmptyOnFreshSocket(t *testing.T) {
	c := newScriptedClient(t, `case "$*" in
  *"list-sessions"*) echo "error connecting to /tmp/tmux-1000/uam (No such file or directory)" >&2; exit 1 ;;
esac
exit 0
`)
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("fresh socket should yield no error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("fresh socket should yield no sessions, got %v", list)
	}
}

// F10 trap — a genuine list-sessions failure (not a missing server) must still
// propagate as an error rather than being swallowed as an empty list.
func TestListPropagatesGenuineError(t *testing.T) {
	c := newScriptedClient(t, `case "$*" in
  *"list-sessions"*) echo "lost server: protocol version mismatch" >&2; exit 1 ;;
esac
exit 0
`)
	if _, err := c.List(context.Background()); err == nil {
		t.Fatal("genuine list-sessions failure must propagate as an error")
	}
}

func TestExecutablePathRejectsUnsafeOverrides(t *testing.T) {
	c := New("uam")
	c.Executable = "tmux"
	if _, err := c.ExecutablePath(); err == nil {
		t.Fatal("relative client executable should be rejected")
	}

	nonExecutable := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(nonExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Executable = nonExecutable
	if _, err := c.ExecutablePath(); err == nil {
		t.Fatal("non-executable client executable should be rejected")
	}

	t.Setenv("UAM_TMUX_BIN", "tmux")
	c.Executable = ""
	if _, err := c.ExecutablePath(); err == nil {
		t.Fatal("relative UAM_TMUX_BIN should be rejected")
	}
}
