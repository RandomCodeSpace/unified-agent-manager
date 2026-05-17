package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type Client struct{ Socket string }

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
	cmd := exec.CommandContext(ctx, "tmux", c.baseArgs(args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (c *Client) CreateSession(ctx context.Context, name, cwd string, env map[string]string, command []string) error {
	args := []string{"new-session", "-d", "-s", name, "-x", "200", "-y", "50"}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, ShellJoin(command)+"; exec bash")
	_, err := c.run(ctx, args...)
	return err
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

func (c *Client) HasSession(ctx context.Context, target string) bool {
	_, err := c.run(ctx, "has-session", "-t", target)
	return err == nil
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
