package board

import "slices"

// Role separates the owner from agents.
type Role string

// The roles. The zero Role is neither and is refused.
const (
	RoleOwner Role = "owner"
	RoleAgent Role = "agent"
)

// Actor is who makes a write. An agent is identified by its Task, plus the
// subagent's ID when a subagent made the call; caps count against the Task.
type Actor struct {
	Role    Role
	TaskID  string
	AgentID string
	// Head is the Project's HEAD at the time of an owner write. Every owner
	// touch pins the touched card to it.
	Head string
}

// Owner is the owner actor. head is the Project's HEAD, "" when unknown.
func Owner(head string) Actor { return Actor{Role: RoleOwner, Head: head} }

// Agent is the actor for a Task's agent, or one of its subagents.
func Agent(taskID, agentID string) Actor {
	return Actor{Role: RoleAgent, TaskID: taskID, AgentID: agentID}
}

func (a Actor) owner() bool { return a.Role == RoleOwner }

// author is the comment author the actor writes as.
func (a Actor) author() string {
	if a.owner() {
		return AuthorOwner
	}
	return TaskAuthor(a.TaskID)
}

// op is one kind of write, as the actor table sees it.
type op string

const (
	opCreate      op = "create"       // a story or subtask under a container
	opCreateTop   op = "create_top"   // an epic, or any card at the root
	opEdit        op = "edit"         // fields of an unconfirmed card
	opChange      op = "change"       // fields of a confirmed card
	opOwnerFields op = "owner_fields" // accept_cmd, paths, project, blocked flag
	opChecklist   op = "checklist"    // tick, untick or add items
	opComment     op = "comment"
	opLink        op = "link"
	opUnlink      op = "unlink"
	opClaim       op = "claim"   // planned|todo → doing, held by the agent's Task
	opRequest     op = "request" // file done, cancel, blocked or split
	opSplit       op = "split"   // a subtask becomes a story
	opLaunch      op = "launch"  // planned|todo → doing, held by a new Task
	opPlan        op = "plan"    // scope a planning Task to a container
	opRelease     op = "release" // doing → todo
	opConfirm     op = "confirm"
	opDismiss     op = "dismiss"
	opDone        op = "done" // → done
	opCancel      op = "cancel"
	opReady       op = "ready" // planned|done → todo
	opRestore     op = "restore"
	opPurge       op = "purge"
	opDecide      op = "decide" // accept or reject a request
	opSettings    op = "settings"
)

// actorTable is ADR 0005 §3: which role may perform each write. Scope, caps
// and the confirmed/unconfirmed split are checked by the write itself. An
// agent's change to a confirmed card is filed as a change request, so
// opChange is allowed for agents only as a request.
var actorTable = map[op]struct{ owner, agent bool }{
	opCreate:      {owner: true, agent: true},
	opCreateTop:   {owner: true},
	opEdit:        {owner: true, agent: true},
	opChange:      {owner: true, agent: true},
	opOwnerFields: {owner: true},
	opChecklist:   {owner: true, agent: true},
	opComment:     {owner: true, agent: true},
	opLink:        {owner: true, agent: true},
	opUnlink:      {owner: true},
	opClaim:       {agent: true},
	opRequest:     {agent: true},
	opSplit:       {owner: true, agent: true},
	opLaunch:      {owner: true},
	opPlan:        {owner: true},
	opRelease:     {owner: true},
	opConfirm:     {owner: true},
	opDismiss:     {owner: true},
	opDone:        {owner: true},
	opCancel:      {owner: true},
	opReady:       {owner: true},
	opRestore:     {owner: true},
	opPurge:       {owner: true},
	opDecide:      {owner: true},
	opSettings:    {owner: true},
}

// leafMoves lists, for each status-changing write, the subtask statuses it
// may start from.
var leafMoves = map[op][]Status{
	opLaunch:  {StatusPlanned, StatusTodo},
	opClaim:   {StatusPlanned, StatusTodo},
	opRelease: {StatusDoing},
	opReady:   {StatusPlanned, StatusDone},
	opDone:    {StatusPlanned, StatusTodo, StatusDoing},
	opCancel:  {StatusPlanned, StatusTodo, StatusDoing},
	opRestore: {StatusCancelled},
	opSplit:   {StatusPlanned, StatusTodo, StatusDoing},
	opRequest: {StatusPlanned, StatusTodo, StatusDoing},
}

// permit is the transition function: it refuses o for a unless the actor
// table allows it and, for a status-changing write on a subtask, from is a
// status the write may start from. Pass "" as from for writes that change no
// subtask status.
func permit(a Actor, o op, from Status) error {
	switch {
	case a.Role == RoleOwner:
	case a.Role == RoleAgent && a.TaskID != "":
	default:
		return refuse(CodeForbidden, "unknown actor")
	}
	rule, ok := actorTable[o]
	if !ok || (a.owner() && !rule.owner) || (!a.owner() && !rule.agent) {
		return refuse(CodeForbidden, "%s may not %s", a.Role, o)
	}
	if from == "" {
		return nil
	}
	if starts, ok := leafMoves[o]; ok && !slices.Contains(starts, from) {
		return invalid("cannot %s a %s subtask", o, from)
	}
	return nil
}
