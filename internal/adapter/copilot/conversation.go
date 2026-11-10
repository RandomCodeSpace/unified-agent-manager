package copilot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// conversation is one open Copilot session. Every Emit happens under mu and
// only while the conversation is open, so nothing is delivered after Close.
type conversation struct {
	p      *webProvider
	client sdkClient
	sess   sdkSession
	id     string
	sink   agentapi.EventSink
	hooks  agentapi.ToolHooks
	// skillDirs are uam's built-in skill folders; reading a file in them
	// needs no approval (preToolUse).
	skillDirs []string

	mu        sync.Mutex
	closed    bool
	pending   map[string]*interaction
	tokenSeen map[string]bool
	// tokenOrder holds tokenSeen's IDs oldest first, so only the last
	// maxTokenSeen usage events stay remembered for the conversation.
	tokenOrder []string
	// questions pairs user_input.requested events with ask_user callbacks.
	questions        questionLinks
	elicits          elicitLinks // elicitation.requested events with elicitation callbacks
	stoppedSubagents map[string]bool
	approvedOnce     map[string]int // owner "Allow once" answers per session-option scope
	prompting        string         // the agent PromptSubagent is sending a follow-up to
	tr               *transcript
	subs             *subagentLog
	turnErr          string
	turnFailure      agentapi.TurnFailure // what came with turnErr; read only while it is set
	idles            int
	// Once the provider reports foreground idle, session.idle is only its
	// broader background-work boundary and must not end another turn.
	assistantIdleSeen bool
	autopilotTurn     bool
	// taskCompleted marks a main-agent session.task_complete in the current
	// turn: the session.idle that follows ends an autopilot run even though
	// it still reports the autopilot mode.
	taskCompleted bool
	// autopilotStopped marks a main-agent session.autopilot_stopped in the
	// current turn (CLI 1.0.103+): the run is over, so its assistant.idle
	// ends the turn without waiting for a session.idle that an attached
	// shell withholds.
	autopilotStopped bool
	completion       *agentapi.TaskCompletion
	completionReady  bool
	foregroundIdle   bool
	// turnRunning is set from a foreground turn's start until its end. Send
	// refuses a prompt while it is: the CLI holds any prompt sent during a
	// turn until session.idle, which never comes while a background shell runs.
	turnRunning bool
	// compaction is the main conversation's compaction in progress.
	compaction compactionState
	// views holds the hash of each view result an agent has in its context
	// (readOnceLocked); viewRepeats counts the turn's replaced repeats.
	views       map[readKey][sha256.Size]byte
	viewRepeats int
	// turnAt is when the running turn started. sending counts prompts on
	// their way to the CLI, which Stop treats as a running turn.
	turnAt  time.Time
	sending int
	// lastEvent and lastEventAt are the latest session event's kind and
	// arrival, for the report of a stop the CLI did not confirm.
	lastEvent   string
	lastEventAt time.Time
	// stopping is the running turn's pending stop check, and stopNote the
	// cancelled turn's note when uam ends the turn itself (stop.go).
	stopping *stopWatch
	stopNote string
	// modelWaitAt is when the main agent's pending model request began, zero
	// while none is; stall is the running turn's stall watch (stall.go).
	modelWaitAt time.Time
	stall       stallState
	// modelGapMax is the longest silence a main-agent model request showed
	// before an event answered it, over the conversation's life; the stall
	// bound adapts to it (stallBoundLocked).
	modelGapMax time.Duration
	// modeOps counts execution mode choices (ensureExecutionMode), so a
	// mode reset Stop deferred does not override a later one.
	modeOps uint64
	// The latest ordinary main-agent user item and its exact native boundary.
	turnChangeUser, turnChangeEvent string
	// idleUnresolved marks a main-agent assistant.idle seen while the mode
	// was autopilot or unknown. The CLI withholds session.idle, and with it
	// the autopilot continuation, while background work runs, so a read
	// showing a non-autopilot mode or a running attached shell ends the turn
	// instead.
	idleUnresolved         bool
	backgroundTasks        *agentapi.BackgroundTasks
	schedules              scheduleReads
	execution              *agentapi.ExecutionState
	executionRevision      uint64
	plans                  planVersions
	planAnswered           []answeredPlan
	planStopped            bool   // Stop/end refuses late callbacks from the same foreground turn
	planEpoch              uint64 // expiry/new foreground turn invalidates outside-lock plan reads
	planPath               string
	planVersion            uint64
	lastPlanChange         string
	planReading, planAgain bool
	mcpStatus              agentapi.MCPStatusSnapshot
	mcpRevision            uint64
	control                sync.Mutex
	// agent is the selected custom agent's ID, "" for the default agent;
	// written under control and mu.
	agent string
	// githubSwitch is held while the built-in GitHub MCP server is turned
	// on or off, so the last switch applies the latest setting.
	githubSwitch     sync.Mutex
	reportEmptyTasks bool
	// turnModel is the model of the turn's latest main-agent model call.
	turnModel string
	// act is the running turn's live activity (activity.go).
	act activity
	// phase is the running turn's model calls and tools in flight (phase.go).
	phase phaseState
	// todos is the todo list and its reads (todos.go).
	todos todoReads
	// byom is what the session has of the custom models.
	byom registered
	// selected is the model this client last selected: at create or by
	// SetModel; "" until then on a resumed session.
	selected string
	// selection is the Task's complete model selection, which agent changes
	// keep (agent.go); nil while unknown. Guarded by mu.
	selection *modelSelection
	// tools gates the session's uam tools, uamTools; nil when it has none.
	tools    *toolGate
	uamTools []copilot.Tool
	// usage is the latest main-agent context report, kept so a model call's
	// cache report can be sent with it.
	usage agentapi.Context
	// nanoAIU is the conversation's cost so far in nano-AI units: the
	// session total the CLI last recorded plus each model call reported
	// since. usageSent is what was last emitted.
	nanoAIU, usageSent float64
	// steers includes sends in flight so idle can record their outcome before
	// the CLI returns the message ID. Accepted, unused steers remain here.
	steers []*steer
	// steering counts Steer calls in flight. While one is, seen records the
	// main-agent user messages the CLI used, because a steer can be used
	// before the Send that carried it returns its message ID.
	steering int
	seen     map[string]bool
	// watch holds the agents only session.tasks.list can settle: a finished
	// one that may take follow-ups, and one running a follow-up, keyed to the
	// time the follow-up was sent. listing is set while a read runs; relist
	// asks it for one more.
	watch           map[string]time.Time
	listing, relist bool
	// retryTasks reads the list again webTasksRetry after a failed read.
	retryTasks *time.Timer
	// taskRPC orders task-list reads and follow-up sends, so a list read from
	// before a send never overwrites the follow-up it started.
	taskRPC sync.Mutex
	// aside cancels the wait of the aside question in flight (aside.go).
	aside context.CancelFunc
}

func (c *conversation) ID() string { return c.id }

func (c *conversation) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *conversation) emitLocked(e agentapi.Event) {
	if !c.closed {
		c.sink.Emit(e)
	}
}

// SetTitle names the session with session.name.set. The CLI records the name
// as set by the user, so neither it nor the terminal CLI renames it later.
func (c *conversation) SetTitle(ctx context.Context, title string) error {
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	if err := c.sess.SetName(ctx, title); err != nil {
		c.p.poke()
		return fmt.Errorf("copilot rename: %s", errText(err))
	}
	return nil
}

func (c *conversation) Diff(ctx context.Context) ([]agentapi.FileDiff, error) {
	return c.nativeDiff(ctx)
}

func (c *conversation) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.releaseTurnLocked()
	c.expirePlansLocked()
	c.plans = planVersions{}
	perms := c.expireLocked()
	c.endIdleLocked()
	c.stopSchedulesLocked()
	// The detach is due before anything can reopen the conversation.
	detached := c.p.detachStarted(c.id)
	c.closed = true
	clear(c.pending)
	if c.aside != nil {
		c.aside()
	}
	c.mu.Unlock()
	return c.release(ctx, perms, detached)
}

// release lets go of a conversation that has ended locally: it is no
// longer tracked, its tools stop, the permission requests perms are
// answered as unavailable, and its SDK session detaches, all within
// webStopTimeout. detached, from detachStarted before the conversation
// ended, is called once the detach returns.
func (c *conversation) release(ctx context.Context, perms []string, detached func()) error {
	c.p.forget(c)
	if c.tools != nil {
		c.tools.stop()
	}
	ctx, cancel := context.WithTimeout(ctx, webStopTimeout)
	defer cancel()
	for _, id := range perms {
		_, _ = c.sess.RespondPermission(ctx, id, &rpc.PermissionDecisionUserNotAvailable{})
	}
	done := make(chan error, 1)
	go func() {
		defer log.Recover("copilot conversation release", &c.mu, &c.p.mu)
		defer detached()
		err := c.sess.Disconnect()
		if err == nil {
			if releaseErr := c.p.recordUsageSession(c.client, c.id, false); releaseErr != nil {
				log.Warn("release copilot usage ownership failed", "conversation", c.id, "error", releaseErr)
				err = fmt.Errorf("release usage ownership: %w", releaseErr)
			}
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			c.p.poke()
			return fmt.Errorf("close copilot conversation: %s", errText(err))
		}
		return nil
	case <-ctx.Done():
		c.p.poke()
		return fmt.Errorf("close copilot conversation: %w", ctx.Err())
	}
}

func (c *conversation) exit(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exitLocked(reason)
}

func (c *conversation) exitLocked(reason string) {
	c.endLocked(agentapi.Event{Kind: agentapi.EventExit, Error: reason})
}

// endLocked ends the conversation here and reports exit, an EventExit.
func (c *conversation) endLocked(exit agentapi.Event) {
	if c.closed {
		return
	}
	c.releaseTurnLocked()
	c.expireLocked()
	clear(c.pending)
	c.endIdleLocked()
	c.emitLocked(exit)
	c.stopSchedulesLocked()
	c.closed = true
}

// endIdleLocked reports idle subagents as completed when the conversation
// ends: without a live task list nothing says they still take follow-ups.
func (c *conversation) endIdleLocked() {
	for _, id := range c.subs.order {
		if sa := c.subs.byID[id]; sa.Status == agentapi.SubagentIdle {
			sa.Status = agentapi.SubagentCompleted
			v := sa.Snapshot()
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
		}
	}
	clear(c.watch)
}
