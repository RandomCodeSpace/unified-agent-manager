package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// callTimeout is the upper bound on a single host round-trip. It is an upper
// bound, not a floor: a tighter caller deadline still wins. Without it a hung
// host could block a refresh indefinitely — the same contract the old
// tmuxCallTimeout enforced (F17).
const callTimeout = 10 * time.Second

// createTimeout bounds how long CreateSession waits for a spawned host to
// report ready. Host startup is local fork/exec plus a PTY open, so this is
// generous; hitting it means the host wedged and gets cleaned up.
const createTimeout = 10 * time.Second

// Client talks to per-session host processes. It is the drop-in replacement
// for the old tmux.Client: same operations, but against uam's own session
// hosts instead of a tmux server.
type Client struct {
	// Dir is the runtime directory holding sockets and state files.
	Dir string
	// Exe overrides the binary used to spawn hosts and attach clients
	// (normally the running uam binary itself). Tests point it at the test
	// binary.
	Exe string
}

type CreateSpec struct {
	Name             string
	Cwd              string
	ProviderIdentity string
	ScrollbackLines  int
	Env              map[string]string
	Command          []string
}

func NewClient() *Client {
	return &Client{Dir: DefaultDir()}
}

// exePath resolves the binary that will run `__host` / `__attach`.
func (c *Client) exePath() (string, error) {
	exe := c.Exe
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve uam binary: %w", err)
		}
	}
	if err := execpath.ValidateAbsoluteExecutable(exe); err != nil {
		return "", fmt.Errorf("invalid uam binary for session host: %w", err)
	}
	return exe, nil
}

// CreateSession spawns a detached host running command in cwd. It returns
// once the host reports the agent started (or with the host's startup error),
// mirroring the synchronous contract of `tmux new-session -d`.
func (c *Client) CreateSession(ctx context.Context, name, cwd string, env map[string]string, command []string) error {
	return c.CreateProviderSession(ctx, CreateSpec{Name: name, Cwd: cwd, Env: env, Command: command})
}

func (c *Client) CreateProviderSession(ctx context.Context, spec CreateSpec) error {
	if err := ValidateName(spec.Name); err != nil {
		return fmt.Errorf("refusing to create session: %w", err)
	}
	if err := validateProviderIdentity(spec.ProviderIdentity); err != nil {
		return fmt.Errorf("refusing to create session: %w", err)
	}
	if len(spec.Command) == 0 {
		return errors.New("create session: empty command")
	}
	scrollbackLines, err := validatedScrollbackLines(spec.ScrollbackLines)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	exe, err := c.exePath()
	if err != nil {
		return err
	}
	if err := EnsureDir(c.Dir); err != nil {
		return err
	}
	args := []string{"__host", "--dir", c.Dir, "--name", spec.Name}
	args = append(args, "--scrollback", fmt.Sprintf("%d", scrollbackLines))
	if spec.Cwd != "" {
		args = append(args, "--cwd", spec.Cwd)
	}
	if spec.ProviderIdentity != "" {
		args = append(args, "--provider", spec.ProviderIdentity)
	}
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "--env", k+"="+spec.Env[k])
	}
	args = append(args, "--")
	args = append(args, spec.Command...)

	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create readiness pipe: %w", err)
	}
	defer func() { _ = r.Close() }()
	cmd := exec.Command(exe, args...) // #nosec G204 -- exe is the validated uam binary; args are built above without a shell.
	// The host must outlive this process: detach it into its own session so
	// TUI exit, terminal close, or Ctrl+C never propagates to running agents.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "UAM_HOST_READY_FD=3")
	cmd.ExtraFiles = []*os.File{w}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return fmt.Errorf("spawn session host: %w", err)
	}
	_ = w.Close()
	// Reap the host whenever it eventually exits so it never lingers as a
	// zombie under a long-lived TUI process.
	go func() { _ = cmd.Wait() }()
	return waitReady(ctx, r, spec.Name, cmd.Process.Pid)
}

func waitReady(ctx context.Context, r *os.File, name string, hostPID int) error {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		ch <- result{line: strings.TrimSpace(line), err: err}
	}()
	select {
	case res := <-ch:
		if res.line == "ok" {
			return nil
		}
		if msg, found := strings.CutPrefix(res.line, "error: "); found {
			return fmt.Errorf("create session %s: %s", name, msg)
		}
		if res.err != nil {
			return fmt.Errorf("create session %s: host exited before ready: %w", name, res.err)
		}
		return fmt.Errorf("create session %s: unexpected host response %q", name, res.line)
	case <-time.After(createTimeout):
		_ = syscall.Kill(hostPID, syscall.SIGKILL)
		return fmt.Errorf("create session %s: host did not become ready", name)
	case <-ctx.Done():
		_ = syscall.Kill(hostPID, syscall.SIGKILL)
		return ctx.Err()
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// List enumerates live sessions by scanning the runtime directory's state
// files — no subprocess, no socket round-trips. Leftovers from a crashed host
// are swept once both the host and its agent are gone.
func (c *Client) List(_ context.Context) ([]Info, error) {
	if err := VerifyDir(c.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan session dir: %w", err)
	}
	var out []Info
	for _, e := range entries {
		if info, keep := c.infoFromStateEntry(e); keep {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) infoFromStateEntry(entry os.DirEntry) (Info, bool) {
	name, isState := strings.CutSuffix(entry.Name(), ".json")
	if !isState || ValidateName(name) != nil {
		return Info{}, false
	}
	st, err := readState(c.Dir, name)
	if err != nil {
		log.Warn("skipping unreadable session state", "file", entry.Name(), "error", err)
		return Info{}, false
	}
	if st.hostAlive() {
		return infoFromState(st), true
	}
	if st.childAlive() {
		// Host crashed but the agent is still winding down. Keep it visible
		// and leave the runtime files for the next sweep.
		return infoFromState(st), true
	}
	_ = removeSessionFiles(c.Dir, name)
	return Info{}, false
}

func infoFromState(st State) Info {
	cwd := procCwd(st.ChildPID)
	if cwd == "" {
		cwd = st.Cwd
	}
	return Info{
		Name:        st.Name,
		CreatedUnix: st.CreatedUnix,
		ChildPID:    st.ChildPID,
		Cwd:         cwd,
		Alive:       st.childAlive(),
	}
}

// Capture returns the rendered tail of the session's terminal, like
// `tmux capture-pane -p -J` did.
func (c *Client) Capture(ctx context.Context, name string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	resp, err := c.roundTrip(ctx, name, request{Op: opPeek, Lines: lines})
	if err != nil {
		return "", err
	}
	return resp.Data, nil
}

// SendLine types text into the session and submits it with a single Enter
// (carriage return). Interior newlines are delivered literally so a
// multi-line prompt lands in the agent's input buffer as one prompt — the
// same contract the tmux SendLine implemented keystroke-by-keystroke (F13).
func (c *Client) SendLine(ctx context.Context, name, text string) error {
	payload := strings.TrimRight(text, "\n") + "\r"
	_, err := c.roundTrip(ctx, name, request{Op: opSend, Text: payload})
	return err
}

// Kill terminates the session's agent and waits for the host to confirm the
// session is gone. Killing a session that does not exist is an error, like
// `tmux kill-session` (callers that need idempotence probe HasSession).
func (c *Client) Kill(ctx context.Context, name string) error {
	rtErr := func() error {
		_, err := c.roundTrip(ctx, name, request{Op: opKill})
		return err
	}()
	if rtErr == nil {
		return nil
	}
	st, stErr := readState(c.Dir, name)
	if stErr != nil {
		if errors.Is(stErr, os.ErrNotExist) {
			// No state at all: nothing to kill. An error, like tmux
			// kill-session on a missing target; callers needing idempotence
			// probe HasSession (Service.Stop does).
			return fmt.Errorf("kill session %s: %w", name, rtErr)
		}
		return stErr
	}
	// State exists but the socket path failed: the host is wedged, crashed,
	// or mid-shutdown. Escalate directly and wait for both processes to go.
	// A live PID is not sufficient authority to signal: fallback control paths
	// require a nonzero matching start identity so a recycled PID can never be
	// signalled as if it were the session.
	if err := signalVerifiedFallback(name, st); err != nil {
		return err
	}
	deadline := time.Now().Add(callTimeout)
	for time.Now().Before(deadline) {
		if !st.childAlive() && !st.hostAlive() {
			_ = removeSessionFiles(c.Dir, name)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("kill session %s: still running", name)
}

func signalVerifiedFallback(name string, st State) error {
	if ProcAlive(st.HostPID) {
		if !procIdentityMatches(st.HostPID, st.HostStart) {
			return fmt.Errorf("kill session %s: cannot verify process identity for host pid %d", name, st.HostPID)
		}
		_ = syscall.Kill(st.HostPID, syscall.SIGTERM)
		return nil
	}
	if !ProcAlive(st.ChildPID) {
		return nil
	}
	if !procIdentityMatches(st.ChildPID, st.ChildStart) {
		return fmt.Errorf("kill session %s: cannot verify process identity for child pid %d", name, st.ChildPID)
	}
	// Orphaned agent (host crashed): signal its process group directly.
	if err := syscall.Kill(-st.ChildPID, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(st.ChildPID, syscall.SIGTERM)
	}
	return nil
}

// KillAll terminates every managed session. It replaces `tmux kill-server`
// and is idempotent: an empty (or missing) runtime directory is success.
func (c *Client) KillAll(ctx context.Context) error {
	infos, err := c.List(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, info := range infos {
		if err := c.Kill(ctx, info.Name); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// HasSession reports whether a live host exists for name.
func (c *Client) HasSession(_ context.Context, name string) bool {
	st, err := readState(c.Dir, name)
	return err == nil && st.hostAlive()
}

// SetSessionLabel records the user-facing label for a live session; the host
// persists it and updates attached terminals' titles. Cosmetic: callers treat
// failures as non-fatal.
func (c *Client) SetSessionLabel(ctx context.Context, name, label string) error {
	_, err := c.roundTrip(ctx, name, request{Op: opLabel, Label: label})
	return err
}

// AttachArgv returns the argv that attaches the current terminal to the
// session — the uam binary's own attach client instead of `tmux attach`.
func (c *Client) AttachArgv(name string) ([]string, error) {
	exe, err := c.exePath()
	if err != nil {
		return nil, err
	}
	return []string{exe, "__attach", "--dir", c.Dir, name}, nil
}

func (c *Client) roundTrip(ctx context.Context, name string, req request) (response, error) {
	if err := ValidateName(strings.TrimPrefix(name, "=")); err != nil {
		return response{}, err
	}
	name = strings.TrimPrefix(name, "=")
	if err := VerifyDir(c.Dir); err != nil {
		return response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", SocketPath(c.Dir, name))
	if err != nil {
		return response{}, fmt.Errorf("session %s: %w", name, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := writeJSONLine(conn, req); err != nil {
		return response{}, fmt.Errorf("session %s: send %s: %w", name, req.Op, err)
	}
	var resp response
	if err := readJSONLine(bufio.NewReader(conn), &resp); err != nil {
		return response{}, fmt.Errorf("session %s: read %s response: %w", name, req.Op, err)
	}
	if !resp.OK {
		if resp.ErrorCode == errorCodeBusy {
			return resp, fmt.Errorf("session %s: %w", name, &SessionBusyError{Operation: req.Op})
		}
		return resp, fmt.Errorf("session %s: %s failed: %s", name, req.Op, resp.Err)
	}
	return resp, nil
}
