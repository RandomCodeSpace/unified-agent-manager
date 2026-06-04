package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// prRescanInterval is how stale a session's last PR scrape may grow before List
// re-captures its pane. The PR URL is the only thing the per-session capture is
// for, and it appears once early then never changes — so capturing every 2s
// refresh tick forked a capture-pane per session per tick for no new signal.
// First discovery still captures immediately; subsequent ticks within this
// window reuse the store-persisted PR (re-hydrated by mergeStoredMetadata), and
// a fresh capture only fires once the window elapses (F16).
const prRescanInterval = 60 * time.Second

// prCaptureLines is the pane tail captured for PR scraping. A PR URL is emitted
// near where `gh`/the agent prints it, so a short tail is enough — the 200-line
// grab the peek path uses is wasteful here (F16).
const prCaptureLines = 40

type CommandCandidate struct {
	Display string
	Args    []string
}

type TmuxAgent struct {
	NameValue          string
	DisplayNameValue   string
	Candidates         []CommandCandidate
	YoloArgs           []string
	Tmux               *tmux.Client
	SessionArgs        func(req ResumeRequest, activity string) []string
	SkipPromptOnResume bool
	randomReader       io.Reader

	// now is the clock used to throttle per-session PR captures; overridable in
	// tests. lastPRScan records, per tmux session name, when its pane was last
	// captured for PR scraping so List can skip the capture within
	// prRescanInterval (F16).
	now        func() time.Time
	prScanMu   sync.Mutex
	lastPRScan map[string]time.Time
}

func NewTmuxAgent(name, display string, candidates []CommandCandidate, yoloArgs []string, client *tmux.Client) *TmuxAgent {
	if client == nil {
		client = tmux.New("uam")
	}
	return &TmuxAgent{NameValue: name, DisplayNameValue: display, Candidates: candidates, YoloArgs: yoloArgs, Tmux: client, randomReader: rand.Reader, now: time.Now, lastPRScan: map[string]time.Time{}}
}

func (a *TmuxAgent) Name() string        { return a.NameValue }
func (a *TmuxAgent) DisplayName() string { return a.DisplayNameValue }

func (a *TmuxAgent) Available() (bool, string) {
	_, ok := a.resolveCommand()
	if ok {
		return true, ""
	}
	if len(a.Candidates) == 0 {
		return false, "no command configured"
	}
	return false, fmt.Sprintf("%s not on PATH", a.Candidates[0].Display)
}

func (a *TmuxAgent) resolveCommand() ([]string, bool) {
	for _, c := range a.Candidates {
		if len(c.Args) == 0 {
			continue
		}
		if _, err := exec.LookPath(c.Args[0]); err == nil {
			return append([]string{}, c.Args...), true
		}
	}
	return nil, false
}

func (a *TmuxAgent) commandForMode(mode string) ([]string, error) {
	cmd, ok := a.resolveCommand()
	if !ok {
		return nil, fmt.Errorf("%s unavailable", a.Name())
	}
	return commandWithModeArgs(cmd, mode, a.YoloArgs), nil
}

func (a *TmuxAgent) commandForRequest(ctx context.Context, req ResumeRequest, extra []string) ([]string, error) {
	if req.CommandAlias == "" {
		cmd, err := a.commandForMode(req.Mode)
		if err != nil {
			return nil, err
		}
		return append(cmd, extra...), nil
	}
	if err := validateCommandAlias(req.CommandAlias); err != nil {
		return nil, err
	}
	cmd := commandWithModeArgs([]string{req.CommandAlias}, req.Mode, a.YoloArgs)
	cmd = append(cmd, extra...)
	if path, err := exec.LookPath(req.CommandAlias); err == nil {
		cmd[0] = path
		return cmd, nil
	}
	return shellAliasCommand(ctx, cmd)
}

func commandWithModeArgs(cmd []string, mode string, yoloArgs []string) []string {
	cmd = append([]string{}, cmd...)
	// Safe mode launches the bare command; no flag is the safe default for
	// claude/codex. Only non-safe modes append the provider's full-access args.
	if mode != "safe" {
		cmd = append(cmd, yoloArgs...)
	}
	return cmd
}

func validateCommandAlias(alias string) error {
	if alias == "" || strings.HasPrefix(alias, "-") {
		return fmt.Errorf("invalid command alias %q", alias)
	}
	for _, r := range alias {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid command alias %q", alias)
	}
	return nil
}

func shellAliasCommand(ctx context.Context, cmd []string) ([]string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if !filepath.IsAbs(shell) {
		return nil, fmt.Errorf("invalid SHELL %q: must be absolute for command alias fallback", shell)
	}
	check := exec.CommandContext(ctx, shell, "-ic", "type "+tmux.ShellJoin([]string{cmd[0]})+" >/dev/null 2>&1") // #nosec G204 -- shell is the user's configured absolute shell; alias name is validated before reaching this path.
	if err := check.Run(); err != nil {
		return nil, fmt.Errorf("command alias %q not found on PATH or in interactive shell: %w", cmd[0], err)
	}
	return []string{shell, "-ic", tmux.ShellJoin(cmd)}, nil
}

func (a *TmuxAgent) Dispatch(ctx context.Context, req DispatchRequest) (Session, error) {
	id, err := newID(a.randomReader)
	if err != nil {
		return Session{}, fmt.Errorf("generate session id: %w", err)
	}
	return a.startSession(ctx, ResumeRequest{ID: id, Name: req.Name, CommandAlias: req.CommandAlias, Prompt: req.Prompt, Cwd: req.Cwd, Mode: req.Mode}, "dispatched")
}

func (a *TmuxAgent) Resume(ctx context.Context, req ResumeRequest) (Session, error) {
	if req.ID == "" {
		id, err := newID(a.randomReader)
		if err != nil {
			return Session{}, fmt.Errorf("generate session id: %w", err)
		}
		req.ID = id
	}
	return a.startSession(ctx, req, "resumed")
}

func (a *TmuxAgent) startSession(ctx context.Context, req ResumeRequest, activity string) (Session, error) {
	extra := []string{}
	if a.SessionArgs != nil {
		extra = append(extra, a.SessionArgs(req, activity)...)
	}
	cmd, err := a.commandForRequest(ctx, req, extra)
	if err != nil {
		return Session{}, err
	}
	cwd, err := resolveSessionCwd(req.Cwd)
	if err != nil {
		return Session{}, err
	}
	tmuxName := req.TmuxSession
	if tmuxName == "" {
		tmuxName = fmt.Sprintf("uam-%s-%s", a.Name(), req.ID[:min(8, len(req.ID))])
	}
	env := map[string]string{"UAM_AGENT": a.Name(), "UAM_ID": req.ID}
	if err := a.Tmux.CreateSession(ctx, tmuxName, cwd, env, cmd); err != nil {
		return Session{}, fmt.Errorf("create tmux session %s: %w", tmuxName, err)
	}
	// Best-effort: apply uam-friendly tmux server settings (mouse/clipboard,
	// Ctrl+Z). This runs AFTER CreateSession so the server exists — applying it
	// first on the very first dispatch fails and used to latch that failure
	// (F25). Failures here don't prevent the session from being created.
	_ = a.Tmux.EnsureServerConfig(ctx)
	// Surface the user-facing name in tmux's status line, terminal title, and
	// window list (the canonical uam-<agent>-<id> stays as #S for uam's own
	// parsing). Cosmetic and best-effort — a failure never affects the session.
	displayName := req.Name
	if displayName == "" {
		displayName = displayNameFromDir(cwd)
	}
	if err := a.Tmux.SetSessionLabel(ctx, tmuxName, displayName+" · "+a.Name(), displayName); err != nil {
		log.Debug("set session label failed", "session", tmuxName, "error", err)
	}
	shouldSendPrompt := strings.TrimSpace(req.Prompt) != "" && (activity != "resumed" || !a.SkipPromptOnResume)
	if shouldSendPrompt {
		if err := a.Tmux.SendLine(ctx, tmuxName, req.Prompt); err != nil {
			// The session is live but never received its prompt. Roll it back so
			// it doesn't linger as an orphan the store records as Exited/closed.
			// Use WithoutCancel so a cancelled dispatch context still tears the
			// session down; the original SendLine error is what the caller sees.
			_ = a.Tmux.Kill(context.WithoutCancel(ctx), tmuxName)
			return Session{}, fmt.Errorf("send prompt to %s: %w", tmuxName, err)
		}
	}
	now := time.Now()
	created := req.CreatedAt
	if created.IsZero() {
		created = now
	}
	return Session{ID: req.ID, AgentType: a.Name(), CommandAlias: req.CommandAlias, DisplayName: displayName, Prompt: req.Prompt, Cwd: cwd, TmuxSession: tmuxName, State: Active, ProcAlive: Alive, CreatedAt: created, LastChange: now}, nil
}

func resolveSessionCwd(cwd string) (string, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
	}
	// Resolve the working directory to an absolute path once, before it is used
	// for both CreateSession (the tmux -c arg) and the returned Session.Cwd that
	// the store persists. A relative cwd persisted verbatim would be re-resolved
	// against uam's process cwd on resume, relaunching the agent in the wrong
	// directory (C2-4).
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return cwd, nil
}

func (a *TmuxAgent) List(ctx context.Context) ([]Session, error) {
	infos, err := a.Tmux.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list %s sessions: %w", a.Name(), err)
	}
	var out []Session
	prefix := "uam-" + a.Name() + "-"
	for _, info := range infos {
		if !strings.HasPrefix(info.Name, prefix) {
			continue
		}
		id := strings.TrimPrefix(info.Name, prefix)
		state, alive := ClassifyPane(tmux.PaneAlive(info.PanePID))
		created := time.Unix(info.CreatedUnix, 0)
		// Scrape the PR URL only on first discovery or once the rescan window
		// elapses. On a throttled tick PR stays nil; service.mergeStoredMetadata
		// re-hydrates it from the persisted record so the dashboard never loses
		// it (F16). Captures are sequential by design — parallelizing would fork
		// a burst of capture-pane subprocesses.
		var prRef *PRRef
		if a.shouldScanPR(info.Name) {
			capture, capErr := a.Tmux.Capture(ctx, info.Name, prCaptureLines)
			if capErr != nil {
				// Per-session and non-fatal: a failed PR scrape just leaves PR nil
				// for this tick (mergeStoredMetadata re-hydrates any known PR). Log
				// at debug so it's diagnosable without spamming the dashboard (F52).
				log.Debug("PR capture failed", "session", info.Name, "error", capErr)
			}
			prRef = ExtractPR(capture)
		}
		out = append(out, Session{ID: id, AgentType: a.Name(), DisplayName: id, Cwd: info.CurrentPath, TmuxSession: info.Name, State: state, ProcAlive: alive, LastChange: time.Now(), CreatedAt: created, PR: prRef})
	}
	return out, nil
}

// shouldScanPR reports whether the pane named tmuxName is due for a PR scrape
// and, if so, stamps the scan time. It is the per-session leaky bucket that
// keeps List from capturing every pane on every refresh tick (F16).
func (a *TmuxAgent) shouldScanPR(tmuxName string) bool {
	clock := a.now
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	a.prScanMu.Lock()
	defer a.prScanMu.Unlock()
	if a.lastPRScan == nil {
		a.lastPRScan = map[string]time.Time{}
	}
	last, seen := a.lastPRScan[tmuxName]
	if seen && now.Sub(last) < prRescanInterval {
		return false
	}
	a.lastPRScan[tmuxName] = now
	return true
}

func (a *TmuxAgent) Peek(ctx context.Context, id string) (PeekResult, error) {
	target := a.target(id)
	capture, err := a.Tmux.Capture(ctx, target, 200)
	if err != nil {
		return PeekResult{}, fmt.Errorf("peek %s session %s: %w", a.Name(), id, err)
	}
	return PeekResult{TailText: capture}, nil
}

func (a *TmuxAgent) Reply(ctx context.Context, id, text string) error {
	return a.Tmux.SendLine(ctx, a.target(id), text)
}
func (a *TmuxAgent) Attach(id string) (AttachSpec, error) {
	argv, err := a.Tmux.AttachArgv(a.target(id))
	if err != nil {
		return AttachSpec{}, fmt.Errorf("attach %s session %s: %w", a.Name(), id, err)
	}
	return AttachSpec{Argv: argv}, nil
}
func (a *TmuxAgent) Stop(ctx context.Context, id string) error { return a.Tmux.Kill(ctx, a.target(id)) }
func (a *TmuxAgent) HasSession(ctx context.Context, id string) bool {
	return a.Tmux.HasSession(ctx, a.target(id))
}

// target resolves an id to a tmux -t target. It anchors the name with tmux's
// `=` exact-match prefix so a longer neighbour that shares the truncated prefix
// is never hit by tmux's default prefix matching (F32). Human-facing prefix
// lookups stay in Service.Find; internal targeting is always exact.
func (a *TmuxAgent) target(id string) string {
	name := id
	if !strings.HasPrefix(id, "uam-") {
		name = fmt.Sprintf("uam-%s-%s", a.Name(), id[:min(8, len(id))])
	}
	return "=" + name
}

func newID(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	b := make([]byte, 16)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// displayNameFromDir derives a default session name from the working
// directory's base name (e.g. "/home/dev/projects/uam" -> "uam"). It is the
// fallback name when a dispatch provides no explicit #name.
func displayNameFromDir(cwd string) string {
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "untitled"
	}
	return base
}
