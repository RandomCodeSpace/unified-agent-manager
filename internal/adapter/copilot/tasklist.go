package copilot

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"
	"unsafe"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// rawRequestFunc is the signature of the SDK's internal jsonrpc2.Client.Request.
type rawRequestFunc = func(context.Context, string, any) (json.RawMessage, error)

// listTasksRaw calls session.tasks.list through the JSON-RPC client inside
// api, an *rpc.TasksAPI. The SDK exposes no raw request and decodes the whole
// list strictly, so one entry it cannot read hides every task. ok is false
// when the SDK's layout no longer matches; the caller then uses the typed call.
// shortcut: reads an unexported SDK field; drop once the SDK decodes
// TaskShellInfo.pid as a number (still int64 in v1.0.29-preview.1).
func listTasksRaw(ctx context.Context, api any, sessionID string) (raw json.RawMessage, ok bool, err error) {
	v := reflect.ValueOf(api)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, false, nil
	}
	f := v.Elem().FieldByName("client")
	if !f.IsValid() || f.Kind() != reflect.Pointer || f.IsNil() {
		return nil, false, nil
	}
	m := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().MethodByName("Request") // #nosec G103 -- read-only access to the SDK's own client field, same type and address.
	if !m.IsValid() {
		return nil, false, nil
	}
	request, ok := m.Interface().(rawRequestFunc)
	if !ok {
		return nil, false, nil
	}
	raw, err = request(ctx, "session.tasks.list", map[string]any{"sessionId": sessionID})
	return raw, true, err
}

// decodeTaskList decodes a session.tasks.list result entry by entry, so one
// unreadable entry leaves the others listed. It fails only when the list
// itself is unreadable or every entry is.
func decodeTaskList(raw json.RawMessage) ([]rpc.TaskInfo, error) {
	var list struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	tasks := make([]rpc.TaskInfo, 0, len(list.Tasks))
	var firstErr error
	for _, entry := range list.Tasks {
		task, err := decodeTask(entry)
		if err != nil {
			log.Warn("copilot task list entry unreadable; skipped", "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		tasks = append(tasks, task)
	}
	if len(tasks) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return tasks, nil
}

// decodeTask decodes one entry with the SDK's own union decoder. CLI 1.0.105
// sends a detached shell's pid as 3892667.0, which the SDK's int64 rejects;
// a whole-number pid is rewritten as an integer and the entry decoded again.
func decodeTask(entry json.RawMessage) (rpc.TaskInfo, error) {
	task, err := decodeTaskEntry(entry)
	if err == nil {
		return task, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(entry, &fields) != nil {
		return nil, err
	}
	pid, perr := strconv.ParseFloat(string(fields["pid"]), 64)
	if perr != nil || pid != math.Trunc(pid) || math.Abs(pid) > 1<<53 {
		return nil, err
	}
	fields["pid"] = strconv.AppendInt(nil, int64(pid), 10)
	fixed, merr := json.Marshal(fields)
	if merr != nil {
		return nil, err
	}
	if task, retryErr := decodeTaskEntry(fixed); retryErr == nil {
		return task, nil
	}
	return nil, err
}

func decodeTaskEntry(entry json.RawMessage) (rpc.TaskInfo, error) {
	var one rpc.TaskList
	if err := json.Unmarshal(slices.Concat([]byte(`{"tasks":[`), entry, []byte(`]}`)), &one); err != nil {
		return nil, err
	}
	return one.Tasks[0], nil
}

// webTasksRetry is how long after a failed task-list read the list is read
// again. Tests shorten it.
var webTasksRetry = 15 * time.Second

// watchLocked has the task list settle agentID from now on. A non-zero sent
// ignores task-list entries that ended before that follow-up.
func (c *conversation) watchLocked(agentID string, sent time.Time) {
	c.watch[agentID] = sent
	c.checkTasksLocked()
}

// checkTasksLocked reads background shells and watched agents on its own
// goroutine: a request made on the event goroutine stalls the connection its
// answer arrives on. One read runs at a time; asking during it adds one more.
func (c *conversation) checkTasksLocked() {
	if c.closed {
		return
	}
	if c.sess == nil {
		c.relist = true
		return
	}
	if c.listing {
		c.relist = true
		return
	}
	c.listing = true
	go c.readTasks(c.sess)
}

func (c *conversation) readTasks(sess sdkSession) {
	defer log.Recover("copilot tasks read", &c.mu, &c.p.mu)
	for {
		c.taskRPC.Lock()
		// Only a read started after the idle can show the CLI still defers it.
		c.mu.Lock()
		afterIdle := c.idleUnresolved
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), webTasksTimeout)
		tasks, err := sess.ListTasks(ctx)
		cancel()
		c.mu.Lock()
		switch {
		case err != nil:
			c.p.poke()
			if !c.closed && c.backgroundTasks != nil {
				snapshot := *c.backgroundTasks
				snapshot.Known = false
				c.backgroundTasks = &snapshot
				c.emitLocked(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &snapshot})
			}
			// Unknown shells and unsettled follow-ups keep the Thread busy;
			// no CLI event may come to read the list again.
			if !c.closed && c.retryTasks == nil {
				c.retryTasks = time.AfterFunc(webTasksRetry, func() {
					c.mu.Lock()
					defer c.mu.Unlock()
					c.retryTasks = nil
					c.checkTasksLocked()
				})
			}
		case !c.closed:
			c.applyShellTasksLocked(tasks)
			c.applyTasksLocked(tasks)
			if afterIdle && c.idleUnresolved && attachedShellRunning(tasks) {
				c.finishTurnLocked(nil, time.Now())
			}
		}
		again := c.relist && !c.closed
		c.listing, c.relist = again, false
		c.mu.Unlock()
		c.taskRPC.Unlock()
		if !again {
			return
		}
	}
}

// restoreIdle reads the task list once for a reopened conversation's
// completed subagents. Recorded events never say that one still takes
// follow-ups; only the live list does.
func (c *conversation) restoreIdle(ctx context.Context, subs []agentapi.Subagent) {
	if !slices.ContainsFunc(subs, func(sa agentapi.Subagent) bool { return sa.Status == agentapi.SubagentCompleted }) {
		return
	}
	c.taskRPC.Lock()
	defer c.taskRPC.Unlock()
	tasks, err := c.sess.ListTasks(ctx)
	if err != nil {
		c.p.poke()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range subs {
		if sa := c.subs.byID[subs[i].ID]; sa != nil && sa.Status == agentapi.SubagentCompleted &&
			subs[i].Status == agentapi.SubagentCompleted && takesFollowUps(agentTask(tasks, sa.ID)) {
			sa.Status, subs[i].Status = agentapi.SubagentIdle, agentapi.SubagentIdle
			subs[i] = subs[i].Snapshot()
		}
	}
}

// attachedShellRunning reports whether tasks hold a running attached shell:
// work the CLI withholds session.idle for.
func attachedShellRunning(tasks []rpc.TaskInfo) bool {
	return slices.ContainsFunc(tasks, func(task rpc.TaskInfo) bool {
		shell, ok := task.(*rpc.TaskShellInfo)
		return ok && shell.AttachmentMode == rpc.TaskShellInfoAttachmentModeAttached && shell.Status == rpc.TaskStatusRunning
	})
}

// autopilotStoppedLocked notes the main agent's session.autopilot_stopped.
// CLI 1.0.103 sends it before the run's assistant.idle and session.idle,
// which then end the turn; an idle already withheld ends it here. Older CLIs
// keep the task_complete and task-list paths.
func (c *conversation) autopilotStoppedLocked(d *rpc.SessionAutopilotStoppedData, at time.Time) {
	requestID := ""
	if d.RequestID != nil {
		requestID = *d.RequestID
	}
	log.Info("copilot autopilot stopped", "conversation", c.id, "reason", d.Reason, "request_id", requestID)
	c.autopilotStopped, c.taskCompleted = true, true
	if c.idleUnresolved {
		c.assistantIdleSeen = true
		c.finishTurnLocked(copilot.Bool(d.Reason == "aborted"), at)
	}
}

func (c *conversation) applyShellTasksLocked(tasks []rpc.TaskInfo) {
	snapshot := agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{}}
	for _, task := range tasks {
		shell, ok := task.(*rpc.TaskShellInfo)
		if !ok {
			continue
		}
		item := agentapi.BackgroundTask{
			ID: shell.ID, Command: clip(displaytext.Sanitize(shell.Command), maxToolText),
			Description: clip(displaytext.Sanitize(shell.Description), maxErrorText),
			Status:      string(shell.Status), StartedAt: shell.StartedAt,
			// The CLI keeps a readable log only for a detached shell.
			OutputUnreadable: shell.AttachmentMode == rpc.TaskShellInfoAttachmentModeAttached,
		}
		if shell.CompletedAt != nil {
			item.EndedAt = *shell.CompletedAt
		}
		snapshot.Tasks = append(snapshot.Tasks, item)
	}
	if c.backgroundTasks == nil && len(snapshot.Tasks) == 0 && !c.reportEmptyTasks {
		return
	}
	c.reportEmptyTasks = false
	if c.backgroundTasks != nil && c.backgroundTasks.Known && slices.Equal(c.backgroundTasks.Tasks, snapshot.Tasks) {
		return
	}
	c.backgroundTasks = &snapshot
	c.emitLocked(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &snapshot})
}

// applyTasksLocked discovers reused agents and settles watched follow-ups. Only
// the entry for the exact agent counts. It is idle only when the list says
// idle with a synchronous wait: a follow-up to a background agent wakes the
// main agent. A finished agent is otherwise left completed; a follow-up ends
// with the status the list reports.
func (c *conversation) applyTasksLocked(tasks []rpc.TaskInfo) {
	for _, task := range tasks {
		t, ok := task.(*rpc.TaskAgentInfo)
		if !ok || t.Status != rpc.TaskStatusRunning {
			continue
		}
		sa := c.subs.byID[t.ID]
		if sa == nil {
			continue
		}
		// Right after completion, the list can still describe the original
		// run. Reopen a watched record only with a newer active period.
		if _, watched := c.watch[t.ID]; watched && (t.ActiveStartedAt == nil || !t.ActiveStartedAt.After(sa.EndedAt)) {
			continue
		}
		started := time.Now()
		if t.ActiveStartedAt != nil {
			started = *t.ActiveStartedAt
		}
		c.resumeSubagentLocked(t.ID, started)
	}
	for id, sent := range c.watch {
		sa, t := c.subs.byID[id], agentTask(tasks, id)
		followUp := sa != nil && sa.Status == agentapi.SubagentRunning
		if !followUp && (sa == nil || sa.Status != agentapi.SubagentCompleted) {
			delete(c.watch, id) // an event ended it meanwhile
			continue
		}
		if t == nil && followUp || t != nil && t.Status == rpc.TaskStatusRunning || followUp && taskEndedBefore(t, sent) {
			continue // not settled yet
		}
		delete(c.watch, id)
		status := agentapi.SubagentCompleted
		switch {
		case takesFollowUps(t):
			status = agentapi.SubagentIdle
		case !followUp:
			continue
		case t.Status == rpc.TaskStatusFailed:
			status = agentapi.SubagentFailed
			if t.Error != nil {
				sa.Error = clip(displaytext.Sanitize(*t.Error), maxErrorText)
			}
		case t.Status == rpc.TaskStatusCancelled:
			status = agentapi.SubagentCancelled
			c.expireSubagentLocked(id)
		}
		sa.Status = status
		c.endAgentPhaseLocked(id)
		if sa.EndedAt.IsZero() {
			sa.EndedAt = time.Now()
			if t.IdleSince != nil && t.Status == rpc.TaskStatusIdle {
				sa.EndedAt = *t.IdleSince
			} else if t.CompletedAt != nil {
				sa.EndedAt = *t.CompletedAt
			}
		}
		v := sa.Snapshot()
		c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
	}
	c.settlePhaseLocked(tasks)
}

// agentTask returns the task list's entry for exactly agentID, or nil.
func agentTask(tasks []rpc.TaskInfo, agentID string) *rpc.TaskAgentInfo {
	for _, t := range tasks {
		if a, ok := t.(*rpc.TaskAgentInfo); ok && a.ID == agentID {
			return a
		}
	}
	return nil
}

// taskEndedBefore rejects an old idle or completed period as evidence that
// the new follow-up ended. Entries without a timestamp remain authoritative.
func taskEndedBefore(t *rpc.TaskAgentInfo, sent time.Time) bool {
	if sent.IsZero() || t == nil {
		return false
	}
	if t.Status == rpc.TaskStatusIdle {
		return t.IdleSince != nil && !t.IdleSince.After(sent)
	}
	return t.CompletedAt != nil && !t.CompletedAt.After(sent)
}

// takesFollowUps reports a task-list entry that waits for a follow-up the
// main agent never sees: idle, and started for a synchronous wait.
func takesFollowUps(t *rpc.TaskAgentInfo) bool {
	return t != nil && t.Status == rpc.TaskStatusIdle && t.ExecutionMode != nil && *t.ExecutionMode == rpc.TaskExecutionModeSync
}
