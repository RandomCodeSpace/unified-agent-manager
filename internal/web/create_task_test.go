package web

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

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

// spawnOK requires a uam_create_task call to be taken, waits for the Task
// it started, and returns that Task and its conversation.
func spawnOK(t *testing.T, m *Manager, prov *agenttest.Provider, conv *agenttest.Conversation, callID, args string) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	res := spawnCall(t, conv, agentapi.HostToolCall{CallID: callID, Arguments: json.RawMessage(args)})
	id, err := spawnID(conv.Request().SessionID, callID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed || !strings.Contains(res.Text, id) || !strings.Contains(res.Text, "no reply comes back to you") {
		t.Fatalf("uam_create_task %s = %+v", args, res)
	}
	waitSpawns(t, m)
	sum, err := m.Summary(id)
	if err != nil {
		t.Fatalf("task %s: %v", id, err)
	}
	return sum, taskConv(t, prov, id)
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

// promptOpened sends text to the Task id, ends the turn it starts, and
// returns the conversation that took it and whether sending opened one.
func promptOpened(t *testing.T, m *Manager, prov *agenttest.Provider, id, text string) (*agenttest.Conversation, bool) {
	t.Helper()
	opens := len(prov.Opens())
	mustSubmit(t, m, id, text, mustUUID(t), ModeSend, SubmissionAccepted)
	conv := taskConv(t, prov, id)
	if sends := conv.Sends(); len(sends) == 0 || sends[len(sends)-1] != text {
		t.Fatalf("%q was not sent to task %s: %q", text, id, sends)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	return conv, len(prov.Opens()) > opens
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
// name: the prompt is its first message, it runs in safe mode whatever the
// Task defaults say, and it records its creator but does not get the tool.
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
	if req := child.Request(); req.Tools != nil || req.CallTool != nil {
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

// A Task starts at most maxSpawns Tasks, its subagents' included, counted
// over the stored records: archiving one keeps its place, deleting it frees
// it, and the count survives a restart. A repeated call starts nothing new.
func TestCreateTaskCap(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	caller, conv := createSession(t, m, prov)
	namedProject(t, m, "Target")
	for i := range maxSpawns {
		call := agentapi.HostToolCall{CallID: fmt.Sprintf("call-%d", i), Arguments: json.RawMessage(`{"project":"Target","prompt":"p"}`)}
		if i%2 == 1 {
			call.AgentID = "sub-1"
		}
		if res := spawnCall(t, conv, call); res.Failed || !strings.Contains(res.Text, fmt.Sprintf("You can start %d more.", maxSpawns-1-i)) {
			t.Fatalf("call %d = %+v", i, res)
		}
	}
	waitSpawns(t, m)
	firstID, err := spawnID(caller.ID, "call-0")
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Summary(firstID)
	if err != nil || first.SpawnedBy != caller.ID {
		t.Fatalf("first created task = %+v, %v", first, err)
	}
	firstConv := taskConv(t, prov, first.ID)
	opens := len(prov.Opens())
	if res := spawnCall(t, conv, agentapi.HostToolCall{CallID: "call-0", Arguments: json.RawMessage(`{"project":"Target","prompt":"p"}`)}); res.Failed || !strings.Contains(res.Text, "already started task "+first.ID) || len(prov.Opens()) != opens {
		t.Fatalf("repeated call = %+v, opens %d → %d", res, opens, len(prov.Opens()))
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
	if req := prov.Last().Request(); req.SessionID != child.ID || req.Tools != nil {
		t.Fatalf("reopened created task = %+v", req)
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

// uam_create_task is always there, so it is not what a prompt compares: with
// the planner off, prompts reopen neither a Task that has the tool nor a
// Task created by it, and each reopens once the planner is switched on.
func TestPromptDoesNotReopenForTheCreateTaskTool(t *testing.T) {
	m, prov, _ := newTestManager(t)
	project := gitProject(t, m, "app")
	caller, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project, Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	child, childConv := spawnOK(t, m, prov, prov.Last(), "call-1", fmt.Sprintf(`{"project":%q,"prompt":"p"}`, project))
	childConv.EmitTurn(agentapi.TurnCompleted, "")
	for _, id := range []string{caller.ID, child.ID, caller.ID, child.ID} {
		if _, reopened := promptOpened(t, m, prov, id, "prompt"); reopened {
			t.Fatalf("a prompt to %s reopened it with the planner off", id)
		}
	}
	setPlanner(t, m, true)
	for id, want := range map[string][]string{caller.ID: plannerTaskTools, child.ID: allBoardTools} {
		conv, reopened := promptOpened(t, m, prov, id, "on")
		if got := toolNames(conv.Request().Tools); !reopened || !slices.Equal(got, want) {
			t.Fatalf("task %s reopened %v with %q", id, reopened, got)
		}
		if _, reopened := promptOpened(t, m, prov, id, "again"); reopened {
			t.Fatalf("a prompt to %s reopened it again", id)
		}
	}
}
