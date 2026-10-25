package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// uam_create_task is the host tool through which a Task starts another Task,
// with a first message (docs/web.md): in an existing Project, or in the
// Project of a folder, which the owner confirms adding when it has none
// (spawn_path.go). The new Task starts in yolo mode when its creator is in
// yolo, else in safe mode; it records its creator (spawnedBy), does not get
// the tool, and one Task may create at most maxSpawns. Each time its turn
// ends, or it is settled or archived, its creator gets an end note
// (childEndLocked).

const (
	createTaskToolName = "uam_create_task"
	// maxSpawns is how many Tasks one Task may create. It counts the stored
	// records naming that Task as their creator, in every stage, and the
	// creates still running: only deleting a Task, which removes its
	// record, frees its place.
	maxSpawns = 5
	// maxSpawnPrompt bounds a created Task's first message, in bytes.
	maxSpawnPrompt = 16 << 10
	// maxChildReply bounds the created Task's last reply in its end note,
	// in bytes.
	maxChildReply = 2 << 10
	// maxChildNotes bounds the end notes waiting for a creator: a turn end
	// and a settle of each Thread it may create.
	maxChildNotes = 2 * maxSpawns
)

var createTaskTool = agentapi.HostTool{
	Name: createTaskToolName,
	Description: fmt.Sprintf("Start a new uam thread with prompt as its first message, in a project or in the project of a folder. "+
		"Use it for any change outside your own thread's folder. Give project or path, not both. "+
		"A path inside an existing project starts the thread there; a folder in no project needs the owner's confirmation in uam before it is added as a project (inside the home folder only; uam makes a missing folder). "+
		"The thread starts in yolo mode when you are in yolo, else in safe mode, and runs on its own: the owner sees it in the uam sidebar. Each time its turn ends, or it is settled, you get a 'uam thread:' note with its end state and last reply, at your next tool call or turn; wait for it instead of checking. "+
		"You can start at most %d threads.", maxSpawns),
	Parameters: toolSchema([]string{"prompt"}, map[string]any{
		"project": stringProp("An existing project: its ID, or its exact name."),
		"path":    stringProp("A folder to work in: absolute, ~/..., or relative to your folder."),
		"prompt":  stringProp(fmt.Sprintf("The new thread's first message, up to %d bytes.", maxSpawnPrompt)),
		"name":    stringProp(fmt.Sprintf("The new thread's name, up to %d characters. Without one, the thread is titled from its prompt.", maxNameRunes)),
		"model":   stringProp("A model ID the provider offers. Without one, the owner's default for new threads."),
	}),
}

type createTaskArgs struct {
	Project string `json:"project"`
	Path    string `json:"path"`
	Prompt  string `json:"prompt"`
	Name    string `json:"name"`
	Model   string `json:"model"`
}

// taskToolsLocked is the Manager's hostTools: uam_create_task unless
// spawned (that tool or a routine's run created the Task), uam_chart
// (charts.go), uam_diagram_read (drawings.go) and the uam read tool. Its
// CallTool dispatches by tool name. The caller holds mu.
func (m *Manager) taskToolsLocked(taskID string, spawned bool) ([]agentapi.HostTool, func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult) {
	var tools []agentapi.HostTool
	if !spawned {
		tools = append(tools, createTaskTool)
	}
	return append(tools, chartTool, diagramReadTool, uamTool), func(ctx context.Context, call agentapi.HostToolCall) agentapi.HostToolResult {
		switch {
		case call.Name == uamToolName:
			return m.uamCall(ctx, taskID, call)
		case call.Name == chartToolName:
			return m.chartCall(ctx, taskID, call)
		case call.Name == diagramReadToolName:
			return m.diagramReadCall(ctx, taskID, call)
		case call.Name == createTaskToolName && !spawned:
			return m.createTask(ctx, taskID, call)
		}
		return agentapi.HostToolResult{Text: fmt.Sprintf("there is no uam tool %q", call.Name), Failed: true}
	}
}

// errTaskEnded ends a host tool call whose Task was settled or archived
// while it ran.
var errTaskEnded = errors.New("the thread was settled or archived, so this call was discarded")

// decodeToolArgs decodes a call's JSON object into v, refusing unknown
// arguments as the schemas do.
func decodeToolArgs(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return newError(http.StatusBadRequest, "invalid arguments: %s", strings.TrimPrefix(err.Error(), "json: "))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return newError(http.StatusBadRequest, "invalid arguments: one JSON object is expected")
	}
	return nil
}

// toolSchema is a strict JSON Schema object of the given properties.
func toolSchema(required []string, props map[string]any) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func listProp(desc string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items, "description": desc}
}

// taskCall is one host tool call of a Task in progress: cancel ends it, and
// done is closed once it has returned.
type taskCall struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// startCall ties a host tool call of the Task id to the Task. It refuses
// unless the Task is active, checked under mu, where Settle and Archive
// change the stage; the returned context ends with errTaskEnded once the
// Task leaves the active stage. end must run when the call returns.
func (m *Manager) startCall(ctx context.Context, id string) (context.Context, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch s := m.sessions[id]; {
	case s == nil:
		return nil, nil, newError(http.StatusNotFound, msgSessionNotFound)
	case s.stage != StageActive:
		return nil, nil, newError(http.StatusConflict, "the thread is %s, so it can no longer use uam tools", stageName(s.stage))
	}
	ctx, cancel := context.WithCancelCause(ctx)
	call := &taskCall{cancel: cancel, done: make(chan struct{})}
	if m.calls == nil {
		m.calls = map[string]map[*taskCall]struct{}{}
	}
	if m.calls[id] == nil {
		m.calls[id] = map[*taskCall]struct{}{}
	}
	m.calls[id][call] = struct{}{}
	return ctx, func() {
		m.mu.Lock()
		delete(m.calls[id], call)
		if len(m.calls[id]) == 0 {
			delete(m.calls, id)
		}
		m.mu.Unlock()
		cancel(nil)
		close(call.done)
	}, nil
}

// endCalls cancels the host tool calls of the Task id in progress and
// returns once they have all returned. The caller has moved the Task out of
// the active stage, so no new call starts.
func (m *Manager) endCalls(id string) {
	m.mu.Lock()
	calls := m.calls[id]
	delete(m.calls, id)
	m.mu.Unlock()
	for call := range calls {
		call.cancel(errTaskEnded)
	}
	for call := range calls {
		<-call.done
	}
}

// createTask runs a uam_create_task call of the Task taskID.
func (m *Manager) createTask(ctx context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.startSpawn(ctx, taskID, call)
	if err != nil {
		m.count("create_task_refused")
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	m.count("create_task_spawned")
	return agentapi.HostToolResult{Text: text}
}

// startSpawn checks a uam_create_task call as Create checks a request, then
// creates the Task and waits up to spawnWait for it. A create that fails in
// that time is refused with its error. Opening a conversation can take
// longer: then the call says the Task is still opening, and a failure after
// that is only logged.
func (m *Manager) startSpawn(ctx context.Context, taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this thread")
	}
	var in createTaskArgs
	if err := decodeToolArgs(call.Arguments, &in); err != nil {
		return "", err
	}
	if err := checkSpawnPrompt(in.Prompt); err != nil {
		return "", err
	}
	if (in.Project == "") == (in.Path == "") {
		return "", errors.New("give project or path, not both")
	}
	ctx, end, err := m.startCall(ctx, taskID)
	if err != nil {
		return "", err
	}
	defer end()
	req := CreateRequest{Name: in.Name, Model: in.Model, Prompt: in.Prompt, Mode: string(store.ModeSafe), spawnedBy: taskID}
	if in.Path != "" {
		var folder string
		if in.Project, folder, err = m.spawnPathProject(ctx, taskID, call.CallID, in.Path); err != nil {
			return "", err
		}
		if folder != "" {
			req.Prompt = "Work in the folder " + folder + ".\n\n" + req.Prompt
		}
	}
	m.mu.Lock()
	if caller := m.sessions[taskID]; caller != nil && caller.mode == store.ModeYolo && !caller.modeUnknown {
		req.Mode = string(store.ModeYolo)
	}
	var projectName string
	req.ProjectID, projectName, err = m.spawnProjectLocked(in.Project)
	var model string
	req.Provider, model = m.newTaskSelectionLocked()
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	if req.Model == "" {
		req.Model = model
	}
	prov, workdir, mode, err := m.checkCreate(&req)
	if err != nil {
		return "", err
	}
	if req.id, err = newUUID(); err != nil {
		return "", fmt.Errorf("generate task id: %w", err)
	}
	m.mu.Lock()
	n, wait := m.spawnsLocked(taskID), m.spawnWait
	switch {
	case m.closed:
		m.mu.Unlock()
		return "", errShuttingDown
	case n >= maxSpawns:
		m.mu.Unlock()
		return "", newError(http.StatusConflict, "this thread has already started %d threads, the most one thread may start", n)
	}
	if m.spawns == nil {
		m.spawns = map[string]string{}
	}
	m.spawns[req.id] = taskID
	done := make(chan error, 1)
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		_, err := m.createChecked(req, prov, workdir, mode)
		m.mu.Lock()
		delete(m.spawns, req.id)
		m.mu.Unlock()
		if err != nil {
			log.Warn("uam_create_task did not create its task", "session", taskID, "task", req.id, "error", err)
		}
		done <- err
	}()
	task := fmt.Sprintf("thread %s", req.id)
	if req.Name != "" {
		task = fmt.Sprintf("thread %s %q", req.id, req.Name)
	}
	tail := fmt.Sprintf("It starts in %s mode and runs on its own: the owner sees it in the uam sidebar. When its turn ends you get a 'uam thread:' note with its last reply. "+
		"You can start %d more.", req.Mode, maxSpawns-n-1)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("the thread was not created: %w", err)
		}
		return fmt.Sprintf("Started %s in project %q (%s), with your prompt as its first message. %s", task, projectName, req.ProjectID, tail), nil
	case <-timer.C:
	case <-ctx.Done():
	}
	return fmt.Sprintf("Started %s in project %q (%s). It is still opening; it appears in the uam sidebar when ready, with your prompt as its first message. %s",
		task, projectName, req.ProjectID, tail), nil
}

// checkSpawnPrompt refuses a first message that is blank, longer than
// maxSpawnPrompt bytes, or holds a control character other than a newline or
// a tab.
func checkSpawnPrompt(prompt string) error {
	switch {
	case strings.TrimSpace(prompt) == "":
		return errors.New("prompt is required")
	case len(prompt) > maxSpawnPrompt:
		return fmt.Errorf("prompt is longer than %d bytes", maxSpawnPrompt)
	case strings.ContainsFunc(prompt, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }):
		return errors.New("prompt has a control character; only newlines and tabs may be used")
	}
	return nil
}

// spawnProjectLocked returns the ID and name of the Project ref names: by
// its ID, or else by its exact name, which exactly one Project must have.
// The caller holds mu.
func (m *Manager) spawnProjectLocked(ref string) (id, name string, err error) {
	if ref == "" {
		return "", "", errors.New("project is required")
	}
	if p := m.projects[ref]; p != nil {
		return p.ID, p.Name, nil
	}
	var ids []string
	for _, p := range m.projects {
		if p.Name == ref {
			ids = append(ids, p.ID)
		}
	}
	slices.Sort(ids)
	switch len(ids) {
	case 0:
		return "", "", fmt.Errorf("no project has the ID or name %q; a thread can be started only in an existing project", ref)
	case 1:
		return ids[0], ref, nil
	}
	return "", "", &Error{Status: http.StatusConflict, Message: fmt.Sprintf("%d projects are named %q; use one of their IDs: %s", len(ids), ref, strings.Join(ids, ", ")), Refs: ids}
}

// newTaskSelectionLocked is the provider and model the browser's New task
// starts with (resolveTaskDefaults in web/src/api.ts): the Task defaults'
// provider when it is available, else the first available one; the Task
// defaults' model when that provider shows it, else auto, else the first
// model shown, else the provider's default. A model hidden in Settings is
// not shown. The caller holds mu.
func (m *Manager) newTaskSelectionLocked() (provider, model string) {
	d := m.settings.TaskDefaults
	provider = d.Provider
	if m.providers[provider] == nil || !m.infos[provider].Available {
		i := slices.IndexFunc(m.order, func(name string) bool { return m.infos[name].Available })
		switch {
		case i >= 0:
			provider = m.order[i]
		case len(m.order) > 0:
			provider = m.order[0]
		}
	}
	var shown []string
	for _, mo := range m.infos[provider].Models {
		if !slices.Contains(m.settings.HiddenModels[provider], mo.ID) {
			shown = append(shown, mo.ID)
		}
	}
	switch {
	case d.Model != "" && slices.Contains(shown, d.Model):
		return provider, d.Model
	case slices.Contains(shown, "auto"):
		return provider, "auto"
	case len(shown) > 0:
		return provider, shown[0]
	}
	return provider, ""
}

// spawnsLocked counts the Tasks the Task caller created or is creating. The
// caller holds mu.
func (m *Manager) spawnsLocked(caller string) int {
	n := 0
	for _, s := range m.sessions {
		if s.spawnedBy == caller {
			n++
		}
	}
	for id, by := range m.spawns {
		if by == caller && m.sessions[id] == nil {
			n++
		}
	}
	return n
}

// spawnRoom refuses to store rec, a Task uam_create_task creates, when the
// stored records already name its creator maxSpawns times.
func spawnRoom(cfg *store.Config, rec store.SessionRecord) error {
	n := 0
	for _, r := range cfg.Sessions {
		if r.Web != nil && r.Web.SpawnedBy == rec.Web.SpawnedBy {
			n++
		}
	}
	if n >= maxSpawns {
		return fmt.Errorf("thread %s has already started %d threads", rec.Web.SpawnedBy, n)
	}
	return nil
}

// childEndLocked queues the end note of s, a Task uam_create_task created,
// for its creator when s's turn ends or s leaves the active stage. Nothing
// is queued for a creator that is no longer active. The caller holds mu.
func (m *Manager) childEndLocked(s *webSession, before, after SessionSummary) {
	parent := m.sessions[s.spawnedBy]
	if s.spawnedBy == "" || parent == nil || parent.removed || parent.stage != StageActive {
		return
	}
	var end string
	switch {
	case before.Stage == StageActive && after.Stage != StageActive:
		end = "was " + stageVerb(after.Stage)
	case after.Stage != StageActive || !busy(before.State) || busy(after.State) || after.State == StateIdle || after.State == StateClosed:
		return
	case after.State == StateCancelled:
		end = "ended its turn: stopped"
	default:
		end = "ended its turn: " + after.State
	}
	if after.Budget != nil && after.Budget.Reached {
		end += " at its spending limit"
	}
	files := ""
	if after.Diff != nil {
		files = fmt.Sprintf(" Files changed: %d.", after.Diff.Files)
	}
	var reply string
	for i := len(s.items) - 1; i >= 0; i-- {
		if it := s.items[i]; it.AgentID == "" && it.Kind == agentapi.ItemAssistant && strings.TrimSpace(it.Text) != "" {
			reply = strings.TrimSpace(it.Text)
			break
		}
	}
	if len(reply) > maxChildReply {
		reply = strings.ToValidUTF8(reply[:maxChildReply], "") + "…"
	}
	text := fmt.Sprintf("uam thread: Thread %q (%s), which you started, %s.%s", firstNonEmpty(s.name, s.title, "New thread"), s.id, end, files)
	if reply != "" {
		// A settled Thread's transcript may already be out of memory.
		text += " Its last reply:\n" + reply
	}
	// shortcut: the oldest notes go past maxChildNotes; key them by Thread if
	// a creator ever needs every note of a long idle wait.
	parent.childNotes = append(parent.childNotes, text)
	m.count("child_end_notes")
	if n := len(parent.childNotes); n > maxChildNotes {
		parent.childNotes = parent.childNotes[n-maxChildNotes:]
	}
}

// takeChildNotes hands the waiting end notes of the Tasks s created to its
// agent, once.
func (m *Manager) takeChildNotes(s *webSession) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.takeChildNotesLocked(s)
}

func (m *Manager) takeChildNotesLocked(s *webSession) string {
	notes := strings.Join(s.childNotes, "\n\n")
	s.childNotes = nil
	return notes
}
