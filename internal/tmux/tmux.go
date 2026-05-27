package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

type Client struct {
	Socket     string
	Executable string

	configOnce sync.Once
	configErr  error
}

func New(socket string) *Client {
	if socket == "" {
		socket = "uam"
	}
	return &Client{Socket: socket}
}

func (c *Client) baseArgs(args ...string) []string {
	out := []string{"-L", c.Socket}
	return append(out, args...)
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	exe, err := c.ExecutablePath()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, exe, c.baseArgs(args...)...) // #nosec G204 -- tmux path is resolved from fixed system directories or injected as an absolute test path; argv args avoid shell expansion.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (c *Client) ExecutablePath() (string, error) {
	if v := os.Getenv("UAM_TMUX_BIN"); v != "" {
		if err := execpath.ValidateAbsoluteExecutable(v); err != nil {
			return "", fmt.Errorf("invalid UAM_TMUX_BIN: %w", err)
		}
		return v, nil
	}
	if c.Executable == "" {
		return execpath.Resolve("tmux")
	}
	if err := execpath.ValidateAbsoluteExecutable(c.Executable); err != nil {
		return "", fmt.Errorf("invalid tmux executable: %w", err)
	}
	return c.Executable, nil
}

func (c *Client) CreateSession(ctx context.Context, name, cwd string, env map[string]string, command []string) error {
	args := []string{"new-session", "-d", "-s", name, "-x", "200", "-y", "50"}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, ShellJoin(commandWithEnv(env, command)))
	_, err := c.run(ctx, args...)
	return err
}

func commandWithEnv(env map[string]string, command []string) []string {
	if len(env) == 0 {
		return command
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, 1+len(keys)+len(command))
	out = append(out, "env")
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	out = append(out, command...)
	return out
}

func (c *Client) List(ctx context.Context) ([]SessionInfo, error) {
	out, err := c.run(ctx, "list-sessions", "-F", ListFormat)
	if err != nil {
		// tmux exits non-zero when the private server has no sessions.
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "failed to connect") {
			return nil, nil
		}
		return nil, err
	}
	// Server is up — apply uam-friendly settings (sync.Once, no-op after first).
	_ = c.EnsureServerConfig(ctx)
	return ParseListSessions(out)
}

func (c *Client) Capture(ctx context.Context, target string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	out, err := c.run(ctx, "capture-pane", "-p", "-t", target, "-S", fmt.Sprintf("-%d", lines), "-J")
	return out, err
}

func (c *Client) SendKeysLiteral(ctx context.Context, target, text string) error {
	_, err := c.run(ctx, "send-keys", "-t", target, "-l", "--", text)
	return err
}

func (c *Client) SendEnter(ctx context.Context, target string) error {
	_, err := c.run(ctx, "send-keys", "-t", target, "Enter")
	return err
}

func (c *Client) SendLine(ctx context.Context, target, text string) error {
	if err := c.SendKeysLiteral(ctx, target, text); err != nil {
		return err
	}
	return c.SendEnter(ctx, target)
}

func (c *Client) Kill(ctx context.Context, target string) error {
	_, err := c.run(ctx, "kill-session", "-t", target)
	return err
}

// EnsureServerConfig applies session-friendly defaults to the private tmux
// server: disable mouse mode so the host terminal owns text selection, and
// swallow Ctrl+Z so it can't suspend the agent in the foreground pane. The
// configuration is applied exactly once per Client.
func (c *Client) EnsureServerConfig(ctx context.Context) error {
	c.configOnce.Do(func() {
		c.configErr = c.applyServerConfig(ctx)
	})
	return c.configErr
}

func (c *Client) applyServerConfig(ctx context.Context) error {
	if _, err := c.run(ctx, "set-option", "-g", "mouse", "off"); err != nil {
		return fmt.Errorf("set mouse off: %w", err)
	}
	// Forward any tmux-side copy to the host terminal's clipboard via OSC 52
	// so the user can paste outside the session with the usual shortcut.
	if _, err := c.run(ctx, "set-option", "-g", "set-clipboard", "on"); err != nil {
		return fmt.Errorf("set set-clipboard on: %w", err)
	}
	if _, err := c.run(ctx, "bind-key", "-n", "C-z", "display-message", "Ctrl+Z is disabled in uam sessions; use Ctrl+b d to detach"); err != nil {
		return fmt.Errorf("bind C-z: %w", err)
	}
	// Hook install is best-effort. If we can't resolve a safe binary path,
	// the rest of uam still works — only the exit-in-session signal is lost,
	// and the user can recover via Ctrl+X or `uam rm`.
	if cmd := sessionClosedHookCommand(); cmd != "" {
		_, _ = c.run(ctx, "set-hook", "-g", "session-closed", cmd)
	}
	return nil
}

// sessionClosedHookCommand returns the tmux command to install as the
// session-closed hook, or empty string if the uam binary path isn't safe
// to embed (path contains characters that would break shell quoting).
//
// The hook fires whenever a session is destroyed — both when the user types
// `exit` inside the agent and when uam itself calls kill-session. In either
// case the record gets flagged closed_by_user; uam-initiated paths that
// follow up by deleting the record (Ctrl+X / `uam rm`) make the flag moot.
func sessionClosedHookCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if err := execpath.ValidateAbsoluteExecutable(exe); err != nil {
		return ""
	}
	// Reject paths with shell metacharacters we'd otherwise need to escape.
	// Real uam installs land in standard bin directories without these,
	// and bailing out is safer than risking a malformed hook.
	if strings.ContainsAny(exe, "\"'\\$`") {
		return ""
	}
	// run-shell receives a /bin/sh command string. tmux substitutes
	// #{hook_session_name} before sh sees it; single quotes around the
	// substitution prevent sh from expanding anything inside.
	return fmt.Sprintf(`run-shell "%s notify-closed '#{hook_session_name}'"`, exe)
}

func (c *Client) HasSession(ctx context.Context, target string) bool {
	_, err := c.run(ctx, "has-session", "-t", target)
	return err == nil
}

func (c *Client) AttachArgv(target string) ([]string, error) {
	exe, err := c.ExecutablePath()
	if err != nil {
		return nil, err
	}
	return append([]string{exe}, c.baseArgs("attach", "-t", target)...), nil
}

func (c *Client) AttachArgs(target string) []string { return c.baseArgs("attach", "-t", target) }

func PaneAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func ShellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !isShellSafeRune(r)
	}) == -1 {
		return s
	}
	return strconv.Quote(s)
}

func isShellSafeRune(r rune) bool {
	if r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '+' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	return r >= 'a' && r <= 'z'
}
