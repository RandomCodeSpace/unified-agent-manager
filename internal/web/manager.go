package web

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Provider call bounds. Each derives from the service context, never from an
// HTTP request, so a browser leaving never aborts provider work.
const (
	checkTimeout   = 15 * time.Second
	openTimeout    = 2 * time.Minute
	sendTimeout    = 2 * time.Minute
	controlTimeout = 30 * time.Second
	// viewWait bounds how long a page load waits for a lazy open before it
	// shows the session as starting; the open itself continues.
	viewWait = 15 * time.Second
)

const (
	maxPromptBytes  = 256 << 10
	maxNameRunes    = 120
	maxDetailRunes  = 300
	maxSubmissions  = 64
	maxQueue        = 20
	recentWorkdirsN = 10
	// modelsMaxAge is how long a model catalog is used before /api/meta
	// reloads it; entitlements can change while the service runs.
	modelsMaxAge = 5 * time.Minute
	// maxHistoryReads bounds the read-only transcript loads running at once.
	maxHistoryReads = 2
	// historyIdle is how long a transcript read without opening the
	// conversation stays in memory after a viewer last asked for it;
	// historyRetry is how soon a failed read is tried again.
	historyIdle  = 10 * time.Minute
	historyRetry = time.Minute
	historySweep = time.Minute
)

// stopOpWait bounds how long Stop waits for another operation on the Thread.
// A var for tests.
var stopOpWait = 3 * time.Second

// lockWithin takes mu if it can within d, and reports whether it did.
func lockWithin(mu *sync.Mutex, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for !mu.TryLock() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// idleClose is how long an open conversation stays open with nothing
// running and no viewer before the sweep closes it; a var for tests.
var idleClose = 15 * time.Minute

// Manager owns every web session, its provider conversation, and the event
// fan-out to browsers.
//
// Two locks per session keep provider callbacks non-blocking: session.op
// serializes operations that call the provider (open, send, close) and may be
// held across those calls, while Manager.mu guards all observable state and is
// only ever held for short, allocation-bounded sections. Provider events take
// only Manager.mu, so a slow provider call or an absent browser never delays
// event processing.
type Manager struct {
	store     *store.Store
	providers map[string]agentapi.Provider
	order     []string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// persistMu orders sessions.json writes: each write snapshots state after
	// taking it, so a later write never carries older state. A write appends
	// the queued turn records outside mu and then takes them from the queues'
	// front, so anything else that removes queued records (a rewind) holds
	// persistMu too. It is taken before mu, never while holding it.
	persistMu sync.Mutex
	wake      chan struct{}

	// surfaceMu serializes uam_surface calls' changes to the Threads'
	// surfaces files (surface.go).
	surfaceMu sync.Mutex

	// timeSplits caches each journal's model/local time split.
	timeSplits timeSplitCache
	// projectMu serializes Project changes, Task creation's final write and
	// Project removal, each of which checks and then writes the store. It is
	// taken before any session.op and never while holding mu.
	projectMu sync.Mutex
	// branchMu orders Project branch reads, so an older read never replaces
	// a newer one. It is never taken while holding mu.
	branchMu sync.Mutex
	// pendingMu orders unsaved git work reads the same way (git_pending.go).
	pendingMu sync.Mutex
	// settingsMu orders settings changes, so the stored and published
	// settings match. It is never taken while holding mu.
	settingsMu sync.Mutex
	// recent shares RecentWorkdirs' store reads between concurrent callers;
	// pins the shown pinned charts of every Project, for storedPins.
	recent sharedReads[[]string]
	pins   sharedReads[map[string][]store.WebChart]

	// imageJobs feeds the one goroutine that stores tool images, so Emit
	// never writes a file. imageWG counts the jobs not yet finished; tests
	// wait on it. storeImageHook, when set, runs before each job is stored.
	imageJobs      chan imageJob
	imageWG        sync.WaitGroup
	storeImageHook func()
	// signIns holds the MCP sign-ins waiting for a pasted callback address.
	signIns mcpSignIns
	// devices holds each provider's device sign-in in progress, or its last outcome.
	devices deviceSignIns
	// cli holds each provider CLI's releases last read and its update.
	cli cliUpdates
	// cliKick wakes a pending CLI restart when a Task changes.
	cliKick chan struct{}
	// glab owns background origin/auth refreshes outside the Manager lock.
	glab   glabGovernor
	gitlab gitLabMonitor
	// mrFiles are the merge request descriptions written for glab (mr_description.go).
	mrFiles mrDescriptionFiles

	mu       sync.Mutex
	infos    map[string]ProviderInfo
	modelsAt map[string]time.Time
	fetching map[string]bool
	projects map[string]*Project
	settings Settings
	// links holds the account each provider is linked to (store
	// WebAccountLinks); accountAt when each provider's account was last read.
	links     map[string]store.AccountLink
	accountAt map[string]time.Time
	sessions  map[string]*webSession
	dirty     map[string]struct{}
	// lazyFlush is true while a flush waits for lazyFlushDelay (flushLaterLocked).
	lazyFlush bool
	creating  map[string]chan struct{}
	epoch     string
	seq       uint64
	subs      map[*Subscriber]struct{}
	closed    bool
	now       func() time.Time
	// pick returns a random int in [0, n) for badge choices; tests replace
	// it before Start.
	pick func(n int) int
	// push keeps the Web Push keys and subscriptions (notify.go).
	push pushStore
	// noticeJournal contains only events produced by this manager.
	noticeJournal    noticeJournal
	connectedNotices *connectedNotices
	// branchAt is when each Project's branch was last read.
	branchAt map[string]time.Time
	// worktreeRoot, when set, replaces ~/.uam/worktrees as the default
	// worktrees folder (defaultWorktreesDir).
	worktreeRoot string
	// oldWorktreeRoots are the folders new worktrees went in before
	// Settings' worktrees_dir last changed (worktreeRoots); under mu.
	oldWorktreeRoots []string
	// worktrees is the removal, recreation and size bookkeeping of Thread
	// worktrees (worktree_cleanup.go).
	worktrees worktreeState
	// gitWrites holds each git write action running, by the real path of its
	// repository, closed when it ends (git_actions.go). A turn starting there
	// waits for it.
	gitWrites map[string]chan struct{}
	// searchSlot lets one search run at a time (search.go).
	searchSlot chan struct{}
	// pendingTop is the real path of the repository holding each Task
	// directory read, "" outside one; gitPending is each repository's
	// unsaved work as last read (git_pending.go).
	pendingTop map[string]string
	gitPending map[string]*GitPending
	// quota is each usage provider's quota cache; quotaPolled is when the
	// latest read began.
	quota       map[string]*quotaCache
	quotaPolled time.Time
	// utilityLive counts the Utility calls in flight; modelCallsTimer is the
	// pending model_calls frame (phase.go).
	utilityLive     int
	modelCallsTimer *time.Timer
	// saveError is why the latest flush failed, "" once one succeeds; the
	// snapshot and save_status frames carry it to the browser.
	saveError string
	// quotaKick asks the usage loop for a read; quotaTick is how often the
	// loop checks for a due one (tests set it before Start).
	quotaKick chan struct{}
	quotaTick time.Duration
	// titles counts the Utility jobs (titles, suggested replies, turn
	// outcomes and commit drafts), which Shutdown waits for
	// before providers stop: a job deletes its throwaway conversation on the
	// way out.
	// titleSlots bounds provider calls shared by titles, suggested replies and
	// turn outcomes.
	titles     sync.WaitGroup
	titleSlots chan struct{}
	// reads holds a slot for each read-only transcript load in progress.
	reads chan struct{}
	// archive caches windows of records read past the retained transcript.
	archive archiveCache
	// Active Server registries; publication/removal is ordered by mu.
	fileGrants map[*tempGrants]struct{}
	// terminals are the open web terminals (terminal.go); terminalWG counts
	// them until they end, and Shutdown waits for it.
	terminals  map[*terminal]struct{}
	terminalWG sync.WaitGroup
	// hostTools, when set, returns the host tools the conversation of a Task
	// registers, spawned when uam_create_task or a routine's run created the
	// Task, and the CallTool bound to that Task. It runs with mu held, so it
	// takes no lock. NewManager sets it to uam_create_task, uam_chart, uam_form and the others
	// (create_task.go).
	hostTools func(taskID string, spawned bool) ([]agentapi.HostTool, func(context.Context, agentapi.HostToolCall) agentapi.HostToolResult)
	// skillDirs hold the built-in skills every Task's conversation loads
	// (skills.go); Start installs them.
	skillDirs []string
	// calls are each Task's host tool calls in progress, which settling or
	// archiving the Task cancels and waits for (create_task.go). Guarded by
	// mu.
	calls map[string]map[*taskCall]struct{}
	// spawns maps each Task uam_create_task is creating to the Task that
	// called it, until the create ends, and spawnWait bounds how long the
	// call waits for it: viewWait, which tests shorten (create_task.go).
	// Guarded by mu.
	spawns    map[string]string
	spawnWait time.Duration
	// pairs are the message pairs between Threads, keyed by pair ID, and
	// pairsDirty is set when flush must write them (messages.go). Guarded
	// by mu.
	pairs      map[string]store.WebMessagePair
	pairsDirty bool
	// utility is the Utility log and today's count of Utility calls
	// (utility.go). suggestPauseLogged is set once a paused suggestion
	// call is logged: every look at a Thread asks again while the pause
	// lasts, and only the first is a log line (assist.go).
	utility            utilityLog
	counts             uamCounts             // uam's own action counts (uam_counts.go)
	fetchFuses         map[string]*fetchFuse // guarded by mu; saved beside the store (fuses.go)
	suggestPauseLogged atomic.Bool
	tokens             tokenLedger   // guarded by mu; persisted by the existing flush loop
	harness            *harnessUsage // optional local harness collector; snapshots guarded by mu
	usageHome          string        // set by RunDaemon before Start; empty disables local discovery
	// chartRuns bounds pinned chart refreshes (chart_pins.go).
	chartRuns chartRuns
	// drawings are the Threads' drawing indexes (drawings.go), and policy
	// the page's Content-Security-Policy, which follows the draw.io
	// setting (drawioPolicy).
	drawings drawingStore
	policy   atomic.Pointer[string]
	// routines is the routine scheduler (routines.go).
	routines routineState
	// binary follows the uam binary on disk for a restart onto a new one
	// (restart.go); nil when the service cannot restart itself. RunDaemon
	// sets it before Start.
	binary *binaryWatch
}

// NewManager builds a manager for providers. Start must run before use.
func NewManager(st *store.Store, providers []agentapi.Provider) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		store:     st,
		epoch:     rand.Text(),
		providers: map[string]agentapi.Provider{},
		links:     map[string]store.AccountLink{},
		accountAt: map[string]time.Time{},
		ctx:       ctx,
		cancel:    cancel,
		wake:      make(chan struct{}, 1),
		infos:     map[string]ProviderInfo{},
		modelsAt:  map[string]time.Time{},
		fetching:  map[string]bool{},
		projects:  map[string]*Project{},
		sessions:  map[string]*webSession{},
		dirty:     map[string]struct{}{},
		creating:  map[string]chan struct{}{},
		subs:      map[*Subscriber]struct{}{},
		now:       time.Now,
		pick:      randomPick,
		branchAt:  map[string]time.Time{},
		quota:     map[string]*quotaCache{},
		quotaKick: make(chan struct{}, 1),
		cliKick:   make(chan struct{}, 1),
		quotaTick: quotaCheck,
		spawnWait: viewWait,

		titleSlots: make(chan struct{}, maxTitleJobs),
		imageJobs:  make(chan imageJob, maxImageJobs),
		reads:      make(chan struct{}, maxHistoryReads),
	}
	for _, p := range providers {
		if p == nil {
			continue
		}
		if _, dup := m.providers[p.Name()]; dup {
			continue
		}
		m.providers[p.Name()] = p
		m.order = append(m.order, p.Name())
	}
	m.hostTools = m.taskToolsLocked
	if st != nil {
		m.push.path = filepath.Join(filepath.Dir(st.Path()), pushFileName)
	}
	return m
}

var knownStates = map[string]bool{
	StateIdle: true, StateStarting: true, StateWorking: true, StateAwaitingPermission: true,
	StateAwaitingAnswer: true, StateCompleted: true, StateCancelled: true, StateFailed: true,
	StateInterrupted: true, StateClosed: true,
}

const interruptedDetail = "the uam web service stopped while this turn was running"

// restartedDetail ends a routine run whose turn a restart interrupted.
const restartedDetail = "service restarted"

// Start checks providers, loads their model catalogs and the web records,
// assigns records from before Projects existed to a Project, and gives each
// Project without a valid badge one. A provider whose check fails is listed
// as unavailable; it is not fatal.
func (m *Manager) Start(ctx context.Context) error {
	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load web sessions: %w", err)
	}
	// What a killed run left mid-write; only this service writes these files.
	removeTempFiles(filepath.Dir(m.store.Path()), ".token-usage-", ".worktrees-", ".uam-counts-", ".fetch-fuses-", utilityLogFile+".tmp.")
	m.loadTokenLedger()
	usageLedger, err := m.prepareHarnessUsage()
	if err != nil {
		return err
	}
	startedUsage := false
	defer func() {
		if usageLedger != nil && !startedUsage {
			_ = usageLedger.Close()
		}
	}()
	// The catalogs include the custom models, so providers get them first.
	m.setCustomModels(cfg.WebSettings.CustomModels)
	m.setSubagentModels(cfg.WebSettings.SubagentModels)
	m.setGitHubMCP(cfg.WebSettings.GitHubMCP)
	m.mu.Lock()
	maps.Copy(m.links, cfg.WebAccountLinks)
	m.mu.Unlock()
	infos := m.checkProviders(ctx)
	if needsProject(cfg) || len(badgeless(cfg.WebProjects)) > 0 {
		now := m.now()
		err := m.store.Update(func(c *store.Config) error {
			if err := assignProjects(c, now); err != nil {
				return err
			}
			assignBadges(c, m.pick)
			cfg = *c
			return nil
		})
		if err != nil {
			// Keep serving: the assignment holds for this run. A record that
			// later gets its project_id written without the Project, or a
			// Project without its badge, is assigned again on the next start.
			log.Warn("assign web sessions to projects failed", "error", err)
			if err := assignProjects(&cfg, now); err != nil {
				return fmt.Errorf("assign web sessions to projects: %w", err)
			}
			assignBadges(&cfg, nil)
		}
	}
	removePlannerFiles(ctx, filepath.Dir(m.store.Path()))
	// Tasks open without the built-in skills when they cannot be installed.
	var skillDirs []string
	skills, err := filepath.Abs(filepath.Join(filepath.Dir(m.store.Path()), "skills"))
	if err == nil {
		err = installSkills(skills)
	}
	if err != nil {
		log.Warn("install the built-in skills failed", "dir", skills, "error", err)
	} else {
		skillDirs = []string{skills}
	}
	m.mu.Lock()
	m.infos = infos
	m.skillDirs = skillDirs
	for name, info := range infos {
		if info.Available {
			m.modelsAt[name] = m.now()
		}
	}
	for id, p := range cfg.WebProjects {
		m.projects[id] = &Project{ID: p.ID, Name: loadedName(p.Name, p.Dir), Dir: p.Dir, CreatedAt: p.CreatedAt, Badge: Badge(p.Badge), Charts: shownPins(p.Charts), BranchLink: loadedBranchLink(p.BranchLink), OutputStyle: p.OutputStyle}
	}
	m.settings = Settings{TokenPrices: cfg.WebSettings.TokenPrices, SendDefault: cmp.Or(cfg.WebSettings.SendDefault, store.WebSendSteer), Terminal: cfg.WebSettings.Terminal, DefaultProjectDir: cfg.WebSettings.DefaultProjectDir, HiddenModels: cfg.WebSettings.HiddenModels, SubagentModels: cfg.WebSettings.SubagentModels, TitleModel: cfg.WebSettings.TitleModel,
		CustomModels: customModelsView(cfg.WebSettings.CustomModels), TaskDefaults: TaskDefaults(cfg.WebSettings.TaskDefaults), UtilityDailyLimit: cfg.WebSettings.UtilityDailyLimit, UtilityUnlimited: cfg.WebSettings.UtilityUnlimited,
		SuggestReplies: suggestSetting(cfg.WebSettings.SuggestReplies == nil || *cfg.WebSettings.SuggestReplies), CompactionThreshold: cfg.WebSettings.CompactionThreshold, GitHubMCP: cfg.WebSettings.GitHubMCP, ThreadMessages: cfg.WebSettings.ThreadMessages, GitLabAutoInvestigate: cfg.WebSettings.GitLabAutoInvestigate,
		GitLabMRPrompt: cfg.WebSettings.GitLabMRPrompt, CommitPrompt: cfg.WebSettings.CommitPrompt, OutputStyles: cfg.WebSettings.OutputStyles, OutputStyle: cfg.WebSettings.OutputStyle, CoAuthor: cfg.WebSettings.CoAuthor, WorktreesDir: cfg.WebSettings.WorktreesDir, DrawIO: DrawIOSettings(cfg.WebSettings.DrawIO)}
	m.oldWorktreeRoots = slices.Clone(cfg.WebSettings.WorktreeRoots)
	m.setGitRules(m.settings)
	m.setPolicy(m.settings)
	if m.settings.SendDefault != store.WebSendQueue {
		m.settings.SendDefault = store.WebSendSteer
	}
	for _, rec := range cfg.Sessions {
		if rec.Surface != store.SurfaceWeb || rec.ID == "" {
			continue
		}
		s := sessionFromRecord(rec)
		// A turn cannot survive the service that drove it: report it as
		// interrupted, never resume or replay it. No page has seen that end.
		if busy(s.base) {
			s.setBase(StateInterrupted, interruptedDetail)
			s.updatedAt, s.unseenEnd = m.now(), true
			m.dirty[s.id] = struct{}{}
		}
		s.persisted = s.key()
		m.sessions[s.id] = s
		if s.budget.WrapUp {
			// The restart ended its wrap-up: it gets the handoff of uam's facts.
			m.finishWrapLocked(s, "", "uam restarted during the wrap-up")
		}
	}
	m.pairsDirty = m.loadPairsLocked(cfg.WebMessagePairs)
	usage := m.usageProviderLocked()
	cliNames := m.cliProvidersLocked()
	m.mu.Unlock()
	m.loadRoutines(cfg)
	m.loadUploads()
	m.sweepUploads()
	m.loadUtilityLog()
	m.sweepPins(cfg)
	if usageLedger != nil {
		m.wg.Add(1)
		go m.harnessUsageLoop(usageLedger)
		startedUsage = true
	}
	// A runtime signed in as another account than the linked one is caught
	// before routines or anything else can run on it.
	for name, info := range infos {
		if info.Available && info.Capabilities.Account {
			_ = m.verifyAccount(name, true)
		}
	}
	m.wg.Add(5)
	go m.supervise("gitlab loop", m.gitlabLoop)
	go m.supervise("persist loop", m.persistLoop)
	go m.supervise("sweep loop", m.sweepLoop)
	go m.supervise("routine loop", m.routineLoop)
	if usage {
		m.wg.Add(1)
		go m.supervise("usage loop", m.usageLoop)
	}
	if len(cliNames) > 0 {
		m.wg.Add(1)
		go m.supervise("cli loop", func() { m.cliLoop(cliNames) })
	}
	if m.binary != nil {
		m.wg.Add(1)
		go m.supervise("binary loop", m.binaryLoop)
	}
	go m.supervise("image loop", m.imageLoop)
	if err := m.flush(); err != nil {
		log.Warn("persist interrupted web sessions failed", "error", err)
	}
	return nil
}

// supervise runs one of the Manager's loops until the Manager stops,
// running it again after a panic so saves, routines and the rest never stop
// for the life of the service.
func (m *Manager) supervise(what string, loop func()) {
	defer m.wg.Done()
	log.Supervise(m.ctx, what, loop, &m.mu)
}

var errShuttingDown = newError(http.StatusServiceUnavailable, "the uam web service is shutting down")

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// Shutdown ends every conversation this service drives: running turns are
// recorded as interrupted, conversations are closed, and every provider is
// asked to stop the runtimes it started.
func (m *Manager) Shutdown(ctx context.Context) error {
	_, err := m.shutdown(ctx, false)
	return err
}

// shutdownIfIdle is Shutdown unless a Task works or waits or a CLI update
// installs, decided under mu, so no turn starts in between. It reports
// whether the manager is shut down.
func (m *Manager) shutdownIfIdle(ctx context.Context) (bool, error) {
	return m.shutdown(ctx, true)
}

func (m *Manager) shutdown(ctx context.Context, idleOnly bool) (bool, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return true, nil
	}
	if idleOnly && (m.busyTasksLocked() > 0 || m.updatingCLI()) {
		m.mu.Unlock()
		return false, nil
	}
	m.closed = true
	for _, s := range m.sessions {
		s.stopPreviews()
	}
	var convs []agentapi.Conversation
	for _, s := range m.sessions {
		before := m.summaryLocked(s)
		if st := s.state(); busy(st) && st != StateStarting {
			s.setBase(StateInterrupted, interruptedDetail)
			// Before the conversations close: what the CLI records after it is this stop's doing.
			s.interruptedAt = m.now()
		}
		if s.conv != nil {
			convs = append(convs, s.conv)
			s.conv = nil
		}
		s.gen++
		m.expirePendingLocked(s, "the uam web service stopped")
		m.endSubagentsLocked(s)
		m.forgetBackgroundTaskStateLocked(s)
		// The queue stays for the last save; the next start loads it paused.
		m.changedLocked(s, before)
	}
	for sub := range m.subs {
		m.dropLocked(sub)
	}
	m.closeTerminalsLocked(errShuttingDown)
	m.mu.Unlock()
	// Abort in-flight opens, sends and utility jobs; their outcome is
	// recorded as usual. Utility jobs delete their throwaway conversations
	// before the providers stop.
	m.cancel()
	wait(ctx, &m.terminalWG)
	wait(ctx, &m.titles)
	// The conversations close together, within the shutdown's time: one
	// after another, a hung runtime would take each one's whole timeout.
	var closing sync.WaitGroup
	for _, conv := range convs {
		closing.Go(func() {
			if err := conv.Close(ctx); err != nil && !errors.Is(err, agentapi.ErrClosed) {
				log.Warn("close web conversation failed", "error", err)
			}
		})
	}
	closing.Wait()
	var firstErr error
	for _, name := range m.order {
		if err := m.providers[name].Shutdown(ctx); err != nil {
			log.Warn("web provider shutdown failed", "provider", name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("shut down %s: %w", name, err)
			}
		}
	}
	wait(ctx, &m.wg)
	if err := m.flush(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("persist web sessions: %w", err)
	}
	m.counts.close()
	return true, firstErr
}

// wait waits for wg until ctx ends.
func wait(ctx context.Context, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
