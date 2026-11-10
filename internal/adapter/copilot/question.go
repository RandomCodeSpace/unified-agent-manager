package copilot

import (
	"crypto/rand"
	"regexp"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// questionLinks pairs each user_input.requested event with the ask_user
// callback for the same question. The callback carries the question and
// choices only; the event carries the same question with its tool call and
// agent. Neither names the other, so they pair by question text in arrival
// order, whichever side comes first. Every entry is bounded and expires:
// the two sides of one question arrive within moments of each other.
type questionLinks struct {
	// events are the events without a callback yet, by question text,
	// oldest first.
	events map[string][]questionEvent
	// waiting are the pending questions without an event yet, oldest first.
	waiting []*interaction
	// spent counts, by question text, the questions that were answered
	// before their event came, with the time of the newest, so a late event
	// is dropped rather than attached to the next question with that text.
	spent map[string]spentQuestions
}

type questionEvent struct {
	toolCallID, agentID string
	at                  time.Time
}

type spentQuestions struct {
	n  int
	at time.Time
}

// maxQuestionLinks bounds each side of questionLinks; questionLinkTTL is how
// long one side waits for the other. A question past either shows without
// its tool call.
const (
	maxQuestionLinks = 16
	questionLinkTTL  = 30 * time.Second
)

// linkQuestionLocked handles a user_input.requested event: it links the
// oldest waiting question with that text, which is emitted again with the
// link and its agent, or is dropped when that question was already answered,
// or waits for its callback.
func (c *conversation) linkQuestionLocked(d *rpc.UserInputRequestedData, agentID string, now time.Time) {
	if d.ToolCallID == nil {
		return
	}
	id := strings.TrimSpace(*d.ToolCallID)
	if id == "" {
		return
	}
	q := &c.questions
	q.expire(now)
	// Questions are matched by the text the owner reads, without the marker.
	text, _ := questionText(d.Question)
	// An older callback may already be answered while the next same-text
	// question waits. Consume that older event before matching a waiter.
	if sp, ok := q.spent[text]; ok {
		if sp.n--; sp.n == 0 {
			delete(q.spent, text)
		} else {
			q.spent[text] = sp
		}
		return
	}
	for i, in := range q.waiting {
		if in.Questions[0].Text != text {
			continue
		}
		q.waiting = slices.Delete(q.waiting, i, i+1)
		in.ToolCallID = id
		if in.AgentID == "" {
			in.AgentID = agentID
		}
		c.emitInteractionLocked(in)
		return
	}
	if q.events == nil {
		q.events = map[string][]questionEvent{}
	}
	if q.count() >= maxQuestionLinks {
		q.dropOldestEvent()
	}
	q.events[text] = append(q.events[text], questionEvent{toolCallID: id, agentID: agentID, at: now})
}

// askedLocked gives a new question its event's tool call and agent when the
// event came first; otherwise the question waits for it.
func (c *conversation) askedLocked(in *interaction, now time.Time) {
	q := &c.questions
	q.expire(now)
	text := in.Questions[0].Text
	if evs := q.events[text]; len(evs) > 0 {
		in.ToolCallID, in.AgentID = evs[0].toolCallID, evs[0].agentID
		if len(evs) == 1 {
			delete(q.events, text)
		} else {
			q.events[text] = evs[1:]
		}
		return
	}
	if len(q.waiting) >= maxQuestionLinks {
		q.waiting = q.waiting[1:]
	}
	q.waiting = append(q.waiting, in)
}

// settledLocked forgets a question that ended. One still waiting for its
// event is counted as spent, so that event links nothing when it comes.
func (c *conversation) settledLocked(in *interaction, now time.Time) {
	q := &c.questions
	i := slices.Index(q.waiting, in)
	if i < 0 {
		return
	}
	q.waiting = slices.Delete(q.waiting, i, i+1)
	if q.spent == nil {
		q.spent = map[string]spentQuestions{}
	}
	if len(q.spent) >= maxQuestionLinks {
		clear(q.spent)
	}
	sp := q.spent[in.Questions[0].Text]
	q.spent[in.Questions[0].Text] = spentQuestions{n: sp.n + 1, at: now}
}

func (q *questionLinks) count() int {
	n := 0
	for _, evs := range q.events {
		n += len(evs)
	}
	return n
}

func (q *questionLinks) dropOldestEvent() {
	var oldest string
	var at time.Time
	for text, evs := range q.events {
		if oldest == "" || evs[0].at.Before(at) {
			oldest, at = text, evs[0].at
		}
	}
	if len(q.events[oldest]) == 1 {
		delete(q.events, oldest)
	} else {
		q.events[oldest] = q.events[oldest][1:]
	}
}

// expire drops the events and spent counts older than questionLinkTTL. A
// waiting question stays as long as it is pending: the user may take long
// to answer, and its event, if it comes at all, comes at once.
func (q *questionLinks) expire(now time.Time) {
	for text, evs := range q.events {
		evs = slices.DeleteFunc(evs, func(e questionEvent) bool { return now.Sub(e.at) > questionLinkTTL })
		if len(evs) == 0 {
			delete(q.events, text)
		} else {
			q.events[text] = evs
		}
	}
	for text, sp := range q.spent {
		if now.Sub(sp.at) > questionLinkTTL {
			delete(q.spent, text)
		}
	}
}

// multipleChoice marks a question whose options may be chosen together: the agent ends
// its text with "(Choose any that apply)" (the taskSystem convention; "select" and "pick",
// "all" and a bare "any" read the same). The marker leaves the text the owner reads.
var multipleChoice = regexp.MustCompile(`(?i)\s*\((?:choose|select|pick)\s+(?:any|all)(?:\s+that\s+apply)?\)\s*$`)

// questionOf is the owner's question for an ask_user request. The owner may always type
// their own answer, so Custom is set even when the agent sent allowFreeform false.
func questionOf(req copilot.UserInputRequest) agentapi.Question {
	q := agentapi.Question{Choices: req.Choices, Custom: true}
	q.Text, q.Multiple = questionText(req.Question)
	return q
}

// questionText is the agent's question text without the multiple-choice
// marker, and whether the marker was there.
func questionText(raw string) (string, bool) {
	if !multipleChoice.MatchString(raw) {
		return raw, false
	}
	return strings.TrimSpace(multipleChoice.ReplaceAllString(raw, "")), true
}

// askUser is the SDK's ask_user callback. It runs on its own goroutine and
// blocks until the user answers or the conversation ends. The owner may always
// type their own answer, even when the agent sent allowFreeform false: the CLI
// hands a free-form answer to the model like any other.
func (c *conversation) askUser(req copilot.UserInputRequest, _ copilot.UserInputInvocation) (copilot.UserInputResponse, error) {
	in := &interaction{
		Interaction: agentapi.Interaction{
			ID:        "question-" + rand.Text(),
			Kind:      agentapi.InteractionQuestion,
			Title:     "Question from Copilot",
			Questions: []agentapi.Question{questionOf(req)},
			State:     agentapi.InteractionPending,
			Time:      time.Now(),
		},
		reply: make(chan userReply, 1),
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return copilot.UserInputResponse{}, errNoUser
	}
	c.askedLocked(in, time.Now())
	c.pending[in.ID] = in
	c.emitInteractionLocked(in)
	c.mu.Unlock()
	r := <-in.reply
	return r.resp, r.err
}
