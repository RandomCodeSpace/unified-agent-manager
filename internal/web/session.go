package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type webSession struct {
	previews map[string]*subagentPreviewState
	outputs  map[string]*toolOutputState
	itemSeq  map[string]uint64

	// op serializes provider-facing operations on this session. It is never
	// taken while holding Manager.mu.
	op sync.Mutex

	// Everything below is guarded by Manager.mu.
	id          string
	projectID   string
	provider    string
	model       string
	effort      string
	contextSize string
	agent       string    // custom agent ID; "" is the provider's default
	style       string    // the Thread's output style name; "" is its Project's
	skillChoice *[]string // the Thread's pinned skill list; nil offers every skill
	// openStyle is the style text the open conversation was given.
	openStyle string
	context   *agentapi.Context
	usage     *agentapi.Usage
	name      string
	title     string
	lastModel string
	// renames counts Rename calls, so a title job knows when a rename came
	// while it ran.
	renames uint64
	// titleWait is the title the first message waits for while its turn
	// runs, to be made from the agent's reply; not persisted.
	titleWait *titlePlan
	// titling is set while a title job is queued or running; not persisted.
	titling bool
	// titleFallback is set while title is the deterministic fallback, made
	// from the first message or reply, which a model's title replaces; not
	// persisted.
	titleFallback bool
	// compacting is set while the open conversation compacts, and
	// compactingBackground while it compacts beside the running turn; not
	// persisted.
	compacting, compactingBackground bool
	// asking is set while an aside question waits for its answer (aside.go);
	// not persisted.
	asking bool
	// compactAt is the compaction threshold, in percent, the conversation
	// was last opened with; not persisted.
	compactAt int
	// leavingAssisted is set while the runtime leaves assisted mode: its
	// approving reviews no longer allow requests; not persisted.
	leavingAssisted bool
	// modeUnknown is set when a failed mode change left the runtime's mode
	// unreadable: nothing is allowed automatically until a change or an open
	// sets it again; not persisted.
	modeUnknown bool
	// mode is safe, yolo or assisted: a yolo Task's permission requests are
	// allowed once without asking; an assisted Task's are when the
	// provider's assisted review by assistedApprovalModel approves them.
	mode    store.Mode
	workdir string
	// realWorkdir is workdir with symbolic links resolved, or "" when the same.
	realWorkdir string
	convID      string
	createdAt   time.Time
	updatedAt   time.Time
	base        string
	detail      string
	// checkout is workdir's Git checkout once located (checkout.go); under mu.
	checkout checkoutLocation
	// place is the branch or worktree the Thread started in, nil on the
	// Project's current branch; branch is the branch checked out in workdir,
	// as last read (refreshBranches), not persisted.
	place  *store.WebPlace
	branch string
	// linkOpens is the branch the Thread's branch link opens, as read for
	// branch linkFor when the Thread was last opened (checkBranchLink).
	linkFor, linkOpens string
	// removed is set once the session was deleted; operations waiting on op
	// must not act on it.
	removed bool
	// stage is StageActive, StageSettled or StageArchived.
	stage                 string
	settledAt, archivedAt time.Time
	// settledBy is settledByAuto when the quiet-Task sweep settled it,
	// settledByMerged when the merged-work sweep did, settledByReviewed when
	// the former done check's sweep did, "" when the owner did or it is not
	// settled.
	settledBy string
	// turnSeq increments whenever base changes, so a failed Send only
	// restores the previous state when nothing else changed it meanwhile.
	turnSeq           uint64
	turnTimings       []TurnTiming
	turnChanges       []TurnChanges
	activeTiming      int
	pendingTimingUser string
	timingRevision    uint64

	conv agentapi.Conversation
	// gen identifies the current conversation; events from an older one are
	// ignored.
	gen     uint64
	opening chan struct{}
	// activeAt is when the open conversation last had an event, work or a
	// viewer; zero until the idle sweep first sees it.
	activeAt time.Time

	items     []agentapi.Item
	itemIdx   map[string]int
	itemBytes int
	truncated bool
	// Delta text grows in place; Item.Text remains an immutable string for snapshots.
	textBuffers map[string]*strings.Builder
	// archiveGone is set once the record cannot page back from the oldest
	// retained items. subagentsArchived marks a record read without its
	// subagents' items, which pages read when a subagent is opened;
	// subagentTails keeps what their list entries show.
	archiveGone       bool
	subagentsArchived bool
	subagentTails     map[string]subagentTail

	interactions []*interaction
	ixIdx        map[string]*interaction
	// ask is the summary's Ask, kept while unchanged (pendingAsk).
	ask *Ask
	// budgetView is the summary's Budget, kept while unchanged
	// (budgetViewLocked).
	budgetView *BudgetView
	// planPath is the exact native scratch file, used only for edit attribution.
	planPath    string
	planVersion uint64
	// eventAt is the last provider event, to the minute.
	eventAt time.Time
	// unseenEnd is set when the Task failed or was interrupted while no page
	// had it open, until one opens it (notify.go). It survives a restart.
	unseenEnd bool
	// interruptedAt is when uam's own stop interrupted the turn, while the
	// state it set stands; zero otherwise. It survives a restart.
	interruptedAt time.Time

	subagents []*agentapi.Subagent
	subIdx    map[string]*agentapi.Subagent
	// subagentsOlder is set once held subagent records were forgotten.
	// Every subagent recorded after subagents[subagentHead] is held; the
	// ones before it that are not are paged from the record.
	subagentsOlder   bool
	subagentHead     int
	stoppedSubagents map[string]bool // accepted stops awaiting the provider event
	backgroundTasks  *agentapi.BackgroundTasks
	execution        *agentapi.ExecutionState
	// schedules is the open conversation's native schedule display
	// metadata; replaced whole, never persisted.
	schedules   *agentapi.ScheduleSnapshot
	mcpStatus   *agentapi.MCPStatusSnapshot
	mcpRevision uint64
	// skills lists the skills the Task's agents loaded, first loaded first;
	// skillLoads holds the loads counted, by agent and provider event ID.
	skills     []LoadedSkill
	skillLoads map[[2]string]struct{}

	commandSubmissions []Submission
	commandLedger      string
	stopSeq            uint64
	stopReason         string // what asked to stop the turn when not the owner; the cancelled state's detail
	stopBy             string // the stop reason of uam's stop request (stopOwner, stopTimeLimit); "" when none
	submissions        []Submission
	last               *Submission
	createReq          string
	fork               *store.ForkLineage
	// rewind is the latest native rewind receipt (rewind.go).
	rewind *store.WebRewind
	// stopCode is why the last turn was cancelled (SessionSummary.StopReason),
	// until the next turn starts.
	stopCode string
	// failKind is the provider's category for the error in detail
	// (SessionSummary.FailureKind); "" when the state has none.
	failKind agentapi.ErrorKind
	// turnActivity is the running turn's live activity (turn_activity.go);
	// not persisted.
	turnActivity TurnActivity
	// workPhase is the provider's latest phase, of the running turn or, between
	// turns, of the subagents that outlived it and compaction, which
	// turnActivity shows with the owner's pending requests (phase.go).
	workPhase *agentapi.Phase
	// todoBase is the todo list as the running or last turn found it.
	todoBase TodoView
	// turnIntent is the running turn's last intent, for its todo snapshot.
	turnIntent string
	// turnTodos are the todo snapshots of ended turns that flush has yet to
	// append to the Task's turn-todos.jsonl (turn_todos.go).
	turnTodos []TurnTodos
	// planRecords are the plan review snapshots and aliases flush has yet
	// to append to the Task's plan-reviews.jsonl (plan.go).
	planRecords []planReviewRecord
	// subagentPrompts are the outcomes of follow-ups to subagents, within
	// maxSubmissions. They are not Task submissions: last never holds one.
	subagentPrompts []Submission

	// queue holds prompts waiting for the running turn, oldest first. The
	// owner's prompts are saved with the Task (savedQueue) and come back
	// paused after a restart; another Thread's message is not.
	queue []QueuedPrompt
	// queueSending is the head removed for a send whose outcome is pending.
	queueSending string
	// queuePaused keeps the queue from draining after a turn that did not
	// complete or a prompt that was not accepted, until the user resumes or
	// clears it. It is never set with an empty queue.
	queuePaused bool
	// queueChanged tells changedLocked to publish the queue.
	queueChanged bool

	// uploads are the Task's stored attachments and tool images by ID.
	uploads map[string]*upload
	// imageMu serializes storing tool images, so an image the live stream
	// and the history both carry is stored once. It is taken before
	// Manager.mu, never while holding it.
	imageMu sync.Mutex

	// history is HistoryLoaded once items and subagents hold the
	// conversation's record, HistoryLoading while it is read, and
	// HistoryUnavailable, with historyReason, when it could not be; "" until
	// a viewer asks. historyRead marks a record read without opening the
	// conversation: only such a record is dropped when idle. historyUsed is
	// when a viewer last asked for it, historyFailed when a read last failed.
	// historyLogged is the failure last warned about: the same one again,
	// as every look at a gone conversation, is a debug line.
	history, historyReason     string
	historyLogged              string
	historyRead                bool
	historyRecordFailed        bool // the latest live record read failed
	historyCancel              context.CancelFunc
	historyDone                <-chan struct{}
	historyGen                 uint64
	historyBytes               int
	historyUsed, historyFailed time.Time
	// terminalID preserves links imported before terminal support was retired.
	// It still requires the provider's external-holder check before sending.
	terminalID string
	imported   bool
	// spawnedBy is the Task whose uam_create_task call created this one. It
	// never changes, and such a Task does not get the tool itself.
	spawnedBy string
	// childNotes are the end notes of the Tasks this one created, and their
	// questions to the owner, waiting for its agent's next tool call or turn
	// (create_task.go).
	childNotes []childNote
	// peerArm is the peer message a send is starting a turn with, and
	// peerTurn the one the running turn answers, once the turn starts
	// (messages.go).
	peerArm, peerTurn *store.TurnPeer
	// routineID is the routine whose run created this Task. It never
	// changes, and such a Task does not get uam_create_task either.
	routineID string

	// fuses are transient Task-local refusals shared with subagents.
	fuses map[fuseKey]*fuseEntry

	// budget is the owner's spending limit (budget.go). budgetWarned is set
	// once the agent was told to wrap up, budgetStopping while uam's stop
	// for it is on its way. wrap is the wrap-up at the limit, and handoff
	// what it left.
	budget                       store.Budget
	budgetWarned, budgetStopping bool
	wrap                         *budgetWrap
	handoff                      *store.WebHandoff

	// memoryNotes are the notes uam re-pins after a compaction (memory.go);
	// memoryThrough is the newest item the last note job read, memoryRun
	// the job in flight (closed when it ends) and memoryPin what the main
	// agent's next tool call carries.
	memoryNotes   []store.WebMemoryNote
	memoryThrough time.Time
	memoryRun     chan struct{}
	memoryPin     string

	// subagentLabels are the Utility model's short subagent names by agent
	// ID (subagent_labels.go).
	subagentLabels map[string]string

	// messageOutcomes are the messages of this Thread's dropped pairs that
	// were not delivered (messages.go).
	messageOutcomes []store.WebPeerMessage

	// edits maps each file the Task's edit tools touched, as the tool gave
	// its path, to when it was last touched, and turnStart is when the
	// latest ordinary prompt to the main agent began (task_changes.go).
	// Both outlive trimmed and evicted transcripts. editsKnown is set once
	// the whole record passed through them. diff is the cached task-scope
	// total; diffRunning and diffDirty schedule its recount.
	trailTurn  time.Time
	trailPaths map[string]bool
	edits      map[string]time.Time
	turnStart  time.Time
	// activity is what the turn evidence keeps of the main agent's items,
	// by ID (turn_evidence.go). Closed and staged Tasks retain these bounded
	// facts without item bodies; other evicted transcripts rebuild them.
	activity      map[string]activity
	editsKnown    bool
	diff          *DiffStat
	nativeChanges bool
	// An on-demand native read started before later activity cannot restore
	// the now-unknown total after that activity invalidates it.
	nativeDiffRevision uint64
	// nativePaths are the files the latest native read listed: with edits,
	// the Thread's own files the Settle guard checks (git_pending.go). They
	// persist, with when a read first listed them, for a closed Thread.
	nativePaths            []string
	nativePathsAt          time.Time
	diffRunning, diffDirty bool
	// targets caches folder searches for names that miss at the top of the
	// Thread's folder (file_resolver.go).
	targets map[string]targetAnswer
	// idleToolEdits counts tool results that landed while no turn ran: a
	// turn's native capture still in flight reads again after them
	// (turn_changes.go).
	idleToolEdits uint64

	// rerunOf is the Task whose last message this one ran again (assist.go).
	rerunOf string
	// outcome is the last completed turn's outcome line.
	outcome string
	// suggestions are the replies suggested for the transcript's last item;
	// suggesting is the call in flight for them.
	suggestions *store.WebSuggestions
	suggesting  *suggestionRun

	persisted persistKey
}

type interaction struct {
	agentapi.Interaction
	// answering is set while this service's answer is with the provider, so
	// a concurrent second answer is refused instead of racing it.
	answering bool
	// yolo is set once yolo mode, or an approving assisted review when
	// assisted is also set, claimed the interaction; it no longer waits for
	// the user.
	yolo, assisted bool
	// reply is set on a question uam itself asks (owner_confirm.go): the
	// answer goes to it instead of the provider, and expiry closes it.
	reply chan agentapi.Answer
}

// public is ix as a browser sees it: Auto says yolo mode is answering it.
func (ix *interaction) public() agentapi.Interaction {
	out := ix.Interaction
	out.Auto = ix.yolo && ix.State == agentapi.InteractionPending
	return out
}

// persistKey is the durable part of a session; sessions.json is written only
// when it changes, never per streamed token.
type persistKey struct {
	timingRevision                                                                                                   uint64
	commandResult                                                                                                    *agentapi.CommandResult
	turn, detail, name, convID, reqID, reqStatus, commandLedger, projectID, model, effort, contextSize, title, stage string
	mode                                                                                                             store.Mode
	settledAt, archivedAt                                                                                            time.Time
	settledBy                                                                                                        string
	outcome                                                                                                          string
	stopReason                                                                                                       string
	failKind                                                                                                         agentapi.ErrorKind
	budget                                                                                                           store.Budget
	handoff                                                                                                          store.WebHandoff
	agent                                                                                                            string
	style                                                                                                            string
	skills                                                                                                           string // skillsKey
	place                                                                                                            store.WebPlace
}

func newSession(id, provider, name, workdir, convID string, created time.Time) *webSession {
	real, err := filepath.EvalSymlinks(workdir)
	if err != nil || real == filepath.Clean(workdir) {
		real = ""
	}
	return &webSession{
		id: id, provider: provider, name: name, workdir: workdir, realWorkdir: real, convID: convID,
		createdAt: created, updatedAt: created, base: StateIdle, mode: store.ModeSafe, activeTiming: -1,
		itemIdx: map[string]int{}, ixIdx: map[string]*interaction{}, subIdx: map[string]*agentapi.Subagent{},
	}
}

func (s *webSession) setBase(state, detail string) {
	s.base = state
	s.detail = detail
	s.failKind = "" // it describes the detail just replaced
	s.interruptedAt = time.Time{}
	s.turnSeq++
}

// openCompactAt is the open conversation's compaction threshold in
// percent; 0 while none is open.
func (s *webSession) openCompactAt() int {
	if s.conv == nil {
		return 0
	}
	return s.compactAt
}

func (s *webSession) pendingKinds() (permissions, questions int) {
	for _, ix := range s.interactions {
		if ix.State != agentapi.InteractionPending || ix.yolo {
			continue
		}
		if ix.Kind == agentapi.InteractionPermission {
			permissions++
		} else {
			questions++
		}
	}
	return permissions, questions
}

// state derives the reported state: opening wins, then pending permission,
// then pending question, then the turn/lifecycle state.
func (s *webSession) state() string {
	if s.opening != nil {
		return StateStarting
	}
	permissions, questions := s.pendingKinds()
	switch {
	case permissions > 0:
		return StateAwaitingPermission
	case questions > 0:
		return StateAwaitingAnswer
	default:
		return s.base
	}
}

func (s *webSession) durableState() string {
	state := s.state()
	if state == StateStarting {
		return s.base
	}
	return state
}

func (s *webSession) key() persistKey {
	k := persistKey{timingRevision: s.timingRevision, turn: s.durableState(), detail: s.detail, name: s.name, convID: s.convID, projectID: s.projectID, model: s.model, effort: s.effort, contextSize: s.contextSize, agent: s.agent, style: s.style, skills: skillsKey(s.skillChoice), title: s.title, mode: s.mode,
		stage: s.stage, settledAt: s.settledAt, archivedAt: s.archivedAt, settledBy: s.settledBy, outcome: s.outcome, stopReason: s.stopCode, failKind: s.failKind, budget: s.budget}
	if s.handoff != nil {
		k.handoff = *s.handoff
	}
	if s.last != nil {
		k.reqID, k.reqStatus = s.last.RequestID, s.last.Status
		k.commandResult = s.last.CommandResult
	}
	k.commandLedger = s.commandLedger
	if s.place != nil {
		k.place = *s.place
	}
	return k
}

// nativePathsRecord is s's native paths as stored; nil before a read. The
// caller holds mu.
func nativePathsRecord(s *webSession) *store.WebNativePaths {
	if s.nativePaths == nil {
		return nil
	}
	return &store.WebNativePaths{Paths: slices.Clone(s.nativePaths), At: s.nativePathsAt}
}

func busy(state string) bool {
	switch state {
	case StateWorking, StateAwaitingPermission, StateAwaitingAnswer, StateStarting:
		return true
	}
	return false
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
