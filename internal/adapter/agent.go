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

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

// prRescanInterval is how stale a session's last PR scrape may grow before List
// re-captures its output. The PR URL is the only thing the per-session capture
// is for, and it appears once early then never changes — so capturing every 2s
// refresh tick burned a capture round-trip per session per tick for no new
// signal. First discovery still captures immediately; subsequent ticks within
// this window reuse the store-persisted PR (re-hydrated by
// mergeStoredMetadata), and a fresh capture only fires once the window elapses
// (F16).
const prRescanInterval = 60 * time.Second

// prCaptureLines is the output tail captured for PR scraping. A PR URL is
// emitted near where `gh`/the agent prints it, so a short tail is enough — the
// 200-line grab the peek path uses is wasteful here (F16).
const prCaptureLines = 40

type CommandCandidate struct {
	Display string
	Args    []string
}

// Backend is the session-management surface an Agent drives: create / list /
// capture / reply / kill / attach against uam's native session hosts
// (internal/session.Client in production, fakes in tests).
type Backend interface {
	CreateSession(ctx context.Context, name, cwd string, env map[string]string, command []string) error
	SetSessionLabel(ctx context.Context, name, label string) error
	List(ctx context.Context) ([]session.Info, error)
	Capture(ctx context.Context, name string, lines int) (string, error)
	SendLine(ctx context.Context, name, text string) error
	Kill(ctx context.Context, name string) error
	HasSession(ctx context.Context, name string) bool
	AttachArgv(name string) ([]string, error)
}

// Agent adapts one provider CLI (claude, codex, ...) onto the shared session
// backend. It was previously named TmuxAgent; the lifecycle contract is
// unchanged, only the backend is now uam's own session hosts.
type Agent struct {
	NameValue        string
	DisplayNameValue string
	Candidates       []CommandCandidate
	YoloArgs         []string
	Backend          Backend
	SessionArgs      func(req ResumeRequest, activity string) []string
	// ProviderSession optionally reports the provider-side session id that
	// the launched agent will use (e.g. the uuid claude was seeded with via
	// --session-id), or "" when unknown. It is persisted so a later resume
	// can target the exact provider session (F-resume).
	ProviderSession    func(req ResumeRequest, activity string) string
	SkipPromptOnResume bool
	randomReader       io.Reader

	// now is the clock used to throttle per-session PR captures; overridable in
	// tests. lastPRScan records, per session name, when its output was last
	// captured for PR scraping so List can skip the capture within
	// prRescanInterval (F16). List prunes entries whose session is gone so the
	// map cannot grow without bound across many session lifetimes.
	now        func() time.Time
	prScanMu   sync.Mutex
	lastPRScan map[string]time.Time
}

func NewAgent(name, display string, candidates []CommandCandidate, yoloArgs []string, backend Backend) *Agent {
	if backend == nil {
		backend = session.NewClient()
	}
	return &Agent{NameValue: name, DisplayNameValue: display, Candidates: candidates, YoloArgs: yoloArgs, Backend: backend, randomReader: rand.Reader, now: time.Now, lastPRScan: map[string]time.Time{}}
}

func (a *Agent) Name() string        { return a.NameValue }
func (a *Agent) DisplayName() string { return a.DisplayNameValue }

func (a *Agent) Available() (bool, string) {
	_, ok := a.resolveCommand()
	if ok {
		return true, ""
	}
	if len(a.Candidates) == 0 {
		return false, "no command configured"
	}
	return false, fmt.Sprintf("%s not on PATH", a.Candidates[0].Display)
}

func (a *Agent) resolveCommand() ([]string, bool) {
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

func (a *Agent) commandForMode(mode string) ([]string, error) {
	cmd, ok := a.resolveCommand()
	if !ok {
		return nil, fmt.Errorf("%s unavailable", a.Name())
	}
	return commandWithModeArgs(cmd, mode, a.YoloArgs), nil
}

func (a *Agent) commandForRequest(ctx context.Context, req ResumeRequest, extra []string) ([]string, error) {
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
	check := exec.CommandContext(ctx, shell, "-ic", "type "+ShellJoin([]string{cmd[0]})+" >/dev/null 2>&1") // #nosec G204,G702 -- shell is the user's configured absolute shell; alias name is validated and shell-quoted before reaching this path.
	if err := check.Run(); err != nil {
		return nil, fmt.Errorf("command alias %q not found on PATH or in interactive shell: %w", cmd[0], err)
	}
	return []string{shell, "-ic", ShellJoin(cmd)}, nil
}

func (a *Agent) Dispatch(ctx context.Context, req DispatchRequest) (Session, error) {
	id, err := newID(a.randomReader)
	if err != nil {
		return Session{}, fmt.Errorf("generate session id: %w", err)
	}
	return a.startSession(ctx, ResumeRequest{ID: id, Name: req.Name, CommandAlias: req.CommandAlias, Prompt: req.Prompt, Cwd: req.Cwd, Mode: req.Mode}, "dispatched")
}

func (a *Agent) Resume(ctx context.Context, req ResumeRequest) (Session, error) {
	if req.ID == "" {
		id, err := newID(a.randomReader)
		if err != nil {
			return Session{}, fmt.Errorf("generate session id: %w", err)
		}
		req.ID = id
	}
	return a.startSession(ctx, req, "resumed")
}

func (a *Agent) startSession(ctx context.Context, req ResumeRequest, activity string) (Session, error) {
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
	sessionName := req.SessionName
	if sessionName == "" {
		sessionName = fmt.Sprintf("uam-%s-%s", a.Name(), req.ID[:min(8, len(req.ID))])
	}
	env := map[string]string{"UAM_AGENT": a.Name(), "UAM_ID": req.ID}
	if err := a.Backend.CreateSession(ctx, sessionName, cwd, env, cmd); err != nil {
		return Session{}, fmt.Errorf("create session %s: %w", sessionName, err)
	}
	// Surface the user-facing name in attached terminals' titles (the
	// canonical uam-<agent>-<id> stays the machine-parseable session name).
	// Cosmetic and best-effort — a failure never affects the session.
	displayName := req.Name
	if displayName == "" {
		displayName = displayNameFromDir(cwd)
	}
	if err := a.Backend.SetSessionLabel(ctx, sessionName, displaytext.Sanitize(displayName+" · "+a.Name())); err != nil {
		log.Debug("set session label failed", "session", sessionName, "error", err)
	}
	shouldSendPrompt := strings.TrimSpace(req.Prompt) != "" && (activity != "resumed" || !a.SkipPromptOnResume)
	if shouldSendPrompt {
		if err := a.Backend.SendLine(ctx, sessionName, req.Prompt); err != nil {
			// The session is live but never received its prompt. Roll it back so
			// it doesn't linger as an orphan the store records as Exited/closed.
			// Use WithoutCancel so a cancelled dispatch context still tears the
			// session down; the original SendLine error is what the caller sees.
			_ = a.Backend.Kill(context.WithoutCancel(ctx), sessionName)
			return Session{}, fmt.Errorf("send prompt to %s: %w", sessionName, err)
		}
	}
	now := time.Now()
	created := req.CreatedAt
	if created.IsZero() {
		created = now
	}
	providerID := req.ProviderSessionID
	if a.ProviderSession != nil {
		if id := a.ProviderSession(req, activity); id != "" {
			providerID = id
		}
	}
	return Session{ID: req.ID, AgentType: a.Name(), CommandAlias: req.CommandAlias, DisplayName: displayName, Prompt: req.Prompt, Cwd: cwd, SessionName: sessionName, ProviderSessionID: providerID, State: Active, ProcAlive: Alive, CreatedAt: created, LastChange: now}, nil
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
	// for both CreateSession and the returned Session.Cwd that the store
	// persists. A relative cwd persisted verbatim would be re-resolved against
	// uam's process cwd on resume, relaunching the agent in the wrong directory
	// (C2-4).
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return cwd, nil
}

func (a *Agent) List(ctx context.Context) ([]Session, error) {
	infos, err := a.Backend.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list %s sessions: %w", a.Name(), err)
	}
	var out []Session
	prefix := "uam-" + a.Name() + "-"
	seen := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		if !strings.HasPrefix(info.Name, prefix) {
			continue
		}
		seen[info.Name] = struct{}{}
		id := strings.TrimPrefix(info.Name, prefix)
		state, alive := ClassifyPane(info.Alive)
		created := time.Unix(info.CreatedUnix, 0)
		// Scrape the PR URL only on first discovery or once the rescan window
		// elapses. On a throttled tick PR stays nil; service.mergeStoredMetadata
		// re-hydrates it from the persisted record so the dashboard never loses
		// it (F16). Captures are sequential by design — parallelizing would fire
		// a burst of capture round-trips.
		var prRef *PRRef
		if a.shouldScanPR(info.Name) {
			capture, capErr := a.Backend.Capture(ctx, info.Name, prCaptureLines)
			if capErr != nil {
				// Per-session and non-fatal: a failed PR scrape just leaves PR nil
				// for this tick (mergeStoredMetadata re-hydrates any known PR). Log
				// at debug so it's diagnosable without spamming the dashboard (F52).
				log.Debug("PR capture failed", "session", info.Name, "error", capErr)
			}
			prRef = ExtractPR(capture)
		}
		out = append(out, Session{ID: id, AgentType: a.Name(), DisplayName: id, Cwd: info.Cwd, SessionName: info.Name, State: state, ProcAlive: alive, LastChange: time.Now(), CreatedAt: created, PR: prRef})
	}
	a.prunePRScan(seen)
	return out, nil
}

// shouldScanPR reports whether the session named name is due for a PR scrape
// and, if so, stamps the scan time. It is the per-session leaky bucket that
// keeps List from capturing every session on every refresh tick (F16).
func (a *Agent) shouldScanPR(name string) bool {
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
	last, seen := a.lastPRScan[name]
	if seen && now.Sub(last) < prRescanInterval {
		return false
	}
	a.lastPRScan[name] = now
	return true
}

// prunePRScan drops scan stamps for sessions no longer live so the throttle
// map cannot grow without bound across many session lifetimes.
func (a *Agent) prunePRScan(live map[string]struct{}) {
	a.prScanMu.Lock()
	defer a.prScanMu.Unlock()
	for name := range a.lastPRScan {
		if _, ok := live[name]; !ok {
			delete(a.lastPRScan, name)
		}
	}
}

func (a *Agent) Peek(ctx context.Context, id string) (PeekResult, error) {
	capture, err := a.Backend.Capture(ctx, a.target(id), 200)
	if err != nil {
		return PeekResult{}, fmt.Errorf("peek %s session %s: %w", a.Name(), id, err)
	}
	return PeekResult{TailText: capture}, nil
}

func (a *Agent) Reply(ctx context.Context, id, text string) error {
	return a.Backend.SendLine(ctx, a.target(id), text)
}
func (a *Agent) Attach(id string) (AttachSpec, error) {
	argv, err := a.Backend.AttachArgv(a.target(id))
	if err != nil {
		return AttachSpec{}, fmt.Errorf("attach %s session %s: %w", a.Name(), id, err)
	}
	return AttachSpec{Argv: argv}, nil
}
func (a *Agent) Stop(ctx context.Context, id string) error { return a.Backend.Kill(ctx, a.target(id)) }
func (a *Agent) HasSession(ctx context.Context, id string) bool {
	return a.Backend.HasSession(ctx, a.target(id))
}

// target resolves an id to its canonical session name. Matching is always
// exact: the backend looks sessions up by full name, so a longer neighbour
// sharing the truncated prefix can never be hit (F32). Human-facing prefix
// lookups stay in Service.Find.
func (a *Agent) target(id string) string {
	if strings.HasPrefix(id, "uam-") {
		return id
	}
	return fmt.Sprintf("uam-%s-%s", a.Name(), id[:min(8, len(id))])
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
