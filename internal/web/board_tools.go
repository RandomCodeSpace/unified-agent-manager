package web

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// The planner's agent tools (ADR 0005 §16) are in-process host tools through
// which a Task reads its Project's Board and proposes changes to it. Every
// call acts as the Task's agent, so the store's actor table, scope and caps
// decide what it may do; no tool takes an owner-only field.

// maxToolCards bounds the cards one board_list call shows.
const maxToolCards = 100

// errTaskEnded ends a planner call whose Task was settled or archived while
// it ran; a done claim's acceptance run is killed and nothing is filed.
var errTaskEnded = errors.New("the task was settled or archived, so this planner call was discarded")

// boardScope is where one planner tool call reads and writes: as actor, on
// the Board of project, whose directory is dir. A non-empty container
// narrows refs further to that card's subtree, as a Utility job's tools
// need (ADR 0005 §18).
type boardScope struct {
	actor     board.Actor
	project   string
	dir       string
	container string
}

// card resolves ref, a card UUID or #seq, inside the scope. A card of
// another Project, of Unassigned, or outside the container is not found,
// with the store's own refusal.
func (sc boardScope) card(ctx context.Context, st *board.Store, ref string) (board.Card, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return board.Card{}, invalidBoard("a card ref is required: #12 or a card id")
	}
	c, err := st.Card(ctx, ref)
	if err != nil {
		return board.Card{}, err
	}
	if in, err := sc.contains(ctx, st, c); err != nil || !in {
		return board.Card{}, cmp.Or[error](err, &board.Error{Code: board.CodeNotFound, Message: fmt.Sprintf("card %s not found", ref)})
	}
	return c, nil
}

// contains reports whether c is inside the scope: on its Project's Board,
// and in the container's subtree when there is one.
func (sc boardScope) contains(ctx context.Context, st *board.Store, c board.Card) (bool, error) {
	if c.ProjectID != sc.project {
		return false, nil
	}
	for p := c; sc.container != "" && p.ID != sc.container; {
		if p.ParentID == "" {
			return false, nil
		}
		var err error
		if p, err = st.Card(ctx, p.ParentID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// toolReply is a planner tool's result, sent to the model as compact JSON:
// Text says what happened, Code and Refs classify a refusal, and Card is the
// card the call was about, which the transcript shows as a chip. The
// adapter records only the text, so the chip is rebuilt from it on reload.
type toolReply struct {
	Text string    `json:"text"`
	Code string    `json:"code,omitempty"`
	Refs []string  `json:"refs,omitempty"`
	Card *toolCard `json:"card,omitempty"`
}

// toolCard is the card of a planner tool's result.
type toolCard struct {
	ID     string       `json:"id"`
	Seq    int64        `json:"seq"`
	Kind   board.Kind   `json:"kind"`
	Title  string       `json:"title"`
	Status board.Status `json:"status"`
}

func cardReply(c board.Card, format string, args ...any) toolReply {
	return toolReply{Text: fmt.Sprintf(format, args...), Card: &toolCard{ID: c.ID, Seq: c.Seq, Kind: c.Kind, Title: c.Title, Status: c.Status}}
}

// maxToolReply bounds a planner tool's encoded result, well under the
// 64 KiB of a tool result the transcript records whole: a result cut there
// is no longer JSON, and its chip is lost.
const maxToolReply = 32 << 10

// toolCutNote ends a result text cut to fit maxToolReply.
const toolCutNote = "\n… the rest was cut to fit the result"

func (r toolReply) result(failed bool) agentapi.HostToolResult {
	encode := func() string {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(r) // plain strings and numbers always encode
		return strings.TrimSuffix(b.String(), "\n")
	}
	out := encode()
	if len(out) > maxToolReply {
		// The longest cut of the text that fits, found on the encoding
		// itself, as escapes make it longer than the text.
		text, lo, hi := r.Text, 0, len(r.Text)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if r.Text = clipText(text, mid) + toolCutNote; len(encode()) <= maxToolReply {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		r.Text = clipText(text, lo) + toolCutNote
		out = encode()
	}
	return agentapi.HostToolResult{Text: out, Failed: failed}
}

// clipText cuts s to at most n bytes at a rune boundary.
func clipText(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// clipNote is s cut to at most n bytes, saying how many more it has.
func clipNote(s string, n int) string {
	cut := clipText(s, n)
	if len(cut) == len(s) {
		return s
	}
	return fmt.Sprintf("%s… %d more bytes", cut, len(s)-len(cut))
}

// toolFailure is a refused or failed call: the message, with the refusal's
// code and refs, such as a guard's open checklist items.
func toolFailure(err error) agentapi.HostToolResult {
	reply := toolReply{Text: err.Error()}
	var apiErr *Error
	var refusal *board.Error
	switch {
	case errors.As(err, &apiErr):
		reply.Code, reply.Refs = apiErr.Code, apiErr.Refs
	case errors.As(err, &refusal):
		reply.Code, reply.Refs = string(refusal.Code), refusal.Refs
	}
	return reply.result(true)
}

// boardTool is one planner tool: what the model is told, and what a call
// runs.
type boardTool struct {
	agentapi.HostTool
	run func(ctx context.Context, m *Manager, sc boardScope, args json.RawMessage) (toolReply, error)
}

// defineTool is a planner tool whose arguments decode strictly into an A.
func defineTool[A any](name, desc string, params map[string]any, run func(*Manager, context.Context, boardScope, A) (toolReply, error)) boardTool {
	return boardTool{
		HostTool: agentapi.HostTool{Name: name, Description: desc, Parameters: params},
		run: func(ctx context.Context, m *Manager, sc boardScope, raw json.RawMessage) (toolReply, error) {
			var args A
			if err := decodeToolArgs(raw, &args); err != nil {
				return toolReply{}, err
			}
			return run(m, ctx, sc, args)
		},
	}
}

// decodeToolArgs decodes a call's JSON object into v, refusing unknown
// arguments as the schemas do.
func decodeToolArgs(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalidBoard("invalid arguments: %s", strings.TrimPrefix(err.Error(), "json: "))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return invalidBoard("invalid arguments: one JSON object is expected")
	}
	return nil
}

// toolSchema is a strict JSON Schema object of the given properties.
func toolSchema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": nonNil(required), "additionalProperties": false}
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func intProp(desc string, lo, hi int) map[string]any {
	p := map[string]any{"type": "integer", "minimum": lo, "description": desc}
	if hi > lo {
		p["maximum"] = hi
	}
	return p
}

func listProp(desc string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items, "description": desc}
}

const refDesc = "A card of this project's board: its number, such as #12, or its id."

var (
	refProp    = stringProp(refDesc)
	prioProp   = intProp("Priority: 1 high, 2 medium, 3 low.", board.PrioHigh, board.PrioLow)
	effortProp = enumProp("Effort estimate.", "S", "M", "L")
	labelsProp = listProp("Labels, without whitespace or a leading #.", map[string]any{"type": "string"})
)

// boardToolSet is every planner tool, in the order they are registered.
var boardToolSet = []boardTool{
	defineTool("board_get", "Read one card of the planner board: its fields, checklist with item indexes, blockers and recent comments. "+
		"For an epic or a story, also its pending subtasks in the order to work on them, blocked ones last. "+
		"It also says when the card's epic is approved and when the card or a card above it is paused.",
		toolSchema([]string{"ref"}, map[string]any{"ref": refProp}), (*Manager).toolGet),
	defineTool("board_list", "List cards of the planner board, in outline order. Every filter is optional.",
		toolSchema(nil, map[string]any{
			"query":  stringProp("Words that must all appear in the title, description or labels; the last may be a prefix."),
			"state":  enumProp("Only cards in this state.", string(board.StatusPlanned), string(board.StatusTodo), string(board.StatusDoing), string(board.StatusDone), string(board.StatusCancelled)),
			"kind":   enumProp("Only cards of this kind.", string(board.KindEpic), string(board.KindStory), string(board.KindSubtask)),
			"parent": stringProp("Only the direct children of this card. " + refDesc),
		}), (*Manager).toolList),
	defineTool("board_create", "Propose a card: an epic at the root of the board, with no parent; a story under an epic; or a subtask under a story or an epic. "+
		"A task not started from a card may propose epics and build them out: stories, subtasks and links under the epics it proposed, until work on a card starts. "+
		"A task started from a card creates only within its scope and under cards it created, and proposes no epics. "+
		"The card stays a proposal until the owner confirms it; under an approved epic, until the owner approves the epic again. "+
		"When the plan of an epic is complete, ask the owner to approve it in the Planner and end your turn: nothing runs before that.",
		toolSchema([]string{"kind", "title"}, map[string]any{
			"kind":          enumProp("An epic holds stories and subtasks and needs no parent; a story holds subtasks; a subtask is one piece of work.", string(board.KindEpic), string(board.KindStory), string(board.KindSubtask)),
			"parent":        stringProp("For a story or subtask: the epic or story to create it under. Leave it out for an epic. " + refDesc),
			"title":         stringProp("One line."),
			"desc":          stringProp("Markdown description."),
			"win_condition": stringProp("One line saying what done means."),
			"prio":          prioProp,
			"effort":        effortProp,
			"labels":        labelsProp,
			"checklist":     listProp("Checklist items, unticked.", map[string]any{"type": "string"}),
		}), (*Manager).toolCreate),
	defineTool("board_edit", "Change a card's fields or move it. A card that has not started changes at once, confirmed or not. "+
		"A subtask in progress or done keeps its plan: the edit is filed as a change request, which the owner can apply once it is released. "+
		"Moving a confirmed card under a proposal, or moving any card into or out of an approved epic, is also filed as a change request. "+
		"Work that has started keeps what it waits on: a move that would make it wait on an open card, such as a confirmed subtask into a done story that started work waits on, is refused.",
		toolSchema([]string{"ref"}, map[string]any{
			"ref":           refProp,
			"title":         stringProp("One line."),
			"desc":          stringProp("Markdown description."),
			"win_condition": stringProp("One line saying what done means."),
			"prio":          prioProp,
			"effort":        effortProp,
			"labels":        labelsProp,
			"parent":        stringProp("Move the card under this epic or story. " + refDesc),
			"rank":          intProp("Place the card at this zero-based position among its siblings.", 0, 0),
		}), (*Manager).toolEdit),
	defineTool("board_checklist", "Tick, untick or add checklist items of a card. Indexes are the zero-based ones board_get shows. "+
		"On a subtask in progress, only the task holding it ticks and unticks, and nothing is added.",
		toolSchema([]string{"ref"}, map[string]any{
			"ref":    refProp,
			"tick":   listProp("Indexes of items to tick.", map[string]any{"type": "integer", "minimum": 0}),
			"untick": listProp("Indexes of items to untick.", map[string]any{"type": "integer", "minimum": 0}),
			"add":    listProp("Texts of items to add, unticked.", map[string]any{"type": "string"}),
		}), (*Manager).toolChecklist),
	defineTool("board_comment", "Add a comment to a card.",
		toolSchema([]string{"ref", "body"}, map[string]any{"ref": refProp, "body": stringProp("The comment, in Markdown.")}), (*Manager).toolComment),
	defineTool("board_link", "Record that another card blocks a card: it can't be finished, nor can anything under it, until the blocker is done or cancelled. "+
		"Links map dependencies one level at a time: epics with epics, stories with stories of the same epic, subtasks with subtasks of the same story. "+
		"Either card may be a proposal, so you can map dependencies while you plan. Neither may be in progress or done, and the blocked card may not be a story or epic with work in progress or done under it.",
		toolSchema([]string{"ref", "blocker"}, map[string]any{"ref": stringProp("The card that is blocked. " + refDesc), "blocker": stringProp("The card that blocks it. " + refDesc)}),
		(*Manager).toolLink),
	defineTool("board_unlink", "Remove the link between two cards, whichever way it points. Neither may be in progress or done, and the blocked card may not be a story or epic with work in progress or done under it.",
		toolSchema([]string{"ref", "blocker"}, map[string]any{"ref": stringProp("The card that is blocked. " + refDesc), "blocker": stringProp("The card that blocks it. " + refDesc)}),
		(*Manager).toolUnlink),
	defineTool("board_delete", "Delete a card in your scope with everything under it, confirmed or not: it is cancelled with the comment \"deleted\", and the owner can restore it. "+
		"Refused while anything in it is in progress or done, while started work waits on it, and when it would leave the story or epic above it done or cancelled. "+
		"Deleting a blocker releases what waits on it, so link the replacement first. "+
		fmt.Sprintf("A deleted card frees its place among the %d live cards you created, but still counts toward the %d you may create in all.", board.CapCreated, board.CapCreatedTotal),
		toolSchema([]string{"ref"}, map[string]any{"ref": refProp}), (*Manager).toolDelete),
	defineTool("board_claim", "Start work on a planned or todo subtask in your scope that the owner confirmed: a proposal, or a subtask under one, waits for the owner. You may hold one subtask at a time without a pending request on it. "+
		"Nothing under an approved epic is claimed.",
		toolSchema([]string{"ref"}, map[string]any{"ref": refProp}), (*Manager).toolClaim),
	defineTool("board_split", "Split a subtask into new subtasks: the given children, then its checklist items. A subtask that has not started splits at once; "+
		"one in progress keeps its plan, and the split is filed as a request, which the owner can accept once it is released. "+
		"Under a story the new subtasks become its siblings and take over its links, both ways, except to a subtask already in progress or done. "+
		"Your new subtasks are proposals, so the split is refused when it would leave the story or epic above it done or cancelled: edit the subtask into the first part and create the others instead.",
		toolSchema([]string{"ref"}, map[string]any{
			"ref": refProp,
			"children": listProp("The new subtasks.", toolSchema([]string{"title"}, map[string]any{
				"title":         stringProp("One line."),
				"win_condition": stringProp("One line saying what done means."),
			})),
		}), (*Manager).toolSplit),
	defineTool("board_request", "File a request on a card. done: you finished the subtask you hold, and evidence of the change is attached. "+
		"When the owner set an acceptance command, it runs and must pass. If the command passes and nothing holds it back, the subtask is done at once; otherwise the request waits for the owner to accept, and the result says why. "+
		"cancel: the card should not be done. blocked: you can't go on, because of the blocker card or what the comment says. The owner decides cancel and blocked.",
		toolSchema([]string{"ref", "kind", "comment"}, map[string]any{
			"ref":                 refProp,
			"kind":                enumProp("What to ask for.", string(board.RequestDone), string(board.RequestCancel), string(board.RequestBlocked)),
			"comment":             stringProp("What you did, or why."),
			"blocker":             stringProp("For blocked only: the card that blocks it. " + refDesc),
			"proposed_accept_cmd": stringProp("For done only: a shell command you suggest as the subtask's acceptance check. It is text for the owner and never runs unless the owner applies it."),
		}), (*Manager).toolRequest),
}

// isBoardTool reports whether name is one of the planner tools.
func isBoardTool(name string) bool {
	return slices.ContainsFunc(boardToolSet, func(t boardTool) bool { return t.Name == name })
}

// boardHostTools returns the named planner tools, every one when names is
// empty, and the CallTool that runs their calls, each in the scope scope
// resolves for it.
func (m *Manager) boardHostTools(scope func(context.Context, agentapi.HostToolCall) (boardScope, error), names ...string) ([]agentapi.HostTool, func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult) {
	var tools []agentapi.HostTool
	byName := map[string]boardTool{}
	for _, t := range boardToolSet {
		if len(names) == 0 || slices.Contains(names, t.Name) {
			tools = append(tools, t.HostTool)
			byName[t.Name] = t
		}
	}
	return tools, func(ctx context.Context, call agentapi.HostToolCall) agentapi.HostToolResult {
		t, ok := byName[call.Name]
		switch {
		case !ok:
			return toolFailure(invalidBoard("there is no planner tool %q", call.Name))
		case len(call.Arguments) > agentapi.MaxHostToolArguments:
			return toolFailure(invalidBoard("the arguments exceed %d bytes", agentapi.MaxHostToolArguments))
		}
		sc, err := scope(ctx, call)
		if err != nil {
			return toolFailure(err)
		}
		reply, err := t.run(ctx, m, sc, call.Arguments)
		if err != nil {
			return toolFailure(err)
		}
		return reply.result(false)
	}
}

// boardToolsLocked reports whether a Task of the Project projectID gets the
// planner tools: the planner is on and the Project has Git. The caller holds
// mu.
func (m *Manager) boardToolsLocked(projectID string) bool {
	p := m.projects[projectID]
	return m.settings.Planner && p != nil && p.NoGit == ""
}

// taskHostToolsLocked is the planner's part of the Manager's hostTools
// (taskToolsLocked): every planner tool, for a Task of a Project with Git
// while the planner is on (boardToolsLocked).
// Both are read when the conversation opens; a Task whose conversation
// opened with another answer reopens on its next prompt (send), and every
// call checks both again. Each call is tied to the Task while it runs
// (startCall). The caller holds mu; the store is used only by the calls.
func (m *Manager) taskHostToolsLocked(taskID, projectID string) ([]agentapi.HostTool, func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult) {
	if !m.boardToolsLocked(projectID) {
		return nil, nil
	}
	tools, run := m.boardHostTools(func(ctx context.Context, call agentapi.HostToolCall) (boardScope, error) {
		return m.taskScope(ctx, taskID, call)
	})
	return tools, func(ctx context.Context, call agentapi.HostToolCall) agentapi.HostToolResult {
		ctx, end, err := m.startCall(ctx, taskID)
		if err != nil {
			return toolFailure(err)
		}
		defer end()
		if m.boardCallHook != nil {
			m.boardCallHook(ctx, call.Name)
		}
		res := run(ctx, call)
		// A call cut short by the Task's end says so whatever step failed;
		// a hold it made anyway is released by what ended the Task.
		if errors.Is(context.Cause(ctx), errTaskEnded) {
			return toolFailure(errTaskEnded)
		}
		return res
	}
}

// taskScope is the scope of a call from the Task taskID, which startCall
// found active: its agent, or the subagent that made the call, on its
// Project's Board. It refuses while the planner is off or the Project has no
// Git.
func (m *Manager) taskScope(ctx context.Context, taskID string, call agentapi.HostToolCall) (boardScope, error) {
	if call.TaskID != taskID {
		return boardScope{}, invalidBoard("the call does not belong to this task")
	}
	m.mu.Lock()
	s := m.sessions[taskID]
	var project string
	if s != nil {
		project = s.projectID
	}
	m.mu.Unlock()
	if s == nil {
		return boardScope{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if err := m.boardOn(); err != nil {
		return boardScope{}, err
	}
	dir, err := m.boardDir(ctx, project)
	if err != nil {
		return boardScope{}, err
	}
	return boardScope{actor: board.Agent(taskID, call.AgentID), project: project, dir: dir}, nil
}

// cardLine is one card in a tool's text, with what an agent needs to know
// about it.
func cardLine(c board.Card, taskID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s %q (%s", c.Seq, c.Kind, c.Title, c.Status)
	if !c.Confirmed() {
		b.WriteString(", proposed")
	}
	switch c.HeldBy {
	case "":
	case taskID:
		b.WriteString(", held by you")
	default:
		b.WriteString(", held by another task")
	}
	if c.Blocked {
		b.WriteString(", flagged blocked")
	}
	b.WriteString(")")
	return b.String()
}

// What board_get shows of a card, so that its result stays under
// maxToolReply: the newest comments, each body and the description cut, and
// at most so many cards of each list.
const (
	maxToolComments    = 10
	maxToolCommentText = 1 << 10
	maxToolDesc        = 8 << 10
	maxToolLinks       = 20
)

type refArgs struct {
	Ref string `json:"ref"`
}

func (m *Manager) toolGet(ctx context.Context, sc boardScope, in refArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		d, err := st.Detail(ctx, c.ID)
		if err != nil {
			return err
		}
		text, err := detailText(ctx, st, sc, d)
		reply = cardReply(c, "%s", text)
		return err
	})
	return reply, err
}

// detailText is board_get's text for the card of d, as the scope sees it:
// its path starts at the container, and a linked card outside the scope is
// left out.
func detailText(ctx context.Context, st *board.Store, sc boardScope, d board.Detail) (string, error) {
	c, taskID := d.Card, sc.actor.TaskID
	var b strings.Builder
	b.WriteString(cardLine(c, taskID))
	path := []string{fmt.Sprintf("#%d", c.Seq)}
	for p := c; p.ID != sc.container && p.ParentID != ""; {
		var err error
		if p, err = st.Card(ctx, p.ParentID); err != nil {
			return "", err
		}
		path = append([]string{fmt.Sprintf("#%d", p.Seq)}, path...)
	}
	fmt.Fprintf(&b, "\nPath: %s\n", strings.Join(path, " › "))
	// lines writes at most maxToolLinks cards under label.
	lines := func(label string, cards []board.Card, line func(board.Card) string) {
		if len(cards) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n", label)
		for _, l := range cards[:min(len(cards), maxToolLinks)] {
			fmt.Fprintf(&b, "- %s\n", line(l))
		}
		if len(cards) > maxToolLinks {
			fmt.Fprintf(&b, "- … %d more\n", len(cards)-maxToolLinks)
		}
	}
	if c.WinCondition != "" {
		fmt.Fprintf(&b, "Win condition: %s\n", c.WinCondition)
	}
	fmt.Fprintf(&b, "Priority %d, effort %s", c.Prio, c.Effort)
	if c.Due != "" {
		fmt.Fprintf(&b, ", due %s", c.Due)
	}
	if len(c.Labels) > 0 {
		fmt.Fprintf(&b, ", labels %s", strings.Join(c.Labels, ", "))
	}
	b.WriteString("\n")
	if c.ExpiresAt != nil {
		fmt.Fprintf(&b, "A proposal: the owner has not confirmed it, and it expires %s unless confirmed.\n", c.ExpiresAt.Format(time.DateOnly))
	}
	if err := writeRunState(ctx, st, &b, c); err != nil {
		return "", err
	}
	if c.Progress != nil {
		fmt.Fprintf(&b, "Progress: %d of %d confirmed subtasks done, %d proposed.\n", c.Progress.Done, c.Progress.Total, c.Progress.Proposed)
	}
	if desc := strings.TrimSpace(c.Desc); desc != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", clipNote(desc, maxToolDesc))
	}
	if len(c.Checklist) > 0 {
		b.WriteString("\nChecklist (index, state, text):\n")
		for i, item := range c.Checklist {
			mark := " "
			if item.Done {
				mark = "x"
			}
			fmt.Fprintf(&b, "%d [%s] %s\n", i, mark, item.Text)
		}
	}
	line := func(l board.Card) string { return cardLine(l, taskID) }
	for _, links := range []struct {
		label string
		ids   []string
	}{{"Blocked by", c.BlockedBy}, {"Blocks", c.Blocks}} {
		cards, err := st.Cards(ctx, links.ids)
		if err != nil {
			return "", err
		}
		var in []board.Card
		for _, l := range cards {
			ok, err := sc.contains(ctx, st, l)
			if err != nil {
				return "", err
			}
			if ok {
				in = append(in, l)
			}
		}
		lines(links.label, in, line)
	}
	if c.PendingRequests > 0 {
		fmt.Fprintf(&b, "\nPending requests for the owner: %d\n", c.PendingRequests)
	}
	if c.Kind != board.KindSubtask {
		pending, err := st.PendingLeaves(ctx, c.ID)
		if err != nil {
			return "", err
		}
		lines("Pending subtasks, in order", pending, func(l board.Card) string {
			if l.WinCondition == "" {
				return line(l)
			}
			return line(l) + ": " + l.WinCondition
		})
	}
	if n := len(d.Comments); n > 0 {
		fmt.Fprintf(&b, "\nComments, oldest first (%d of %d):\n", min(n, maxToolComments), n)
		for _, cm := range d.Comments[max(0, n-maxToolComments):] {
			author := cm.Author
			switch {
			case author == board.TaskAuthor(taskID):
				author = "you"
			case strings.HasPrefix(author, "task:"):
				author = "another task"
			}
			fmt.Fprintf(&b, "- %s: %s\n", author, clipNote(cm.Body, maxToolCommentText))
		}
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// epicOf returns the epic c sits under, c itself for an epic; nil at the
// root, which has no epic.
func epicOf(ctx context.Context, st *board.Store, c board.Card) (*board.Card, error) {
	for c.ParentID != "" {
		var err error
		if c, err = st.Card(ctx, c.ParentID); err != nil {
			return nil, err
		}
	}
	if c.Kind != board.KindEpic {
		return nil, nil
	}
	return &c, nil
}

// writeRunState writes, for board_get, whether c's epic is approved and
// which cards from the root to c are paused (ADR 0006 §6.4).
func writeRunState(ctx context.Context, st *board.Store, b *strings.Builder, c board.Card) error {
	path := []board.Card{c}
	for p := c; p.ParentID != ""; {
		var err error
		if p, err = st.Card(ctx, p.ParentID); err != nil {
			return err
		}
		path = append([]board.Card{p}, path...)
	}
	if e := path[0]; e.Kind == board.KindEpic && e.Run != nil {
		fmt.Fprintf(b, "#%d is an approved epic: nothing under it is claimed or started by hand, and a card added under it waits for the owner to approve #%d again.\n", e.Seq, e.Seq)
	}
	var paused []string
	for _, p := range path {
		switch p.Paused {
		case board.PausedOwner:
			paused = append(paused, fmt.Sprintf("#%d by the owner", p.Seq))
		case board.PausedUAM:
			paused = append(paused, fmt.Sprintf("#%d by uam, as an attempt ended without landing", p.Seq))
		}
	}
	if len(paused) > 0 {
		fmt.Fprintf(b, "Paused: %s. Nothing at or under a paused card starts.\n", strings.Join(paused, "; "))
	}
	return nil
}

type listArgs struct {
	Query  string       `json:"query"`
	State  board.Status `json:"state"`
	Kind   board.Kind   `json:"kind"`
	Parent string       `json:"parent"`
}

func (m *Manager) toolList(ctx context.Context, sc boardScope, in listArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		f := board.Filter{Query: in.Query, Status: in.State, Kind: in.Kind}
		if strings.TrimSpace(in.Parent) != "" {
			parent, err := sc.card(ctx, st, in.Parent)
			if err != nil {
				return err
			}
			f.Parent = parent.ID
		}
		cards, err := st.List(ctx, sc.project, f)
		if err != nil {
			return err
		}
		snap, err := st.Board(ctx, sc.project)
		if err != nil {
			return err
		}
		byID := make(map[string]board.Card, len(snap.Cards))
		for _, c := range snap.Cards {
			byID[c.ID] = c
		}
		if sc.container != "" {
			cards = slices.DeleteFunc(cards, func(c board.Card) bool {
				id := c.ID
				for id != "" && id != sc.container {
					id = byID[id].ParentID
				}
				return id == ""
			})
		}
		if len(cards) == 0 {
			reply.Text = "No cards match."
			return nil
		}
		var b strings.Builder
		for _, c := range cards[:min(len(cards), maxToolCards)] {
			b.WriteString(cardLine(c, sc.actor.TaskID))
			if parent, ok := byID[c.ParentID]; ok {
				fmt.Fprintf(&b, " under #%d", parent.Seq)
			}
			b.WriteString("\n")
		}
		if len(cards) > maxToolCards {
			fmt.Fprintf(&b, "%d more; narrow the filters to see them.\n", len(cards)-maxToolCards)
		}
		reply.Text = strings.TrimSuffix(b.String(), "\n")
		return nil
	})
	return reply, err
}

type createArgs struct {
	Kind         board.Kind `json:"kind"`
	Parent       string     `json:"parent"`
	Title        string     `json:"title"`
	Desc         string     `json:"desc"`
	WinCondition string     `json:"win_condition"`
	Prio         int        `json:"prio"`
	Effort       string     `json:"effort"`
	Labels       []string   `json:"labels"`
	Checklist    []string   `json:"checklist"`
}

func (m *Manager) toolCreate(ctx context.Context, sc boardScope, in createArgs) (toolReply, error) {
	if strings.TrimSpace(in.Parent) == "" && (in.Kind == board.KindStory || in.Kind == board.KindSubtask) {
		return toolReply{}, invalidBoard("a %s needs a parent: the epic or story to create it under", in.Kind)
	}
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		// No parent is the root, where the store takes only an epic, and only
		// from a Task with no scope.
		var parent board.Card
		if strings.TrimSpace(in.Parent) != "" {
			var err error
			if parent, err = sc.card(ctx, st, in.Parent); err != nil {
				return err
			}
		}
		checklist := make([]board.Check, 0, len(in.Checklist))
		for _, text := range in.Checklist {
			checklist = append(checklist, board.Check{Text: text})
		}
		c, err := st.Create(ctx, sc.actor, board.NewCard{ProjectID: sc.project, Kind: in.Kind, ParentID: parent.ID, Title: in.Title, Desc: in.Desc,
			WinCondition: in.WinCondition, Prio: in.Prio, Effort: in.Effort, Labels: in.Labels, Checklist: checklist})
		if err != nil {
			return err
		}
		where := "at the root of the board"
		if parent.ID != "" {
			where = fmt.Sprintf("under #%d", parent.Seq)
		}
		epic, err := epicOf(ctx, st, c)
		switch {
		case err != nil:
			return err
		case c.Kind == board.KindEpic:
			// The hand-off (ADR 0006 §6.1): the owner approves the plan once.
			reply = cardReply(c, "Created #%d %s. When the plan is complete, ask the owner to approve #%d in the Planner; nothing runs before that.", c.Seq, where, c.Seq)
		case epic != nil && epic.Run != nil:
			reply = cardReply(c, "Created #%d %s. #%d is approved, so it stays a proposal until the owner approves #%d again.", c.Seq, where, epic.Seq, epic.Seq)
		default:
			reply = cardReply(c, "Created #%d %s. It stays a proposal until the owner confirms it.", c.Seq, where)
		}
		return nil
	})
	return reply, err
}

type editArgs struct {
	Ref          string    `json:"ref"`
	Title        *string   `json:"title"`
	Desc         *string   `json:"desc"`
	WinCondition *string   `json:"win_condition"`
	Prio         *int      `json:"prio"`
	Effort       *string   `json:"effort"`
	Labels       *[]string `json:"labels"`
	Parent       *string   `json:"parent"`
	Rank         *int      `json:"rank"`
}

func (m *Manager) toolEdit(ctx context.Context, sc boardScope, in editArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		p := board.Patch{Title: in.Title, Desc: in.Desc, WinCondition: in.WinCondition, Prio: in.Prio, Effort: in.Effort, Labels: in.Labels, Rank: in.Rank}
		if in.Parent != nil {
			parent, err := sc.card(ctx, st, *in.Parent)
			if err != nil {
				return err
			}
			p.ParentID = &parent.ID
		}
		res, err := st.Edit(ctx, sc.actor, c.ID, p)
		if err != nil {
			return err
		}
		switch {
		case res.AcrossRun:
			reply = cardReply(res.Card, "Moving #%d into or out of an approved epic changes what it runs under, so the move was filed as a change request for the owner to decide. It replaces your earlier pending one.", res.Card.Seq)
		case res.OutOfPause:
			reply = cardReply(res.Card, "Moving #%d out from under a pause would let it start, so the move was filed as a change request for the owner to decide. It replaces your earlier pending one.", res.Card.Seq)
		case res.Request != nil && res.Card.Status != board.StatusDoing && res.Card.Status != board.StatusDone:
			reply = cardReply(res.Card, "#%d is confirmed and the move puts it under a proposal, so the edit was filed as a change request for the owner to decide. It replaces your earlier pending one.", res.Card.Seq)
		case res.Request != nil:
			reply = cardReply(res.Card, "#%d is in progress, so its plan is locked: the edit was filed as a change request, which the owner can apply once it is released. It replaces your earlier pending one.", res.Card.Seq)
		default:
			reply = cardReply(res.Card, "Updated #%d.", res.Card.Seq)
		}
		return nil
	})
	return reply, err
}

type checklistArgs struct {
	Ref    string   `json:"ref"`
	Tick   []int    `json:"tick"`
	Untick []int    `json:"untick"`
	Add    []string `json:"add"`
}

func (m *Manager) toolChecklist(ctx context.Context, sc boardScope, in checklistArgs) (toolReply, error) {
	if len(in.Tick)+len(in.Untick)+len(in.Add) == 0 {
		return toolReply{}, invalidBoard("nothing to change: give tick, untick or add")
	}
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		if c, err = st.Checklist(ctx, sc.actor, c.ID, board.ChecklistEdit{Tick: in.Tick, Untick: in.Untick, Add: in.Add}); err != nil {
			return err
		}
		done := 0
		for _, item := range c.Checklist {
			if item.Done {
				done++
			}
		}
		reply = cardReply(c, "Updated the checklist of #%d: %d of %d items done.", c.Seq, done, len(c.Checklist))
		return nil
	})
	return reply, err
}

type commentArgs struct {
	Ref  string `json:"ref"`
	Body string `json:"body"`
}

func (m *Manager) toolComment(ctx context.Context, sc boardScope, in commentArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		if _, err := st.AddComment(ctx, sc.actor, c.ID, in.Body); err != nil {
			return err
		}
		reply = cardReply(c, "Commented on #%d.", c.Seq)
		return nil
	})
	return reply, err
}

type linkArgs struct {
	Ref     string `json:"ref"`
	Blocker string `json:"blocker"`
}

func (m *Manager) toolLink(ctx context.Context, sc boardScope, in linkArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		blocker, err := sc.card(ctx, st, in.Blocker)
		if err != nil {
			return err
		}
		if err := st.Link(ctx, sc.actor, blocker.ID, c.ID); err != nil {
			return err
		}
		reply = cardReply(c, "#%d now blocks #%d, which can't be finished until #%d is done or cancelled.", blocker.Seq, c.Seq, blocker.Seq)
		return nil
	})
	return reply, err
}

func (m *Manager) toolUnlink(ctx context.Context, sc boardScope, in linkArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		blocker, err := sc.card(ctx, st, in.Blocker)
		if err != nil {
			return err
		}
		if err := st.Unlink(ctx, sc.actor, blocker.ID, c.ID); err != nil {
			return err
		}
		reply = cardReply(c, "Removed the link between #%d and #%d.", blocker.Seq, c.Seq)
		return nil
	})
	return reply, err
}

func (m *Manager) toolDelete(ctx context.Context, sc boardScope, in refArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		res, err := st.Delete(ctx, sc.actor, c.ID)
		if err != nil {
			return err
		}
		reply = cardReply(res.Card, "Deleted #%d and everything under it. The owner can restore it.", res.Card.Seq)
		if len(res.Released) > 0 {
			reply.Text += fmt.Sprintf(" It no longer blocks %s.", seqList(res.Released))
		}
		return nil
	})
	return reply, err
}

func (m *Manager) toolClaim(ctx context.Context, sc boardScope, in refArgs) (toolReply, error) {
	var c board.Card
	err := m.withBoard(func(st *board.Store) error {
		var err error
		c, err = sc.card(ctx, st, in.Ref)
		return err
	})
	if err != nil {
		return toolReply{}, err
	}
	// The hold's evidence baseline; git runs before the store is used.
	base, err := baseline(ctx, sc.dir)
	if err != nil {
		return toolReply{}, err
	}
	err = m.withBoard(func(st *board.Store) error {
		c, err = st.Claim(ctx, sc.actor, c.ID, base)
		return err
	})
	if err != nil {
		return toolReply{}, err
	}
	return cardReply(c, "You hold #%d now. Finish it with a done request (board_request); you never mark it done yourself.", c.Seq), nil
}

type splitArgs struct {
	Ref      string             `json:"ref"`
	Children []board.SplitChild `json:"children"`
}

func (m *Manager) toolSplit(ctx context.Context, sc boardScope, in splitArgs) (toolReply, error) {
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		res, err := st.Split(ctx, sc.actor, c.ID, in.Children)
		switch {
		case err != nil:
			return err
		case res.Request != nil:
			reply = cardReply(res.Card, "#%d is in progress, so its plan is locked: the split was filed as a request, which the owner can accept once it is released.", c.Seq)
		case res.Card.Kind == board.KindStory:
			reply = cardReply(res.Card, "Split #%d: it is now a story holding the new subtasks.", c.Seq)
		default:
			reply = cardReply(res.Card, "Split #%d into new subtasks after it; #%d was cancelled, and they took over its links.", c.Seq, c.Seq)
			if len(res.NotLinked) > 0 {
				reply.Text += fmt.Sprintf(" They do not block %s, which already started.", seqList(res.NotLinked))
			}
		}
		return nil
	})
	return reply, err
}

type requestArgs struct {
	Ref               string            `json:"ref"`
	Kind              board.RequestKind `json:"kind"`
	Comment           string            `json:"comment"`
	Blocker           string            `json:"blocker"`
	ProposedAcceptCmd string            `json:"proposed_accept_cmd"`
}

func (m *Manager) toolRequest(ctx context.Context, sc boardScope, in requestArgs) (toolReply, error) {
	if strings.TrimSpace(in.Comment) == "" {
		return toolReply{}, invalidBoard("a request needs a comment")
	}
	if in.Kind == board.RequestDone {
		if strings.TrimSpace(in.Blocker) != "" {
			return toolReply{}, invalidBoard("only a blocked request names a blocker")
		}
		return m.requestDone(ctx, sc, in)
	}
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		c, err := sc.card(ctx, st, in.Ref)
		if err != nil {
			return err
		}
		r := board.RequestInput{Kind: in.Kind, Comment: in.Comment, ProposedAcceptCmd: in.ProposedAcceptCmd}
		if strings.TrimSpace(in.Blocker) != "" {
			blocker, err := sc.card(ctx, st, in.Blocker)
			if err != nil {
				return err
			}
			r.Blocker = blocker.ID
		}
		if _, err := st.FileRequest(ctx, sc.actor, c.ID, r); err != nil {
			return err
		}
		reply = cardReply(c, "Filed a %s request on #%d for the owner to decide.", in.Kind, c.Seq)
		return nil
	})
	return reply, err
}

// requestDone files a done claim (ADR 0005 §6): evaluateClaim gathers the
// evidence and runs the subtask's acceptance command, then the request is
// filed, and accepted at once when the command passed (decision 5). Like
// every call of the Task it ends when the Task is settled or archived
// (startCall): the run is killed and nothing is filed.
func (m *Manager) requestDone(ctx context.Context, sc boardScope, in requestArgs) (toolReply, error) {
	task := sc.actor.TaskID
	var c board.Card
	var since time.Time
	err := m.withBoard(func(st *board.Store) error {
		var err error
		if c, err = sc.card(ctx, st, in.Ref); err != nil {
			return err
		}
		d, err := st.Detail(ctx, c.ID)
		if h, ok := openHold(d.Holds, task); ok {
			since = h.StartedAt
		}
		return err
	})
	var res claimResult
	if err == nil {
		touched, span := m.taskWork(task, since)
		res, err = evaluateClaim(ctx, boardClaims{m}, &m.accept, claimInput{
			Actor: sc.actor, Ref: c.ID, Dir: sc.dir, Comment: in.Comment, ProposedAcceptCmd: in.ProposedAcceptCmd,
			Touched: touched, OtherHolds: m.otherHolds(task), Transcript: span,
		})
	}
	var filed board.FiledRequest
	if err == nil {
		err = m.withBoard(func(st *board.Store) error {
			var err error
			if filed, err = st.FileRequestDetail(ctx, sc.actor, c.ID, res.Request); err != nil || filed.Request.Status != board.RequestAccepted {
				return err
			}
			c, err = st.Card(ctx, c.ID)
			return err
		})
	}
	if err != nil {
		return toolReply{}, err
	}
	if filed.Request.Status == board.RequestAccepted {
		return cardReply(c, "#%d is done; the acceptance command passed. Claim the next pending subtask, if any.", c.Seq), nil
	}
	ev := res.Evidence
	byTask := 0
	for _, f := range ev.Diff.Files {
		if f.ByTask {
			byTask++
		}
	}
	text := fmt.Sprintf("Filed a done request on #%d; it waits for the owner to accept, because %s. Evidence: +%d −%d in %d files (%d touched by this task), %d commits",
		c.Seq, waitReason(filed), ev.Diff.Added, ev.Diff.Deleted, len(ev.Diff.Files), byTask, len(ev.Commits))
	if ev.Accept != nil && ev.Accept.Exit >= 0 {
		text += fmt.Sprintf(", acceptance command exited %d", ev.Accept.Exit)
	}
	text += "."
	if len(res.Request.Flags) > 0 {
		text += " Flags: " + strings.Join(res.Request.Flags, ", ") + "."
	}
	if ev.Transcript != nil && ev.Transcript.Partial {
		text += " The transcript uam holds does not reach back to the hold's start, so the files touched by this task may be incomplete."
	}
	return cardReply(c, "%s", text), nil
}

// flagReasons says why each flag leaves a done request for the owner.
var flagReasons = map[string]string{
	board.FlagAcceptanceCouldNotRun: "the acceptance command could not run",
	board.FlagBaselineMissing:       "the hold's baseline commit is gone",
	board.FlagNoChangeInTree:        "nothing changed since the hold began",
	board.FlagTestsOrBuildChanged:   "the change touches test or build files",
	board.FlagOverlap:               "the change overlaps another Task's hold",
}

// waitReason says why the done request filed waits for the owner, from the
// reason the store gave (ADR 0005 decision 5), with each flag it carries.
func waitReason(filed board.FiledRequest) string {
	var reasons []string
	switch filed.Wait {
	case board.WaitNoCommand:
		reasons = append(reasons, "no acceptance command is set")
	case board.WaitCommandChanged:
		reasons = append(reasons, "the acceptance command changed while it ran")
	case board.WaitClosesWithProposals:
		has := "has"
		if len(filed.Closes) > 1 {
			has = "have"
		}
		reasons = append(reasons, fmt.Sprintf("accepting it would close %s, which still %s proposals %s", seqList(filed.Closes), has, seqList(filed.Proposals)))
	}
	// WaitCouldNotRun and WaitFlagged are said by the flags.
	for _, f := range filed.Request.Flags {
		reasons = append(reasons, flagReasons[f])
	}
	return andList(reasons)
}

// seqList is cards as "#1, #2 and #3".
func seqList(cards []board.Card) string {
	refs := make([]string, len(cards))
	for i, c := range cards {
		refs[i] = fmt.Sprintf("#%d", c.Seq)
	}
	return andList(refs)
}

// andList joins items as "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// openHold returns the open hold of task among holds.
func openHold(holds []board.Hold, task string) (board.Hold, bool) {
	i := slices.IndexFunc(holds, func(h board.Hold) bool { return h.EndedAt == nil && h.TaskID == task })
	if i < 0 {
		return board.Hold{}, false
	}
	return holds[i], true
}

// taskCall is one planner call of a Task in progress: cancel ends it, and
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
		return nil, nil, newError(http.StatusConflict, "the task is %s, so it can no longer use uam tools", stageName(s.stage))
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

// endCalls cancels the planner calls of the Task id in progress, which kills
// a done claim's acceptance run and discards its result, and returns once
// they have all returned. The caller has moved the Task out of the active
// stage, so no new call starts, and what it does next sees every hold the
// calls made.
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

// taskWork returns the files the Task id's edit tools touched since since,
// its subagents' included, and the span of its transcript since then; nil
// when it has no item since then and nothing is missing. Only the retained
// transcript is read; the span is marked partial when that may miss some,
// or a tool call since then was clipped.
func (m *Manager) taskWork(id string, since time.Time) ([]string, *EvidenceTranscript) {
	var tools []agentapi.Item
	var span *EvidenceTranscript
	clipped := false
	m.mu.Lock()
	if s := m.sessions[id]; s != nil {
		// Unread, or trimmed at the front past the hold's start, or read
		// without its subagents' items.
		if s.history != HistoryLoaded || s.subagentsArchived || s.truncated && (len(s.items) == 0 || s.items[0].Time.After(since)) {
			span = &EvidenceTranscript{TaskID: id, Partial: true}
		}
		for _, it := range s.items {
			if it.Time.Before(since) {
				continue
			}
			if it.Tool != nil {
				tool := *it.Tool
				tools = append(tools, agentapi.Item{Tool: &tool})
				// A clipped input may have lost the path it edits.
				clipped = clipped || it.Clipped
			}
			if it.AgentID == "" {
				if span == nil {
					span = &EvidenceTranscript{TaskID: id}
				}
				if span.FromItem == "" {
					span.FromItem = it.ID
				}
				span.ToItem = it.ID
			}
		}
	}
	m.mu.Unlock()
	if clipped {
		span = cmp.Or(span, &EvidenceTranscript{TaskID: id})
		span.Partial = true
	}
	return touchedFiles(tools), span
}

// otherHolds lists the live holds in a card's Project other than the Task
// task's, each with the files its Task touched since its hold started.
func (m *Manager) otherHolds(task string) func(context.Context, board.Card) ([]heldFiles, error) {
	return func(ctx context.Context, card board.Card) ([]heldFiles, error) {
		var out []heldFiles
		var since []time.Time
		err := m.withBoard(func(st *board.Store) error {
			held, err := st.Held(ctx)
			if err != nil {
				return err
			}
			for _, h := range held {
				if h.ProjectID != card.ProjectID || h.HeldBy == task {
					continue
				}
				d, err := st.Detail(ctx, h.ID)
				if err != nil {
					return err
				}
				hold, _ := openHold(d.Holds, h.HeldBy)
				out = append(out, heldFiles{Card: h.Seq, TaskID: h.HeldBy})
				since = append(since, hold.StartedAt)
			}
			return nil
		})
		for i := range out {
			out[i].Files, _ = m.taskWork(out[i].TaskID, since[i])
		}
		return out, err
	}
}

// boardClaims is the planner store as a claim evaluates through it: each
// call holds the store open only for itself, never across the acceptance
// run.
type boardClaims struct{ m *Manager }

func (b boardClaims) CheckFinishable(ctx context.Context, a board.Actor, ref string) (board.Finishable, error) {
	var out board.Finishable
	err := b.m.withBoard(func(st *board.Store) error {
		var err error
		out, err = st.CheckFinishable(ctx, a, ref)
		return err
	})
	return out, err
}

func (b boardClaims) Detail(ctx context.Context, ref string) (board.Detail, error) {
	var out board.Detail
	err := b.m.withBoard(func(st *board.Store) error {
		var err error
		out, err = st.Detail(ctx, ref)
		return err
	})
	return out, err
}
