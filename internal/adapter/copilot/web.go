package copilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const (
	webPingInterval = 10 * time.Second
	webPingTimeout  = 10 * time.Second
	webPingMisses   = 3
	webStartTimeout = 30 * time.Second
	webStopTimeout  = 15 * time.Second
	webCheckTimeout = 10 * time.Second
	webTasksTimeout = 10 * time.Second
	maxToolText     = 64 << 10
	maxErrorText    = 512
	maxErrorCode    = 64 // the provider's error code or remediation action
	maxIntention    = 300
	// subagentPromptBytes bounds the prompt a subagent's Utility name reads.
	subagentPromptBytes = 2 << 10
	// Read-only history retains at most webReadBytes encoded events from
	// webReadPages newest pages. Older pages are drained to release the snapshot.
	webReadEvents = 1000
	webReadPages  = 64
	webReadBytes  = 16 << 20
	// A window read keeps at most webWindowBytes of items on each side of
	// its item, and starts over at most webWindowAttempts times when the
	// journal it reads is replaced.
	webWindowBytes    = 4 << 20
	webWindowAttempts = 3
	// A read keeps the first webResultBytes of at most webReadResults tool
	// outputs of the events it drains.
	webReadResults = 4096
	webResultBytes = 512
	// shortcut: a redelivered usage event older than the last maxTokenSeen
	// would count again; raise it if the CLI ever replays further back.
	maxTokenSeen = 4096
)

var (
	errNoUser   = errors.New("no user is available to answer")
	errDeclined = errors.New("the user declined to answer")
)

// webProvider drives the installed copilot CLI through the Copilot Go SDK. It
// owns one CLI process, started by the first Open and kept until Shutdown or
// until the watchdog finds it dead.
type webProvider struct {
	newClient func() (sdkClient, error)
	pingEvery time.Duration
	kick      chan struct{}

	mu           sync.Mutex
	client       sdkClient
	stop         chan struct{} // closed to end the current watchdog
	convs        map[*conversation]struct{}
	detaching    map[string][]*detachWait // by session ID, the detaches in flight (awaitDetach)
	shut         bool
	importProbed bool
	// starting is the CLI start in flight, nil when none is: it runs
	// without mu held, and ensureStarted waits for it.
	starting *cliStart
	// The flags Capabilities reads are atomic so it never takes p.mu.
	importSupported   atomic.Bool
	forkUnsupported   atomic.Bool
	rewindUnsupported atomic.Bool
	// assistedRefused says why the running CLI refused assisted
	// permissions; a new CLI is asked again. Written under p.mu.
	assistedRefused atomic.Pointer[string]
	// updating is closed when a CLI update releases the install; starts
	// wait for it meanwhile. nil while no update holds it.
	updating chan struct{}
	// outdated: the running client runs a release an update replaced on
	// disk; RestartCLI stops it once nothing runs on it. utilities counts
	// the utility prompts running, which it waits for too.
	outdated  bool
	utilities int
	// stalls counts the turns the running CLI's model requests stalled
	// twice in (stall.go); Settings points at RestartRuntime with it.
	stalls atomic.Int32
	// installedCLI is the binary a start runs now, and its file; nil leaves
	// CLIOutdated to updates made here. cliPath and cliFile are what it read
	// before the running client started.
	installedCLI func() (string, os.FileInfo, error)
	cliPath      string
	cliFile      os.FileInfo
	// swept: CLIs a uam that is gone left running were looked for, once,
	// before this provider's first CLI.
	swept bool
	// signInChecked is when a signed-out CLI was last restarted to look
	// for a sign-in made outside UAM (Account).
	signInChecked time.Time

	// Guarded by mu; invoked outside the lock before a session can send.
	usageSessionRecorder func(string, bool) error
	usageMu              sync.Mutex
	usageOwned           map[string]sdkClient
	// customMu guards custom, the owner's custom models, subagent, the
	// models subagents may use (empty for any), githubMCP, whether
	// sessions start the built-in GitHub MCP server, and gitRules and
	// coauthor (git_rules.go); it is never held with mu.
	customMu  sync.Mutex
	custom    []agentapi.CustomModel
	subagent  []string
	githubMCP bool
	gitRules  string
	coauthor  bool
	// quotaMu guards live, the quota snapshots by type from the latest model
	// call's response since this CLI started. account.getQuota keeps
	// returning what the CLI read at sign-in, so these supersede it. It also
	// guards what account.getQuota does not report: plan, the account's plan
	// tier, and credits, whether a quota type is billed by tokens.
	quotaMu sync.Mutex
	live    map[string]rpc.AssistantUsageQuotaSnapshot
	plan    string
	credits map[string]bool
}

// NewWebProvider returns the Copilot integration for the web service.
func NewWebProvider() agentapi.Provider {
	p := newWebProvider(newSDKClient, webPingInterval)
	p.installedCLI = installedCLI
	return p
}

func newWebProvider(newClient func() (sdkClient, error), pingEvery time.Duration) *webProvider {
	return &webProvider{newClient: newClient, pingEvery: pingEvery, kick: make(chan struct{}, 1), convs: map[*conversation]struct{}{}}
}

func (p *webProvider) Name() string        { return agentapi.ProviderCopilot }
func (p *webProvider) DisplayName() string { return "GitHub Copilot" }

func (p *webProvider) Capabilities() agentapi.Capabilities {
	refused := ""
	if r := p.assistedRefused.Load(); r != nil {
		refused = *r
	}
	return agentapi.Capabilities{Cancel: true, ExecutionModes: true, Permissions: true, Questions: true, Plan: true, History: true, Fork: !p.forkUnsupported.Load(), Rewind: !p.rewindUnsupported.Load(), SessionDiff: true, SessionDiffNeedsTracking: true, ContextSize: true, ContextBreakdown: true, Usage: true, Aside: true, UsageMetrics: true, Titles: true, Import: p.importSupported.Load(), HostTools: true, Account: true, DeviceSignIn: true, MCP: true, CLIUpdate: true, SubagentModels: true, GitHubMCP: true, CustomAgents: true, AssistedPermissions: refused == "", AssistedUnavailable: refused}
}

func (p *webProvider) Check(ctx context.Context) error {
	path, err := resolveCopilot()
	if err != nil {
		return err
	}
	vctx, cancel := context.WithTimeout(ctx, webCheckTimeout)
	defer cancel()
	if _, err := cliVersion(vctx, path); err != nil {
		return err
	}
	if p.signedOut(ctx) {
		return fmt.Errorf("copilot is not signed in: %w", agentapi.ErrSignedOut)
	}
	return nil
}
