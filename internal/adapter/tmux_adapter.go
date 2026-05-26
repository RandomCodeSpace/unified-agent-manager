package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux/tmuxbackend"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

type CommandCandidate struct {
	Display string
	Args    []string
}

// TmuxAgent is the legacy tmux-coupled adapter shape, retained for binary
// compatibility with provider factories that haven't migrated to BackendAgent.
//
// Deprecated: New code should construct BackendAgent directly. TmuxAgent is
// scheduled for removal at v0.4.0.
type TmuxAgent struct {
	NameValue          string
	DisplayNameValue   string
	Candidates         []CommandCandidate
	YoloArgs           []string
	SafeArgs           []string
	Patterns           Patterns
	Tmux               *tmux.Client
	SessionArgs        func(req ResumeRequest, activity string) []string
	SkipPromptOnResume bool
	mu                 sync.Mutex
	hashes             map[string]paneHashState
}

type paneHashState struct {
	hash     uint64
	changed  time.Time
	observed bool
}

func NewTmuxAgent(name, display string, candidates []CommandCandidate, yoloArgs []string, patterns Patterns, client *tmux.Client) *TmuxAgent {
	if client == nil {
		client = tmux.New("uam")
	}
	return &TmuxAgent{NameValue: name, DisplayNameValue: display, Candidates: candidates, YoloArgs: yoloArgs, Patterns: patterns, Tmux: client, hashes: map[string]paneHashState{}}
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
	if mode == "safe" {
		cmd = append(cmd, a.SafeArgs...)
	} else {
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
	// Best-effort: apply uam-friendly tmux server settings (mouse off, swallow
	// Ctrl+Z). Failures don't prevent the session from being created.
	_ = a.Tmux.EnsureServerConfig(ctx)
	if err := a.Tmux.CreateSession(ctx, tmuxName, cwd, env, cmd); err != nil {
		return Session{}, err
	}
	shouldSendPrompt := strings.TrimSpace(req.Prompt) != "" && (activity != "resumed" || !a.SkipPromptOnResume)
	if shouldSendPrompt {
		if err := a.Tmux.SendLine(ctx, tmuxName, req.Prompt); err != nil {
			return Session{}, err
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
	return Session{ID: req.ID, AgentType: a.Name(), DisplayName: name, Prompt: req.Prompt, Cwd: cwd, TmuxSession: tmuxName, State: Working, ProcAlive: Alive, Activity: activity, CreatedAt: created, LastChange: now}, nil
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
		capture, _ := a.Tmux.Capture(ctx, info.Name, 200)
		lines := strings.Split(capture, "\n")
		changedRecently := a.changedRecently(info.Name, capture, 15*time.Second)
		state, alive, summary := ClassifyPane(lines, info.CurrentCommand, tmux.PaneAlive(info.PanePID), changedRecently, a.Patterns)
		created := time.Unix(info.CreatedUnix, 0)
		out = append(out, Session{ID: id, AgentType: a.Name(), DisplayName: id, Cwd: info.CurrentPath, TmuxSession: info.Name, State: state, ProcAlive: alive, Activity: summary, LastChange: time.Now(), CreatedAt: created, PR: ExtractPR(capture)})
	}
	return out, nil
}

func (a *TmuxAgent) Peek(ctx context.Context, id string) (PeekResult, error) {
	target := a.target(id)
	capture, err := a.Tmux.Capture(ctx, target, 200)
	if err != nil {
		return PeekResult{}, err
	}
	state, _, summary := ClassifyPane(strings.Split(capture, "\n"), a.Name(), true, true, a.Patterns)
	return PeekResult{TailText: capture, Summary: summary, AwaitingInput: state == NeedsInput, State: state}, nil
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
func (a *TmuxAgent) Stop(ctx context.Context, id string) error                  { return a.Tmux.Kill(ctx, a.target(id)) }
func (a *TmuxAgent) Rename(ctx context.Context, id, newName string) error       { return nil }
func (a *TmuxAgent) Subscribe(ctx context.Context) (<-chan SessionEvent, error) { return nil, nil }

func (a *TmuxAgent) target(id string) string {
	if strings.HasPrefix(id, "uam-") {
		return id
	}
	return fmt.Sprintf("uam-%s-%s", a.Name(), id[:min(8, len(id))])
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

func (a *TmuxAgent) changedRecently(target, capture string, window time.Duration) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(capture))
	sum := h.Sum64()
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := a.hashes[target]
	if !prev.observed || prev.hash != sum {
		a.hashes[target] = paneHashState{hash: sum, changed: now, observed: true}
		return true
	}
	return now.Sub(prev.changed) <= window
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

// NewBackendAgentFromTmuxClient builds a BackendAgent backed by the tmux
// backend. Use during the v0.1.11–v0.1.13 transition; removed at v0.4.0.
func NewBackendAgentFromTmuxClient(name, display string, candidates []CommandCandidate, yoloArgs []string, patterns Patterns, c *tmux.Client) *BackendAgent {
	return NewBackendAgent(name, display, candidates, yoloArgs, patterns, tmuxbackend.New(c))
}
