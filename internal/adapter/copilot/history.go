package copilot

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// ReadHistory reads the conversation's persisted journal through the
// experimental sessions.readPersistedEvents, which neither resumes nor locks
// the session: nothing is written, no hook or MCP server starts, and a
// terminal resuming it meanwhile sees no other holder. Measured against CLI
// 1.0.88 (#193), it returns the same transcript as a resume. The newest
// webReadPages pages of a larger journal are kept.
func (p *webProvider) ReadHistory(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error) {
	if req.ConversationID == "" {
		return agentapi.History{}, errors.New("copilot: ReadRequest.ConversationID is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return agentapi.History{}, err
	}
	// A read whose snapshot expired starts over, as ReadHistoryWindow does.
	for attempt := 1; ; attempt++ {
		h, err := p.readHistory(ctx, client, req)
		if !errors.Is(err, errJournalChanged) || attempt == webWindowAttempts {
			return h, err
		}
	}
}

func (p *webProvider) readHistory(ctx context.Context, client sdkClient, req agentapi.ReadRequest) (agentapi.History, error) {
	// Newest first, so a journal cut at webReadPages keeps its latest turns.
	backward, perPage := rpc.EventsReadDirectionBackward, int64(webReadEvents)
	read := &rpc.SessionsReadPersistedEventsRequest{SessionID: req.ConversationID, Direction: &backward, Max: &perPage}
	var pages [][]copilot.SessionEvent
	truncated := false
	bytesKept := 0
	model := ""
	// Every recorded subagent is listed: the drained events keep their
	// subagent lifecycle events and the start of up to webReadResults tool
	// outputs, a subagent's result when its parent tool call is not kept.
	var drained [][]copilot.SessionEvent
	results := map[string]string{}
	drain := func(evs []copilot.SessionEvent) {
		var lifecycle []copilot.SessionEvent
		for _, ev := range evs {
			switch d := ev.Data.(type) {
			case *rpc.SubagentStartedData, *rpc.SubagentConfiguredData, *rpc.SubagentCompletedData, *rpc.SubagentFailedData:
				lifecycle = append(lifecycle, ev)
			case *rpc.ToolExecutionCompleteData:
				if d.Result != nil && len(results) < webReadResults {
					results[d.ToolCallID] = clip(d.Result.Content, webResultBytes)
				}
			}
		}
		drained = append(drained, lifecycle)
	}
	for {
		res, err := p.readEvents(ctx, client, read)
		if err != nil {
			return agentapi.History{}, err
		}
		// The last model selection may predate the retained transcript. Keep
		// it while draining older pages so import preserves the model.
		for i := len(res.Events) - 1; i >= 0 && model == ""; i-- {
			if agentOf(res.Events[i]) == "" {
				model = recordedModel(res.Events[i])
			}
		}
		if !truncated {
			start := len(res.Events)
			for start > 0 {
				encoded, err := json.Marshal(res.Events[start-1])
				if err != nil {
					return agentapi.History{}, fmt.Errorf("measure copilot history: %w", err)
				}
				if bytesKept+len(encoded) > webReadBytes {
					truncated = true
					break
				}
				bytesKept += len(encoded)
				start--
			}
			pages = append(pages, slices.Clone(res.Events[start:]))
			drain(res.Events[:start])
			if len(pages) == webReadPages && res.HasMore {
				truncated = true
			}
		} else {
			drain(res.Events)
		}
		if !res.HasMore {
			break
		}
		// Finish a truncated snapshot without retaining older pages. The SDK
		// has no release-cursor API; completion releases its pinned journal.
		cursor := res.Cursor
		read = &rpc.SessionsReadPersistedEventsRequest{SessionID: req.ConversationID, Cursor: &cursor, Direction: &backward, Max: &perPage}
	}
	var evs []copilot.SessionEvent
	for i := len(drained) - 1; i >= 0; i-- {
		evs = append(evs, drained[i]...)
	}
	started := map[[2]string]bool{}
	for i := len(pages) - 1; i >= 0; i-- {
		evs = append(evs, pages[i]...)
		for _, ev := range pages[i] {
			if d, ok := ev.Data.(*rpc.ToolExecutionStartData); ok {
				started[[2]string{agentOf(ev), d.ToolCallID}] = true
			}
		}
	}
	recorded := history(evs)
	if truncated {
		// A tool call that began before the kept events belongs, whole, to
		// the older part ReadHistoryWindow reads. Its later half here would
		// show it twice, and out of place.
		recorded.Items = slices.DeleteFunc(recorded.Items, func(it agentapi.Item) bool {
			return it.Tool != nil && !started[[2]string{it.AgentID, it.ID}]
		})
	}
	parents := map[string]*agentapi.Subagent{}
	for i := range recorded.Subagents {
		sa := &recorded.Subagents[i]
		sa.Result, parents[sa.ParentToolCallID] = results[sa.ParentToolCallID], sa
	}
	for _, it := range recorded.Items {
		if sa := parents[it.ID]; sa != nil && it.Tool != nil {
			sa.Result = clip(it.Tool.Output, webResultBytes)
		}
	}
	recorded.Model = model
	recorded.Truncated = truncated
	return recorded, nil
}

var errJournalChanged = errors.New("read copilot history: the journal changed while it was read")

// readEvents reads one page of a persisted journal.
func (p *webProvider) readEvents(ctx context.Context, client sdkClient, read *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error) {
	res, err := client.ReadEvents(ctx, read)
	switch {
	case err != nil && strings.Contains(err.Error(), "journal is unavailable"):
		return nil, fmt.Errorf("%w: %s", agentapi.ErrConversationNotFound, read.SessionID)
	case err != nil:
		p.poke()
		return nil, fmt.Errorf("read copilot history: %s", errText(err))
	case res == nil || res.CursorStatus == rpc.EventsCursorStatusExpired:
		return nil, errJournalChanged
	}
	return res, nil
}

// ReadHistoryWindow reads one agent's items around req.ItemID from the
// persisted journal, whole: unlike ReadHistory it clips no text. The
// journal is read forward in one pass that keeps only the window; the rest
// is still read, to release the read's pinned snapshot. The journal of an
// open conversation grows meanwhile, which the snapshot does not see; a read
// whose snapshot expired starts over and finds the item again.
func (p *webProvider) ReadHistoryWindow(ctx context.Context, req agentapi.WindowRequest) (agentapi.HistoryWindow, error) {
	if req.ConversationID == "" {
		return agentapi.HistoryWindow{}, errors.New("copilot: ReadRequest.ConversationID is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return agentapi.HistoryWindow{}, err
	}
	for attempt := 1; ; attempt++ {
		w, err := p.readWindow(ctx, client, req)
		if !errors.Is(err, errJournalChanged) || attempt == webWindowAttempts {
			return w, err
		}
	}
}

func (p *webProvider) readWindow(ctx context.Context, client sdkClient, req agentapi.WindowRequest) (agentapi.HistoryWindow, error) {
	t, f := newTranscript(), newWindowFold(req)
	t.whole = true
	err := p.readForward(ctx, client, req.ConversationID, func(ev copilot.SessionEvent) {
		for _, it := range t.items(ev) {
			if it.AgentID == req.AgentID {
				f.add(it)
			}
		}
	})
	if err != nil {
		return agentapi.HistoryWindow{}, err
	}
	return f.window()
}

// readForward folds the whole persisted journal into fold, oldest first and
// without its ephemeral events, in one pass.
func (p *webProvider) readForward(ctx context.Context, client sdkClient, convID string, fold func(copilot.SessionEvent)) error {
	forward, perPage := rpc.EventsReadDirectionForward, int64(webReadEvents)
	read := &rpc.SessionsReadPersistedEventsRequest{SessionID: convID, Direction: &forward, Max: &perPage}
	for {
		res, err := p.readEvents(ctx, client, read)
		if err != nil {
			return err
		}
		for _, ev := range res.Events {
			if ev.Ephemeral == nil || !*ev.Ephemeral {
				fold(ev)
			}
		}
		if !res.HasMore {
			return nil
		}
		cursor := res.Cursor
		read = &rpc.SessionsReadPersistedEventsRequest{SessionID: convID, Cursor: &cursor, Direction: &forward, Max: &perPage}
	}
}

// ReadSubagents reads the subagents recorded up to req.AgentID from the
// persisted journal in one forward pass that keeps only the window, as
// ReadHistoryWindow does. A record's Result is the start of its parent tool
// call's output, as ReadHistory reports it.
func (p *webProvider) ReadSubagents(ctx context.Context, req agentapi.SubagentRequest) (agentapi.SubagentWindow, error) {
	if req.ConversationID == "" {
		return agentapi.SubagentWindow{}, errors.New("copilot: ReadRequest.ConversationID is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return agentapi.SubagentWindow{}, err
	}
	for attempt := 1; ; attempt++ {
		f := newSubagentFold(req)
		err := p.readForward(ctx, client, req.ConversationID, f.add)
		if err == nil {
			return f.window()
		}
		if !errors.Is(err, errJournalChanged) || attempt == webWindowAttempts {
			return agentapi.SubagentWindow{}, err
		}
	}
}

// subagentFold keeps the subagents recorded up to a requested one while a
// journal is folded oldest first: at most req.Before of those before it,
// holding at most webWindowBytes. Later events still update the kept ones.
type subagentFold struct {
	req agentapi.SubagentRequest
	log *subagentLog
	// seen holds every subagent met before the requested one, so an update
	// never brings back one dropped from the front.
	seen    map[string]bool
	dropped int
	bytes   int // held by the records before the requested one
	found   bool
}

func newSubagentFold(req agentapi.SubagentRequest) *subagentFold {
	return &subagentFold{req: req, log: newSubagentLog(), seen: map[string]bool{}}
}

func (f *subagentFold) add(ev copilot.SessionEvent) {
	id := agentOf(ev)
	switch d := ev.Data.(type) {
	case *rpc.ToolExecutionStartData:
		f.log.noteCall(d)
		return
	case *rpc.ToolExecutionCompleteData:
		if parent := f.log.byCall[d.ToolCallID]; f.log.byID[parent] != nil && d.Result != nil {
			f.change(parent, func(sa *agentapi.Subagent) { sa.Result = clip(d.Result.Content, webResultBytes) })
		}
		return
	case *rpc.SubagentStartedData, *rpc.SubagentConfiguredData:
	case *rpc.SubagentCompletedData:
		id = cmp.Or(id, f.log.byCall[d.ToolCallID])
	case *rpc.SubagentFailedData:
		id = cmp.Or(id, f.log.byCall[d.ToolCallID])
	default:
		return
	}
	switch {
	case id == "":
	case f.log.byID[id] != nil:
		f.change(id, func(*agentapi.Subagent) { f.log.apply(ev) })
	case f.found || f.seen[id]:
	default:
		f.seen[id] = true
		f.log.apply(ev)
		if id == f.req.AgentID {
			f.found = true
			return
		}
		f.bytes += subagentBytes(*f.log.byID[id])
		f.trim()
	}
}

// change applies an update to the kept record id.
func (f *subagentFold) change(id string, update func(*agentapi.Subagent)) {
	sa, counted := f.log.byID[id], id != f.req.AgentID
	if counted {
		f.bytes -= subagentBytes(*sa)
	}
	update(sa)
	if counted {
		f.bytes += subagentBytes(*sa)
		f.trim()
	}
}

// trim drops the oldest records beyond the bounds before the requested one.
func (f *subagentFold) trim() {
	for {
		before := len(f.log.order)
		if f.found {
			before--
		}
		if before == 0 || before <= f.req.Before && f.bytes <= webWindowBytes {
			return
		}
		id := f.log.order[0]
		sa := f.log.byID[id]
		f.bytes -= subagentBytes(*sa)
		if f.log.byCall[sa.ParentToolCallID] == id {
			delete(f.log.byCall, sa.ParentToolCallID)
		}
		delete(f.log.byID, id)
		f.log.order = f.log.order[1:]
		f.dropped++
	}
}

func (f *subagentFold) window() (agentapi.SubagentWindow, error) {
	if !f.found {
		return agentapi.SubagentWindow{}, fmt.Errorf("read copilot subagents: %w: %s", agentapi.ErrItemNotFound, f.req.AgentID)
	}
	return agentapi.SubagentWindow{Subagents: f.log.list(), Start: f.dropped == 0}, nil
}

func subagentBytes(sa agentapi.Subagent) int {
	return len(sa.ID) + len(sa.Name) + len(sa.Description) + len(sa.Type) + len(sa.Model) + len(sa.Effort) + len(sa.Error) + len(sa.ParentToolCallID) + len(sa.Result) +
		len(sa.Runs)*agentapi.SubagentRunBytes
}

// windowFold keeps one agent's items around a requested item while a
// journal is folded oldest first. As in history, an item keeps the place of
// its first event and later events update it.
type windowFold struct {
	req   agentapi.WindowRequest
	items []agentapi.Item
	// index holds each kept item's position counted from the first item met.
	index map[string]int
	// seen holds every item met before the requested one, so an update
	// never brings back an item dropped from the front.
	seen    map[string]struct{}
	dropped int
	at      int    // position of the requested item, -1 until it is met
	bytes   [2]int // kept before and after it
	next    string
}

func newWindowFold(req agentapi.WindowRequest) *windowFold {
	return &windowFold{req: req, index: map[string]int{}, seen: map[string]struct{}{}, at: -1}
}

func (f *windowFold) add(it agentapi.Item) {
	if pos, ok := f.index[it.ID]; ok {
		i := pos - f.dropped
		it.Time = f.items[i].Time
		if side := f.side(pos); side >= 0 {
			f.bytes[side] += itemBytes(it) - itemBytes(f.items[i])
		}
		f.items[i] = it
		f.trim()
		return
	}
	if f.at >= 0 {
		if f.next == "" && f.dropped+len(f.items)-f.at-1 < f.req.After && f.bytes[1] < webWindowBytes {
			f.index[it.ID] = f.dropped + len(f.items)
			f.items = append(f.items, it)
			f.bytes[1] += itemBytes(it)
			f.trim()
		} else if f.next == "" {
			f.next = it.ID
		}
		return
	}
	if _, ok := f.seen[it.ID]; ok {
		return
	}
	f.seen[it.ID] = struct{}{}
	f.index[it.ID] = f.dropped + len(f.items)
	f.items = append(f.items, it)
	if it.ID == f.req.ItemID {
		f.at = f.dropped + len(f.items) - 1
		return
	}
	f.bytes[0] += itemBytes(it)
	f.trim()
}

// side is 0 before the requested item, 1 after it and -1 for it.
func (f *windowFold) side(pos int) int {
	switch {
	case f.at < 0 || pos < f.at:
		return 0
	case pos == f.at:
		return -1
	}
	return 1
}

// trim drops the oldest items beyond the bounds before the requested item
// and the newest beyond the bytes after it. The requested item stays.
func (f *windowFold) trim() {
	for {
		before := len(f.items)
		if f.at >= 0 {
			before = f.at - f.dropped
		}
		if before == 0 || before <= f.req.Before && f.bytes[0] <= webWindowBytes {
			break
		}
		f.bytes[0] -= itemBytes(f.items[0])
		delete(f.index, f.items[0].ID)
		f.items = f.items[1:]
		f.dropped++
	}
	for f.at >= 0 && f.bytes[1] > webWindowBytes && f.dropped+len(f.items)-1 > f.at {
		last := f.items[len(f.items)-1]
		f.bytes[1] -= itemBytes(last)
		delete(f.index, last.ID)
		f.items = f.items[:len(f.items)-1]
		f.next = last.ID
	}
}

func (f *windowFold) window() (agentapi.HistoryWindow, error) {
	at := len(f.items)
	if f.at >= 0 {
		at = f.at - f.dropped
	} else if f.req.ItemID != "" {
		return agentapi.HistoryWindow{}, fmt.Errorf("read copilot history: %w: %s", agentapi.ErrItemNotFound, f.req.ItemID)
	}
	return agentapi.HistoryWindow{Items: f.items, At: at, Start: f.dropped == 0, Next: f.next}, nil
}

// itemBytes approximates the memory an item's text and images hold.
func itemBytes(it agentapi.Item) int {
	n := len(it.ID) + len(it.Text)
	if c := it.Completion; c != nil {
		n += len(c.Decision) + len(c.UserItemID) + len(c.Summary) + len(c.Reason)
		if c.Blocker != nil {
			n += len(c.Blocker.Kind) + len(c.Blocker.Reason)
		}
	}
	if it.Tool != nil {
		n += len(it.Tool.Name) + len(it.Tool.Title) + len(it.Tool.Input) + len(it.Tool.Output) + len(it.Tool.EditEventID) + len(it.Tool.Progress)
		for _, edit := range it.Tool.FileEdits {
			n += len(edit.Path) + len(edit.Kind) + len(edit.DiffStatus) + 16
		}
	}
	for _, img := range it.Images {
		n += len(img.Data)
	}
	return n
}

func (c *conversation) History(ctx context.Context) (agentapi.History, error) {
	c.mu.Lock()
	closed, planEpoch := c.closed, c.planEpoch
	c.mu.Unlock()
	if closed {
		return agentapi.History{}, agentapi.ErrClosed
	}
	evs, err := c.sess.Events(ctx)
	if err != nil {
		c.p.poke()
		return agentapi.History{}, fmt.Errorf("copilot history: %s", errText(err))
	}
	// Reconcile cancelled agents before a resumed CLI can replay abandoned
	// permission requests. History does not replace newer terminal live state.
	c.mu.Lock()
	if !c.closed && !c.planStopped && c.planEpoch == planEpoch && c.plans.revision == 0 {
		seen := map[string]bool{}
		for _, ev := range evs {
			if id, _, content, ok := recordedPlan(ev); ok && agentOf(ev) == "" && !seen[id] {
				seen[id] = true
				c.plans.observe(content)
			}
		}
		if !c.turnRunning {
			c.plans.dropBodies()
		}
	}
	for _, ev := range evs {
		c.subs.apply(ev)
	}
	for _, sa := range c.subs.byID {
		if sa.Status == agentapi.SubagentCancelled {
			c.expireSubagentLocked(sa.ID)
		}
	}
	c.mu.Unlock()
	recorded := history(evs)
	// Model-call reports are not recorded; the CLI's session total is. Live
	// calls after the last recorded total keep the higher live figure.
	if total, ok := recordedTotal(evs); ok {
		c.mu.Lock()
		c.nanoAIU = max(c.nanoAIU, total)
		recorded.Usage = &agentapi.Usage{AIUnits: c.nanoAIU / nanoPerUnit}
		c.mu.Unlock()
	}
	c.restoreIdle(ctx, recorded.Subagents)
	return recorded, nil
}

// history maps a recorded event log to transcript items, oldest first, with
// one item per agent and id, and to the subagents it records. Ephemeral
// events (streaming deltas) are skipped.
func history(evs []copilot.SessionEvent) agentapi.History {
	t, subs := newTranscript(), newSubagentLog()
	var items []agentapi.Item
	var model string
	var skills []agentapi.SkillLoad
	index, loaded := map[[2]string]int{}, map[[2]string]bool{}
	for _, ev := range evs {
		if ev.Ephemeral != nil && *ev.Ephemeral {
			continue
		}
		if m := recordedModel(ev); m != "" && agentOf(ev) == "" {
			model = m
		}
		subs.apply(ev)
		if load, ok := skillLoad(ev); ok && !loaded[[2]string{load.AgentID, load.ID}] {
			loaded[[2]string{load.AgentID, load.ID}] = true
			skills = append(skills, load)
		}
		for _, it := range t.items(ev) {
			key := [2]string{it.AgentID, it.ID}
			if i, seen := index[key]; seen {
				it.Time = items[i].Time
				items[i] = it
				continue
			}
			index[key] = len(items)
			items = append(items, it)
		}
	}
	h := agentapi.History{Items: items, Subagents: subs.list(), Model: model, Skills: skills}
	if total, ok := recordedTotal(evs); ok {
		h.Usage = &agentapi.Usage{AIUnits: total / nanoPerUnit}
	}
	return h
}

// recordedTotal is the session-wide nano-AI units of the latest recorded
// usage checkpoint or shutdown. The CLI never records assistant.usage.
func recordedTotal(evs []copilot.SessionEvent) (total float64, ok bool) {
	for _, ev := range evs {
		var t *float64
		switch d := ev.Data.(type) {
		case *rpc.SessionUsageCheckpointData:
			t = &d.TotalNanoAiu
		case *rpc.SessionShutdownData:
			t = d.TotalNanoAiu
		}
		if t != nil && *t >= 0 && (ev.Ephemeral == nil || !*ev.Ephemeral) {
			total, ok = *t, true
		}
	}
	return total, ok
}

// recordedModel is the model an event records as selected, or "".
func recordedModel(ev copilot.SessionEvent) string {
	var selected *string
	switch d := ev.Data.(type) {
	case *rpc.SessionModelChangeData:
		return d.NewModel
	case *rpc.SessionStartData:
		selected = d.SelectedModel
	case *rpc.SessionResumeData:
		selected = d.SelectedModel
	}
	if selected == nil {
		return ""
	}
	return *selected
}
