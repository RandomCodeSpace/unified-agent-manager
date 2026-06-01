package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// ErrInvalidSessionName is returned when a session name fails the allow-list.
var ErrInvalidSessionName = fmt.Errorf("session name failed allow-list")

// tmuxCallTimeout is the upper bound on a single tmux invocation. It is an
// upper bound, not a floor: a tighter caller deadline still wins. Without it a
// hung tmux (stuck server, lost pty) would block a refresh indefinitely (F17).
const tmuxCallTimeout = 10 * time.Second

// tmuxWaitDelay is the short grace period after the context is cancelled before
// the child's I/O pipes are force-closed. Without it CombinedOutput keeps
// blocking on a pipe a grandchild inherited, so the deadline never unblocks the
// caller (F17). Kept short so a cancelled call returns promptly.
const tmuxWaitDelay = 2 * time.Second

// listCacheTTL is how long a List result is reused before re-querying tmux. A
// single refresh tick calls List once per enabled adapter against the same
// shared Client; without a cache that is one whole-server list-sessions per
// adapter per tick. The TTL is short enough that a cached result is always from
// the current tick, so freshness is unaffected, while N adapter scans collapse
// to one shell-out (F60).
const listCacheTTL = 250 * time.Millisecond

// sessionNameRE is the allow-list for tmux session names uam may create. It
// matches the canonical shape minted by adapter.startSession
// ("uam-<provider>-<id>"): a lowercase-alphanumeric provider segment and a
// hex id segment. The pattern admits no shell metacharacters, so a name that
// passes can be embedded in tmux argv without risk.
var sessionNameRE = regexp.MustCompile(`^uam-[a-z0-9]+-[0-9a-f]{1,16}$`)

type Client struct {
	Socket     string
	Executable string

	configMu   sync.Mutex
	configDone bool

	// now is the clock used to expire the List cache; overridable in tests.
	now func() time.Time
	// listMu guards the cached List result and the time it was taken. The cache
	// collapses the per-adapter list-sessions storm within a single refresh tick
	// into one shell-out (F60).
	listMu       sync.Mutex
	listCache    []SessionInfo
	listCachedAt time.Time
	listCacheOK  bool
}

func New(socket string) *Client {
	if socket == "" {
		socket = "uam"
	}
	return &Client{Socket: socket, now: time.Now}
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
	// Bound every tmux invocation so a hung process can't wedge a refresh. This
	// is an upper bound only — a tighter caller deadline still applies (F17).
	// Interactive attach does not flow through run (cli execs the argv directly),
	// so this never caps a foreground session.
	ctx, cancel := context.WithTimeout(ctx, tmuxCallTimeout)
	defer cancel()
	// The tmux path is resolved from fixed system directories or injected as an
	// absolute test path. tmux's own args are passed via argv (no shell). Where
	// an arg is itself a /bin/sh command string (the new-session command built
	// by ShellJoin), every value is POSIX single-quote escaped by shellQuote, so
	// $(), ``, $VAR, and word-splitting cannot fire inside it.
	cmd := exec.CommandContext(ctx, exe, c.baseArgs(args...)...) // #nosec G204
	// Reap a hung tmux promptly once the deadline fires: WaitDelay caps how long
	// CombinedOutput waits for the (possibly inherited) output pipe before force-
	// closing it, so the timeout actually unblocks the caller (F17).
	cmd.WaitDelay = tmuxWaitDelay
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
	if !sessionNameRE.MatchString(name) {
		return fmt.Errorf("refusing to create session: invalid name %q: %w", name, ErrInvalidSessionName)
	}
	args := []string{"new-session", "-d", "-s", name, "-x", "200", "-y", "50"}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, ShellJoin(commandWithEnv(env, command)))
	_, err := c.run(ctx, args...)
	return err
}

// SetSessionLabel records the user-facing label for a live session so tmux's
// status line, terminal title, and window list show the name the user gave it
// rather than the canonical uam-<agent>-<id> session name (which must stay
// machine-parseable — see CreateSession / sessionNameRE). It writes the label
// to the @uam_label session option (applyServerConfig points status-left and
// set-titles-string at it, falling back to #S when unset) and renames the
// session's window to the short name. Cosmetic: callers treat failures as
// non-fatal.
func (c *Client) SetSessionLabel(ctx context.Context, session, label, window string) error {
	if _, err := c.run(ctx, "set-option", "-t", session, "@uam_label", label); err != nil {
		return fmt.Errorf("set @uam_label for %s: %w", session, err)
	}
	if window != "" {
		if _, err := c.run(ctx, "rename-window", "-t", session, window); err != nil {
			return fmt.Errorf("rename-window for %s: %w", session, err)
		}
	}
	return nil
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
	if cached, ok := c.cachedList(); ok {
		return cached, nil
	}
	out, err := c.run(ctx, "list-sessions", "-F", ListFormat)
	if err != nil {
		// tmux exits non-zero when the private server has no sessions. Match the
		// known no-server phrasings only; a genuine failure must still propagate.
		if isNoServerErr(err) {
			c.storeList(nil)
			return nil, nil
		}
		return nil, err
	}
	// Server is up — apply uam-friendly settings (latches once it succeeds).
	_ = c.EnsureServerConfig(ctx)
	// A malformed line (e.g. a cwd containing '|') yields the parsed subset plus
	// ErrMalformedSessionLines; ParseListSessions already logged it. Returning
	// the subset keeps the healthy sessions visible instead of blanking the
	// whole list, so the sentinel is intentionally not propagated (F11).
	sessions, err := ParseListSessions(out)
	if errors.Is(err, ErrMalformedSessionLines) {
		err = nil
	}
	if err != nil {
		return sessions, err
	}
	c.storeList(sessions)
	return cloneSessionInfos(sessions), nil
}

// cachedList returns a defensive copy of the cached List result when it is
// still within listCacheTTL, collapsing the per-adapter list-sessions storm in
// one refresh tick into a single shell-out (F60).
func (c *Client) cachedList() ([]SessionInfo, bool) {
	clock := c.now
	if clock == nil {
		clock = time.Now
	}
	c.listMu.Lock()
	defer c.listMu.Unlock()
	if !c.listCacheOK || clock().Sub(c.listCachedAt) >= listCacheTTL {
		return nil, false
	}
	return cloneSessionInfos(c.listCache), true
}

func (c *Client) storeList(sessions []SessionInfo) {
	clock := c.now
	if clock == nil {
		clock = time.Now
	}
	c.listMu.Lock()
	defer c.listMu.Unlock()
	c.listCache = cloneSessionInfos(sessions)
	c.listCachedAt = clock()
	c.listCacheOK = true
}

// cloneSessionInfos returns a shallow copy so a caller mutating the returned
// slice cannot corrupt the cache (SessionInfo holds only value fields).
func cloneSessionInfos(in []SessionInfo) []SessionInfo {
	if in == nil {
		return nil
	}
	out := make([]SessionInfo, len(in))
	copy(out, in)
	return out
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

// SendLine types text into the target pane and submits it with a single Enter.
//
// tmux's `send-keys -l` interprets an embedded newline as Enter, so passing a
// multi-line prompt as one literal made the agent submit it line-by-line (F13).
// Instead we trim a trailing newline, then send each interior line as its own
// literal keystroke separated by a literal "\n" keystroke — no interior Enter
// events — and submit once at the end. A single-line prompt takes the original
// one-literal-plus-one-Enter path byte-for-byte.
func (c *Client) SendLine(ctx context.Context, target, text string) error {
	text = strings.TrimRight(text, "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 0 {
			// Send the line separator as its own literal keystroke so it lands
			// in the input buffer instead of submitting the partial prompt.
			if err := c.SendKeysLiteral(ctx, target, "\n"); err != nil {
				return err
			}
		}
		if err := c.SendKeysLiteral(ctx, target, line); err != nil {
			return err
		}
	}
	return c.SendEnter(ctx, target)
}

func (c *Client) Kill(ctx context.Context, target string) error {
	_, err := c.run(ctx, "kill-session", "-t", target)
	return err
}

// KillServer tears down the entire private tmux server (and every session it
// holds) via `tmux -L <socket> kill-server`. It is idempotent: a server that is
// already down makes tmux exit non-zero with a no-server message, which is
// treated as success so `uam kill-all` is safe to run repeatedly. A genuine
// failure still propagates (F24).
func (c *Client) KillServer(ctx context.Context) error {
	if _, err := c.run(ctx, "kill-server"); err != nil {
		if isNoServerErr(err) {
			return nil
		}
		return err
	}
	return nil
}

// isNoServerErr reports whether err is tmux's "the private server has no
// running instance" message. Different tmux versions phrase it differently;
// 3.4 reports the missing socket as "(No such file or directory)".
func isNoServerErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "failed to connect") ||
		strings.Contains(msg, "No such file or directory")
}

// EnsureServerConfig applies session-friendly defaults to the private tmux
// server: disable mouse mode so the host terminal owns text selection, and
// swallow Ctrl+Z so it can't suspend the agent in the foreground pane.
//
// The configuration is applied at most once SUCCESSFULLY. The first dispatch
// runs before the server exists, so set-option fails; latching that failure
// (the old sync.Once behaviour) meant the config never applied for the life of
// the process (F25). Instead we retry until a call succeeds, then latch.
func (c *Client) EnsureServerConfig(ctx context.Context) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	if c.configDone {
		return nil
	}
	if err := c.applyServerConfig(ctx); err != nil {
		return err
	}
	c.configDone = true
	return nil
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
	// Surface the user-facing session name (written per-session to @uam_label by
	// SetSessionLabel) in the status line, terminal title, and window list,
	// instead of the canonical uam-<agent>-<id> name that stays as #S for uam's
	// own parsing. automatic-rename/allow-rename are disabled so the running
	// agent can't clobber the window name. These are cosmetic, so a tmux that
	// rejects one must not block the rest of the config or its latch — log, not
	// fail.
	for _, opt := range [][2]string{
		{"automatic-rename", "off"},
		{"allow-rename", "off"},
		{"status-left-length", "40"},
		{"status-left", " #{?@uam_label,#{@uam_label},#S} "},
		{"set-titles", "on"},
		{"set-titles-string", "#{?@uam_label,#{@uam_label},#S}"},
	} {
		if out, err := c.run(ctx, "set-option", "-g", opt[0], opt[1]); err != nil {
			log.Warn("set tmux display option failed", "option", opt[0], "error", err, "output", strings.TrimSpace(out))
		}
	}
	// Hook install is best-effort. If we can't resolve a safe binary path,
	// the rest of uam still works — only the exit-in-session signal is lost,
	// and the user can recover via Ctrl+X or `uam rm`. We log (not return) the
	// failure so a missing hook is diagnosable without bricking dispatch (F56).
	if cmd := sessionClosedHookCommand(); cmd != "" {
		if out, err := c.run(ctx, "set-hook", "-g", "session-closed", cmd); err != nil {
			log.Warn("installing session-closed hook failed", "error", err, "output", strings.TrimSpace(out))
		}
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
	return hookCommandForExe(exe)
}

// hookCommandForExe builds the session-closed hook command for a given binary
// path, or returns empty string when the path isn't safe to embed. It is split
// out from sessionClosedHookCommand so the rejection branch can be table-tested
// directly without faking os.Executable (F51). The real-file check
// (ValidateAbsoluteExecutable) stays in the caller; this seam only enforces the
// quoting-safety rules that govern whether a path can be embedded.
func hookCommandForExe(exe string) string {
	// An absolute path is required: the hook is a /bin/sh command string and a
	// relative path would resolve against tmux's cwd, not uam's install dir.
	if !filepath.IsAbs(exe) {
		return ""
	}
	// Reject paths with shell metacharacters we'd otherwise need to escape.
	// Real uam installs land in standard bin directories without these,
	// and bailing out is safer than risking a malformed hook.
	if strings.ContainsAny(exe, "\"'\\$`") {
		return ""
	}
	// run-shell receives a /bin/sh command string. tmux expands
	// #{hook_session_name} INTO that string before sh parses it, so the single
	// quotes here do NOT neutralize a hostile name on their own — a name
	// containing a quote would break out. Safety comes from CreateSession's
	// allow-list (sessionNameRE), which guarantees every name we ever create is
	// [a-z0-9-] only; the quoting then merely keeps a benign name as one argv
	// token.
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
	// POSIX single-quote escaping: wrap in single quotes and rewrite any
	// embedded single quote as the close-reopen idiom '\''. Inside single
	// quotes /bin/sh performs no expansion, so $(), ``, $VAR, and newlines
	// all reach the command literally.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
