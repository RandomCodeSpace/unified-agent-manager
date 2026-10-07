package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// spawnCall calls uam_create_task from conv's Task as an adapter would.
func spawnCall(t *testing.T, conv *agenttest.Conversation, call agentapi.HostToolCall) agentapi.HostToolResult {
	t.Helper()
	call.Name = createTaskToolName
	res, err := conv.CallTool(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func toolNames(tools []agentapi.HostTool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// plainTaskTools are the tools of a Task that may create Tasks.
var plainTaskTools = []string{createTaskToolName, chartToolName}

// startedTask is the Task ID a uam_create_task result names.
var startedTask = regexp.MustCompile(`^Started task ([0-9a-f-]{36})\b`)

// spawnOK requires a uam_create_task call to create its Task within the
// call, and returns that Task and its conversation.
func spawnOK(t *testing.T, m *Manager, prov *agenttest.Provider, conv *agenttest.Conversation, callID, args string) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	res := spawnCall(t, conv, agentapi.HostToolCall{CallID: callID, Arguments: json.RawMessage(args)})
	match := startedTask.FindStringSubmatch(res.Text)
	if res.Failed || match == nil || strings.Contains(res.Text, "still opening") || !strings.Contains(res.Text, "no reply comes back to you") {
		t.Fatalf("uam_create_task %s = %+v", args, res)
	}
	sum, err := m.Summary(match[1])
	if err != nil {
		t.Fatalf("task %s: %v", match[1], err)
	}
	return sum, taskConv(t, prov, sum.ID)
}

// taskConv returns the latest conversation opened for the Task id.
func taskConv(t *testing.T, prov *agenttest.Provider, id string) *agenttest.Conversation {
	t.Helper()
	convs := prov.Conversations()
	for i := len(convs) - 1; i >= 0; i-- {
		if convs[i].Request().SessionID == id {
			return convs[i]
		}
	}
	t.Fatalf("no conversation for task %s", id)
	return nil
}

// spawnRefused requires a uam_create_task call to be refused with a text
// holding want, and to start nothing.
func spawnRefused(t *testing.T, m *Manager, prov *agenttest.Provider, conv *agenttest.Conversation, args, want string) {
	t.Helper()
	opens := len(prov.Opens())
	res := spawnCall(t, conv, agentapi.HostToolCall{CallID: "refused", Arguments: json.RawMessage(args)})
	if !res.Failed || !strings.Contains(res.Text, want) {
		t.Fatalf("uam_create_task %s = %+v; want a refusal with %q", args, res, want)
	}
	waitSpawns(t, m)
	if len(prov.Opens()) != opens {
		t.Fatalf("a refused uam_create_task %s opened a conversation", args)
	}
}

// waitSpawns waits until no uam_create_task create runs.
func waitSpawns(t *testing.T, m *Manager) {
	t.Helper()
	waitUntil(t, "uam_create_task creates", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.spawns) == 0
	})
}

func namedProject(t *testing.T, m *Manager, name string) string {
	t.Helper()
	p, err := m.AddProject(t.TempDir(), name)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// A Task starts a Task in an existing Project, named by its ID or its exact
// name: the prompt is its first message, it starts in safe mode whatever
// the Task defaults say, and it records its creator but does not get the tool.
func TestCreateTaskStartsATaskInAnExistingProject(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	if got := toolNames(conv.Request().Tools); !slices.Equal(got, plainTaskTools) {
		t.Fatalf("tools = %q", got)
	}
	if _, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &TaskDefaults{Provider: prov.Name(), Mode: "yolo"}}); err != nil {
		t.Fatal(err)
	}
	target := namedProject(t, m, "Target")

	byID, child := spawnOK(t, m, prov, conv, "call-1", fmt.Sprintf(`{"project":%q,"prompt":"fix the build\n\tthen test","name":"Build fix"}`, target))
	if byID.ProjectID != target || byID.Name != "Build fix" || byID.Mode != "safe" || byID.SpawnedBy != caller.ID || byID.Model != "" {
		t.Fatalf("created task = %+v", byID)
	}
	if sends := child.Sends(); !slices.Equal(sends, []string{"fix the build\n\tthen test"}) {
		t.Fatalf("first messages = %q", sends)
	}
	if req := child.Request(); !slices.Equal(toolNames(req.Tools), []string{chartToolName}) || req.CallTool == nil {
		t.Fatalf("a created task has tools: %q", toolNames(req.Tools))
	}

	byName, child := spawnOK(t, m, prov, conv, "call-2", `{"project":"Target","prompt":"second"}`)
	if byName.ProjectID != target || byName.Name != "" || byName.SpawnedBy != caller.ID || !slices.Equal(child.Sends(), []string{"second"}) {
		t.Fatalf("created by name = %+v, sends %q", byName, child.Sends())
	}
	if list := m.List(); len(list) != 3 {
		t.Fatalf("tasks = %d", len(list))
	}
}

// The Project must exist and be named unambiguously; no path is taken, and
// the prompt and name are checked before anything starts.
func TestCreateTaskRefusals(t *testing.T) {
	m, prov, _ := newTestManager(t)
	_, conv := createSession(t, m, prov)
	a, b := namedProject(t, m, "Twin"), namedProject(t, m, "Twin")
	dir := t.TempDir()
	target, err := m.AddProject(dir, "Target")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ args, want string }{
		"unknown project":     {`{"project":"Nowhere","prompt":"p"}`, `no project has the ID or name "Nowhere"`},
		"ambiguous name":      {`{"project":"Twin","prompt":"p"}`, "2 projects are named \"Twin\""},
		"project directory":   {fmt.Sprintf(`{"project":%q,"prompt":"p"}`, dir), "no project has the ID or name"},
		"no project":          {`{"prompt":"p"}`, "project is required"},
		"path argument":       {fmt.Sprintf(`{"project":%q,"prompt":"p","dir":"/tmp"}`, target.ID), `unknown field "dir"`},
		"blank prompt":        {`{"project":"Target","prompt":"  \n"}`, "prompt is required"},
		"long prompt":         {fmt.Sprintf(`{"project":"Target","prompt":%q}`, strings.Repeat("x", maxSpawnPrompt+1)), "longer than 16384 bytes"},
		"control in prompt":   {`{"project":"Target","prompt":"ring\u0007"}`, "control character"},
		"carriage return":     {`{"project":"Target","prompt":"a\r\nb"}`, "control character"},
		"long name":           {fmt.Sprintf(`{"project":"Target","prompt":"p","name":%q}`, strings.Repeat("n", maxNameRunes+1)), "name is longer than"},
		"not an object":       {`["Target"]`, "invalid arguments"},
		"model not offered":   {`{"project":"Target","prompt":"p","model":"nope"}`, `model "nope" is not offered`},
		"effort not accepted": {`{"project":"Target","prompt":"p","effort":"high"}`, `unknown field "effort"`},
	} {
		t.Run(name, func(t *testing.T) { spawnRefused(t, m, prov, conv, tc.args, tc.want) })
	}
	res := spawnCall(t, conv, agentapi.HostToolCall{CallID: "twin", Arguments: json.RawMessage(`{"project":"Twin","prompt":"p"}`)})
	if !strings.Contains(res.Text, a) || !strings.Contains(res.Text, b) {
		t.Fatalf("an ambiguous name does not list the matching ids: %q", res.Text)
	}
}

// A model is checked as Create checks it; without one, the Task starts with
// the model the browser's New task picks: the Task defaults' model unless
// it is hidden, else auto.
func TestCreateTaskModel(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "auto", Name: "Auto"}, {ID: "good", Name: "Good"}, {ID: "other", Name: "Other"}}, nil)
	m := startManager(t, openTestStore(t), prov)
	_, conv := createSession(t, m, prov)
	namedProject(t, m, "Target")

	if sum, _ := spawnOK(t, m, prov, conv, "call-1", `{"project":"Target","prompt":"p"}`); sum.Model != "auto" {
		t.Fatalf("model without task defaults = %q", sum.Model)
	}
	if _, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &TaskDefaults{Provider: "fake", Model: "good", Mode: "safe"}}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := spawnOK(t, m, prov, conv, "call-2", `{"project":"Target","prompt":"p"}`); sum.Model != "good" {
		t.Fatalf("model from task defaults = %q", sum.Model)
	}
	if _, err := m.UpdateSettings(SettingsPatch{HiddenModels: map[string][]string{"fake": {"good"}}}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := spawnOK(t, m, prov, conv, "call-3", `{"project":"Target","prompt":"p"}`); sum.Model != "auto" {
		t.Fatalf("model with the default hidden = %q", sum.Model)
	}
	// As with Create, a model named outright may be a hidden one.
	if sum, _ := spawnOK(t, m, prov, conv, "call-4", `{"project":"Target","prompt":"p","model":"good"}`); sum.Model != "good" || sum.Effort != "" || sum.ContextSize != "default" {
		t.Fatalf("named model = %+v", sum)
	}
	spawnRefused(t, m, prov, conv, `{"project":"Target","prompt":"p","model":"gone"}`, `model "gone" is not offered`)
}

// A Task starts at most maxSpawns Tasks, counted over the stored records:
// archiving one keeps its place, deleting it frees it, and the count
// survives a restart.
func TestCreateTaskCap(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	caller, conv := createSession(t, m, prov)
	namedProject(t, m, "Target")
	first, firstConv := spawnOK(t, m, prov, conv, "call-0", `{"project":"Target","prompt":"p"}`)
	for i := 1; i < maxSpawns; i++ {
		res := spawnCall(t, conv, agentapi.HostToolCall{CallID: fmt.Sprintf("call-%d", i), Arguments: json.RawMessage(`{"project":"Target","prompt":"p"}`)})
		if res.Failed || !strings.Contains(res.Text, fmt.Sprintf("You can start %d more.", maxSpawns-1-i)) {
			t.Fatalf("call %d = %+v", i, res)
		}
	}
	if first.SpawnedBy != caller.ID {
		t.Fatalf("first created task = %+v", first)
	}
	spawnRefused(t, m, prov, conv, `{"project":"Target","prompt":"p"}`, "already started 5 tasks")

	firstConv.EmitTurn(agentapi.TurnCompleted, "")
	if _, err := m.Archive(first.ID); err != nil {
		t.Fatal(err)
	}
	spawnRefused(t, m, prov, conv, `{"project":"Target","prompt":"p"}`, "already started 5 tasks")

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	m = startManager(t, st, prov)
	if sum, err := m.Summary(first.ID); err != nil || sum.SpawnedBy != caller.ID {
		t.Fatalf("after a restart: %+v, %v", sum, err)
	}
	if _, err := m.Commands(context.Background(), caller.ID); err != nil {
		t.Fatal(err)
	}
	conv = prov.Last()
	if conv.Request().SessionID != caller.ID || !slices.Equal(toolNames(conv.Request().Tools), plainTaskTools) {
		t.Fatalf("reopened caller = %+v", conv.Request())
	}
	spawnRefused(t, m, prov, conv, `{"project":"Target","prompt":"p"}`, "already started 5 tasks")
	if err := m.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	spawnOK(t, m, prov, conv, "after-delete", `{"project":"Target","prompt":"p"}`)
}

// A Task created by the tool keeps its creator and stays without the tool
// across a restart.
func TestCreateTaskSpawnedBySurvivesARestart(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	caller, conv := createSession(t, m, prov)
	child, childConv := spawnOK(t, m, prov, conv, "call-1", fmt.Sprintf(`{"project":%q,"prompt":"p"}`, caller.ProjectID))
	childConv.EmitTurn(agentapi.TurnCompleted, "")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	m = startManager(t, st, prov)
	if sum, err := m.Summary(child.ID); err != nil || sum.SpawnedBy != caller.ID {
		t.Fatalf("after a restart: %+v, %v", sum, err)
	}
	mustSubmit(t, m, child.ID, "again", mustUUID(t), ModeSend, SubmissionAccepted)
	if req := prov.Last().Request(); req.SessionID != child.ID || !slices.Equal(toolNames(req.Tools), []string{chartToolName}) {
		t.Fatalf("reopened created task = %+v", req)
	}
}

// blockSpawnOpens makes the scripted provider's opens wait for release,
// which returns each open's error.
func blockSpawnOpens(prov *scriptedProvider) chan error {
	release := make(chan error)
	prov.script(func(p *scriptedProvider) { p.onOpen = func(agentapi.OpenRequest) error { return <-release } })
	return release
}

// A create that fails within the call is refused with its error, and holds
// no place in the cap.
func TestCreateTaskReturnsAFailedCreate(t *testing.T) {
	prov := newScripted("fake")
	m := startManager(t, openTestStore(t), prov)
	caller, conv := createSession(t, m, prov.Provider)
	prov.script(func(p *scriptedProvider) {
		p.onOpen = func(agentapi.OpenRequest) error { return errors.New("the key variable is unset") }
	})
	res := spawnCall(t, conv, agentapi.HostToolCall{CallID: "call-1", Arguments: json.RawMessage(fmt.Sprintf(`{"project":%q,"prompt":"p"}`, caller.ProjectID))})
	if !res.Failed || !strings.Contains(res.Text, "the task was not created") || !strings.Contains(res.Text, "the key variable is unset") {
		t.Fatalf("failed create = %+v", res)
	}
	m.mu.Lock()
	n := m.spawnsLocked(caller.ID)
	m.mu.Unlock()
	if len(m.List()) != 1 || n != 0 {
		t.Fatalf("tasks %d, spawns %d", len(m.List()), n)
	}
}

// A create still opening when the wait ends is reported by its ID, and the
// Task appears with its prompt once it opens.
func TestCreateTaskStillOpening(t *testing.T) {
	prov := newScripted("fake")
	m := startManager(t, openTestStore(t), prov)
	caller, conv := createSession(t, m, prov.Provider)
	m.mu.Lock()
	m.spawnWait = 10 * time.Millisecond
	m.mu.Unlock()
	release := blockSpawnOpens(prov)
	res := spawnCall(t, conv, agentapi.HostToolCall{CallID: "call-1", Arguments: json.RawMessage(fmt.Sprintf(`{"project":%q,"prompt":"later"}`, caller.ProjectID))})
	match := startedTask.FindStringSubmatch(res.Text)
	if res.Failed || match == nil || !strings.Contains(res.Text, "It is still opening") || !strings.Contains(res.Text, "You can start 4 more.") {
		t.Fatalf("slow create = %+v", res)
	}
	release <- nil
	waitSpawns(t, m)
	if sum, err := m.Summary(match[1]); err != nil || sum.SpawnedBy != caller.ID || !slices.Equal(taskConv(t, prov.Provider, sum.ID).Sends(), []string{"later"}) {
		t.Fatalf("created task = %+v, %v", sum, err)
	}
}

// A create that finishes while the service shuts down writes no record.
func TestCreateTaskDuringShutdownWritesNoRecord(t *testing.T) {
	prov := newScripted("fake")
	st := openTestStore(t)
	m := startManager(t, st, prov)
	caller, conv := createSession(t, m, prov.Provider)
	m.mu.Lock()
	m.spawnWait = 10 * time.Millisecond
	m.mu.Unlock()
	release := blockSpawnOpens(prov)
	if res := spawnCall(t, conv, agentapi.HostToolCall{CallID: "call-1", Arguments: json.RawMessage(fmt.Sprintf(`{"project":%q,"prompt":"p"}`, caller.ProjectID))}); !strings.Contains(res.Text, "still opening") {
		t.Fatalf("slow create = %+v", res)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- m.Shutdown(context.Background()) }()
	waitUntil(t, "shutdown to begin", m.isClosed)
	release <- nil
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range cfg.Sessions {
		if rec.Web != nil && rec.Web.SpawnedBy != "" {
			t.Fatalf("a create during shutdown left a record: %+v", rec)
		}
	}
}

// Only an active Task may start one.
func TestCreateTaskRefusedWhenTheCallerIsNotActive(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	if _, err := m.Settle(caller.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	// A call the adapter still delivers after the Task was settled.
	res := conv.Request().CallTool(context.Background(), agentapi.HostToolCall{Name: createTaskToolName, CallID: "late", TaskID: caller.ID,
		Arguments: json.RawMessage(fmt.Sprintf(`{"project":%q,"prompt":"p"}`, caller.ProjectID))})
	if !res.Failed || !strings.Contains(res.Text, "the task is settled") {
		t.Fatalf("call from a settled task = %+v", res)
	}
	waitSpawns(t, m)
	if len(prov.Opens()) != opens {
		t.Fatal("a settled task started a task")
	}
}
