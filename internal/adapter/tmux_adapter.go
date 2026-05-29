package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	return &TmuxAgent{NameValue: name, DisplayNameValue: display, Candidates: candidates, YoloArgs: yoloArgs, Tmux: client, now: time.Now, lastPRScan: map[string]time.Time{}}
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
	// Safe mode launches the bare command; no flag is the safe default for
	// claude/codex. Only non-safe modes append the provider's full-access args.
	if mode != "safe" {
		cmd = append(cmd, a.YoloArgs...)
	}
	return cmd, nil
}

func (a *TmuxAgent) Dispatch(ctx context.Context, req DispatchRequest) (Session, error) {
	id := newID()
	return a.startSession(ctx, ResumeRequest{ID: id, Name: req.Name, Prompt: req.Prompt, Cwd: req.Cwd, Mode: req.Mode}, "dispatched")
}

func (a *TmuxAgent) Resume(ctx context.Context, req ResumeRequest) (Session, error) {
	if req.ID == "" {
		req.ID = newID()
	}
	return a.startSession(ctx, req, "resumed")
}

func (a *TmuxAgent) startSession(ctx context.Context, req ResumeRequest, activity string) (Session, error) {
	cmd, err := a.commandForMode(req.Mode)
	if err != nil {
		return Session{}, err
	}
	if a.SessionArgs != nil {
		cmd = append(cmd, a.SessionArgs(req, activity)...)
	}
	cwd := req.Cwd
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return Session{}, err
		}
	}
	tmuxName := req.TmuxSession
	if tmuxName == "" {
		tmuxName = fmt.Sprintf("uam-%s-%s", a.Name(), req.ID[:min(8, len(req.ID))])
	}
	env := map[string]string{"UAM_AGENT": a.Name(), "UAM_ID": req.ID}
	if err := a.Tmux.CreateSession(ctx, tmuxName, cwd, env, cmd); err != nil {
		return Session{}, err
	}
	// Best-effort: apply uam-friendly tmux server settings (mouse off, swallow
	// Ctrl+Z). This runs AFTER CreateSession so the server exists — applying it
	// first on the very first dispatch fails and used to latch that failure
	// (F25). Failures here don't prevent the session from being created.
	_ = a.Tmux.EnsureServerConfig(ctx)
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
	name := req.Name
	if name == "" {
		name = displayNameFromDir(cwd)
	}
	now := time.Now()
	created := req.CreatedAt
	if created.IsZero() {
		created = now
	}
	return Session{ID: req.ID, AgentType: a.Name(), DisplayName: name, Prompt: req.Prompt, Cwd: cwd, TmuxSession: tmuxName, State: Active, ProcAlive: Alive, CreatedAt: created, LastChange: now}, nil
}

func (a *TmuxAgent) List(ctx context.Context) ([]Session, error) {
	infos, err := a.Tmux.List(ctx)
	if err != nil {
		return nil, err
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
			capture, _ := a.Tmux.Capture(ctx, info.Name, prCaptureLines)
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
		return PeekResult{}, err
	}
	return PeekResult{TailText: capture}, nil
}

func (a *TmuxAgent) Reply(ctx context.Context, id, text string) error {
	return a.Tmux.SendLine(ctx, a.target(id), text)
}
func (a *TmuxAgent) Attach(id string) (AttachSpec, error) {
	argv, err := a.Tmux.AttachArgv(a.target(id))
	if err != nil {
		return AttachSpec{}, err
	}
	return AttachSpec{Argv: argv}, nil
}
func (a *TmuxAgent) Stop(ctx context.Context, id string) error { return a.Tmux.Kill(ctx, a.target(id)) }
func (a *TmuxAgent) HasSession(ctx context.Context, id string) bool {
	return a.Tmux.HasSession(ctx, a.target(id))
}
func (a *TmuxAgent) Rename(ctx context.Context, id, newName string) error       { return nil }
func (a *TmuxAgent) Subscribe(ctx context.Context) (<-chan SessionEvent, error) { return nil, nil }

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

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
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
