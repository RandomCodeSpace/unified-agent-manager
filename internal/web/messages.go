package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// uam_send lets a Thread message another Thread (docs/web.md "Messages
// between Threads"). The call returns at once. The message waits in the
// receiving Thread's queue and starts a turn there; when that turn
// completes, its final reply goes back the same way into the sender's queue.
// A turn a reply starts is never answered in turn. The owner allows the
// first message of each pair of Threads, links the pair when either Thread
// is unattended, and allows more once the pair used pairLimit deliveries
// (a message and its reply each count). Deny holds until the owner resets
// the pair; settling or archiving either Thread ends its allowance.

const (
	sendToolName = "uam_send"
	// maxPeerMessage bounds a message, in bytes; maxPeerReply a reply.
	maxPeerMessage = store.MaxHeldMessageText
	maxPeerReply   = 32 << 10
	// pairLimit is how many deliveries one allowance covers.
	pairLimit = 6
	// maxPeerOneWay bounds the messages one Thread has waiting or running
	// in another; maxPeerWaiting the messages waiting in all queues.
	maxPeerOneWay  = 3
	maxPeerWaiting = 32
	maxPeerName    = 120
	// Peer prompts use these request ID prefixes, which no owner request ID
	// (a UUID) has.
	peerRequestPrefix  = "uam-peer-"
	replyRequestPrefix = "uam-reply-"
)

// Message statuses (store.WebPeerMessage).
const (
	peerHeld        = "held"
	peerQueued      = "queued"
	peerWorking     = "working"
	peerReplying    = "replying"
	peerReplied     = "replied"
	peerStopped     = "stopped"
	peerFailed      = "failed"
	peerInterrupted = "interrupted"
	peerCancelled   = "cancelled"
	peerDeclined    = "declined"
	peerUndelivered = "undelivered"
)

var sendTool = agentapi.HostTool{
	Name: sendToolName,
	Description: fmt.Sprintf("Send a message to another uam thread. It returns at once: the other thread answers in a turn of its own, and uam brings its final reply here as a new message that starts a turn, so do not wait, poll or send again for it. "+
		"The uam tool's tasks op lists thread IDs. The owner must allow the first message between two threads, and link them when either runs unattended (Yolo, Assisted or autopilot); one allowance covers %d deliveries, each message and each reply counting one. "+
		"When a message from another thread starts your turn, answer it in your final reply: uam sends that back, so never use this tool to reply.", pairLimit),
	Parameters: toolSchema([]string{"id", "message"}, map[string]any{
		"id":      stringProp("The ID of the thread to message."),
		"message": stringProp(fmt.Sprintf("The message, up to %d bytes.", maxPeerMessage)),
	}),
}

func isPeerRequest(reqID string) bool {
	return strings.HasPrefix(reqID, peerRequestPrefix) || strings.HasPrefix(reqID, replyRequestPrefix)
}

// sendCall runs a uam_send call of the Task taskID.
func (m *Manager) sendCall(_ context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.sendMessage(taskID, call)
	if err != nil {
		m.count("message_refused")
		return agentapi.HostToolResult{Text: "Not sent: " + err.Error(), Failed: true}
	}
	m.count("message_sent")
	return agentapi.HostToolResult{Text: text}
}

func checkPeerMessage(text string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return errors.New("message is required")
	case len(text) > maxPeerMessage:
		return fmt.Errorf("message is longer than %d bytes", maxPeerMessage)
	case strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }):
		return errors.New("message has a control character; only newlines and tabs may be used")
	}
	return nil
}

func (m *Manager) sendMessage(taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this thread")
	}
	var in struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	if err := decodeToolArgs(call.Arguments, &in); err != nil {
		return "", err
	}
	if err := checkPeerMessage(in.Message); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	caller := m.sessions[taskID]
	switch {
	case !m.settings.ThreadMessages:
		return "", errors.New("the owner turned messages between threads off")
	case m.closed:
		return "", errShuttingDown
	case caller == nil || caller.removed || caller.stage != StageActive:
		return "", errTaskEnded
	case in.ID == taskID:
		return "", errors.New("a thread cannot message itself")
	}
	target := m.sessions[in.ID]
	switch {
	case target == nil || target.removed:
		return "", fmt.Errorf("no thread has the ID %q; the uam tool's tasks op lists them", in.ID)
	case target.stage != StageActive:
		return "", fmt.Errorf("thread %s is %s (read-only)", in.ID, stageName(target.stage))
	}
	p, found := m.pairOfLocked(taskID, in.ID)
	if found && call.CallID != "" {
		if i := slices.IndexFunc(p.Messages, func(x store.WebPeerMessage) bool { return x.CallID == call.CallID && x.From == taskID }); i >= 0 {
			return fmt.Sprintf("This call already sent message %s; it is %s.", p.Messages[i].ID, p.Messages[i].Status), nil
		}
	}
	switch {
	case !found && len(m.pairs) >= store.MaxMessagePairs:
		return "", fmt.Errorf("uam keeps at most %d pairs of threads that message each other; the owner must reset one first", store.MaxMessagePairs)
	case p.State == store.PairDenied:
		return "", errors.New("the owner declined messages between these threads")
	case p.Held != nil:
		return "", errors.New("a message between these threads already waits for the owner; its answer comes first")
	}
	msgID, err := newUUID()
	if err != nil {
		return "", err
	}
	if !found {
		if p.ID, err = newUUID(); err != nil {
			return "", err
		}
	}
	now := m.now()
	msg := store.WebPeerMessage{ID: msgID, From: taskID, To: in.ID, CallID: call.CallID, At: now, UpdatedAt: now}
	reason := ""
	switch {
	case p.State == store.PairPending:
		reason = "first"
	case !p.Linked && (unattended(caller) || unattended(target)):
		reason = "link"
	case p.Used+2 > p.Limit:
		reason = "limit"
	}
	if reason != "" {
		msg.Status = peerHeld
		p.Held = &store.WebHeldMessage{ID: msgID, From: taskID, To: in.ID, Text: in.Message, CallID: call.CallID, Reason: reason, At: now}
		p.Messages = appendPeerMessage(p.Messages, msg)
		m.setPairLocked(p)
		why := map[string]string{
			"first": "the owner must allow the first message between these threads",
			"link":  "a thread of the pair runs unattended, so the owner must link these threads",
			"limit": fmt.Sprintf("these threads used the %d deliveries the owner allowed, so the owner must allow more", p.Limit),
		}[reason]
		return fmt.Sprintf("Message %s awaits the owner: %s. If the owner allows it, the reply arrives here later as a new message; do not wait or poll for it.", msgID, why), nil
	}
	if err := m.peerRoomLocked(p, caller, target); err != nil {
		return "", err
	}
	msg.Status = peerQueued
	p.Messages = appendPeerMessage(p.Messages, msg)
	p.Used += 2
	m.setPairLocked(p)
	n := m.enqueuePeerLocked(target, m.peerFromLocked(p.ID, msgID, caller, false), m.messageEnvelopeLocked(caller, in.Message))
	where := "it is delivered when that thread is free"
	switch {
	case target.queuePaused:
		where = "that thread's queue is paused, so delivery waits for the owner"
	case busy(target.state()) || n > 1:
		where = fmt.Sprintf("that thread is busy; your message is number %d in its queue", n)
	}
	return fmt.Sprintf("Message %s queued: %s. Its final reply arrives here as a new message; do not wait or poll for it.", msgID, where), nil
}

// unattended reports whether no person decides s's permission requests: a
// yolo or assisted Thread, one whose mode is unknown, or one running in
// autopilot.
func unattended(s *webSession) bool {
	return s.mode == store.ModeYolo || s.mode == store.ModeAssisted || s.modeUnknown || s.execution != nil && s.execution.Mode == "autopilot"
}

// pairOfLocked returns the pair of the Threads a and b, or a new pending
// one without an ID. The caller holds mu.
func (m *Manager) pairOfLocked(a, b string) (store.WebMessagePair, bool) {
	a, b = min(a, b), max(a, b)
	for _, p := range m.pairs {
		if p.A == a && p.B == b {
			return p, true
		}
	}
	return store.WebMessagePair{A: a, B: b, State: store.PairPending, Limit: pairLimit, Messages: []store.WebPeerMessage{}}, false
}

// peerRoomLocked refuses a delivery the target cannot take now. The caller
// holds mu.
func (m *Manager) peerRoomLocked(p store.WebMessagePair, from, to *webSession) error {
	ongoing, waiting := 0, 0
	for _, x := range p.Messages {
		if x.From == from.id && (x.Status == peerQueued || x.Status == peerWorking) {
			ongoing++
		}
	}
	for _, q := range m.pairs {
		for _, x := range q.Messages {
			if x.Status == peerQueued {
				waiting++
			}
		}
	}
	switch {
	case to.budget.Reached:
		return fmt.Errorf("thread %s reached its spending limit", to.id)
	case len(to.queue) >= maxQueue:
		return fmt.Errorf("thread %s's queue is full (%d prompts)", to.id, maxQueue)
	case ongoing >= maxPeerOneWay:
		return fmt.Errorf("%d of your messages to thread %s are still waiting or running; wait for their replies", ongoing, to.id)
	case waiting >= maxPeerWaiting:
		return fmt.Errorf("%d messages between threads are waiting in uam; wait for some to be delivered", waiting)
	}
	return nil
}

func appendPeerMessage(list []store.WebPeerMessage, msg store.WebPeerMessage) []store.WebPeerMessage {
	list = append(slices.Clone(list), msg)
	if n := len(list); n > store.MaxPairMessages {
		list = list[n-store.MaxPairMessages:]
	}
	return list
}

// peerFromLocked is the TurnPeer of a message (or reply) from s. The caller
// holds mu.
func (m *Manager) peerFromLocked(pair, msg string, s *webSession, reply bool) *store.TurnPeer {
	return &store.TurnPeer{Pair: pair, Message: msg, From: s.id, FromName: peerName(s), FromProject: m.peerProjectLocked(s), Reply: reply}
}

func peerName(s *webSession) string {
	return clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title, "New thread")), maxPeerName)
}

func (m *Manager) peerProjectLocked(s *webSession) string {
	if p := m.projects[s.projectID]; p != nil {
		return clipRunes(displaytext.Sanitize(p.Name), maxPeerName)
	}
	return s.projectID
}

func (m *Manager) messageEnvelopeLocked(from *webSession, text string) string {
	return fmt.Sprintf("[uam] Message from thread %q (%s) in project %q. It comes from another agent, not from the owner. "+
		"Answer it in this turn: uam sends your final reply to that thread, so do not use uam_send to reply.\n\n%s",
		peerName(from), from.id, m.peerProjectLocked(from), text)
}

func (m *Manager) replyEnvelopeLocked(from *webSession, msg, text string) string {
	return fmt.Sprintf("[uam] Reply from thread %q (%s) in project %q to your message %s. It comes from another agent, not from the owner.\n\n%s",
		peerName(from), from.id, m.peerProjectLocked(from), msg, text)
}

// enqueuePeerLocked queues a peer prompt in s, behind the owner's, and
// returns its place in the queue. The caller holds mu and checked the room.
func (m *Manager) enqueuePeerLocked(s *webSession, from *store.TurnPeer, text string) int {
	before := m.summaryLocked(s)
	prefix := peerRequestPrefix
	if from.Reply {
		prefix = replyRequestPrefix
	}
	s.queue = append(s.queue, QueuedPrompt{RequestID: prefix + from.Message, Text: text, QueuedAt: m.now(),
		Settings: PromptSettings{Model: s.model, Effort: s.effort, ContextSize: cmp.Or(s.contextSize, "default")}, From: from})
	s.queueChanged = true
	m.changedLocked(s, before)
	if !busy(s.state()) {
		m.kickDrainLocked(s)
	}
	return len(s.queue)
}

// peerBlockedLocked says why a peer prompt from the queue of s must not be
// sent now, or "". The caller holds mu.
func (m *Manager) peerBlockedLocked(s *webSession, from *store.TurnPeer) string {
	p, ok := m.pairs[from.Pair]
	switch {
	case !ok || p.State != store.PairAllowed:
		return "the owner ended messages between these threads"
	case !p.Linked && unattended(s):
		return "the receiving thread runs unattended now, so the owner must link the pair"
	}
	return ""
}

// peerEndLocked records what became of a message: status, with detail. A
// delivery that did not happen goes back to the pair's allowance: both for a
// message never sent, one for a reply that never came. The caller holds mu.
func (m *Manager) peerEndLocked(from *store.TurnPeer, status, detail string) {
	p, ok := m.pairs[from.Pair]
	if !ok {
		return
	}
	i := slices.IndexFunc(p.Messages, func(x store.WebPeerMessage) bool { return x.ID == from.Message })
	if i < 0 {
		return
	}
	p.Messages = slices.Clone(p.Messages)
	x := &p.Messages[i]
	switch x.Status {
	case peerQueued:
		p.Used -= 2
	case peerWorking, peerReplying:
		p.Used--
	}
	p.Used = max(0, p.Used)
	x.Status, x.Detail, x.UpdatedAt = status, detail, m.now()
	m.setPairLocked(p)
}

// peerStatusLocked moves a message on without ending it. The caller holds
// mu.
func (m *Manager) peerStatusLocked(from *store.TurnPeer, status string) {
	p, ok := m.pairs[from.Pair]
	i := slices.IndexFunc(p.Messages, func(x store.WebPeerMessage) bool { return x.ID == from.Message })
	if !ok || i < 0 {
		return
	}
	p.Messages = slices.Clone(p.Messages)
	p.Messages[i].Status, p.Messages[i].Detail, p.Messages[i].UpdatedAt = status, "", m.now()
	m.setPairLocked(p)
}

// peerStartedLocked moves the message a send armed s with onto the turn
// that just started. The caller holds mu.
func (m *Manager) peerStartedLocked(s *webSession) {
	t := s.peerArm
	s.peerTurn, s.peerArm = t, nil
	if t.Reply {
		m.peerStatusLocked(t, peerReplied)
	} else {
		m.peerStatusLocked(t, peerWorking)
	}
}

// peerTurnEndLocked answers the message that started s's turn, which just
// ended in state. The caller holds mu.
func (m *Manager) peerTurnEndLocked(s *webSession, state agentapi.TurnState) {
	t := cmp.Or(s.peerTurn, s.peerArm)
	s.peerTurn, s.peerArm = nil, nil
	if t == nil || t.Reply {
		return
	}
	switch state {
	case agentapi.TurnCompleted:
		m.replyLocked(s, t)
	case agentapi.TurnCancelled:
		m.peerEndLocked(t, peerStopped, "the turn was stopped before it replied")
	default:
		m.peerEndLocked(t, peerFailed, "the turn failed before it replied")
	}
}

// peerGoneLocked ends the peer turn of s, whose conversation ended. The
// caller holds mu.
func (m *Manager) peerGoneLocked(s *webSession) {
	t := cmp.Or(s.peerTurn, s.peerArm)
	s.peerTurn, s.peerArm = nil, nil
	if t != nil && !t.Reply {
		m.peerEndLocked(t, peerInterrupted, "the conversation ended during the turn")
	}
}

// replyLocked sends the final reply of s's completed turn to the Thread whose
// message t started it. The caller holds mu.
func (m *Manager) replyLocked(s *webSession, t *store.TurnPeer) {
	sender := m.sessions[t.From]
	p, ok := m.pairs[t.Pair]
	why := ""
	switch {
	case !ok || p.State != store.PairAllowed:
		why = "the owner ended messages between these threads"
	case sender == nil || sender.removed || sender.stage != StageActive:
		why = "the sending thread is no longer active"
	case !p.Linked && unattended(sender):
		why = "the sending thread runs unattended now, so the owner must link the pair"
	case len(sender.queue) >= maxQueue:
		why = "the sending thread's queue is full"
	}
	if why != "" {
		m.peerEndLocked(t, peerUndelivered, why)
		return
	}
	m.peerStatusLocked(t, peerReplying)
	m.enqueuePeerLocked(sender, m.peerFromLocked(t.Pair, t.Message, s, true), m.replyEnvelopeLocked(s, t.Message, lastReply(s)))
}

// lastReply is the main agent's last text in the turn that just ended in s,
// clipped to maxPeerReply.
func lastReply(s *webSession) string {
	for i := len(s.items) - 1; i >= 0; i-- {
		it := s.items[i]
		if it.AgentID != "" {
			continue
		}
		if it.Kind == agentapi.ItemUser && it.Delivery == "" {
			break
		}
		if text := strings.TrimSpace(it.Text); it.Kind == agentapi.ItemAssistant && text != "" {
			if len(text) > maxPeerReply {
				text = strings.ToValidUTF8(text[:maxPeerReply], "") + "\n\n[reply clipped by uam]"
			}
			return text
		}
	}
	return fmt.Sprintf("Thread %q ended its turn without a text reply.", peerName(s))
}

// setPairLocked stores p and publishes it, and the summaries of its
// Threads. The caller holds mu.
func (m *Manager) setPairLocked(p store.WebMessagePair) {
	var ids []string
	// Only a held message changes the Threads' summaries.
	if old, ok := m.pairs[p.ID]; !ok || (old.Held != nil) != (p.Held != nil) {
		ids = []string{p.A, p.B}
	}
	m.changePairsLocked(ids, func() {
		p.UpdatedAt = m.now()
		if m.pairs == nil {
			m.pairs = map[string]store.WebMessagePair{}
		}
		m.pairs[p.ID] = p
	})
}

// changePairsLocked runs change, which edits m.pairs, then schedules the
// write and publishes the pairs and the summaries of the Threads ids.
func (m *Manager) changePairsLocked(ids []string, change func()) {
	type seen struct {
		s      *webSession
		before SessionSummary
	}
	var threads []seen
	for _, id := range ids {
		if s := m.sessions[id]; s != nil && !slices.ContainsFunc(threads, func(t seen) bool { return t.s == s }) {
			threads = append(threads, seen{s, m.summaryLocked(s)})
		}
	}
	change()
	m.pairsDirty = true
	select {
	case m.wake <- struct{}{}:
	default:
	}
	pairs := m.pairListLocked()
	m.broadcastLocked("message_pairs", "", func(seq uint64) any { return messagePairsEvent{Seq: seq, MessagePairs: pairs} })
	for _, t := range threads {
		m.changedLocked(t.s, t.before)
	}
}

// dropPairsLocked removes the pairs of the Thread id: every one when all,
// else the ones the owner did not deny. The caller holds mu and publishes
// id's own summary.
func (m *Manager) dropPairsLocked(id string, all bool) {
	var ids []string
	found := false
	for _, p := range m.pairs {
		if (p.A == id || p.B == id) && (all || p.State != store.PairDenied) {
			found = true
			other := p.A
			if other == id {
				other = p.B
			}
			ids = append(ids, other)
		}
	}
	if !found {
		return
	}
	m.changePairsLocked(ids, func() {
		for _, p := range m.pairs {
			if (p.A == id || p.B == id) && (all || p.State != store.PairDenied) {
				delete(m.pairs, p.ID)
			}
		}
	})
}

type messagePairsEvent struct {
	Seq          uint64                 `json:"seq"`
	MessagePairs []store.WebMessagePair `json:"message_pairs"`
}

// pairListLocked is every pair, oldest change first. The caller holds mu.
func (m *Manager) pairListLocked() []store.WebMessagePair {
	out := make([]store.WebMessagePair, 0, len(m.pairs))
	for _, p := range m.pairs {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b store.WebMessagePair) int {
		return cmp.Or(a.UpdatedAt.Compare(b.UpdatedAt), strings.Compare(a.ID, b.ID))
	})
	return out
}

// heldMessagesLocked counts the held messages to or from the Thread id.
func (m *Manager) heldMessagesLocked(id string) int {
	n := 0
	for _, p := range m.pairs {
		if p.Held != nil && (p.A == id || p.B == id) {
			n++
		}
	}
	return n
}

// loadPairsLocked keeps the stored pairs whose Threads still exist. A
// message that was waiting in a queue or running in a turn was lost with
// the service: it is interrupted, never sent again. It reports whether
// anything changed. The caller holds mu.
func (m *Manager) loadPairsLocked(stored map[string]store.WebMessagePair) bool {
	changed := false
	m.pairs = map[string]store.WebMessagePair{}
	for id, p := range stored {
		if m.sessions[p.A] == nil || m.sessions[p.B] == nil {
			changed = true
			continue
		}
		p.Messages = slices.Clone(p.Messages)
		for i := range p.Messages {
			x := &p.Messages[i]
			switch x.Status {
			case peerQueued:
				p.Used -= 2
			case peerWorking, peerReplying:
				p.Used--
			default:
				continue
			}
			x.Status, x.Detail, x.UpdatedAt = peerInterrupted, "uam restarted before it was answered", m.now()
			changed = true
		}
		p.Used = max(0, p.Used)
		m.pairs[id] = p
	}
	return changed
}

// uamPairRow is a pair as the uam tool's tasks op shows it to one of its
// Threads.
type uamPairRow struct {
	State  string `json:"state"`
	Linked bool   `json:"linked,omitempty"`
	Used   int    `json:"used"`
	Limit  int    `json:"limit"`
	Held   bool   `json:"held,omitempty"`
}

// uamPairLocked is the pair of caller and s for the uam tool, nil while the
// owner has messages between Threads off. The caller holds mu.
func (m *Manager) uamPairLocked(caller, s *webSession) *uamPairRow {
	if !m.settings.ThreadMessages || caller == s {
		return nil
	}
	p, _ := m.pairOfLocked(caller.id, s.id)
	return &uamPairRow{State: p.State, Linked: p.Linked, Used: p.Used, Limit: p.Limit, Held: p.Held != nil}
}

// MessagePairAction is the owner's decision about a pair: allow (with
// link, to link it too), deny or reset.
func (m *Manager) MessagePairAction(id, action string, link bool) (store.WebMessagePair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pairs[id]
	switch {
	case m.closed:
		return store.WebMessagePair{}, errShuttingDown
	case !ok:
		return store.WebMessagePair{}, newError(http.StatusNotFound, "message pair not found")
	}
	switch action {
	case "reset":
		m.changePairsLocked([]string{p.A, p.B}, func() { delete(m.pairs, id) })
		return store.WebMessagePair{}, nil
	case "deny":
		p.State = store.PairDenied
		if p.Held != nil {
			p.Messages = endHeld(p, peerDeclined, "the owner declined it", m.now())
			p.Held = nil
		}
		m.setPairLocked(p)
		return m.pairs[id], nil
	case "allow":
	default:
		return store.WebMessagePair{}, newError(http.StatusBadRequest, "action must be allow, deny or reset")
	}
	held := p.Held
	if held == nil {
		return store.WebMessagePair{}, newError(http.StatusConflict, "no message between these threads waits for the owner")
	}
	from, to := m.sessions[held.From], m.sessions[held.To]
	if from == nil || to == nil || from.removed || to.removed {
		return store.WebMessagePair{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if !p.Linked && !link && (unattended(from) || unattended(to)) {
		return store.WebMessagePair{}, newError(http.StatusConflict, "a thread of the pair runs unattended: allow it with link")
	}
	p.State, p.Linked, p.Used, p.Limit = store.PairAllowed, p.Linked || link, 0, pairLimit
	status, detail := peerQueued, ""
	if err := m.peerRoomLocked(p, from, to); err != nil {
		status, detail = peerUndelivered, err.Error()
	}
	if from.stage != StageActive || to.stage != StageActive {
		status, detail = peerUndelivered, "a thread of the pair is no longer active"
	}
	p.Messages = endHeld(p, status, detail, m.now())
	p.Held = nil
	if status == peerQueued {
		p.Used = 2
	}
	m.setPairLocked(p)
	if status == peerQueued {
		m.enqueuePeerLocked(to, m.peerFromLocked(p.ID, held.ID, from, false), m.messageEnvelopeLocked(from, held.Text))
	}
	return m.pairs[id], nil
}

// endHeld is p's messages with the held one moved to status.
func endHeld(p store.WebMessagePair, status, detail string, now time.Time) []store.WebPeerMessage {
	list := slices.Clone(p.Messages)
	for i := range list {
		if list[i].ID == p.Held.ID {
			list[i].Status, list[i].Detail, list[i].UpdatedAt = status, detail, now
		}
	}
	return list
}

func (s *Server) messageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/message-pairs/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Link bool `json:"link"`
		}
		if r.ContentLength != 0 && !decodeBody(w, r, &body) {
			return
		}
		p, err := s.m.MessagePairAction(r.PathValue("id"), r.PathValue("action"), body.Link)
		if err != nil {
			writeFailure(w, err)
			return
		}
		if p.ID == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
}
