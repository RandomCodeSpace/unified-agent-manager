package adapter

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
)

// BackendAgent is the engine-agnostic equivalent of TmuxAgent. It implements
// AgentAdapter and ResumableAdapter by delegating session operations to a
// mux.Backend.
type BackendAgent struct {
	NameValue          string
	DisplayNameValue   string
	Candidates         []CommandCandidate
	YoloArgs           []string
	SafeArgs           []string
	Patterns           Patterns
	Backend            mux.Backend
	SessionArgs        func(req ResumeRequest, activity string) []string
	SkipPromptOnResume bool

	mu     sync.Mutex
	hashes map[string]paneHashState
}

// NewBackendAgent constructs an adapter wired to a mux.Backend.
func NewBackendAgent(name, display string, candidates []CommandCandidate, yoloArgs []string, patterns Patterns, backend mux.Backend) *BackendAgent {
	return &BackendAgent{
		NameValue:        name,
		DisplayNameValue: display,
		Candidates:       candidates,
		YoloArgs:         yoloArgs,
		Patterns:         patterns,
		Backend:          backend,
		hashes:           map[string]paneHashState{},
	}
}

func (a *BackendAgent) Name() string        { return a.NameValue }
func (a *BackendAgent) DisplayName() string { return a.DisplayNameValue }

func (a *BackendAgent) Available() (bool, string) {
	_, ok := a.resolveCommand()
	if ok {
		return true, ""
	}
	if len(a.Candidates) == 0 {
		return false, "no command configured"
	}
	return false, fmt.Sprintf("%s not on PATH", a.Candidates[0].Display)
}

func (a *BackendAgent) resolveCommand() ([]string, bool) {
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

func (a *BackendAgent) commandForMode(mode string) ([]string, error) {
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

func (a *BackendAgent) Dispatch(ctx context.Context, req DispatchRequest) (Session, error) {
	id := newID()
	return a.startSession(ctx, ResumeRequest{ID: id, Name: req.Name, Prompt: req.Prompt, Cwd: req.Cwd, Mode: req.Mode}, "dispatched")
}

func (a *BackendAgent) Resume(ctx context.Context, req ResumeRequest) (Session, error) {
	if req.ID == "" {
		req.ID = newID()
	}
	return a.startSession(ctx, req, "resumed")
}

func (a *BackendAgent) startSession(ctx context.Context, req ResumeRequest, activity string) (Session, error) {
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
	sessionName := req.TmuxSession
	if sessionName == "" {
		sessionName = fmt.Sprintf("uam-%s-%s", a.Name(), req.ID[:min(8, len(req.ID))])
	}
	env := []string{"UAM_AGENT=" + a.Name(), "UAM_ID=" + req.ID}

	handle, err := a.Backend.Spawn(ctx, mux.SpawnSpec{
		SessionName: sessionName,
		Argv:        cmd,
		Env:         env,
		Cwd:         cwd,
		Cols:        200,
		Rows:        50,
	})
	if err != nil {
		return Session{}, err
	}

	shouldSendPrompt := strings.TrimSpace(req.Prompt) != "" && (activity != "resumed" || !a.SkipPromptOnResume)
	if shouldSendPrompt {
		if err := a.Backend.Write(ctx, handle, []byte(req.Prompt+"\r")); err != nil {
			return Session{}, err
		}
	}
	name := req.Name
	if name == "" {
		name = displayNameFromPrompt(req.Prompt)
	}
	now := time.Now()
	created := req.CreatedAt
	if created.IsZero() {
		created = now
	}
	return Session{
		ID: req.ID, AgentType: a.Name(), DisplayName: name, Prompt: req.Prompt,
		Cwd: cwd, TmuxSession: sessionName, State: Working, ProcAlive: Alive,
		Activity: activity, CreatedAt: created, LastChange: now,
	}, nil
}

func (a *BackendAgent) List(ctx context.Context) ([]Session, error) {
	prefix := "uam-" + a.Name() + "-"
	infos, err := a.Backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(infos))
	for _, info := range infos {
		// Defensive: a backend implementation may ignore the prefix filter
		// (e.g. in-memory test fakes). Re-check here so List always honors
		// the per-agent namespace, matching legacy TmuxAgent semantics.
		if !strings.HasPrefix(string(info.Handle), prefix) {
			continue
		}
		id := strings.TrimPrefix(string(info.Handle), prefix)
		capture, _ := a.Backend.Capture(ctx, info.Handle, 200)
		changedRecently := a.changedRecently(string(info.Handle), capture.Lines, 15*time.Second)
		alive := info.PanePID > 0 && pidAlive(info.PanePID)
		state, liveness, summary := ClassifyPane(capture.Lines, info.PaneCmd, alive, changedRecently, a.Patterns)
		out = append(out, Session{
			ID: id, AgentType: a.Name(), DisplayName: id,
			Cwd: info.Cwd, TmuxSession: string(info.Handle),
			State: state, ProcAlive: liveness, Activity: summary,
			LastChange: time.Now(), CreatedAt: info.CreatedAt,
			PR: ExtractPR(strings.Join(capture.Lines, "\n")),
		})
	}
	return out, nil
}

func (a *BackendAgent) Peek(ctx context.Context, id string) (PeekResult, error) {
	target := a.target(id)
	capture, err := a.Backend.Capture(ctx, mux.SessionHandle(target), 200)
	if err != nil {
		return PeekResult{}, err
	}
	state, _, summary := ClassifyPane(capture.Lines, a.Name(), true, true, a.Patterns)
	return PeekResult{TailText: strings.Join(capture.Lines, "\n"), Summary: summary, AwaitingInput: state == NeedsInput, State: state}, nil
}

func (a *BackendAgent) Reply(ctx context.Context, id, text string) error {
	return a.Backend.Write(ctx, mux.SessionHandle(a.target(id)), []byte(text+"\r"))
}

func (a *BackendAgent) Attach(id string) (AttachSpec, error) {
	// For v0.1.11 the tmux backend still owns attach via tmux exec. The
	// AttachSpec.Argv path stays. At v0.1.13, ipcclient.Attach will
	// provide raw-mode tunneling through a separate uam attach --raw
	// subcommand.
	return AttachSpec{Argv: []string{"uam", "attach", "--raw", id}}, nil
}

func (a *BackendAgent) Stop(ctx context.Context, id string) error {
	return a.Backend.Kill(ctx, mux.SessionHandle(a.target(id)))
}

func (a *BackendAgent) Rename(ctx context.Context, id, newName string) error { return nil }

func (a *BackendAgent) Subscribe(ctx context.Context) (<-chan SessionEvent, error) {
	return nil, nil
}

func (a *BackendAgent) target(id string) string {
	if strings.HasPrefix(id, "uam-") {
		return id
	}
	return fmt.Sprintf("uam-%s-%s", a.Name(), id[:min(8, len(id))])
}

func (a *BackendAgent) changedRecently(target string, lines []string, window time.Duration) bool {
	h := fnv.New64a()
	for _, line := range lines {
		_, _ = h.Write([]byte(line))
		_, _ = h.Write([]byte{'\n'})
	}
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

// pidAlive replaces the tmux.PaneAlive helper for the backend-agnostic path.
// It exists in package-internal scope to avoid coupling adapter to tmux.
// On Unix, os.FindProcess + Signal(nil) reduces to kill(pid, 0).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(nil) == nil
}
