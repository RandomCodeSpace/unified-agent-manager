package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// callTool calls a planner tool as agent of the Task task, through the
// Task's conversation as an adapter would, and decodes the reply.
func (f *plannerFixture) callTool(task, agent, name, args string) (toolReply, bool) {
	f.t.Helper()
	res, err := f.conversation(task).CallTool(context.Background(), agentapi.HostToolCall{Name: name, CallID: "call-" + name, AgentID: agent, Arguments: json.RawMessage(args)})
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return decodeReply(f.t, res), res.Failed
}

func decodeReply(t *testing.T, res agentapi.HostToolResult) toolReply {
	t.Helper()
	var r toolReply
	if err := json.Unmarshal([]byte(res.Text), &r); err != nil {
		t.Fatalf("reply %q: %v", res.Text, err)
	}
	return r
}

// toolOK requires the call to succeed.
func (f *plannerFixture) toolOK(task, name, args string) toolReply {
	f.t.Helper()
	r, failed := f.callTool(task, "", name, args)
	if failed {
		f.t.Fatalf("%s %s failed: %+v", name, args, r)
	}
	return r
}

// toolRefused requires the call to fail with code.
func (f *plannerFixture) toolRefused(task, name, args, code string) toolReply {
	f.t.Helper()
	r, failed := f.callTool(task, "", name, args)
	if !failed || r.Code != code || r.Text == "" || r.Card != nil {
		f.t.Fatalf("%s %s = %+v, failed %v; want code %q", name, args, r, failed, code)
	}
	return r
}

// planTask starts a planning Task on the container ref.
func (f *plannerFixture) planTask(ref string) string {
	f.t.Helper()
	var reply struct {
		Session SessionSummary `json:"session"`
	}
	f.call(http.MethodPost, "/api/board/cards/"+ref+"/plan", `{}`, http.StatusCreated, &reply)
	return reply.Session.ID
}

func (f *plannerFixture) newTask(project string) SessionSummary {
	f.t.Helper()
	s, err := f.m.Create(CreateRequest{Provider: f.ts.prov.Name(), ProjectID: project, Name: "task"})
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *plannerFixture) setAcceptCmd(cmd string) {
	f.t.Helper()
	f.store(func(ctx context.Context, st *board.Store) error {
		return st.SetProjectAcceptCmd(ctx, board.Owner(""), f.project, cmd)
	})
}

func toolNames(tools []agentapi.HostTool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

var allBoardTools = []string{"board_get", "board_list", "board_create", "board_edit", "board_checklist", "board_comment", "board_link", "board_unlink", "board_delete", "board_claim", "board_split", "board_request"}

// plannerTaskTools are the tools of a Task that gets the planner's:
// uam_create_task and uam_chart follow them.
var plannerTaskTools = append(slices.Clone(allBoardTools), createTaskToolName, chartToolName)

// plainTaskTools are the tools of a Task without the planner's.
var plainTaskTools = []string{createTaskToolName, chartToolName}

// No schema takes an owner-only field or a status change (ADR 0005 §3,
// §16, test plan 1), and every object in them is strict.
func TestBoardToolSchemasAreStrictAndOwnerFree(t *testing.T) {
	banned := []string{"accept_cmd", "paths", "status", "blocked", "force", "restore", "purge"}
	for _, tool := range boardToolSet {
		// As the model gets it: the adapter sends the schema as JSON.
		data, err := json.Marshal(tool.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch v := v.(type) {
			case map[string]any:
				if v["type"] == "object" {
					props, ok := v["properties"].(map[string]any)
					required, ok2 := v["required"].([]any)
					if !ok || !ok2 || v["additionalProperties"] != false {
						t.Errorf("%s: object %s is not strict: %v", tool.Name, path, v)
					}
					for _, name := range required {
						if props[name.(string)] == nil {
							t.Errorf("%s: %s requires an unknown %v", tool.Name, path, name)
						}
					}
				}
				for key, child := range v {
					if slices.Contains(banned, key) {
						t.Errorf("%s: schema key %q at %s", tool.Name, key, path)
					}
					walk(path+"."+key, child)
				}
			case []any:
				for _, child := range v {
					walk(path+"[]", child)
				}
			}
		}
		walk(tool.Name, schema)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	if got := toolNames(func() []agentapi.HostTool {
		tools, _ := (&Manager{}).boardHostTools(nil)
		return tools
	}()); !slices.Equal(got, allBoardTools) {
		t.Fatalf("tools = %q", got)
	}
}

// The tools go only into Tasks of git Projects while the planner is on,
// read when the conversation opens, and every call checks both again.
func TestBoardToolsOnlyInGitProjectsWhilePlannerIsOn(t *testing.T) {
	f := newPlanner(t)
	f.call(http.MethodPatch, "/api/settings", `{"planner":false}`, http.StatusOK, nil)
	early := f.newTask(f.project)
	if got := toolNames(f.conversation(early.ID).Request().Tools); !slices.Equal(got, plainTaskTools) {
		t.Fatalf("a task opened with the planner off has tools: %q", got)
	}
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	task := f.newTask(f.project)
	if got := toolNames(f.conversation(task.ID).Request().Tools); !slices.Equal(got, plannerTaskTools) {
		t.Fatalf("tools = %q", got)
	}
	plain := f.newTask(addProject(t, f.m, t.TempDir()))
	if got := toolNames(f.conversation(plain.ID).Request().Tools); !slices.Equal(got, plainTaskTools) {
		t.Fatalf("a task of a project without git has tools: %q", got)
	}
	// Turning the switch on reaches an earlier Task when its conversation
	// opens again.
	if _, err := f.m.Close(early.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Commands(context.Background(), early.ID); err != nil {
		t.Fatal(err)
	}
	if reopened := f.ts.prov.Last(); reopened.Request().SessionID != early.ID || !slices.Equal(toolNames(reopened.Request().Tools), plannerTaskTools) {
		t.Fatalf("reopened with %q", toolNames(reopened.Request().Tools))
	}

	if r := f.toolOK(task.ID, "board_list", `{}`); r.Text != "No cards match." || r.Card != nil {
		t.Fatalf("empty list = %+v", r)
	}
	f.call(http.MethodPatch, "/api/settings", `{"planner":false}`, http.StatusOK, nil)
	if r := f.toolRefused(task.ID, "board_list", `{}`, codePlannerOff); !strings.Contains(r.Text, "the planner is off") {
		t.Fatalf("off = %+v", r)
	}
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	if err := os.RemoveAll(filepath.Join(f.repo, ".git")); err != nil {
		t.Fatal(err)
	}
	f.m.refreshBranches(context.Background(), true, f.project)
	f.toolRefused(task.ID, "board_list", `{}`, codeNoGit)
}

// setPlanner turns the planner switch on or off.
func setPlanner(t *testing.T, m *Manager, on bool) {
	t.Helper()
	if _, err := m.UpdateSettings(SettingsPatch{Planner: &on}); err != nil {
		t.Fatal(err)
	}
}

// promptTask sends text to the Task id, ends the turn it starts, and returns
// the conversation that took it and whether sending opened a conversation.
func promptTask(t *testing.T, m *Manager, prov *agenttest.Provider, id, text string) (*agenttest.Conversation, bool) {
	t.Helper()
	opens := len(prov.Opens())
	mustSubmit(t, m, id, text, mustUUID(t), ModeSend, SubmissionAccepted)
	conv := prov.Last()
	if sends := conv.Sends(); len(sends) == 0 || sends[len(sends)-1] != text {
		t.Fatalf("%q was not sent to the open conversation: %q", text, sends)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	return conv, len(prov.Opens()) > opens
}

// A Task opened with the planner off gets the tools on its next prompt once
// the switch is on: the prompt reopens the conversation. A slash command,
// and a prompt whose Task already has the right tools, reopen nothing.
func TestPromptReopensATaskForThePlannerSwitchedOn(t *testing.T) {
	m, prov, _ := newTestManager(t)
	project := gitProject(t, m, "app")
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project, Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	first := prov.Last()
	if got := toolNames(first.Request().Tools); !slices.Equal(got, plainTaskTools) {
		t.Fatalf("a task opened with the planner off has tools: %q", got)
	}
	if _, reopened := promptTask(t, m, prov, sum.ID, "before"); reopened {
		t.Fatal("a prompt reopened a task that has the right tools")
	}
	setPlanner(t, m, true)
	prov.SetCommands([]agentapi.Command{{Name: "review"}}, nil)
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review"}); err != nil || len(prov.Opens()) != 1 || len(first.CommandRuns()) != 1 {
		t.Fatalf("command = %v, opens %d, runs %d", err, len(prov.Opens()), len(first.CommandRuns()))
	}
	first.EmitTurn(agentapi.TurnCompleted, "")
	conv, reopened := promptTask(t, m, prov, sum.ID, "after")
	if !reopened || conv == first || first.Closes() != 1 || conv.Request().ConversationID != sum.ConversationID {
		t.Fatalf("reopened %v, closes %d, request %+v", reopened, first.Closes(), conv.Request())
	}
	if got := toolNames(conv.Request().Tools); !slices.Equal(got, plannerTaskTools) || conv.Request().CallTool == nil {
		t.Fatalf("reopened with %q", got)
	}
	if next, reopened := promptTask(t, m, prov, sum.ID, "again"); reopened || next != conv {
		t.Fatal("a prompt reopened a task that has the right tools")
	}
}

// A Task opened with the tools loses them on its next prompt once the
// planner is off or its Project has no git.
func TestPromptReopensATaskThatLostTheTools(t *testing.T) {
	for name, lose := range map[string]func(*testing.T, *Manager, string, string){
		"planner off": func(t *testing.T, m *Manager, _, _ string) { setPlanner(t, m, false) },
		"no git": func(t *testing.T, m *Manager, dir, project string) {
			if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
				t.Fatal(err)
			}
			m.refreshBranches(context.Background(), true, project)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			setPlanner(t, m, true)
			dir := branchRepo(t)
			project := addProject(t, m, dir)
			sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project, Name: "task"})
			if err != nil {
				t.Fatal(err)
			}
			first := prov.Last()
			if got := toolNames(first.Request().Tools); !slices.Equal(got, plannerTaskTools) {
				t.Fatalf("tools = %q", got)
			}
			lose(t, m, dir, project)
			conv, reopened := promptTask(t, m, prov, sum.ID, "after")
			if !reopened || first.Closes() != 1 || !slices.Equal(toolNames(conv.Request().Tools), plainTaskTools) {
				t.Fatalf("reopened %v, closes %d, tools %q", reopened, first.Closes(), toolNames(conv.Request().Tools))
			}
		})
	}
}

// A conversation that still runs or waits for something, an idle
// subagent's follow-up included, is not reopened for its tools: the prompt
// goes to it as it is.
func TestPromptKeepsABusyConversationWithStaleTools(t *testing.T) {
	for name, keep := range map[string]func(*Manager, string, *agenttest.Conversation){
		"background task": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Status: "running"}}}})
		},
		"running subagent": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.EmitSubagent(agentapi.Subagent{ID: "sa", Status: agentapi.SubagentRunning})
		},
		"idle subagent": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.EmitSubagent(agentapi.Subagent{ID: "sa", Status: agentapi.SubagentIdle})
		},
		"paused queue": func(m *Manager, id string, _ *agenttest.Conversation) {
			m.mu.Lock()
			s := m.sessions[id]
			s.queue, s.queuePaused = []QueuedPrompt{{RequestID: "queued", Text: "later"}}, true
			m.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: gitProject(t, m, "app"), Name: "task"})
			if err != nil {
				t.Fatal(err)
			}
			conv := prov.Last()
			keep(m, sum.ID, conv)
			setPlanner(t, m, true)
			mustSubmit(t, m, sum.ID, "prompt", mustUUID(t), ModeSend, SubmissionAccepted)
			if len(prov.Opens()) != 1 || conv.Closes() != 0 || !slices.Equal(conv.Sends(), []string{"prompt"}) {
				t.Fatalf("opens %d, closes %d, sent %q", len(prov.Opens()), conv.Closes(), conv.Sends())
			}
		})
	}
}

// Reopening for the tools keeps the transcript the Task holds, even when
// the reopened conversation's record cannot be read.
func TestToolsReopenKeepsTheTranscript(t *testing.T) {
	prov := newScripted("fake")
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: gitProject(t, m, "app"), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	first := prov.Last()
	first.EmitItem(agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: "earlier"})
	setPlanner(t, m, true)
	prov.script(func(p *scriptedProvider) { p.historyErr = errors.New("record unreadable") })
	if _, reopened := promptTask(t, m, prov.Provider, sum.ID, "after"); !reopened || first.Closes() != 1 {
		t.Fatalf("reopened %v, closes %d", reopened, first.Closes())
	}
	d := detail(t, m, sum.ID)
	if d.History != HistoryLoaded || !slices.ContainsFunc(d.Items, func(it agentapi.Item) bool { return it.ID == "a1" && it.Text == "earlier" }) {
		t.Fatalf("history %q %q, items %+v", d.History, d.HistoryReason, d.Items)
	}
}

// A ref resolves only on the Task's own Board: a card of another Project, or
// of Unassigned, is not found.
func TestBoardToolsResolveRefsOnlyInTheTasksProject(t *testing.T) {
	f := newPlanner(t)
	leaf := f.create(board.KindSubtask, "", "Here")
	other := addProject(t, f.m, branchRepo(t))
	removed := addProject(t, f.m, branchRepo(t))
	var there, gone board.Card
	f.store(func(ctx context.Context, st *board.Store) error {
		var err error
		if there, err = st.Create(ctx, board.Owner(""), board.NewCard{ProjectID: other, Kind: board.KindSubtask, Title: "There"}); err != nil {
			return err
		}
		gone, err = st.Create(ctx, board.Owner(""), board.NewCard{ProjectID: removed, Kind: board.KindSubtask, Title: "Gone"})
		return err
	})
	if err := f.m.RemoveProject(removed); err != nil {
		t.Fatal(err)
	}
	_, task := f.launch(leaf.ID)
	for _, ref := range []string{there.ID, fmt.Sprintf("#%d", there.Seq), fmt.Sprint(gone.Seq), gone.ID, "#999"} {
		f.toolRefused(task.ID, "board_get", fmt.Sprintf(`{"ref":%q}`, ref), string(board.CodeNotFound))
	}
	f.toolRefused(task.ID, "board_link", fmt.Sprintf(`{"ref":%q,"blocker":%q}`, leaf.ID, there.ID), string(board.CodeNotFound))
	f.toolRefused(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"blocked","comment":"waits","blocker":%q}`, leaf.ID, there.ID), string(board.CodeNotFound))
	f.toolRefused(task.ID, "board_list", fmt.Sprintf(`{"parent":%q}`, there.ID), string(board.CodeNotFound))
	if r := f.toolOK(task.ID, "board_get", `{"ref":"#1"}`); r.Card == nil || r.Card.ID != leaf.ID {
		t.Fatalf("own card = %+v", r)
	}
}

// The tools read the Board and write what agents may write: ticks and
// comments on the subtask the Task holds, which keeps its plan, and
// checklist items and links on the rest of its scope. Arguments are strict.
func TestBoardToolsReadAndAnnotate(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story", `,"win_condition":"story done"`)
	one := f.create(board.KindSubtask, story.ID, "One", `,"desc":"Do the thing.","checklist":[{"text":"write it","done":false}]`)
	f.create(board.KindSubtask, story.ID, "Two", `,"win_condition":"two works"`)
	_, task := f.launch(one.ID)

	r := f.toolOK(task.ID, "board_get", `{"ref":"#2"}`)
	if r.Card == nil || *r.Card != (toolCard{ID: one.ID, Seq: 2, Kind: board.KindSubtask, Title: "One", Status: board.StatusDoing}) {
		t.Fatalf("card = %+v", r.Card)
	}
	for _, part := range []string{`#2 subtask "One" (doing, held by you)`, "Path: #1 › #2\n", "Description:\nDo the thing.", "0 [ ] write it"} {
		if !strings.Contains(r.Text, part) {
			t.Fatalf("get text %q lacks %q", r.Text, part)
		}
	}
	if r := f.toolOK(task.ID, "board_get", fmt.Sprintf(`{"ref":%q}`, story.ID)); !strings.Contains(r.Text, "Pending subtasks, in order:\n- #3 subtask \"Two\" (planned): two works") ||
		!strings.Contains(r.Text, "Progress: 0 of 2 confirmed subtasks done, 0 proposed.") {
		t.Fatalf("container text = %q", r.Text)
	}

	if r := f.toolOK(task.ID, "board_checklist", `{"ref":"#2","tick":[0]}`); r.Text != "Updated the checklist of #2: 1 of 1 items done." {
		t.Fatalf("checklist = %+v", r)
	}
	f.toolRefused(task.ID, "board_checklist", `{"ref":"#2","tick":[9]}`, string(board.CodeInvalid))
	f.toolRefused(task.ID, "board_checklist", `{"ref":"#2"}`, string(board.CodeInvalid))
	// The subtask it holds keeps its plan; the rest of its scope it plans.
	if r := f.toolRefused(task.ID, "board_checklist", `{"ref":"#2","add":["document it"]}`, string(board.CodeInProgress)); r.Text != "Release #2 first: it is in progress, and a card in progress keeps its plan" {
		t.Fatalf("add on a started subtask = %+v", r)
	}
	f.toolRefused(task.ID, "board_link", `{"ref":"#2","blocker":"#3"}`, string(board.CodeInProgress))
	f.toolOK(task.ID, "board_comment", `{"ref":"#2","body":"on it"}`)
	f.create(board.KindSubtask, story.ID, "Three")
	f.toolOK(task.ID, "board_checklist", `{"ref":"#3","add":["document it"]}`)
	f.toolOK(task.ID, "board_link", `{"ref":"#4","blocker":"#3"}`)
	d := f.card(one.ID)
	if !slices.Contains(comments(d), "task:"+task.ID+": on it") || !slices.Equal(f.card("#4").Card.BlockedBy, []string{f.card("#3").Card.ID}) {
		t.Fatalf("after annotate = %+v, %v", d.Card, comments(d))
	}
	if r := f.toolOK(task.ID, "board_get", `{"ref":"#4"}`); !strings.Contains(r.Text, "Blocked by:\n- #3 subtask \"Two\" (planned)") {
		t.Fatalf("get text %q lacks the blocker", r.Text)
	}
	r = f.toolOK(task.ID, "board_get", `{"ref":"#2"}`)
	for _, part := range []string{"0 [x] write it", "Comments, oldest first (1 of 1):\n- you: on it"} {
		if !strings.Contains(r.Text, part) {
			t.Fatalf("get text %q lacks %q", r.Text, part)
		}
	}
	if r := f.toolOK(task.ID, "board_list", `{"state":"doing"}`); r.Text != "#1 story \"Story\" (doing)\n#2 subtask \"One\" (doing, held by you) under #1" || r.Card != nil {
		t.Fatalf("list = %+v", r)
	}
	if r := f.toolOK(task.ID, "board_list", `{"query":"tw","kind":"subtask"}`); r.Text != `#3 subtask "Two" (planned) under #1` {
		t.Fatalf("search = %+v", r)
	}

	// Strict arguments: unknown ones, missing refs, oversized calls.
	f.toolRefused(task.ID, "board_get", `{"ref":"#2","status":"done"}`, string(board.CodeInvalid))
	f.toolRefused(task.ID, "board_get", `{}`, string(board.CodeInvalid))
	for _, extra := range []string{` {}`, `]`, `}`} {
		f.toolRefused(task.ID, "board_get", `{"ref":"#2"}`+extra, string(board.CodeInvalid))
	}
	f.toolRefused(task.ID, "board_edit", `{"ref":"#2","accept_cmd":"true"}`, string(board.CodeInvalid))
	f.toolRefused(task.ID, "board_get", `{"ref":"`+strings.Repeat("x", agentapi.MaxHostToolArguments)+`"}`, string(board.CodeInvalid))
}

// board_get on a large card stays under maxToolReply, so the transcript
// records its result whole: the description and comment bodies are cut, and
// the lists say how many more they have. A card larger still has its text
// cut at the end.
func TestBoardToolsGetBoundsALargeCard(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Big", fmt.Sprintf(`,"desc":%q`, strings.Repeat("d", 60<<10)))
	leaf := f.create(board.KindSubtask, story.ID, "Leaf")
	for i := range 24 {
		f.create(board.KindSubtask, story.ID, fmt.Sprintf("Pending %d", i))
	}
	f.store(func(ctx context.Context, st *board.Store) error {
		for range 12 {
			if _, err := st.AddComment(ctx, board.Owner(""), story.ID, strings.Repeat("c", 10<<10)); err != nil {
				return err
			}
		}
		for i := range 25 {
			// Links join siblings; cancelled, the blockers leave the pending list.
			blocker, err := st.Create(ctx, board.Owner(""), board.NewCard{ProjectID: f.project, Kind: board.KindSubtask, ParentID: story.ID, Title: fmt.Sprintf("Blocker %d", i)})
			if err != nil {
				return err
			}
			if err := st.Link(ctx, board.Owner(""), blocker.ID, leaf.ID); err != nil {
				return err
			}
			if _, err := st.SetStatus(ctx, board.Owner(""), blocker.ID, board.StatusCancelled, "x", false); err != nil {
				return err
			}
		}
		return nil
	})
	task := f.newTask(f.project)
	get := func(ref string) toolReply {
		t.Helper()
		res, err := f.conversation(task.ID).CallTool(context.Background(), agentapi.HostToolCall{Name: "board_get", CallID: "get", Arguments: json.RawMessage(fmt.Sprintf(`{"ref":%q}`, ref))})
		if err != nil || len(res.Text) > maxToolReply || res.Failed {
			t.Fatalf("get %s: %d bytes, failed %v, %v", ref, len(res.Text), res.Failed, err)
		}
		return decodeReply(t, res)
	}
	r := get(story.ID)
	for _, part := range []string{"… 53248 more bytes", "(10 of 12)", ": " + strings.Repeat("c", 1<<10) + "… 9216 more bytes", "- … 5 more"} {
		if !strings.Contains(r.Text, part) {
			t.Fatalf("story text lacks %q", part)
		}
	}
	if r.Card == nil || r.Card.ID != story.ID || strings.Contains(r.Text, toolCutNote) {
		t.Fatalf("story reply cut or without its card: %+v", r.Card)
	}
	if r := get(leaf.ID); !strings.Contains(r.Text, "Blocked by:\n") || strings.Count(r.Text, `"Blocker `) != maxToolLinks || !strings.Contains(r.Text, "- … 5 more") {
		t.Fatalf("leaf text = %q", r.Text)
	}

	// A hundred checklist items and labels, each escaped to twice its size.
	var items, labels []string
	for i := range 100 {
		items = append(items, fmt.Sprintf(`{"text":%q}`, strings.Repeat(`"`, 490)))
		labels = append(labels, fmt.Sprintf("%q", fmt.Sprintf("l%d%s", i, strings.Repeat(`"`, 490))))
	}
	huge := f.create(board.KindSubtask, "", "Huge", `,"checklist":[`+strings.Join(items, ",")+`],"labels":[`+strings.Join(labels, ",")+`]`)
	r = get(huge.ID)
	if r.Card == nil || r.Card.ID != huge.ID || !strings.HasSuffix(r.Text, toolCutNote) || len(r.Text) < maxToolReply/4 {
		t.Fatalf("huge reply: card %+v, %d bytes of text", r.Card, len(r.Text))
	}
}

// A planning Task plans under its container: its edits of cards that have
// not started apply, confirmed or not, and the caps count per Task, its
// subagents' calls included.
func TestBoardToolsPlanUnderTheContainer(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	outside := f.create(board.KindEpic, "", "Outside")
	plan := f.planTask(epic.ID)

	r := f.toolOK(plan, "board_create", `{"kind":"subtask","parent":"#2","title":"Proposed","win_condition":"it works","prio":1,"effort":"M","labels":["api"],"checklist":["a"]}`)
	if r.Card == nil || r.Card.Seq != 4 || r.Card.Status != board.StatusPlanned || !strings.Contains(r.Text, "proposal") {
		t.Fatalf("create = %+v", r)
	}
	if c := f.card("#4").Card; c.Confirmed || c.Prio != 1 || c.Effort != "M" || !slices.Equal(c.Labels, []string{"api"}) || len(c.Checklist) != 1 {
		t.Fatalf("created = %+v", c)
	}
	if r := f.toolOK(plan, "board_edit", `{"ref":"#4","title":"Renamed"}`); r.Text != "Updated #4." || r.Card.Title != "Renamed" {
		t.Fatalf("edit proposal = %+v", r)
	}
	// A confirmed card that has not started is planned directly too.
	r = f.toolOK(plan, "board_edit", `{"ref":"#2","title":"Story 2","parent":"#1","rank":0}`)
	if r.Text != "Updated #2." {
		t.Fatalf("edit confirmed = %+v", r)
	}
	if d := f.card(story.ID); d.Card.Title != "Story 2" || !d.Card.Confirmed || len(d.Requests) != 0 {
		t.Fatalf("confirmed card after edit = %+v", d)
	}
	f.toolRefused(plan, "board_create", `{"kind":"epic","parent":"#1","title":"E"}`, string(board.CodeInvalid))
	f.toolRefused(plan, "board_create", fmt.Sprintf(`{"kind":"story","parent":%q,"title":"S"}`, outside.ID), string(board.CodeForbidden))
	f.toolRefused(plan, "board_create", `{"kind":"story","parent":"#1","title":" story 2 "}`, string(board.CodeDuplicate))
	if r := f.toolRefused(plan, "board_create", `{"kind":"subtask","title":"No parent"}`, string(board.CodeInvalid)); r.Text != "a subtask needs a parent: the epic or story to create it under" {
		t.Fatalf("no parent = %+v", r)
	}
	f.toolRefused(plan, "board_create", `{"kind":"epic","title":"From a planning task"}`, string(board.CodeForbidden))

	for i := range board.CapUnconfirmed - 1 {
		agent := ""
		if i%2 == 1 {
			agent = "sub-1"
		}
		if r, failed := f.callTool(plan, agent, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":"#2","title":"Leaf %d"}`, i)); failed {
			t.Fatalf("create %d: %+v", i, r)
		}
	}
	if r, failed := f.callTool(plan, "sub-2", "board_create", `{"kind":"subtask","parent":"#2","title":"One too many"}`); !failed || r.Code != string(board.CodeLimit) {
		t.Fatalf("past the cap = %+v, %v", r, failed)
	}
}

// A Task not started from a card proposes epics at the root (ADR 0005
// decision 4) and builds them out (decision 9): each stays a proposal with
// its expiry until the owner confirms or dismisses it, within the caps and
// the duplicate rule. Stories and subtasks still need a parent, and a Task
// started from a card proposes no epic.
func TestBoardToolsProposeRootEpics(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Owner's epic")
	leaf := f.create(board.KindSubtask, epic.ID, "Leaf")
	task := f.newTask(f.project).ID

	r := f.toolOK(task, "board_create", `{"kind":"epic","title":"Offline mode","win_condition":"works offline","prio":1}`)
	if r.Card == nil || r.Card.Kind != board.KindEpic || r.Card.Status != board.StatusPlanned || r.Text != fmt.Sprintf("Created #%[1]d at the root of the board. When the plan is complete, ask the owner to approve #%[1]d in the Planner; nothing runs before that.", r.Card.Seq) {
		t.Fatalf("create = %+v", r)
	}
	c := f.card(r.Card.ID).Card
	if c.Confirmed || c.ParentID != nil || c.ExpiresAt == nil || !c.ExpiresAt.Equal(c.CreatedAt.Add(board.ExpiryWindow)) || c.WinCondition != "works offline" || c.Prio != 1 {
		t.Fatalf("created = %+v", c)
	}
	second := f.toolOK(task, "board_create", `{"kind":"epic","parent":" ","title":"Sync"}`).Card
	f.toolRefused(task, "board_create", `{"kind":"epic","title":" offline MODE "}`, string(board.CodeDuplicate))
	for _, kind := range []board.Kind{board.KindStory, board.KindSubtask} {
		r := f.toolRefused(task, "board_create", fmt.Sprintf(`{"kind":%q,"parent":"","title":"Loose"}`, kind), string(board.CodeInvalid))
		if want := fmt.Sprintf("a %s needs a parent: the epic or story to create it under", kind); r.Text != want {
			t.Fatalf("loose %s = %q, want %q", kind, r.Text, want)
		}
	}
	if r := f.toolRefused(task, "board_create", fmt.Sprintf(`{"kind":"epic","parent":%q,"title":"Nested"}`, second.ID), string(board.CodeInvalid)); r.Text != "an epic cannot hold an epic" {
		t.Fatalf("nested = %q", r.Text)
	}
	// It plans its epics (decision 9): edits them, links them, and builds
	// them out with stories and subtasks; the owner's epic stays out of reach.
	for name, args := range map[string]string{
		"board_edit":      `{"ref":%q,"title":"Offline first"}`,
		"board_comment":   `{"ref":%q,"body":"note"}`,
		"board_checklist": `{"ref":%q,"add":["item"]}`,
	} {
		f.toolOK(task, name, fmt.Sprintf(args, r.Card.ID))
	}
	story := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"story","parent":%q,"title":"Cache"}`, r.Card.ID)).Card
	sub := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Store"}`, story.ID)).Card
	next := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Read"}`, story.ID)).Card
	f.toolOK(task, "board_link", fmt.Sprintf(`{"ref":%q,"blocker":%q}`, next.ID, sub.ID))
	f.toolOK(task, "board_link", fmt.Sprintf(`{"ref":%q,"blocker":"#1"}`, r.Card.ID))
	f.toolOK(task, "board_link", fmt.Sprintf(`{"ref":%q,"blocker":%q}`, second.ID, r.Card.ID))
	f.toolRefused(task, "board_link", fmt.Sprintf(`{"ref":"#1","blocker":%q}`, second.ID), string(board.CodeForbidden))
	f.toolRefused(task, "board_create", `{"kind":"story","parent":"#1","title":"In theirs"}`, string(board.CodeForbidden))
	f.toolRefused(task, "board_link", fmt.Sprintf(`{"ref":%q,"blocker":%q}`, r.Card.ID, leaf.ID), string(board.CodeInvalid))
	if d := f.card(r.Card.ID); d.Card.Title != "Offline first" || len(d.Comments) != 1 || len(d.Card.Checklist) != 1 || !slices.Equal(d.Card.BlockedBy, []string{epic.ID}) || len(d.Requests) != 0 {
		t.Fatalf("its epic = %+v", d)
	}
	if d := f.card(next.ID); d.Card.Confirmed || !slices.Equal(d.Card.BlockedBy, []string{sub.ID}) {
		t.Fatalf("its subtask = %+v", d)
	}
	for i := range board.CapUnconfirmed - 2 {
		f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"epic","title":"Epic %d"}`, i))
	}
	f.toolRefused(task, "board_create", `{"kind":"epic","title":"One too many"}`, string(board.CodeLimit))

	// A Task planning under a card, or launched from one, proposes no epic.
	plan := f.planTask(epic.ID)
	_, worker := f.launch(leaf.ID)
	for _, id := range []string{plan, worker.ID} {
		f.toolRefused(id, "board_create", `{"kind":"epic","title":"From a scoped task"}`, string(board.CodeForbidden))
	}

	// The owner confirms one and dismisses the other.
	var got BoardCard
	f.call(http.MethodPost, "/api/board/cards/"+r.Card.ID+"/confirm", ``, http.StatusOK, &got)
	if !got.Confirmed || got.ExpiresAt != nil || got.PinnedSHA != gitOutput(t, f.repo, "rev-parse", "HEAD") {
		t.Fatalf("confirmed = %+v", got)
	}
	f.call(http.MethodPost, "/api/board/cards/"+second.ID+"/dismiss", `{}`, http.StatusOK, &got)
	if got.Status != board.StatusCancelled {
		t.Fatalf("dismissed = %+v", got)
	}
	// Each frees a place under the cap.
	f.toolOK(task, "board_create", `{"kind":"epic","title":"After the owner decided"}`)
	f.toolOK(task, "board_create", `{"kind":"epic","title":"Sync"}`)
}

// Claim holds a pending subtask for "Do whole story", within the one-hold
// cap; a planning Task holds nothing.
func TestBoardToolsClaim(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	one := f.create(board.KindSubtask, story.ID, "One")
	two := f.create(board.KindSubtask, story.ID, "Two")
	held, task := f.launch(story.ID)
	if held.ID != one.ID {
		t.Fatalf("held %+v", held)
	}
	f.toolRefused(task.ID, "board_claim", fmt.Sprintf(`{"ref":%q}`, two.ID), string(board.CodeLimit))
	if r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"one is done"}`, one.ID)); !strings.Contains(r.Text, "no_change_in_tree") {
		t.Fatalf("done = %+v", r)
	}
	writeRepoFile(t, f.repo, "wip.txt", "x")
	r := f.toolOK(task.ID, "board_claim", `{"ref":"#3"}`)
	if r.Card == nil || r.Card.ID != two.ID || r.Card.Status != board.StatusDoing {
		t.Fatalf("claim = %+v", r)
	}
	d := f.card(two.ID)
	if d.Card.HeldBy != task.ID || len(d.Holds) != 1 || d.Holds[0].BaselineHead != gitOutput(t, f.repo, "rev-parse", "HEAD") || !slices.Equal(d.Holds[0].BaselineDirty, []string{"wip.txt"}) {
		t.Fatalf("claimed = %+v", d)
	}
	f.toolRefused(task.ID, "board_claim", `{"ref":"#1"}`, string(board.CodeInvalid))
	three := f.create(board.KindSubtask, story.ID, "Three")
	plan := f.planTask(story.ID)
	f.toolRefused(plan, "board_claim", fmt.Sprintf(`{"ref":%q}`, three.ID), string(board.CodeForbidden))
}

// A working Task maps dependencies among its proposals and the confirmed
// subtasks in its scope, cycles still refused, while the subtask it holds
// keeps its plan; it claims only what the owner confirmed: a claim of a
// proposal is refused, naming it.
func TestBoardToolsLinkProposalsButClaimOnlyConfirmed(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	one := f.create(board.KindSubtask, story.ID, "One")
	f.create(board.KindSubtask, story.ID, "Two")
	_, task := f.launch(one.ID)
	f.toolOK(task.ID, "board_create", `{"kind":"subtask","parent":"#1","title":"Proposed A"}`)
	f.toolOK(task.ID, "board_create", `{"kind":"subtask","parent":"#1","title":"Proposed B"}`)
	if r := f.toolOK(task.ID, "board_link", `{"ref":"#5","blocker":"#4"}`); r.Text != "#4 now blocks #5, which can't be finished until #4 is done or cancelled." {
		t.Fatalf("link proposals = %+v", r)
	}
	f.toolOK(task.ID, "board_link", `{"ref":"#3","blocker":"#5"}`)
	f.toolRefused(task.ID, "board_link", `{"ref":"#4","blocker":"#3"}`, string(board.CodeInvalid)) // a cycle
	f.toolRefused(task.ID, "board_link", `{"ref":"#2","blocker":"#5"}`, string(board.CodeInProgress))
	if c := f.card("#3").Card; !slices.Equal(c.BlockedBy, []string{f.card("#5").Card.ID}) {
		t.Fatalf("confirmed subtask blocked by %v", c.BlockedBy)
	}
	f.toolOK(task.ID, "board_request", `{"ref":"#2","kind":"blocked","comment":"waits on the proposals"}`)
	r := f.toolRefused(task.ID, "board_claim", `{"ref":"#4"}`, string(board.CodeUnconfirmed))
	if !slices.Equal(r.Refs, []string{"#4"}) || !strings.Contains(r.Text, "owner confirms it") {
		t.Fatalf("claim of a proposal = %+v", r)
	}
	if c := f.card("#4").Card; c.HeldBy != "" || c.Confirmed {
		t.Fatalf("a refused claim wrote %+v", c)
	}
	// It unlinks and deletes while planning, a confirmed card too, never
	// the subtask it holds; deleting a blocker names what it released.
	if r := f.toolOK(task.ID, "board_unlink", `{"ref":"#3","blocker":"#5"}`); r.Text != "Removed the link between #5 and #3." {
		t.Fatalf("unlink = %+v", r)
	}
	f.toolRefused(task.ID, "board_unlink", `{"ref":"#3","blocker":"#5"}`, string(board.CodeNotFound))
	if r := f.toolOK(task.ID, "board_delete", `{"ref":"#4"}`); r.Card == nil || r.Card.Status != board.StatusCancelled ||
		r.Text != "Deleted #4 and everything under it. The owner can restore it. It no longer blocks #5." {
		t.Fatalf("delete = %+v", r)
	}
	if r := f.toolOK(task.ID, "board_delete", `{"ref":"#3"}`); r.Text != "Deleted #3 and everything under it. The owner can restore it." {
		t.Fatalf("delete of a confirmed card = %+v", r)
	}
	f.toolRefused(task.ID, "board_delete", `{"ref":"#2"}`, string(board.CodeInProgress))
}

// Work that has started keeps what it waits on: a split into siblings
// says which started subtask its parts do not block, and the links of a
// story with started work are the owner's, the refusal naming that work.
func TestBoardToolsKeepStartedWorkWaiting(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	first := f.create(board.KindSubtask, story.ID, "First")
	second := f.create(board.KindSubtask, story.ID, "Second")
	other := f.create(board.KindStory, epic.ID, "Other")
	f.create(board.KindSubtask, other.ID, "Other one")
	f.call(http.MethodPost, "/api/board/links", fmt.Sprintf(`{"blocker":%q,"blocked":%q}`, first.ID, second.ID), http.StatusNoContent, nil)
	f.launch(second.ID)
	plan := f.planTask(epic.ID)
	r := f.toolOK(plan, "board_split", fmt.Sprintf(`{"ref":%q,"children":[{"title":"A"},{"title":"B"}]}`, first.ID))
	if want := fmt.Sprintf("Split #%d into new subtasks after it; #%d was cancelled, and they took over its links. They do not block #%d, which already started.", first.Seq, first.Seq, second.Seq); r.Text != want {
		t.Fatalf("split = %q, want %q", r.Text, want)
	}
	r = f.toolRefused(plan, "board_link", fmt.Sprintf(`{"ref":%q,"blocker":%q}`, story.ID, other.ID), string(board.CodeInProgress))
	if !slices.Equal(r.Refs, []string{fmt.Sprintf("#%d", second.Seq)}) {
		t.Fatalf("link onto started work = %+v", r)
	}
}

// Split applies at once to a subtask that has not started and is a request on a
// held one; blocked names its blocker on one that has not started. Neither is accepted
// by a passing acceptance command: only done is.
func TestBoardToolsSplitAndBlocked(t *testing.T) {
	f := newPlanner(t)
	f.setAcceptCmd("true")
	epic := f.create(board.KindEpic, "", "Epic")
	blocker := f.create(board.KindSubtask, epic.ID, "Blocker")
	plan := f.planTask(epic.ID)
	big := f.toolOK(plan, "board_create", `{"kind":"subtask","parent":"#1","title":"Big","checklist":["part a"]}`).Card
	r := f.toolOK(plan, "board_split", fmt.Sprintf(`{"ref":"#%d","children":[{"title":"Part b","win_condition":"b works"}]}`, big.Seq))
	if r.Card == nil || r.Card.Kind != board.KindStory || !strings.Contains(r.Text, "now a story") {
		t.Fatalf("split = %+v", r)
	}
	list := f.toolOK(plan, "board_list", fmt.Sprintf(`{"parent":%q}`, big.ID)).Text
	if !strings.Contains(list, `"Part b" (planned, proposed)`) || !strings.Contains(list, `"part a" (planned, proposed)`) {
		t.Fatalf("children = %q", list)
	}
	// Under a story, which can't hold a story, the split makes siblings.
	part := f.toolOK(plan, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Part c","checklist":["c1","c2"]}`, big.ID)).Card
	if r := f.toolOK(plan, "board_split", fmt.Sprintf(`{"ref":%q}`, part.ID)); r.Card.Status != board.StatusCancelled || !strings.Contains(r.Text, "was cancelled") {
		t.Fatalf("split into siblings = %+v", r)
	}

	leaf := f.create(board.KindSubtask, epic.ID, "Confirmed")
	_, task := f.launch(leaf.ID)
	if r := f.toolOK(task.ID, "board_split", fmt.Sprintf(`{"ref":%q,"children":[{"title":"Half"}]}`, leaf.ID)); !strings.Contains(r.Text, "request") || r.Card.Kind != board.KindSubtask {
		t.Fatalf("split request = %+v", r)
	}
	f.toolRefused(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"x","blocker":"#%d"}`, leaf.ID, blocker.Seq), string(board.CodeInvalid))
	f.toolRefused(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"cancel","comment":" "}`, leaf.ID), string(board.CodeInvalid))
	// The held subtask keeps its plan, so its blocked request names no
	// blocker; one that has not started may.
	f.toolRefused(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"blocked","comment":"needs the blocker","blocker":"#%d"}`, leaf.ID, blocker.Seq), string(board.CodeInProgress))
	if r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"blocked","comment":"needs #%d"}`, leaf.ID, blocker.Seq)); r.Text != fmt.Sprintf("Filed a blocked request on #%d for the owner to decide.", r.Card.Seq) {
		t.Fatalf("blocked = %+v", r)
	}
	waiting := f.create(board.KindSubtask, epic.ID, "Waiting")
	f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"blocked","comment":"needs the blocker","blocker":"#%d"}`, waiting.ID, blocker.Seq))
	var kinds []board.RequestKind
	for _, req := range f.card(leaf.ID).Requests {
		kinds = append(kinds, req.Kind)
		if req.Status != board.RequestPending {
			t.Fatalf("%s request = %+v", req.Kind, req)
		}
	}
	var payload struct {
		Blocker string `json:"blocker"`
	}
	if reqs := f.card(waiting.ID).Requests; len(reqs) == 1 {
		_ = json.Unmarshal(reqs[0].Payload, &payload)
	}
	if !slices.Equal(kinds, []board.RequestKind{board.RequestSplit, board.RequestBlocked}) || payload.Blocker != blocker.ID {
		t.Fatalf("requests = %v, blocker %q", kinds, payload.Blocker)
	}
}

// Done runs the finishing guard first: open checklist items refuse it with
// the list, before any acceptance run.
func TestBoardToolsDoneRefusedWithOpenItems(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("touch ran")
	leaf := f.create(board.KindSubtask, "", "Leaf", `,"checklist":[{"text":"write it","done":true},{"text":"test it","done":false},{"text":"ship it","done":false}]`)
	_, task := f.launch(leaf.ID)
	done := fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"finished"}`, leaf.ID)
	if r := f.toolRefused(task.ID, "board_request", done, string(board.CodeGuardOpenItems)); !slices.Equal(r.Refs, []string{"test it", "ship it"}) {
		t.Fatalf("refusal = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "ran")); !os.IsNotExist(err) || len(f.card(leaf.ID).Requests) != 0 {
		t.Fatalf("a refused claim ran or filed: %v, %+v", err, f.card(leaf.ID).Requests)
	}
	f.toolOK(task.ID, "board_checklist", fmt.Sprintf(`{"ref":%q,"tick":[1,2]}`, leaf.ID))
	f.toolOK(task.ID, "board_request", done)
	if _, err := os.Stat(filepath.Join(f.repo, "ran")); err != nil {
		t.Fatal("the acceptance command did not run:", err)
	}
}

// A done claim runs the owner's acceptance command, then files the request
// with evidence of the Task's work since the hold started; a command that
// passes accepts it at once (ADR 0005 decision 5), and the owner can move
// the subtask back to To do. A subtask whose command is the empty one, none,
// runs nothing and waits for the owner.
func TestBoardToolsDoneFilesEvidence(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("test -f made.txt && echo checked")
	leaf := f.create(board.KindSubtask, "", "Make it")
	_, task := f.launch(leaf.ID)
	writeRepoFile(t, f.repo, "made.txt", "a\nb\n")
	f.conversation(task.ID).EmitItem(agentapi.Item{ID: "edit-1", Kind: agentapi.ItemTool, Time: time.Now(),
		Tool: &agentapi.ToolCall{Name: "create", Status: agentapi.ToolCompleted, Input: `{"path":"made.txt"}`}})
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"made it","proposed_accept_cmd":"make check"}`, leaf.ID))
	if want := "#1 is done; the acceptance command passed. Claim the next pending subtask, if any."; r.Text != want || r.Card == nil || r.Card.Status != board.StatusDone {
		t.Fatalf("done = %+v, want %q", r, want)
	}
	d := f.card(leaf.ID)
	if len(d.Requests) != 1 || d.Requests[0].Kind != board.RequestDone || d.Requests[0].Comment != "made it" || d.Requests[0].TaskID != task.ID ||
		!strings.Contains(string(d.Requests[0].Payload), `"proposed_accept_cmd":"make check"`) ||
		d.Requests[0].Status != board.RequestAccepted || d.Requests[0].DecidedBy != board.AuthorUAM {
		t.Fatalf("requests = %+v", d.Requests)
	}
	if d.Card.Status != board.StatusDone || d.Card.HeldBy != "" || d.Holds[0].EndReason != string(board.ReleaseAccepted) ||
		!slices.Equal(comments(d), []string{"task:" + task.ID + ": made it", "uam: " + board.AutoAcceptComment}) || !d.Comments[1].Automatic {
		t.Fatalf("accepted = %+v, comments %q", d, comments(d))
	}
	var ev Evidence
	if err := json.Unmarshal(d.Requests[0].Evidence, &ev); err != nil {
		t.Fatal(err)
	}
	if f := evidenceFiles(ev)["made.txt"]; !f.ByTask || f.Added != 2 {
		t.Fatalf("diff = %+v", ev.Diff)
	}
	if ev.Accept == nil || ev.Accept.Exit != 0 || !strings.Contains(ev.Accept.Tail, "checked") || ev.Transcript == nil || ev.Transcript.TaskID != task.ID || ev.Transcript.ToItem != "edit-1" || ev.Transcript.Partial {
		t.Fatalf("evidence = %+v, accept %+v, transcript %+v", ev, ev.Accept, ev.Transcript)
	}
	// The owner moves it back to To do.
	var backToTodo BoardCard
	f.call(http.MethodPost, "/api/board/cards/"+leaf.ID+"/status", `{"status":"todo","comment":"one more case"}`, http.StatusOK, &backToTodo)
	if backToTodo.Status != board.StatusTodo || backToTodo.HeldBy != "" || !slices.Contains(comments(f.card(leaf.ID)), "owner: one more case") {
		t.Fatalf("back to To do = %+v", backToTodo)
	}

	none := f.create(board.KindSubtask, "", "Nothing to run")
	f.store(func(ctx context.Context, st *board.Store) error {
		_, err := st.Edit(ctx, board.Owner(""), none.ID, board.Patch{AcceptCmd: &sql.NullString{Valid: true}})
		return err
	})
	f.setAcceptCmd("exit 1")
	_, other := f.launch(none.ID)
	// No argument lets an agent say a command passed.
	f.toolRefused(other.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"x","passed_cmd":"true"}`, none.ID), string(board.CodeInvalid))
	r = f.toolOK(other.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"nothing changed","proposed_accept_cmd":"true"}`, none.ID))
	if want := "Filed a done request on #2; it waits for the owner to accept, because no acceptance command is set and nothing changed since the hold began. Evidence: +0 −0 in 0 files (0 touched by this task), 0 commits. Flags: no_change_in_tree."; r.Text != want || r.Card.Status != board.StatusDoing {
		t.Fatalf("done without a command = %+v, want %q", r, want)
	}
	d = f.card(none.ID)
	var bare Evidence
	if err := json.Unmarshal(d.Requests[0].Evidence, &bare); err != nil || bare.Accept != nil || d.Requests[0].Status != board.RequestPending || d.Requests[0].DecidedBy != "" {
		t.Fatalf("request without a command = %+v, accept %+v, %v", d.Requests[0], bare.Accept, err)
	}
	var snap BoardSnapshot
	f.call(http.MethodGet, "/api/board?project_id="+f.project, "", http.StatusOK, &snap)
	if len(snap.Requests) != 1 || snap.Requests[0].ID != d.Requests[0].ID {
		t.Fatalf("inbox = %+v", snap.Requests)
	}
	// Rejecting it while the Task is live still steers the Task, which keeps the hold.
	var rejected Rejection
	f.call(http.MethodPost, "/api/board/requests/"+d.Requests[0].ID+"/reject", `{"reason":"add a test"}`, http.StatusOK, &rejected)
	if !rejected.Steered || rejected.DecidedBy != "owner" || f.card(none.ID).Card.HeldBy != other.ID {
		t.Fatalf("rejected = %+v", rejected)
	}
}

// A shell that does not start files the claim flagged, for the owner to
// accept: nothing passed.
func TestBoardToolsDoneWaitsWhenAcceptanceCouldNotRun(t *testing.T) {
	acceptEnv(t)
	shell := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(shell, []byte{0, 1, 2, 3}, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	f := newPlanner(t)
	f.setAcceptCmd("true")
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	writeRepoFile(t, f.repo, "made.txt", "x\n")
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))
	if !strings.HasPrefix(r.Text, "Filed a done request on #1; it waits for the owner to accept, because the acceptance command could not run.") ||
		!strings.Contains(r.Text, board.FlagAcceptanceCouldNotRun) {
		t.Fatalf("done = %q", r.Text)
	}
	if d := f.card(leaf.ID); d.Card.Status != board.StatusDoing || d.Requests[0].Status != board.RequestPending {
		t.Fatalf("after a run that did not start = %+v", d)
	}
}

// A green command with anything flagged leaves the request for the owner,
// and the reply says why (ADR 0005 decision 5). So does a blank command,
// which is none.
func TestBoardToolsDoneWaitsOnAFlag(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("true")
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	writeRepoFile(t, f.repo, "made_test.go", "package x\n")
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))
	if want := "Filed a done request on #1; it waits for the owner to accept, because the change touches test or build files. Evidence: +1 −0 in 1 files (0 touched by this task), 0 commits, acceptance command exited 0. Flags: tests_or_build_changed."; r.Text != want {
		t.Fatalf("done = %q, want %q", r.Text, want)
	}
	if d := f.card(leaf.ID); d.Card.Status != board.StatusDoing || d.Requests[0].Status != board.RequestPending {
		t.Fatalf("after a flagged claim = %+v", d)
	}

	var settings struct {
		AcceptCmd string `json:"accept_cmd"`
	}
	f.call(http.MethodPatch, "/api/board/projects/"+f.project, `{"accept_cmd":"  "}`, http.StatusOK, &settings)
	if settings.AcceptCmd != "" {
		t.Fatalf("a blank default = %q", settings.AcceptCmd)
	}
	blank := f.create(board.KindSubtask, "", "Blank")
	_, other := f.launch(blank.ID)
	writeRepoFile(t, f.repo, "other.txt", "x\n")
	r = f.toolOK(other.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, blank.ID))
	if !strings.HasPrefix(r.Text, "Filed a done request on #2; it waits for the owner to accept, because no acceptance command is set. ") {
		t.Fatalf("done with a blank command = %q", r.Text)
	}
}

// A green claim whose acceptance would close the story it is in and the
// epic above it, while they still have proposals, waits for the owner:
// closing would cancel them, and the reply names them all.
func TestBoardToolsDoneWaitsWhenItWouldCloseProposals(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("true")
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	leaf := f.create(board.KindSubtask, story.ID, "Leaf")
	_, task := f.launch(leaf.ID)
	followUp := f.toolOK(task.ID, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Follow-up"}`, story.ID)).Card
	var underEpic board.Card
	f.store(func(ctx context.Context, st *board.Store) error {
		if err := st.StartPlanning(ctx, board.Owner(""), epic.ID, "planner"); err != nil {
			return err
		}
		var err error
		underEpic, err = st.Create(ctx, board.Agent("planner", ""), board.NewCard{ProjectID: f.project, Kind: board.KindSubtask, ParentID: epic.ID, Title: "Under the epic"})
		return err
	})
	writeRepoFile(t, f.repo, "made.txt", "x\n")
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))
	if want := fmt.Sprintf("Filed a done request on #3; it waits for the owner to accept, because accepting it would close #2 and #1, which still have proposals #%d and #%d. ", followUp.Seq, underEpic.Seq); !strings.HasPrefix(r.Text, want) {
		t.Fatalf("done = %q, want the prefix %q", r.Text, want)
	}
	for _, id := range []string{followUp.ID, underEpic.ID} {
		if d := f.card(id); d.Card.Status != board.StatusPlanned {
			t.Fatalf("the proposal = %+v", d.Card)
		}
	}
	if d := f.card(leaf.ID); d.Card.Status != board.StatusDoing || d.Requests[0].Status != board.RequestPending {
		t.Fatalf("the subtask = %+v", d)
	}
}

// A command the owner changes while it runs no longer vouches for the claim:
// it waits, and the reply says so.
func TestBoardToolsDoneWaitsWhenTheCommandChangedWhileItRan(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd(`touch started; i=0; while [ ! -f go ] && [ $i -lt 400 ]; do sleep 0.05; i=$((i+1)); done`)
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	writeRepoFile(t, f.repo, "made.txt", "x\n")
	results := make(chan agentapi.HostToolResult, 1)
	go func() {
		res, _ := f.conversation(task.ID).CallTool(context.Background(), agentapi.HostToolCall{Name: "board_request", CallID: "c1",
			Arguments: json.RawMessage(fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))})
		results <- res
	}()
	waitUntil(t, "the acceptance command to start", func() bool {
		_, err := os.Stat(filepath.Join(f.repo, "started"))
		return err == nil
	})
	f.call(http.MethodPatch, "/api/board/projects/"+f.project, `{"accept_cmd":"make check"}`, http.StatusOK, nil)
	writeRepoFile(t, f.repo, "go", "")
	select {
	case res := <-results:
		r := decodeReply(t, res)
		if res.Failed || !strings.HasPrefix(r.Text, "Filed a done request on #1; it waits for the owner to accept, because the acceptance command changed while it ran. ") {
			t.Fatalf("done = %+v", r)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the claim did not finish")
	}
	if d := f.card(leaf.ID); d.Card.Status != board.StatusDoing || d.Requests[0].Status != board.RequestPending {
		t.Fatalf("the subtask = %+v", d)
	}
}

// A file already dirty when the owner launched the subtask, and unchanged
// since, is not the Task's work: it is left out of the diff, so another hold
// that touched it raises no overlap.
func TestBoardToolsDoneLeavesOutWhatWasDirtyAtLaunch(t *testing.T) {
	f := newPlanner(t)
	writeRepoFile(t, f.repo, "pre.txt", "committed\n")
	gitIn(t, f.repo, "add", ".")
	gitIn(t, f.repo, "commit", "-q", "-m", "pre")
	writeRepoFile(t, f.repo, "pre.txt", "dirty before the launch\n")
	writeRepoFile(t, f.repo, "new.txt", "untracked before the launch\n")
	mine, theirs := f.create(board.KindSubtask, "", "Mine"), f.create(board.KindSubtask, "", "Theirs")
	_, task := f.launch(mine.ID)
	_, other := f.launch(theirs.ID)
	for _, p := range []string{"pre.txt", "new.txt"} {
		f.conversation(other.ID).EmitItem(agentapi.Item{ID: "edit-" + p, Kind: agentapi.ItemTool, Time: time.Now(),
			Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted, Input: fmt.Sprintf(`{"path":%q}`, p)}})
	}
	if d := f.card(mine.ID); len(d.Holds) != 1 || !slices.Equal(d.Holds[0].BaselineDirty, []string{"pre.txt", "new.txt"}) {
		t.Fatalf("launch baseline = %+v", d.Holds)
	}
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"nothing to do"}`, mine.ID))
	if !strings.HasSuffix(r.Text, "in 0 files (0 touched by this task), 0 commits. Flags: no_change_in_tree.") {
		t.Fatalf("done = %q", r.Text)
	}
	var ev Evidence
	if err := json.Unmarshal(f.card(mine.ID).Requests[0].Evidence, &ev); err != nil || len(ev.Diff.Files) != 0 {
		t.Fatalf("diff = %+v, %v", ev.Diff, err)
	}
}

// A launch whose baseline can't be read is refused before any Task starts:
// the subtask stays as it was, and nothing holds it.
func TestBoardLaunchRefusedWithoutABaseline(t *testing.T) {
	f := newPlanner(t)
	leaf := f.create(board.KindSubtask, "", "Leaf")
	writeRepoFile(t, f.repo, filepath.Join(".git", "index"), "not an index")
	if w := f.do(http.MethodPost, "/api/board/cards/"+leaf.ID+"/launch", `{}`); w.Code < 400 {
		t.Fatalf("launch = %d %s", w.Code, w.Body)
	}
	if n := len(f.ts.prov.Conversations()); n != 0 {
		t.Fatalf("%d conversations opened", n)
	}
	if d := f.card(leaf.ID); d.Card.Status != leaf.Status || d.Card.HeldBy != "" || len(d.Holds) != 0 {
		t.Fatalf("after the refused launch = %+v", d)
	}
}

// A transcript uam holds only from after the hold started marks the
// evidence partial: the touched files may be incomplete.
func TestBoardToolsDoneNotesAPartialTranscript(t *testing.T) {
	f := newPlanner(t)
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	f.conversation(task.ID).EmitItem(agentapi.Item{ID: "late", Kind: agentapi.ItemAssistant, Text: "hi", Time: time.Now().Add(time.Minute)})
	waitUntil(t, "the item", func() bool {
		f.m.mu.Lock()
		defer f.m.mu.Unlock()
		_, ok := f.m.sessions[task.ID].itemIdx[itemKey("", "late")]
		return ok
	})
	f.m.mu.Lock()
	f.m.sessions[task.ID].truncated = true
	f.m.mu.Unlock()
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))
	if !strings.Contains(r.Text, "may be incomplete") {
		t.Fatalf("done = %q", r.Text)
	}
	var ev Evidence
	if err := json.Unmarshal(f.card(leaf.ID).Requests[0].Evidence, &ev); err != nil || ev.Transcript == nil || !ev.Transcript.Partial || ev.Transcript.FromItem != "late" {
		t.Fatalf("transcript = %+v, %v", ev.Transcript, err)
	}
}

// A tool call clipped since the hold started, whose input may have lost the
// path it edits, marks the transcript partial too.
func TestBoardToolsDoneNotesAClippedToolCall(t *testing.T) {
	f := newPlanner(t)
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	f.conversation(task.ID).EmitItem(agentapi.Item{ID: "big", Kind: agentapi.ItemTool, Clipped: true, Time: time.Now().Add(time.Minute),
		Tool: &agentapi.ToolCall{Name: "create", Input: `{"file_text":"xxx`, Status: agentapi.ToolCompleted}})
	waitUntil(t, "the item", func() bool {
		f.m.mu.Lock()
		defer f.m.mu.Unlock()
		_, ok := f.m.sessions[task.ID].itemIdx[itemKey("", "big")]
		return ok
	})
	r := f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))
	if !strings.Contains(r.Text, "may be incomplete") {
		t.Fatalf("done = %q", r.Text)
	}
	var ev Evidence
	if err := json.Unmarshal(f.card(leaf.ID).Requests[0].Evidence, &ev); err != nil || ev.Transcript == nil || !ev.Transcript.Partial {
		t.Fatalf("transcript = %+v, %v", ev.Transcript, err)
	}
}

// A command that exits non-zero refuses the claim with its output tail and
// the hint for subtasks that are red by design.
func TestBoardToolsDoneRefusedWhenAcceptanceFails(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("echo boom-tail; exit 3")
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	r := f.toolRefused(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID), string(board.CodeAcceptanceFailed))
	for _, part := range []string{"exited 3", "boom-tail", "red by design", "command to ''"} {
		if !strings.Contains(r.Text, part) {
			t.Fatalf("refusal %q lacks %q", r.Text, part)
		}
	}
	if len(f.card(leaf.ID).Requests) != 0 {
		t.Fatal("a failed claim was filed")
	}
}

// Archiving the Task kills its running acceptance command and files
// nothing (ADR 0005 §6, test plan 4).
func TestBoardToolsArchiveDiscardsARunningClaim(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("touch started; sleep 30; touch finished")
	leaf := f.create(board.KindSubtask, "", "Slow")
	_, task := f.launch(leaf.ID)
	f.idle(task.ID)
	conv := f.conversation(task.ID)
	results := make(chan agentapi.HostToolResult, 1)
	go func() {
		res, _ := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: "board_request", CallID: "c1",
			Arguments: json.RawMessage(fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))})
		results <- res
	}()
	waitUntil(t, "the acceptance command to start", func() bool {
		_, err := os.Stat(filepath.Join(f.repo, "started"))
		return err == nil
	})
	if _, err := f.m.Archive(task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-results:
		if r := decodeReply(t, res); !res.Failed || r.Text != errTaskEnded.Error() {
			t.Fatalf("claim after archive = %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("archive did not end the acceptance run")
	}
	d := f.card(leaf.ID)
	if len(d.Requests) != 0 || d.Card.Status != board.StatusTodo || d.Card.HeldBy != "" {
		t.Fatalf("after archive = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "finished")); !os.IsNotExist(err) {
		t.Fatalf("the command ran on: %v", err)
	}
	// An archived Task's calls are refused before they start.
	res := conv.Request().CallTool(context.Background(), agentapi.HostToolCall{Name: "board_get", CallID: "c2", TaskID: task.ID,
		Arguments: json.RawMessage(fmt.Sprintf(`{"ref":%q}`, leaf.ID))})
	if r := decodeReply(t, res); !res.Failed || !strings.Contains(r.Text, "the task is archived") {
		t.Fatalf("call of an archived task = %+v", r)
	}
}

// A board_claim in flight when the Task is archived or settled ends before
// the holds are reconciled or decided, and a hold it made as the Task ended
// is released: no hold is left on an ended Task.
func TestBoardToolsTaskEndReleasesAClaimInFlight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		end     func(*Manager, string, string) error
		oneHeld bool
	}{
		{"archive", func(m *Manager, task, _ string) error {
			_, err := m.Archive(task)
			return err
		}, false},
		{"settle", func(m *Manager, task, one string) error {
			_, err := m.SettleHolds(task, map[string]HoldDecision{one: {Action: holdKeep}})
			return err
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPlanner(t)
			story := f.create(board.KindStory, "", "Story")
			one := f.create(board.KindSubtask, story.ID, "One")
			two := f.create(board.KindSubtask, story.ID, "Two")
			_, task := f.launch(story.ID)
			f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"one is done"}`, one.ID))
			f.idle(task.ID)
			head := gitOutput(t, f.repo, "rev-parse", "HEAD")
			entered := make(chan struct{})
			f.m.boardCallHook = func(ctx context.Context, tool string) {
				if tool != "board_claim" {
					return
				}
				close(entered)
				<-ctx.Done()
				// The claim's write commits just as the Task ends.
				if err := f.m.withBoard(func(st *board.Store) error {
					_, err := st.Claim(context.Background(), board.Agent(task.ID, ""), two.ID, board.Baseline{Head: head})
					return err
				}); err != nil {
					t.Error(err)
				}
			}
			conv := f.conversation(task.ID)
			results := make(chan agentapi.HostToolResult, 1)
			go func() {
				res, _ := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: "board_claim", CallID: "c1",
					Arguments: json.RawMessage(fmt.Sprintf(`{"ref":%q}`, two.ID))})
				results <- res
			}()
			<-entered
			if err := tc.end(f.m, task.ID, one.ID); err != nil {
				t.Fatal(err)
			}
			if res := <-results; !res.Failed || decodeReply(t, res).Text != errTaskEnded.Error() {
				t.Fatalf("claim as the task ended = %+v", res)
			}
			if d := f.card(two.ID); d.Card.HeldBy != "" || d.Card.Status != board.StatusTodo {
				t.Fatalf("the claimed subtask after the task ended = %+v", d.Card)
			}
			if held := f.card(one.ID).Card.HeldBy == task.ID; held != tc.oneHeld {
				t.Fatalf("the kept subtask held = %v, want %v", held, tc.oneHeld)
			}
		})
	}
}

// A subset of the tools in a container's scope, as a Utility job builds it
// (ADR 0005 §18): refs outside the container are not found.
func TestBoardToolSubsetInAContainer(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	inside := f.create(board.KindSubtask, story.ID, "Inside")
	f.create(board.KindStory, epic.ID, "Outside")
	// The actor is a Utility job, which no Task session has: its calls are
	// not tied to one.
	f.store(func(ctx context.Context, st *board.Store) error {
		return st.StartPlanning(ctx, board.Owner(""), story.ID, "utility-job")
	})
	tools, call := f.m.boardHostTools(func(context.Context, agentapi.HostToolCall) (boardScope, error) {
		return boardScope{actor: board.Agent("utility-job", ""), project: f.project, dir: f.repo, container: story.ID}, nil
	}, "board_create", "board_edit", "board_get", "board_list")
	if got := toolNames(tools); !slices.Equal(got, []string{"board_get", "board_list", "board_create", "board_edit"}) {
		t.Fatalf("tools = %q", got)
	}
	run := func(name, args string) (toolReply, bool) {
		res := call(context.Background(), agentapi.HostToolCall{Name: name, Arguments: json.RawMessage(args)})
		return decodeReply(t, res), res.Failed
	}
	for _, ref := range []string{epic.ID, "#4"} {
		if r, failed := run("board_get", fmt.Sprintf(`{"ref":%q}`, ref)); !failed || r.Code != string(board.CodeNotFound) {
			t.Fatalf("get %s = %+v", ref, r)
		}
	}
	if r, failed := run("board_get", fmt.Sprintf(`{"ref":%q}`, inside.ID)); failed || r.Card.ID != inside.ID {
		t.Fatalf("get inside = %+v", r)
	}
	if r, _ := run("board_list", `{}`); r.Text != "#2 story \"Story\" (planned) under #1\n#3 subtask \"Inside\" (planned) under #2" {
		t.Fatalf("list = %q", r.Text)
	}
	if r, failed := run("board_claim", `{"ref":"#3"}`); !failed || r.Code != string(board.CodeInvalid) {
		t.Fatalf("a tool outside the subset = %+v", r)
	}
	r, failed := run("board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Found by the job"}`, story.ID))
	if failed || r.Card == nil || r.Card.Title != "Found by the job" {
		t.Fatalf("create = %+v", r)
	}
	if d := f.card(r.Card.ID); d.Card.ParentID == nil || *d.Card.ParentID != story.ID || d.Card.Confirmed {
		t.Fatalf("created = %+v", d.Card)
	}
	// Scoped to its container, the job proposes no epic at the root.
	if r, failed := run("board_create", `{"kind":"epic","title":"Epic from the job"}`); !failed || r.Code != string(board.CodeForbidden) {
		t.Fatalf("root epic from the job = %+v", r)
	}

	// board_get shows nothing outside the container: the path starts at it,
	// and a blocker outside it is left out.
	sibling := f.create(board.KindSubtask, story.ID, "Sibling")
	f.store(func(ctx context.Context, st *board.Store) error {
		if err := st.Link(ctx, board.Owner(""), "#4", story.ID); err != nil {
			return err
		}
		return st.Link(ctx, board.Owner(""), sibling.ID, inside.ID)
	})
	get, _ := run("board_get", fmt.Sprintf(`{"ref":%q}`, inside.ID))
	if !strings.Contains(get.Text, "\nPath: #2 › #3\n") || !strings.Contains(get.Text, "Blocked by:\n- #6 subtask \"Sibling\"") || strings.Contains(get.Text, "#4") || strings.Contains(get.Text, "#1") {
		t.Fatalf("get inside = %q", get.Text)
	}
	if get, _ := run("board_get", fmt.Sprintf(`{"ref":%q}`, story.ID)); strings.Contains(get.Text, "#4") || strings.Contains(get.Text, "Blocked by") {
		t.Fatalf("get the container = %q", get.Text)
	}
}

// The hand-off and an approved epic as the tools see it (ADR 0006 §6.1,
// §6.4): board_create on an epic asks for the approval; under the approved
// epic a new card waits for the next one, board_get names the approval and
// a pause on the path, nothing is claimed, and a move out of the epic or
// out from under a pause goes to the owner.
func TestBoardToolsUnderAnApprovedEpic(t *testing.T) {
	f := newPlanner(t)
	f.setAcceptCmd("go test ./...")
	task := f.newTask(f.project).ID
	epic := f.toolOK(task, "board_create", `{"kind":"epic","title":"Calculator"}`).Card
	story := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"story","parent":%q,"title":"Parse"}`, epic.ID)).Card
	leaf := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Tokens"}`, story.ID)).Card
	sibling := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Tree"}`, story.ID)).Card
	free := f.toolOK(task, "board_create", `{"kind":"epic","title":"Free"}`).Card
	freeStory := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"story","parent":%q,"title":"Loose"}`, free.ID)).Card
	f.store(func(ctx context.Context, st *board.Store) error {
		var items []board.ApproveItem
		for _, id := range []string{epic.ID, story.ID, leaf.ID, sibling.ID} {
			c, err := st.Card(ctx, id)
			if err != nil {
				return err
			}
			items = append(items, board.ApproveItem{ID: c.ID, Revision: c.Revision})
		}
		_, err := st.Approve(ctx, board.Owner(""), epic.ID, board.RunSettings{Provider: "fake", Model: "luna", Mode: "safe", Parallel: 2}, items)
		return err
	})

	r := f.toolOK(task, "board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Errors"}`, story.ID))
	if want := fmt.Sprintf("Created #%d under #%d. #%d is approved, so it stays a proposal until the owner approves #%d again.", r.Card.Seq, story.Seq, epic.Seq, epic.Seq); r.Text != want {
		t.Fatalf("create under the approved epic = %q, want %q", r.Text, want)
	}
	f.call(http.MethodPatch, "/api/board/cards/"+story.ID, `{"paused":true}`, http.StatusOK, nil)
	got := f.toolOK(task, "board_get", fmt.Sprintf(`{"ref":%q}`, leaf.ID)).Text
	for _, part := range []string{
		fmt.Sprintf("#%d is an approved epic: nothing under it is claimed or started by hand", epic.Seq),
		fmt.Sprintf("Paused: #%d by the owner. Nothing at or under a paused card starts.", story.Seq),
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("board_get = %q, lacks %q", got, part)
		}
	}
	f.toolRefused(task, "board_claim", fmt.Sprintf(`{"ref":%q}`, leaf.ID), string(board.CodeRunOwned))
	r = f.toolOK(task, "board_edit", fmt.Sprintf(`{"ref":%q,"parent":%q}`, sibling.ID, freeStory.ID))
	if want := fmt.Sprintf("Moving #%d into or out of an approved epic changes what it runs under, so the move was filed as a change request for the owner to decide. It replaces your earlier pending one.", sibling.Seq); r.Text != want {
		t.Fatalf("move out of the approved epic = %q, want %q", r.Text, want)
	}
	if c := f.card(sibling.ID).Card; *c.ParentID != story.ID {
		t.Fatalf("the move applied: %+v", c)
	}
	r = f.toolOK(task, "board_edit", fmt.Sprintf(`{"ref":%q,"parent":%q}`, leaf.ID, epic.ID))
	if want := fmt.Sprintf("Moving #%d out from under a pause would let it start, so the move was filed as a change request for the owner to decide. It replaces your earlier pending one.", leaf.Seq); r.Text != want {
		t.Fatalf("move out from under the pause = %q, want %q", r.Text, want)
	}
	if c := f.card(leaf.ID).Card; *c.ParentID != story.ID {
		t.Fatalf("the move applied: %+v", c)
	}
}
