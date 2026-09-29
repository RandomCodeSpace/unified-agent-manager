package web

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// The planner's agent tools (ADR 0005 §16) are in-process host tools through
// which a Task reads its Project's Board and proposes changes to it. Every
// call acts as the Task's agent, so the store's actor table, scope and caps
// decide what it may do; no tool takes an owner-only field.

// maxToolCards bounds the cards one board_list call shows.
const maxToolCards = 100

// errClaimDiscarded ends a done claim whose Task was archived while the
// claim was evaluated.
var errClaimDiscarded = errors.New("the task was archived, so its done claim was discarded")

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
	notFound := &board.Error{Code: board.CodeNotFound, Message: fmt.Sprintf("card %s not found", ref)}
	c, err := st.Card(ctx, ref)
	if err != nil || c.ProjectID != sc.project {
		return board.Card{}, cmp.Or[error](err, notFound)
	}
	for p := c; sc.container != "" && p.ID != sc.container; {
		if p.ParentID == "" {
			return board.Card{}, notFound
		}
		if p, err = st.Card(ctx, p.ParentID); err != nil {
			return board.Card{}, err
		}
	}
	return c, nil
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

func (r toolReply) result(failed bool) agentapi.HostToolResult {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(r) // plain strings and numbers always encode
	return agentapi.HostToolResult{Text: strings.TrimSuffix(b.String(), "\n"), Failed: failed}
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
	if dec.More() {
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
		"For an epic or a story, also its pending subtasks in the order to work on them, blocked ones last.",
		toolSchema([]string{"ref"}, map[string]any{"ref": refProp}), (*Manager).toolGet),
	defineTool("board_list", "List cards of the planner board, in outline order. Every filter is optional.",
		toolSchema(nil, map[string]any{
			"query":  stringProp("Words that must all appear in the title, description or labels; the last may be a prefix."),
			"state":  enumProp("Only cards in this state.", string(board.StatusPlanned), string(board.StatusTodo), string(board.StatusDoing), string(board.StatusDone), string(board.StatusCancelled)),
			"kind":   enumProp("Only cards of this kind.", string(board.KindEpic), string(board.KindStory), string(board.KindSubtask)),
			"parent": stringProp("Only the direct children of this card. " + refDesc),
		}), (*Manager).toolList),
	defineTool("board_create", "Propose a story or subtask under an epic or story in your scope. It stays a proposal until the owner confirms it.",
		toolSchema([]string{"kind", "parent", "title"}, map[string]any{
			"kind":          enumProp("A story holds subtasks; a subtask is one piece of work.", string(board.KindStory), string(board.KindSubtask)),
			"parent":        stringProp("The epic or story to create it under. " + refDesc),
			"title":         stringProp("One line."),
			"desc":          stringProp("Markdown description."),
			"win_condition": stringProp("One line saying what done means."),
			"prio":          prioProp,
			"effort":        effortProp,
			"labels":        labelsProp,
			"checklist":     listProp("Checklist items, unticked.", map[string]any{"type": "string"}),
		}), (*Manager).toolCreate),
	defineTool("board_edit", "Change a card's fields or move it. A proposal changes at once; on a card the owner confirmed, the edit is filed as a change request for the owner.",
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
	defineTool("board_checklist", "Tick, untick or add checklist items of a card. Indexes are the zero-based ones board_get shows.",
		toolSchema([]string{"ref"}, map[string]any{
			"ref":    refProp,
			"tick":   listProp("Indexes of items to tick.", map[string]any{"type": "integer", "minimum": 0}),
			"untick": listProp("Indexes of items to untick.", map[string]any{"type": "integer", "minimum": 0}),
			"add":    listProp("Texts of items to add, unticked.", map[string]any{"type": "string"}),
		}), (*Manager).toolChecklist),
	defineTool("board_comment", "Add a comment to a card.",
		toolSchema([]string{"ref", "body"}, map[string]any{"ref": refProp, "body": stringProp("The comment, in Markdown.")}), (*Manager).toolComment),
	defineTool("board_link", "Record that another card, which the owner confirmed, blocks a card: it can't be finished until the blocker is done or cancelled.",
		toolSchema([]string{"ref", "blocker"}, map[string]any{"ref": stringProp("The card that is blocked. " + refDesc), "blocker": stringProp("The card that blocks it. " + refDesc)}),
		(*Manager).toolLink),
	defineTool("board_claim", "Start work on a planned or todo subtask in your scope. You may hold one subtask at a time without a pending request on it.",
		toolSchema([]string{"ref"}, map[string]any{"ref": refProp}), (*Manager).toolClaim),
	defineTool("board_split", "Split a subtask into new subtasks: the given children, then its checklist items. A proposal nobody holds splits at once; otherwise the split is filed as a request for the owner.",
		toolSchema([]string{"ref"}, map[string]any{
			"ref": refProp,
			"children": listProp("The new subtasks.", toolSchema([]string{"title"}, map[string]any{
				"title":         stringProp("One line."),
				"win_condition": stringProp("One line saying what done means."),
			})),
		}), (*Manager).toolSplit),
	defineTool("board_request", "Ask the owner to decide on a card. done: you finished the subtask you hold; its acceptance command must pass, and evidence of the change is attached. "+
		"cancel: the card should not be done. blocked: you can't go on, because of the blocker card or what the comment says.",
		toolSchema([]string{"ref", "kind", "comment"}, map[string]any{
			"ref":                 refProp,
			"kind":                enumProp("What to ask for.", string(board.RequestDone), string(board.RequestCancel), string(board.RequestBlocked)),
			"comment":             stringProp("What you did, or why."),
			"blocker":             stringProp("For blocked only: the card that blocks it. " + refDesc),
			"proposed_accept_cmd": stringProp("For done only: a shell command you suggest as the subtask's acceptance check. It is text for the owner and never runs unless the owner applies it."),
		}), (*Manager).toolRequest),
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

// taskHostToolsLocked is the Manager's hostTools: every planner tool, for a
// Task of a Project with Git while the planner is on. The switch is read
// when the conversation opens, so turning it on reaches the Tasks opened
// afterwards, and every call checks both again. The caller holds mu; the
// store is used only by the calls.
func (m *Manager) taskHostToolsLocked(taskID, projectID string) ([]agentapi.HostTool, func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult) {
	if p := m.projects[projectID]; !m.settings.Planner || p == nil || p.NoGit != "" {
		return nil, nil
	}
	return m.boardHostTools(func(ctx context.Context, call agentapi.HostToolCall) (boardScope, error) {
		return m.taskScope(ctx, taskID, call)
	})
}

// taskScope is the scope of a call from the Task taskID: its agent, or the
// subagent that made the call, on its Project's Board. It refuses while the
// planner is off or the Project has no Git.
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

// maxToolComments bounds the comments board_get shows, newest kept.
const maxToolComments = 10

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
		text, err := detailText(ctx, st, sc.actor.TaskID, d)
		reply = cardReply(c, "%s", text)
		return err
	})
	return reply, err
}

// detailText is board_get's text for the card of d, as the Task taskID sees
// it.
func detailText(ctx context.Context, st *board.Store, taskID string, d board.Detail) (string, error) {
	c := d.Card
	var b strings.Builder
	b.WriteString(cardLine(c, taskID))
	path := []string{fmt.Sprintf("#%d", c.Seq)}
	for id := c.ParentID; id != ""; {
		parent, err := st.Card(ctx, id)
		if err != nil {
			return "", err
		}
		path, id = append([]string{fmt.Sprintf("#%d", parent.Seq)}, path...), parent.ParentID
	}
	fmt.Fprintf(&b, "\nPath: %s\n", strings.Join(path, " › "))
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
	if c.Progress != nil {
		fmt.Fprintf(&b, "Progress: %d of %d confirmed subtasks done, %d proposed.\n", c.Progress.Done, c.Progress.Total, c.Progress.Proposed)
	}
	if desc := strings.TrimSpace(c.Desc); desc != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", desc)
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
	for _, links := range []struct {
		label string
		ids   []string
	}{{"Blocked by", c.BlockedBy}, {"Blocks", c.Blocks}} {
		cards, err := st.Cards(ctx, links.ids)
		if err != nil {
			return "", err
		}
		if len(cards) > 0 {
			fmt.Fprintf(&b, "\n%s:\n", links.label)
			for _, l := range cards {
				fmt.Fprintf(&b, "- %s\n", cardLine(l, taskID))
			}
		}
	}
	if c.PendingRequests > 0 {
		fmt.Fprintf(&b, "\nPending requests for the owner: %d\n", c.PendingRequests)
	}
	if c.Kind != board.KindSubtask {
		pending, err := st.PendingLeaves(ctx, c.ID)
		if err != nil {
			return "", err
		}
		if len(pending) > 0 {
			b.WriteString("\nPending subtasks, in order:\n")
			for _, l := range pending {
				fmt.Fprintf(&b, "- %s", cardLine(l, taskID))
				if l.WinCondition != "" {
					fmt.Fprintf(&b, ": %s", l.WinCondition)
				}
				b.WriteString("\n")
			}
		}
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
			fmt.Fprintf(&b, "- %s: %s\n", author, cm.Body)
		}
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
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
	var reply toolReply
	err := m.withBoard(func(st *board.Store) error {
		parent, err := sc.card(ctx, st, in.Parent)
		if err != nil {
			return err
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
		reply = cardReply(c, "Created #%d under #%d. It stays a proposal until the owner confirms it.", c.Seq, parent.Seq)
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
		if res.Request != nil {
			reply = cardReply(res.Card, "#%d is confirmed, so the edit was filed as a change request for the owner to decide. It replaces your earlier pending one.", res.Card.Seq)
		} else {
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
	return cardReply(c, "You hold #%d now. Finish it with a done request (board_request); only the owner closes it.", c.Seq), nil
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
			reply = cardReply(res.Card, "#%d is confirmed or held, so the split was filed as a request for the owner to decide.", c.Seq)
		case res.Card.Kind == board.KindStory:
			reply = cardReply(res.Card, "Split #%d: it is now a story holding the new subtasks.", c.Seq)
		default:
			reply = cardReply(res.Card, "Split #%d into new subtasks after it; #%d was cancelled.", c.Seq, c.Seq)
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
// filed. The claim is bound to the Task: archiving it cancels the run and
// files nothing.
func (m *Manager) requestDone(ctx context.Context, sc boardScope, in requestArgs) (toolReply, error) {
	task := sc.actor.TaskID
	ctx, end := m.startClaim(ctx, task)
	defer end()
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
	if err == nil {
		err = m.withBoard(func(st *board.Store) error {
			_, err := st.FileRequest(ctx, sc.actor, c.ID, res.Request)
			return err
		})
	}
	// A claim cut short by the Task's archive says so, whatever step failed.
	if ctx.Err() != nil {
		return toolReply{}, context.Cause(ctx)
	}
	if err != nil {
		return toolReply{}, err
	}
	ev := res.Evidence
	byTask := 0
	for _, f := range ev.Diff.Files {
		if f.ByTask {
			byTask++
		}
	}
	text := fmt.Sprintf("Filed a done request on #%d for the owner to accept. Evidence: +%d −%d in %d files (%d touched by this task), %d commits",
		c.Seq, ev.Diff.Added, ev.Diff.Deleted, len(ev.Diff.Files), byTask, len(ev.Commits))
	if ev.Accept != nil && ev.Accept.Exit >= 0 {
		text += fmt.Sprintf(", acceptance command exited %d", ev.Accept.Exit)
	}
	text += "."
	if len(res.Request.Flags) > 0 {
		text += " Flags: " + strings.Join(res.Request.Flags, ", ") + "."
	}
	return cardReply(c, "%s", text), nil
}

// openHold returns the open hold of task among holds.
func openHold(holds []board.Hold, task string) (board.Hold, bool) {
	i := slices.IndexFunc(holds, func(h board.Hold) bool { return h.EndedAt == nil && h.TaskID == task })
	if i < 0 {
		return board.Hold{}, false
	}
	return holds[i], true
}

// claimRun is one done claim in progress; cancel ends it.
type claimRun struct{ cancel context.CancelCauseFunc }

// startClaim binds a done claim of the Task id to the Task: the returned
// context ends with errClaimDiscarded when the Task is archived, or already
// has been. end releases it.
func (m *Manager) startClaim(ctx context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	run := &claimRun{cancel: cancel}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s == nil || s.stage == StageArchived {
		cancel(errClaimDiscarded)
		return ctx, func() {}
	}
	if m.claims == nil {
		m.claims = map[string]map[*claimRun]struct{}{}
	}
	if m.claims[id] == nil {
		m.claims[id] = map[*claimRun]struct{}{}
	}
	m.claims[id][run] = struct{}{}
	return ctx, func() {
		m.mu.Lock()
		delete(m.claims[id], run)
		if len(m.claims[id]) == 0 {
			delete(m.claims, id)
		}
		m.mu.Unlock()
		cancel(nil)
	}
}

// endClaims cancels the done claims the Task id is evaluating: their
// acceptance runs are killed and their results discarded. A Task is deleted
// only once archived, so archiving ends them all.
func (m *Manager) endClaims(id string) {
	m.mu.Lock()
	runs := m.claims[id]
	delete(m.claims, id)
	m.mu.Unlock()
	for run := range runs {
		run.cancel(errClaimDiscarded)
	}
}

// taskWork returns the files the Task id's edit tools touched since since,
// its subagents' included, and the span of its transcript since then; nil
// when it has no item since then. Only the retained transcript is read.
func (m *Manager) taskWork(id string, since time.Time) ([]string, *EvidenceTranscript) {
	var tools []agentapi.Item
	var span *EvidenceTranscript
	m.mu.Lock()
	if s := m.sessions[id]; s != nil {
		for _, it := range s.items {
			if it.Time.Before(since) {
				continue
			}
			if it.Tool != nil {
				tool := *it.Tool
				tools = append(tools, agentapi.Item{Tool: &tool})
			}
			if it.AgentID == "" {
				if span == nil {
					span = &EvidenceTranscript{TaskID: id, FromItem: it.ID}
				}
				span.ToItem = it.ID
			}
		}
	}
	m.mu.Unlock()
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
