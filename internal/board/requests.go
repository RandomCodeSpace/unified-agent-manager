package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// RequestKind is what an agent asks the owner to decide.
type RequestKind string

// The request kinds.
const (
	RequestDone    RequestKind = "done"
	RequestCancel  RequestKind = "cancel"
	RequestBlocked RequestKind = "blocked"
	RequestSplit   RequestKind = "split"
	RequestChange  RequestKind = "change"
)

// RequestStatus is where a request stands.
type RequestStatus string

// The request statuses.
const (
	RequestPending   RequestStatus = "pending"
	RequestAccepted  RequestStatus = "accepted"
	RequestRejected  RequestStatus = "rejected"
	RequestWithdrawn RequestStatus = "withdrawn"
)

// The flags a done request may carry. The caller gathers the evidence and
// decides the flags; the store only records them.
const (
	FlagAcceptanceCouldNotRun = "acceptance_could_not_run"
	FlagNoChangeInTree        = "no_change_in_tree"
	FlagTestsOrBuildChanged   = "tests_or_build_changed"
	FlagOverlap               = "overlap"
	// FlagBaselineMissing marks evidence gathered without the hold's
	// baseline commit, which no longer exists.
	FlagBaselineMissing = "baseline_missing"
)

var knownFlags = []string{FlagAcceptanceCouldNotRun, FlagNoChangeInTree, FlagTestsOrBuildChanged, FlagOverlap, FlagBaselineMissing}

// Request is one inbox row. BaseRevision is the card's revision when it was
// filed.
type Request struct {
	ID              string
	CardID          string
	TaskID          string
	AgentID         string
	Kind            RequestKind
	Comment         string
	Payload         json.RawMessage
	Evidence        json.RawMessage
	Flags           []string
	BaseRevision    int64
	Status          RequestStatus
	CreatedAt       time.Time
	DecidedAt       *time.Time
	DecisionComment string
	// DecidedBy is "" while pending and when withdrawn.
	DecidedBy DecidedBy
}

// DecidedBy is who decided a request.
type DecidedBy string

// The deciders: the owner, or uam for a done request accepted automatically.
const (
	DecidedByOwner DecidedBy = AuthorOwner
	DecidedByUAM   DecidedBy = AuthorUAM
)

// AutoAcceptComment is the automatic comment on a subtask whose done request
// was accepted because its acceptance command passed.
const AutoAcceptComment = "Accepted automatically: the acceptance command passed"

// RequestInput is an agent's done, cancel or blocked request. Split
// requests are filed by Split and change requests by Edit.
type RequestInput struct {
	Kind    RequestKind
	Comment string
	// Evidence and Flags belong to done requests.
	Evidence json.RawMessage
	Flags    []string
	// Blocker is a blocked request's optional blocking card ref; accepting
	// the request links it instead of setting the blocked flag.
	Blocker string
	// ProposedAcceptCmd is text only; it never runs until the owner copies
	// it into the subtask.
	ProposedAcceptCmd string
	// PassedCmd is the acceptance command the caller ran for a done request
	// and saw exit 0, "" when none ran or it did not pass. While the subtask
	// still resolves to it and nothing else holds it back, the request is
	// accepted as soon as it is filed (ADR 0005 decision 5).
	PassedCmd string
}

// SplitChild is one subtask a split creates.
type SplitChild struct {
	Title        string `json:"title"`
	WinCondition string `json:"win_condition,omitempty"`
}

// payload is a request's kind-specific content.
type payload struct {
	Patch             *Patch       `json:"patch,omitempty"`
	Children          []SplitChild `json:"children,omitempty"`
	Blocker           string       `json:"blocker,omitempty"`
	ProposedAcceptCmd string       `json:"proposed_accept_cmd,omitempty"`
	// SplitOf marks a done request a split filed for a ticked checklist
	// item; it needs no hold.
	SplitOf string `json:"split_of,omitempty"`
	Tick    string `json:"tick,omitempty"`
}

// SplitResult is a split's outcome: the card as the split left it (a story,
// or cancelled after a split into siblings), and the split request filed
// instead when it did not apply. NotLinked are the started dependents of a
// subtask split into siblings, which the siblings were not linked to.
type SplitResult struct {
	Card      Card
	Request   *Request
	NotLinked []Card
}

// Finishable is a subtask that passed the finishing guard, with the
// acceptance command it resolves to ("" for none).
type Finishable struct {
	Card      Card
	AcceptCmd string
}

// CheckFinishable runs the finishing guard on the subtask ref for a, so the
// caller can refuse a claim before running acceptance. For an agent the
// subtask must also be doing and held by the agent's Task. FileRequest runs
// the same checks again inside the filing transaction.
func (s *Store) CheckFinishable(ctx context.Context, a Actor, ref string) (Finishable, error) {
	var out Finishable
	err := s.read(ctx, func(t *txn) error {
		o, n, err := t.find(ref)
		if err != nil {
			return err
		}
		if err := t.finishable(o, a, n); err != nil {
			return err
		}
		out.Card = n.Card
		out.AcceptCmd, err = t.acceptCmd(n)
		return err
	})
	return out, err
}

func (t *txn) finishable(o *outline, a Actor, n *node) error {
	if o.project == "" {
		return errReadOnly
	}
	if n.container() {
		return invalid("%s is a %s; only subtasks finish", n.ref(), n.Kind)
	}
	if !a.owner() && (n.stored != StatusDoing || n.HeldBy != a.TaskID) {
		return refuse(CodeNotHeld, "%s is not held by this Task", n.ref())
	}
	return t.guard(o, n)
}

// acceptCmd resolves n's acceptance command: its own when set, else the
// Project default. A blank command is none; commands are stored trimmed, so
// only one stored before that is blank.
func (t *txn) acceptCmd(n *node) (string, error) {
	if n.AcceptCmd != nil {
		return strings.TrimSpace(*n.AcceptCmd), nil
	}
	settings, err := t.settings(n.ProjectID)
	return strings.TrimSpace(settings.AcceptCmd), err
}

// FileRequest files an agent's done, cancel or blocked request on the card
// ref. A done request needs the subtask doing and held by the agent's Task
// and passes the finishing guard; the caller supplies its evidence and
// flags. A newer request of the same kind from the same Task replaces the
// older pending one. A done request whose PassedCmd is the command the
// subtask resolves to is accepted at once, as the owner's Accept would
// accept it, and is returned accepted, unless something holds it back (see
// WaitReason).
func (s *Store) FileRequest(ctx context.Context, a Actor, ref string, in RequestInput) (Request, error) {
	f, err := s.FileRequestDetail(ctx, a, ref, in)
	return f.Request, err
}

// WaitReason is why a done request was left pending for the owner instead
// of accepted as it was filed (ADR 0005 decision 5).
type WaitReason string

// The wait reasons, in the order they are checked.
const (
	// WaitNone: the request was accepted, or is not a done request.
	WaitNone WaitReason = ""
	// WaitNoCommand: the subtask resolves to no acceptance command.
	WaitNoCommand WaitReason = "no_command"
	// WaitCouldNotRun: the command's shell did not start.
	WaitCouldNotRun WaitReason = "could_not_run"
	// WaitFlagged: the request carries another flag.
	WaitFlagged WaitReason = "flagged"
	// WaitCommandChanged: the command the subtask resolves to is not the one
	// that passed, as when the owner changed it during the run.
	WaitCommandChanged WaitReason = "command_changed"
	// WaitClosesWithProposals: accepting it would close containers that
	// still have proposals.
	WaitClosesWithProposals WaitReason = "closes_with_proposals"
)

// FiledRequest is a filed request, with Wait saying why a done request was
// left pending. For WaitClosesWithProposals, Closes are the containers
// accepting it would close, nearest first, and Proposals their live
// unconfirmed subtasks, which closing would cancel.
type FiledRequest struct {
	Request   Request
	Wait      WaitReason
	Closes    []Card
	Proposals []Card
}

// FileRequestDetail is FileRequest, also saying why a done request was left
// pending.
func (s *Store) FileRequestDetail(ctx context.Context, a Actor, ref string, in RequestInput) (FiledRequest, error) {
	if err := permit(a, opRequest, ""); err != nil {
		return FiledRequest{}, err
	}
	f, err := checkInput(in)
	if err != nil {
		return FiledRequest{}, err
	}
	var out FiledRequest
	_, err = s.agentWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if n.container() {
			if in.Kind != RequestCancel || n.stored == StatusCancelled || n.Status == StatusDone {
				return invalid("%s is a %s; only a cancel request may be filed on it", n.ref(), n.Kind)
			}
		} else if err := permit(a, opRequest, n.stored); err != nil {
			return err
		}
		switch in.Kind {
		case RequestDone:
			if err := t.finishable(o, a, n); err != nil {
				return err
			}
		case RequestBlocked:
			if in.Blocker != "" && n.started() {
				return refuse(CodeInProgress, "%s is in progress, so no link is added to it until it is released: file blocked without a blocker, and name the blocker in the comment", n.ref())
			}
			if in.Blocker != "" {
				blocker, err := t.blocker(o, n, in.Blocker)
				if err != nil {
					return err
				}
				f.payload.Blocker = blocker.ID
			}
		}
		out = FiledRequest{}
		out.Request, err = t.fileRequest(o, a, n, f)
		if err != nil || in.Kind != RequestDone {
			return err
		}
		cmd, err := t.acceptCmd(n)
		if err != nil {
			return err
		}
		if out.Wait = waitReason(f, in.PassedCmd, cmd); out.Wait != WaitNone {
			return nil
		}
		if out.Closes, out.Proposals = o.closesWithProposals(n); len(out.Closes) > 0 {
			out.Wait = WaitClosesWithProposals
			return nil
		}
		return t.acceptPassed(o, a, n, &out.Request)
	})
	return out, err
}

// waitReason is why the done request f, whose caller saw passed exit 0 on a
// subtask that now resolves to cmd, can't be accepted as it is filed, before
// the containers it would close are looked at; WaitNone when nothing there
// holds it back.
func waitReason(f requestFiling, passed, cmd string) WaitReason {
	switch {
	case cmd == "":
		return WaitNoCommand
	case slices.Contains(f.flags, FlagAcceptanceCouldNotRun):
		return WaitCouldNotRun
	case len(f.flags) > 0:
		return WaitFlagged
	case passed != cmd:
		return WaitCommandChanged
	}
	return WaitNone
}

// closesWithProposals returns the containers, nearest first, that marking the
// subtask n done would close while they still have live unconfirmed
// subtasks, and those subtasks, which settle would cancel with them; nil when
// there are none.
func (o *outline) closesWithProposals(n *node) ([]Card, []Card) {
	var closes, proposals []Card
	seen := map[string]bool{}
	for c := o.byID[n.ParentID]; c != nil; c = o.byID[c.ParentID] {
		facts := make([]leafFact, len(c.leaves))
		var cancels []*node
		for i, l := range c.leaves {
			if l == n {
				facts[i] = leafFact{status: StatusDone, confirmed: true}
				continue
			}
			facts[i] = leafFactOf(l)
			if cancelsOnClose(l) {
				cancels = append(cancels, l)
			}
		}
		if status, _ := derive(c.stored == StatusCancelled, facts); status != StatusDone || len(cancels) == 0 {
			continue
		}
		closes = append(closes, c.Card)
		for _, l := range cancels {
			if !seen[l.ID] {
				seen[l.ID] = true
				proposals = append(proposals, l.Card)
			}
		}
	}
	return closes, proposals
}

// acceptPassed accepts the done request r on n just filed, because the
// acceptance command n resolves to passed (ADR 0005 decision 5). It takes the
// owner's Accept path, but records uam as the decider and adds an automatic
// comment saying so; r keeps its evidence. r is read back as accepted.
func (t *txn) acceptPassed(o *outline, a Actor, n *node, r *Request) error {
	if err := t.decide(r, RequestAccepted, DecidedByUAM, ""); err != nil {
		return err
	}
	if err := t.acceptDone(o, a, n, r, false); err != nil {
		return err
	}
	if _, err := t.addComment(n, AuthorUAM, "", AutoAcceptComment, true, false); err != nil {
		return err
	}
	var err error
	*r, err = t.request(r.ID)
	return err
}

// requestFiling is a validated request, ready to write.
type requestFiling struct {
	kind     RequestKind
	comment  string
	payload  payload
	evidence json.RawMessage
	flags    []string
}

func checkInput(in RequestInput) (requestFiling, error) {
	f := requestFiling{kind: in.Kind}
	switch in.Kind {
	case RequestDone, RequestCancel, RequestBlocked:
	default:
		return f, invalid("invalid request kind %q", in.Kind)
	}
	var err error
	if f.comment, err = checkComment(in.Comment); err != nil {
		return f, err
	}
	if in.Kind != RequestDone && (len(in.Evidence) > 0 || len(in.Flags) > 0 || in.ProposedAcceptCmd != "" || in.PassedCmd != "") {
		return f, invalid("only a done request carries evidence, flags or an acceptance command")
	}
	if in.Kind != RequestBlocked && in.Blocker != "" {
		return f, invalid("only a blocked request names a blocker")
	}
	if len(in.Evidence) > 0 {
		if len(in.Evidence) > maxEvidenceBytes || !json.Valid(in.Evidence) {
			return f, invalid("evidence must be JSON of at most %d bytes", maxEvidenceBytes)
		}
		f.evidence = in.Evidence
	}
	for _, flag := range in.Flags {
		if !slices.Contains(knownFlags, flag) {
			return f, invalid("unknown flag %q", flag)
		}
	}
	f.flags = in.Flags
	if len(in.ProposedAcceptCmd) > maxTextBytes {
		return f, invalid("proposed command exceeds %d bytes", maxTextBytes)
	}
	f.payload.ProposedAcceptCmd = in.ProposedAcceptCmd
	return f, nil
}

// fileRequest writes a pending request from a's Task on n, withdrawing the
// Task's older pending request of the same kind on n.
func (t *txn) fileRequest(o *outline, a Actor, n *node, f requestFiling) (Request, error) {
	ids, err := t.ids(`SELECT id FROM requests WHERE card_id = ? AND task_id = ? AND kind = ? AND status = 'pending'`,
		n.ID, a.TaskID, string(f.kind))
	if err != nil {
		return Request{}, err
	}
	for _, id := range ids {
		if err := t.exec(`UPDATE requests SET status = 'withdrawn', decided_at = ? WHERE id = ?`, stamp(t.now), id); err != nil {
			return Request{}, err
		}
		t.requestChanged(o.project, id)
	}
	r := Request{
		ID: t.s.newID(), CardID: n.ID, TaskID: a.TaskID, AgentID: a.AgentID, Kind: f.kind, Comment: f.comment,
		Evidence: f.evidence, Flags: orEmpty(f.flags), BaseRevision: n.Revision, Status: RequestPending, CreatedAt: t.now,
	}
	r.Payload = json.RawMessage(encode(f.payload))
	if err := t.exec(`INSERT INTO requests (id, card_id, task_id, agent_id, kind, comment, payload, evidence, flags,
		base_revision, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
		r.ID, r.CardID, r.TaskID, r.AgentID, string(r.Kind), r.Comment, string(r.Payload), string(r.Evidence), encode(r.Flags),
		r.BaseRevision, stamp(t.now)); err != nil {
		return Request{}, err
	}
	t.requestChanged(o.project, r.ID)
	t.changed(o.project, n.ID)
	return r, nil
}

// Split splits the subtask ref. Under an epic or at the root it becomes a
// story whose children are the given ones, then its checklist items in order.
// Under a story, which can't hold a story, those cards become its siblings,
// placed right after it, and the subtask is cancelled under its own cascade
// with the automatic comment "split into #a, #b, …", so Restore brings it
// back and leaves the siblings. Unticked items become planned subtasks and
// ticked ones subtasks with a pending done request that cites the tick: a
// split never creates a done subtask. A subtask that has not started splits
// at once, for the owner and for an agent in scope; an agent's parts are
// proposals, so its split is refused, as a delete is, when it would bring a
// container above the subtask to done or cancelled. A started one keeps its
// plan (lock.go): the owner's split is refused, and an agent's is filed as
// one split request, which the owner can accept once the subtask is
// released.
func (s *Store) Split(ctx context.Context, a Actor, ref string, children []SplitChild) (SplitResult, error) {
	if err := permit(a, opSplit, ""); err != nil {
		return SplitResult{}, err
	}
	children = slices.Clone(children)
	for i := range children {
		children[i].Title = strings.TrimSpace(children[i].Title)
		children[i].WinCondition = strings.TrimSpace(children[i].WinCondition)
		if err := validateFields(Card{Title: children[i].Title, WinCondition: children[i].WinCondition, Prio: PrioDefault, Effort: DefaultEffort}); err != nil {
			return SplitResult{}, err
		}
	}
	var out SplitResult
	card, err := s.agentWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if n.container() {
			return invalid("%s is already a %s", n.ref(), n.Kind)
		}
		if err := permit(a, opSplit, n.stored); err != nil {
			return err
		}
		if err := checkSplit(o, n, children); err != nil {
			return err
		}
		count := len(children) + len(n.Checklist)
		if n.started() && a.owner() {
			return inProgress(n)
		}
		if n.started() {
			if err := t.caps(o, a, n.ID, count, 0); err != nil {
				return err
			}
			req, err := t.fileRequest(o, a, n, requestFiling{kind: RequestSplit, payload: payload{Children: children}})
			out.Request = &req
			return err
		}
		parent, unconfirmed := n.ID, count
		if o.splitsIntoSiblings(n) {
			// The subtask is cancelled, so it leaves its parent's count.
			parent = n.ParentID
			if n.CreatedBy == a.author() {
				unconfirmed--
			}
		}
		if err := t.caps(o, a, parent, count, unconfirmed); err != nil {
			return err
		}
		above := o.above(n)
		var err error
		if out.NotLinked, err = t.applySplit(o, a, a.TaskID, n, children, false); err != nil || a.owner() {
			return err
		}
		return t.closesNone(o.project, n, above,
			"Splitting %[1]s would make %[2]s, and a split closes no other card: edit %[1]s into the first part and create the others instead, or ask the owner to split it")
	})
	out.Card = card
	return out, err
}

// splitsIntoSiblings reports whether a split of the subtask n makes siblings
// rather than a story: its parent can't hold a story.
func (o *outline) splitsIntoSiblings(n *node) bool {
	parent := o.byID[n.ParentID]
	return parent != nil && !parent.Kind.canHold(KindStory)
}

// checkSplit refuses a split that leaves nothing to split into or repeats a
// title, among its new cards or, for a split into siblings, among the live
// siblings.
func checkSplit(o *outline, n *node, children []SplitChild) error {
	if len(children)+len(n.Checklist) == 0 {
		return invalid("a split needs children or checklist items")
	}
	siblings := o.splitsIntoSiblings(n)
	if !siblings {
		if err := o.unlinkedFor(n, "a split makes it a story"); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, title := range splitTitles(n, children) {
		key := normalTitle(title)
		if seen[key] {
			return refuse(CodeDuplicate, "the split repeats the title %q", title)
		}
		seen[key] = true
		if siblings {
			if err := o.duplicate(n.ParentID, title, n.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func splitTitles(n *node, children []SplitChild) []string {
	var out []string
	for _, c := range children {
		out = append(out, c.Title)
	}
	for _, c := range n.Checklist {
		out = append(out, strings.TrimSpace(c.Text))
	}
	return out
}

// applySplit applies the split of n (see Split). requester is the Task the
// ticked items' done requests are filed for; accept accepts them at once, as
// accepting a split request does. Siblings take n's place in the plan: n's
// links are copied to each of them, both ways, except to a dependent that
// has started, which keeps what it waits on (ADR 0006). It returns those
// dependents.
func (t *txn) applySplit(o *outline, a Actor, requester string, n *node, children []SplitChild, accept bool) ([]Card, error) {
	// A started subtask keeps its plan (lock.go), so n is never held here.
	siblings := o.splitsIntoSiblings(n)
	parent, to, cascade := n.ID, StatusPlanned, ""
	if siblings {
		parent, to, cascade = n.ParentID, StatusCancelled, t.s.newID()
	}
	if err := t.setStatus(n, to, "", cascade); err != nil {
		return nil, err
	}
	checklist := n.Checklist
	// The owner's parts take n's confirmation: a split plans, so it confirms
	// no proposal (decision 10), and a confirmed n has confirmed ancestors.
	confirmed := a.owner() && n.Confirmed()
	switch {
	case !siblings:
		n.Kind, n.Checklist, n.Progress = KindStory, nil, &Progress{}
		if a.owner() {
			if err := t.planned(o, a, n); err != nil {
				return nil, err
			}
		}
		if err := t.updateCard(n); err != nil {
			return nil, err
		}
	case a.owner() && !confirmed:
		if err := t.rearmAncestors(o, parent); err != nil {
			return nil, err
		}
	}
	type planned struct {
		child SplitChild
		tick  bool
	}
	var list []planned
	for _, c := range children {
		list = append(list, planned{child: c})
	}
	for _, c := range checklist {
		list = append(list, planned{child: SplitChild{Title: strings.TrimSpace(c.Text)}, tick: c.Done})
	}
	createdBy := a.author()
	if accept {
		createdBy = TaskAuthor(requester)
	}
	sibs := slices.Clone(o.kids[parent])
	var made []*node
	for _, p := range list {
		kid := &node{stored: StatusPlanned, Card: Card{
			ProjectID: n.ProjectID, Kind: KindSubtask, ParentID: parent, Title: p.child.Title,
			WinCondition: p.child.WinCondition, Status: StatusPlanned, Prio: PrioDefault, Effort: DefaultEffort,
			CreatedBy: createdBy, CreatedAt: t.now, UpdatedAt: t.now, MovedAt: t.now,
		}}
		if confirmed {
			kid.PinnedSHA = a.Head
		} else {
			rearm(t.now, kid)
		}
		if err := t.insertCard(o, kid); err != nil {
			return nil, err
		}
		o.kids[parent] = append(o.kids[parent], kid)
		made = append(made, kid)
		if !p.tick {
			continue
		}
		req, err := t.fileRequest(o, Agent(requester, ""), kid, requestFiling{
			kind: RequestDone, comment: fmt.Sprintf("ticked on %s before the split: %s", n.ref(), p.child.Title),
			payload: payload{SplitOf: n.ID, Tick: p.child.Title},
		})
		if err != nil {
			return nil, err
		}
		if accept {
			if err := t.decide(&req, RequestAccepted, DecidedByOwner, ""); err != nil {
				return nil, err
			}
			if err := t.markDone(o, kid, &req, a); err != nil {
				return nil, err
			}
		}
	}
	if siblings {
		at := slices.Index(sibs, n) + 1
		o.kids[parent] = slices.Concat(sibs[:at], made, sibs[at:])
		if err := t.rerank(o, o.kids[parent], nil); err != nil {
			return nil, err
		}
		refs := make([]string, len(made))
		for i, m := range made {
			refs[i] = m.ref()
		}
		if _, err := t.addComment(n, AuthorUAM, "", "split into "+strings.Join(refs, ", "), true, false); err != nil {
			return nil, err
		}
		return t.copyLinks(o, n, made)
	}
	return nil, nil
}

// copyLinks links the subtasks made from n as n is linked: each waits on
// n's blockers and blocks n's dependents, except a dependent that has
// started, which it returns. The made subtasks are new and at n's level, so
// no level or cycle check is needed.
func (t *txn) copyLinks(o *outline, n *node, made []*node) ([]Card, error) {
	var pairs [][2]string
	var skipped []Card
	for _, id := range n.BlockedBy {
		for _, m := range made {
			pairs = append(pairs, [2]string{id, m.ID})
		}
	}
	for _, id := range n.Blocks {
		if d := o.byID[id]; d != nil && d.started() {
			skipped = append(skipped, d.Card)
			continue
		}
		for _, m := range made {
			pairs = append(pairs, [2]string{m.ID, id})
		}
	}
	for _, p := range pairs {
		if err := t.exec(`INSERT INTO links (blocker_id, blocked_id) VALUES (?, ?) ON CONFLICT(blocker_id, blocked_id) DO NOTHING`, p[0], p[1]); err != nil {
			return nil, err
		}
		t.changed(o.project, p[0])
		t.changed(o.project, p[1])
	}
	return skipped, nil
}

// Accept accepts the pending request id. Accepting a done request needs
// the subtask doing and held by the requesting Task (unless a split filed
// it for a ticked item) and the finishing guard to pass; the claim text
// becomes the close comment and the acceptance is an owner touch. comment
// is the owner's decision note.
func (s *Store) Accept(ctx context.Context, a Actor, id, comment string) (Request, error) {
	if err := permit(a, opDecide, ""); err != nil {
		return Request{}, err
	}
	return s.decideWrite(ctx, a, id, func(t *txn, o *outline, n *node, r *Request, p payload) error {
		if err := t.decide(r, RequestAccepted, DecidedByOwner, strings.TrimSpace(comment)); err != nil {
			return err
		}
		switch r.Kind {
		case RequestDone:
			return t.acceptDone(o, a, n, r, p.SplitOf != "")
		case RequestCancel:
			if n.stored == StatusCancelled || n.Status == StatusDone {
				return invalid("%s is already %s", n.ref(), n.Status)
			}
			return t.cancel(o, n, r.Comment, TaskAuthor(r.TaskID))
		case RequestBlocked:
			if n.stored.terminal() {
				return invalid("%s is already %s", n.ref(), n.stored)
			}
			if p.Blocker != "" {
				blocker, err := t.blocker(o, n, p.Blocker)
				if err != nil {
					return err
				}
				if err := t.link(o, blocker, n); err != nil && CodeOf(err) != CodeDuplicate {
					return err
				}
			} else {
				n.Blocked = true
			}
			// Under an approved epic only its approval confirms a proposal.
			if o.approved(n) == nil {
				if err := t.confirm(o, a, n); err != nil {
					return err
				}
			}
			return t.updateCard(n)
		case RequestSplit:
			if n.container() || n.stored.terminal() {
				return invalid("%s can no longer be split", n.ref())
			}
			if err := inProgress(n); err != nil {
				return err
			}
			if err := checkSplit(o, n, p.Children); err != nil {
				return err
			}
			_, err := t.applySplit(o, a, r.TaskID, n, p.Children, true)
			return err
		}
		if n.stored == StatusCancelled || p.Patch == nil {
			return invalid("%s can no longer change", n.ref())
		}
		if p.Patch.planning(n) {
			if err := inProgress(n); err != nil {
				return err
			}
		}
		plan, err := t.planEdit(o, a, n, *p.Patch)
		if err != nil {
			return err
		}
		return t.applyEdit(o, a, n, *p.Patch, plan)
	})
}

// acceptDone marks the subtask n done by accepting the done request r, which
// needs n doing and held by r's Task, unless a split filed r for a ticked
// item (split), and the finishing guard to pass.
func (t *txn) acceptDone(o *outline, a Actor, n *node, r *Request, split bool) error {
	if n.stored.terminal() {
		return invalid("%s is already %s", n.ref(), n.stored)
	}
	if !split && (n.stored != StatusDoing || n.HeldBy != r.TaskID) {
		return refuse(CodeNotHeld, "%s is not held by the requesting Task", n.ref())
	}
	if err := t.guard(o, n); err != nil {
		return err
	}
	return t.markDone(o, n, r, a)
}

// markDone marks the subtask n done by accepting r: the claim text becomes the
// close comment, the hold ends, and the acceptance confirms n.
func (t *txn) markDone(o *outline, n *node, r *Request, a Actor) error {
	author := AuthorUAM
	if r.TaskID != "" {
		author = TaskAuthor(r.TaskID)
	}
	if _, err := t.addComment(n, author, r.AgentID, r.Comment, author == AuthorUAM, true); err != nil {
		return err
	}
	if n.HeldBy != "" {
		if err := t.releaseHold(n, ReleaseAccepted, StatusDone, ""); err != nil {
			return err
		}
	} else if err := t.setStatus(n, StatusDone, "", ""); err != nil {
		return err
	}
	if err := t.confirm(o, a, n); err != nil {
		return err
	}
	return t.updateCard(n)
}

// Reject rejects the pending request id with a reason. When the requesting
// Task holds the card and is not Active, the hold is released to todo with
// the reason as a comment; while it is Active the hold stays and the caller
// sends the reason to the Task.
func (s *Store) Reject(ctx context.Context, a Actor, id, reason string, holderActive bool) (Request, error) {
	if err := permit(a, opDecide, ""); err != nil {
		return Request{}, err
	}
	body, err := checkComment(reason)
	if err != nil {
		return Request{}, invalid("a rejection needs a reason")
	}
	return s.decideWrite(ctx, a, id, func(t *txn, _ *outline, n *node, r *Request, _ payload) error {
		if err := t.decide(r, RequestRejected, DecidedByOwner, body); err != nil {
			return err
		}
		if holderActive || r.TaskID == "" || n.HeldBy != r.TaskID {
			return nil
		}
		if err := t.releaseHold(n, ReleaseRejected, StatusTodo, ""); err != nil {
			return err
		}
		_, err := t.addComment(n, AuthorOwner, "", body, false, false)
		return err
	})
}

// decideWrite loads the pending request id and its card inside one write by
// a to the card's Project, runs fn, and returns the request afterwards.
func (s *Store) decideWrite(ctx context.Context, a Actor, id string, fn func(*txn, *outline, *node, *Request, payload) error) (Request, error) {
	var out Request
	_, err := s.write(ctx, func(t *txn) error {
		r, err := t.request(id)
		if err != nil {
			return err
		}
		if r.Status != RequestPending {
			return invalid("the request is %s", r.Status)
		}
		var p payload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return fmt.Errorf("board: request %s payload: %w", r.ID, err)
		}
		project, _, err := t.locate(r.CardID)
		if err != nil {
			return err
		}
		if project == "" {
			return errReadOnly
		}
		err = t.mutate(a, project, true, func() error {
			o, n, err := t.cardIn(project, r.CardID)
			if err != nil {
				return err
			}
			return fn(t, o, n, &r, p)
		})
		if err != nil {
			return err
		}
		out, err = t.request(id)
		return err
	})
	return out, err
}

// decide records by's decision on r: status, with the decision comment.
func (t *txn) decide(r *Request, status RequestStatus, by DecidedBy, comment string) error {
	if err := t.exec(`UPDATE requests SET status = ?, decided_at = ?, decision_comment = ?, decided_by = ? WHERE id = ?`,
		string(status), stamp(t.now), comment, string(by), r.ID); err != nil {
		return err
	}
	project, _, err := t.locate(r.CardID)
	if err != nil {
		return err
	}
	r.Status = status
	t.requestChanged(project, r.ID)
	t.changed(project, r.CardID)
	return nil
}

// Request returns the request id.
func (s *Store) Request(ctx context.Context, id string) (Request, error) {
	var out Request
	err := s.read(ctx, func(t *txn) error {
		var err error
		out, err = t.request(id)
		return err
	})
	return out, err
}

const requestCols = `SELECT r.id, r.card_id, r.task_id, r.agent_id, r.kind, r.comment, r.payload, r.evidence, r.flags,
	r.base_revision, r.status, r.created_at, r.decided_at, r.decision_comment, r.decided_by
	FROM requests r JOIN cards c ON c.id = r.card_id `

func (t *txn) request(id string) (Request, error) {
	list, err := t.queryRequests(requestCols+`WHERE r.id = ?`, id)
	if err != nil {
		return Request{}, err
	}
	if len(list) == 0 {
		return Request{}, refuse(CodeNotFound, "request %s not found", id)
	}
	return list[0], nil
}

func (t *txn) queryRequests(query string, args ...any) ([]Request, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("board: query requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Request
	for rows.Next() {
		var r Request
		var kind, status, payloadText, evidence, flags, created, decided, decidedBy string
		if err := rows.Scan(&r.ID, &r.CardID, &r.TaskID, &r.AgentID, &kind, &r.Comment, &payloadText, &evidence, &flags,
			&r.BaseRevision, &status, &created, &decided, &r.DecisionComment, &decidedBy); err != nil {
			return nil, fmt.Errorf("board: scan request: %w", err)
		}
		r.Kind, r.Status, r.Payload, r.DecidedBy = RequestKind(kind), RequestStatus(status), json.RawMessage(payloadText), DecidedBy(decidedBy)
		if evidence != "" {
			r.Evidence = json.RawMessage(evidence)
		}
		if err := json.Unmarshal([]byte(flags), &r.Flags); err != nil {
			return nil, fmt.Errorf("board: request %s flags: %w", r.ID, err)
		}
		if err := parseStamps([]string{created}, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("board: request %s: %w", r.ID, err)
		}
		if decided != "" {
			at, err := parseStamp(decided)
			if err != nil {
				return nil, fmt.Errorf("board: request %s: %w", r.ID, err)
			}
			r.DecidedAt = &at
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("board: query requests: %w", err)
	}
	return out, nil
}

// ProjectSettings holds a Project's planner settings. AcceptCmd is the
// default acceptance command, "" for none.
type ProjectSettings struct {
	ProjectID string
	AcceptCmd string
}

// ProjectSettings returns projectID's settings.
func (s *Store) ProjectSettings(ctx context.Context, projectID string) (ProjectSettings, error) {
	var out ProjectSettings
	err := s.read(ctx, func(t *txn) error {
		var err error
		out, err = t.settings(projectID)
		return err
	})
	return out, err
}

func (t *txn) settings(projectID string) (ProjectSettings, error) {
	out := ProjectSettings{ProjectID: projectID}
	err := t.tx.QueryRowContext(t.ctx, `SELECT accept_cmd FROM project_settings WHERE project_id = ?`, projectID).Scan(&out.AcceptCmd)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("board: read project settings: %w", err)
	}
	return out, nil
}

// SetProjectAcceptCmd sets projectID's default acceptance command, trimmed;
// "" means none. Only the owner writes acceptance commands.
func (s *Store) SetProjectAcceptCmd(ctx context.Context, a Actor, projectID, cmd string) error {
	if err := permit(a, opSettings, ""); err != nil {
		return err
	}
	if projectID == "" {
		return errReadOnly
	}
	// A blank command would run and pass; it is none.
	cmd = strings.TrimSpace(cmd)
	if len(cmd) > maxTextBytes {
		return invalid("acceptance command exceeds %d bytes", maxTextBytes)
	}
	_, err := s.write(ctx, func(t *txn) error {
		// The default is part of the Project's Board, so writing it is a
		// change: the revision moves and a change is reported.
		t.set(projectID)
		if err := t.exec(`INSERT INTO project_settings (project_id, accept_cmd) VALUES (?, ?)
			ON CONFLICT(project_id) DO UPDATE SET accept_cmd = excluded.accept_cmd`, projectID, cmd); err != nil {
			return err
		}
		return t.commandChanged(projectID, "")
	})
	return err
}

// commandChanged reports as changed the pending done requests on cardID, or
// with cardID "" on every card of project inheriting the default, after a
// write to the acceptance command they resolve to. Their evidence is
// unchanged, but readers show an acceptance row stale once its command is no
// longer the card's (ADR 0005 §6), so the board frame carries them again.
func (t *txn) commandChanged(project, cardID string) error {
	query := `SELECT r.id FROM requests r JOIN cards c ON c.id = r.card_id
		WHERE r.status = 'pending' AND r.kind = 'done' AND c.project_id = ? AND c.accept_cmd IS NULL`
	args := []any{project}
	if cardID != "" {
		query = `SELECT id FROM requests WHERE status = 'pending' AND kind = 'done' AND card_id = ?`
		args = []any{cardID}
	}
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("board: query requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("board: query requests: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("board: query requests: %w", err)
	}
	for _, id := range ids {
		t.requestChanged(project, id)
	}
	return nil
}
