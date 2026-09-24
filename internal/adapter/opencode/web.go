package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const (
	webRequestTimeout = 15 * time.Second
	// webStreamWindow bounds how long /event may stay unconnected, including
	// the first connection (a cold OpenCode instance needs about 7 s).
	webStreamWindow      = 30 * time.Second
	webReconcileMessages = 50
	webMaxParentDepth    = 4
)

// webRuntime is one running OpenCode server as seen by the web provider.
type webRuntime struct {
	client     *apiClient
	done       <-chan struct{}
	stop       func()
	exitReason func() string
}

type webStarter func(ctx context.Context, command providerCommand, directory string) (*webRuntime, error)

// NewWebProvider returns the OpenCode integration for the uam web service.
// It starts one `opencode serve` per project directory on first use and
// stops it when the last conversation on it closes or at Shutdown.
//
// Close only detaches: it never aborts a turn or deletes the session. A turn
// keeps running while other open conversations keep the same server alive;
// closing the last conversation stops the server, which ends any turn still
// running on it.
func NewWebProvider() agentapi.Provider {
	return newWebProvider(resolveWebCommand, startWebRuntime)
}

func newWebProvider(resolve func(context.Context) (providerCommand, error), start webStarter) *webProvider {
	ctx, cancel := context.WithCancel(context.Background())
	return &webProvider{
		resolve:      resolve,
		start:        start,
		streamWindow: webStreamWindow,
		ctx:          ctx,
		cancel:       cancel,
		servers:      map[string]*webServer{},
		running:      map[*webServer]struct{}{},
	}
}

func resolveWebCommand(ctx context.Context) (providerCommand, error) {
	path, err := exec.LookPath("opencode")
	if err != nil {
		return providerCommand{}, fmt.Errorf("OpenCode is not installed: the opencode command was not found on PATH (version %s or newer is required)", minimumVersion)
	}
	if path, err = filepath.Abs(path); err != nil {
		return providerCommand{}, fmt.Errorf("resolve OpenCode executable: %w", err)
	}
	command, err := providerCommandFromFlags(path, "", "")
	if err != nil {
		return providerCommand{}, err
	}
	if err := requireMinimumVersion(ctx, command); err != nil {
		return providerCommand{}, err
	}
	return command, nil
}

func startWebRuntime(ctx context.Context, command providerCommand, directory string) (*webRuntime, error) {
	server, err := startOpenCodeServer(ctx, supervisorOptions{Command: command, Directory: directory, ParentDeathSignal: true})
	if err != nil {
		return nil, err
	}
	server.client.allEvents = true
	password := server.client.password
	return &webRuntime{
		client: server.client,
		done:   server.process.done,
		stop: func() {
			terminateAndReap(server.process)
			server.client.http.CloseIdleConnections()
		},
		exitReason: func() string {
			return serverFailureError("unexpectedly", server, password).Error()
		},
	}, nil
}

type webProvider struct {
	resolve      func(context.Context) (providerCommand, error)
	start        webStarter
	streamWindow time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	closed  bool
	servers map[string]*webServer   // live server per directory
	running map[*webServer]struct{} // every server whose run has not returned
}

func (p *webProvider) Name() string { return agentapi.ProviderOpenCode }

func (p *webProvider) DisplayName() string { return "OpenCode" }

func (p *webProvider) Capabilities() agentapi.Capabilities {
	return agentapi.Capabilities{Cancel: true, Permissions: true, Questions: true, SessionDiff: true, History: true}
}

func (p *webProvider) Check(ctx context.Context) error {
	_, err := p.resolve(ctx)
	return err
}

// Models is not offered by this unregistered adapter; the provider default
// applies.
func (p *webProvider) Models(context.Context) ([]agentapi.Model, error) {
	return nil, agentapi.ErrUnsupported
}

func (p *webProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	if req.Events == nil {
		return nil, fmt.Errorf("OpenCode conversation requires an event sink")
	}
	if err := validateCanonicalDirectory(req.Workdir); err != nil {
		return nil, fmt.Errorf("invalid OpenCode project directory: %w", err)
	}
	if req.ConversationID != "" && !validOpenCodeSessionID(req.ConversationID) {
		return nil, fmt.Errorf("invalid OpenCode conversation ID")
	}
	command, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	server, err := p.acquire(req.Workdir, command)
	if err != nil {
		return nil, err
	}
	conversation, err := server.open(ctx, req)
	if err != nil {
		p.release(server)
		return nil, err
	}
	return conversation, nil
}

// ReadHistory reads a conversation's recorded transcript through the GET
// requests a reopen uses: nothing is posted, and #138 found OpenCode keeps no
// per-session lock another client would notice. Events of the brief open are
// dropped.
func (p *webProvider) ReadHistory(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error) {
	if req.ConversationID == "" {
		return agentapi.History{}, errors.New("OpenCode history requires a conversation ID")
	}
	conversation, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: req.ConversationID, Workdir: req.Workdir, Events: discardEvents{}})
	if err != nil {
		return agentapi.History{}, err
	}
	defer func() { _ = conversation.Close(context.WithoutCancel(ctx)) }()
	return conversation.History(ctx)
}

type discardEvents struct{}

func (discardEvents) Emit(agentapi.Event) {}

func (p *webProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	running := make([]*webServer, 0, len(p.running))
	for server := range p.running {
		running = append(running, server)
	}
	p.servers = map[string]*webServer{}
	p.mu.Unlock()
	for _, server := range running {
		server.closeConversations()
	}
	p.cancel()
	for _, server := range running {
		select {
		case <-server.stopped:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// acquire returns the live server for directory, starting one if needed,
// and counts the caller as a user until release.
func (p *webProvider) acquire(directory string, command providerCommand) (*webServer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("OpenCode web provider is shut down")
	}
	server := p.servers[directory]
	if server == nil {
		ctx, cancel := context.WithCancel(p.ctx)
		server = &webServer{
			provider:  p,
			directory: directory,
			ctx:       ctx,
			cancel:    cancel,
			ready:     make(chan struct{}),
			stopped:   make(chan struct{}),
			routes:    map[string]*webConversation{},
			unknown:   map[string]struct{}{},
		}
		p.servers[directory] = server
		p.running[server] = struct{}{}
		go server.run(command)
	}
	server.refs++
	return server, nil
}

// release drops one user; the last one stops the server.
func (p *webProvider) release(server *webServer) {
	p.mu.Lock()
	server.refs--
	last := server.refs <= 0
	if last && p.servers[server.directory] == server {
		delete(p.servers, server.directory)
	}
	p.mu.Unlock()
	if last {
		server.cancel()
	}
}

// forget makes a failed server unreachable so a later Open starts a new one.
func (p *webProvider) forget(server *webServer) {
	p.mu.Lock()
	if p.servers[server.directory] == server {
		delete(p.servers, server.directory)
	}
	p.mu.Unlock()
}

type webServer struct {
	provider  *webProvider
	directory string
	ctx       context.Context
	cancel    context.CancelFunc
	ready     chan struct{}
	stopped   chan struct{}
	err       error       // start failure, valid after ready
	runtime   *webRuntime // valid after ready when err is nil
	refs      int         // guarded by provider.mu

	mu      sync.Mutex
	dead    bool
	routes  map[string]*webConversation // root and child session IDs
	unknown map[string]struct{}         // sessions known not to belong here
}

func (s *webServer) run(command providerCommand) {
	defer func() {
		s.mu.Lock()
		s.dead = true
		s.mu.Unlock()
		s.provider.mu.Lock()
		delete(s.provider.running, s)
		s.provider.mu.Unlock()
		close(s.stopped)
	}()
	startCtx, cancelStart := context.WithTimeout(s.ctx, serverStartupTimeout)
	runtime, err := s.provider.start(startCtx, command, s.directory)
	cancelStart()
	if err != nil {
		s.finishStart(nil, err)
		return
	}

	streamCtx, stopStream := context.WithCancel(s.ctx)
	events := make(chan eventEnvelope, 256)
	reconnected := make(chan struct{}, 1)
	connected := make(chan struct{})
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- streamWebEvents(streamCtx, runtime.client, s.provider.streamWindow, connected, reconnected, events)
	}()
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		s.dispatchLoop(streamCtx, runtime.client, events, reconnected)
	}()

	reason := ""
	streamFinished := false
	select {
	case <-connected:
		s.finishStart(runtime, nil)
		reason, streamFinished = s.watch(runtime, streamDone)
	case <-runtime.done:
		s.finishStart(nil, errors.New(runtime.exitReason()))
	case err := <-streamDone:
		streamFinished = true
		s.finishStart(nil, s.streamFailure(runtime, err))
	case <-s.ctx.Done():
		s.finishStart(nil, s.ctx.Err())
	}
	stopStream()
	runtime.stop()
	<-dispatchDone
	if !streamFinished {
		<-streamDone
	}
	if reason != "" {
		s.fail(reason)
	}
}

// watch waits for the server to stop being usable and returns the reason to
// report to conversations ("" when the provider stopped it on purpose).
func (s *webServer) watch(runtime *webRuntime, streamDone <-chan error) (string, bool) {
	select {
	case <-runtime.done:
		return runtime.exitReason(), false
	case err := <-streamDone:
		if s.ctx.Err() != nil {
			return "", true
		}
		select {
		case <-runtime.done:
			return runtime.exitReason(), true
		default:
		}
		return s.streamFailure(runtime, err).Error(), true
	case <-s.ctx.Done():
		return "", false
	}
}

func (s *webServer) streamFailure(runtime *webRuntime, err error) error {
	return sanitizedSupervisorError("OpenCode event stream failed", err, runtime.client.password)
}

func (s *webServer) finishStart(runtime *webRuntime, err error) {
	s.runtime = runtime
	s.err = err
	if err != nil {
		s.provider.forget(s)
	}
	close(s.ready)
}

// fail reports an unexpected server loss to every open conversation.
func (s *webServer) fail(reason string) {
	s.provider.forget(s)
	for _, conversation := range s.detachAll() {
		conversation.exit(reason)
	}
}

func (s *webServer) closeConversations() {
	for _, conversation := range s.detachAll() {
		conversation.markClosed()
	}
}

func (s *webServer) detachAll() []*webConversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead = true
	conversations := s.conversationsLocked()
	s.routes = map[string]*webConversation{}
	return conversations
}

func (s *webServer) conversations() []*webConversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conversationsLocked()
}

func (s *webServer) conversationsLocked() []*webConversation {
	result := make([]*webConversation, 0, len(s.routes))
	for id, conversation := range s.routes {
		if id == conversation.id {
			result = append(result, conversation)
		}
	}
	return result
}

func (s *webServer) register(conversation *webConversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return fmt.Errorf("OpenCode server for this project is no longer running")
	}
	if existing := s.routes[conversation.id]; existing != nil {
		return fmt.Errorf("OpenCode conversation %s is already open", conversation.id)
	}
	s.routes[conversation.id] = conversation
	clear(s.unknown)
	return nil
}

func (s *webServer) unregister(conversation *webConversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, routed := range s.routes {
		if routed == conversation {
			delete(s.routes, id)
		}
	}
}

func (s *webServer) open(ctx context.Context, req agentapi.OpenRequest) (*webConversation, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	client := s.runtime.client
	callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	var (
		info sessionInfo
		err  error
	)
	if req.ConversationID == "" {
		info, err = client.createSession(callCtx, req.Title)
	} else {
		info, err = client.getSession(callCtx, req.ConversationID)
		if errors.Is(err, errSessionNotFound) {
			return nil, fmt.Errorf("%w: OpenCode session %s", agentapi.ErrConversationNotFound, req.ConversationID)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := validateRootSession(info, req.ConversationID, s.directory); err != nil {
		return nil, err
	}
	conversation := newWebConversation(s, info.ID, req.Events)
	if err := s.register(conversation); err != nil {
		return nil, err
	}
	if req.ConversationID == "" {
		return conversation, nil
	}
	sequence := conversation.statusSequence()
	snapshot, err := s.snapshot(callCtx, client)
	if err != nil {
		s.unregister(conversation)
		conversation.markClosed()
		return nil, fmt.Errorf("read OpenCode conversation state: %w", err)
	}
	conversation.applyOpenSnapshot(snapshot, sequence)
	return conversation, nil
}

// streamWebEvents keeps one /event subscription alive. It returns when ctx
// ends or when no connection could be established for window.
func streamWebEvents(ctx context.Context, client *apiClient, window time.Duration, connected, reconnected chan<- struct{}, events chan<- eventEnvelope) error {
	everConnected := false
	lostAt := time.Now()
	var lastErr error
	for attempt := 0; ; {
		wait := time.Until(lostAt.Add(window))
		if wait <= 0 {
			if lastErr == nil {
				return fmt.Errorf("not re-established within %s", window)
			}
			return fmt.Errorf("not re-established within %s: %w", window, lastErr)
		}
		attemptCtx, cancel := context.WithCancel(ctx)
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- client.subscribe(attemptCtx, ready, events) }()
		timer := time.NewTimer(wait)
		finished := false
		select {
		case <-ready:
		case lastErr = <-done:
			finished = true
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
		connectedNow := false
		select {
		case <-ready:
			connectedNow = true
			if !everConnected {
				everConnected = true
				close(connected)
			} else {
				select {
				case reconnected <- struct{}{}:
				default:
				}
			}
			if !finished {
				lastErr = <-done
				finished = true
			}
			lostAt = time.Now()
		default:
		}
		cancel()
		if !finished {
			<-done
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connectedNow {
			attempt = 0
		} else {
			attempt++
		}
		timer = time.NewTimer(reconnectBackoff(attempt))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (s *webServer) dispatchLoop(ctx context.Context, client *apiClient, events <-chan eventEnvelope, reconnected <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			s.dispatch(ctx, client, event)
		case <-reconnected:
			s.reconcile(ctx, client)
		}
	}
}

func (s *webServer) dispatch(ctx context.Context, client *apiClient, event eventEnvelope) {
	var head struct {
		SessionID string `json:"sessionID"`
	}
	if json.Unmarshal(event.Properties, &head) != nil || head.SessionID == "" {
		return
	}
	if event.Type == "session.created" || event.Type == "session.updated" {
		s.noteSession(event.Properties)
	}
	asked := event.Type == "permission.asked" || event.Type == "question.asked"
	conversation := s.route(ctx, client, head.SessionID, asked)
	if conversation == nil {
		return
	}
	if conversation.id != head.SessionID && !webInteractionEvent(event.Type) {
		return
	}
	conversation.handleEvent(event)
}

// noteSession routes a new child session (subagent) to its parent's
// conversation so the child's permission requests and questions surface.
func (s *webServer) noteSession(properties json.RawMessage) {
	var payload struct {
		Info sessionInfo `json:"info"`
	}
	if json.Unmarshal(properties, &payload) != nil {
		return
	}
	info := payload.Info
	if info.ParentID == "" || info.Directory != s.directory || !validOpenCodeSessionID(info.ID) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if parent := s.routes[info.ParentID]; parent != nil {
		s.routes[info.ID] = parent
		delete(s.unknown, info.ID)
	}
}

// route finds the conversation for sessionID. With lookup, an unknown session
// is resolved through its parent chain so subagent interactions reach the
// root conversation that owns them.
func (s *webServer) route(ctx context.Context, client *apiClient, sessionID string, lookup bool) *webConversation {
	s.mu.Lock()
	conversation := s.routes[sessionID]
	_, unknown := s.unknown[sessionID]
	s.mu.Unlock()
	if conversation != nil || !lookup || unknown || !validOpenCodeSessionID(sessionID) {
		return conversation
	}
	chain := []string{sessionID}
	current := sessionID
	for depth := 0; depth < webMaxParentDepth; depth++ {
		callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
		info, err := client.getSession(callCtx, current)
		cancel()
		if err != nil && !errors.Is(err, errSessionNotFound) {
			return nil
		}
		if err != nil || info.ParentID == "" || info.Directory != s.directory {
			break
		}
		s.mu.Lock()
		parent := s.routes[info.ParentID]
		if parent != nil {
			for _, id := range chain {
				s.routes[id] = parent
			}
		}
		s.mu.Unlock()
		if parent != nil {
			return parent
		}
		chain = append(chain, info.ParentID)
		current = info.ParentID
	}
	s.mu.Lock()
	s.unknown[sessionID] = struct{}{}
	s.mu.Unlock()
	return nil
}

type webPendingRequests struct {
	permissions []webPermissionRequest
	questions   []webQuestionRequest
}

// webServerSnapshot holds provider state read after a gap. A nil field means
// that part could not be read.
type webServerSnapshot struct {
	statuses map[string]webSessionStatus
	pending  map[*webConversation]*webPendingRequests
}

func (s *webServer) snapshot(ctx context.Context, client *apiClient) (webServerSnapshot, error) {
	var snapshot webServerSnapshot
	callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	statuses, statusErr := client.webStatuses(callCtx)
	if statusErr == nil {
		snapshot.statuses = statuses
	}
	permissions, permissionErr := client.webPermissions(callCtx)
	questions, questionErr := client.webQuestions(callCtx)
	if err := errors.Join(permissionErr, questionErr); err != nil {
		return snapshot, errors.Join(statusErr, err)
	}
	snapshot.pending = map[*webConversation]*webPendingRequests{}
	pendingFor := func(sessionID string) *webPendingRequests {
		conversation := s.route(callCtx, client, sessionID, true)
		if conversation == nil {
			return nil
		}
		if snapshot.pending[conversation] == nil {
			snapshot.pending[conversation] = &webPendingRequests{}
		}
		return snapshot.pending[conversation]
	}
	for _, request := range permissions {
		if pending := pendingFor(request.SessionID); pending != nil {
			pending.permissions = append(pending.permissions, request)
		}
	}
	for _, request := range questions {
		if pending := pendingFor(request.SessionID); pending != nil {
			pending.questions = append(pending.questions, request)
		}
	}
	return snapshot, statusErr
}

// reconcile repairs every open conversation after the event stream
// reconnected: re-read recent messages, turn state, and pending interactions.
func (s *webServer) reconcile(ctx context.Context, client *apiClient) {
	conversations := s.conversations()
	if len(conversations) == 0 {
		return
	}
	snapshot, _ := s.snapshot(ctx, client)
	for _, conversation := range conversations {
		callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
		messages, _, err := client.webMessagePage(callCtx, conversation.id, webReconcileMessages, "")
		cancel()
		conversation.reconcile(messages, err == nil, snapshot)
	}
}

func newWebConversation(server *webServer, id string, sink agentapi.EventSink) *webConversation {
	return &webConversation{
		server:   server,
		id:       id,
		sink:     sink,
		roles:    map[string]string{},
		parts:    map[string]webPartState{},
		pending:  map[string]agentapi.Interaction{},
		resolved: map[string]struct{}{},

		commandText: map[string]string{},
		commandPart: map[string]string{},
		toolParts:   map[string]string{},
		partOf:      map[string]string{},
	}
}

type webConversation struct {
	server *webServer
	id     string
	sink   agentapi.EventSink
	sendMu sync.Mutex

	// mu guards the state below and serialises Emit, so no event is emitted
	// after Close returns. Sinks must not call back into the conversation
	// from Emit.
	mu               sync.Mutex
	closed           bool
	released         bool
	roles            map[string]string // message ID → "user" / "assistant"
	parts            map[string]webPartState
	busy             bool
	statusSeq        uint64
	sessionErr       *webProviderError
	lastAssistantID  string
	lastAssistantErr *webProviderError
	lastUserID       string
	pending          map[string]agentapi.Interaction
	resolved         map[string]struct{}
	// commandText holds "/name arguments" per command message this
	// conversation sent, shown instead of the expanded template; commandPart
	// is the one text part that shows it. Both last as long as the
	// conversation is open.
	commandText map[string]string
	commandPart map[string]string
	// toolParts maps a running tool call's ID to the ID of the part that
	// shows the call, which is the call's tool item ID; a call is forgotten
	// once it completes. partOf keeps the part each pending request was
	// linked to, so its resolution carries the same link after that.
	toolParts map[string]string
	partOf    map[string]string
}

func (c *webConversation) ID() string { return c.id }

func (c *webConversation) client() (*apiClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, agentapi.ErrClosed
	}
	return c.server.runtime.client, nil
}

// History returns the main transcript only; this unregistered adapter does
// not read subagent sessions.
func (c *webConversation) History(ctx context.Context) (agentapi.History, error) {
	items, err := c.historyItems(ctx)
	return agentapi.History{Items: items}, err
}

// SetModel is not offered: the model would have to travel with the next
// prompt, which this unregistered adapter does not do.
func (c *webConversation) SetModel(context.Context, string, string, string) error {
	return agentapi.ErrUnsupported
}

func (c *webConversation) historyItems(ctx context.Context) ([]agentapi.Item, error) {
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	var pages [][]webMessage
	seen := map[string]struct{}{}
	before := ""
	for {
		if len(pages) >= webMaxHistoryPages {
			return nil, fmt.Errorf("OpenCode history exceeds %d pages", webMaxHistoryPages)
		}
		callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
		page, next, err := client.webMessagePage(callCtx, c.id, webHistoryPageSize, before)
		cancel()
		if errors.Is(err, errWebNotFound) {
			return nil, fmt.Errorf("%w: OpenCode session %s", agentapi.ErrConversationNotFound, c.id)
		}
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
		if next == "" {
			break
		}
		if _, repeated := seen[next]; repeated {
			return nil, fmt.Errorf("OpenCode history pagination repeated a cursor")
		}
		seen[next] = struct{}{}
		before = next
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, agentapi.ErrClosed
	}
	var items []agentapi.Item
	for index := len(pages) - 1; index >= 0; index-- {
		for _, message := range pages[index] {
			items = append(items, c.noteSnapshotMessageLocked(message)...)
		}
	}
	return items, nil
}

// Send posts the text part, then one file part per referenced file, which
// OpenCode reads into the prompt itself.
func (c *webConversation) Send(ctx context.Context, prompt agentapi.Prompt) error {
	if strings.TrimSpace(prompt.Text) == "" {
		return fmt.Errorf("prompt is empty")
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	client, err := c.client()
	if err != nil {
		return err
	}
	if err := c.checkIdle(ctx, client); err != nil {
		return err
	}
	messageID, err := newAscendingMessageID()
	if err != nil {
		return err
	}
	parts := append([]map[string]any{{"type": "text", "text": prompt.Text}}, webFileParts(prompt)...)
	postCtx, cancelPost := context.WithTimeout(ctx, webRequestTimeout)
	status, postErr := client.webPrompt(postCtx, c.id, messageID, parts)
	cancelPost()
	if postErr == nil {
		switch {
		case successfulStatus(status):
			return nil
		case status == 404:
			return fmt.Errorf("%w: OpenCode session %s", agentapi.ErrConversationNotFound, c.id)
		default:
			return fmt.Errorf("OpenCode rejected the prompt with HTTP %d", status)
		}
	}
	// The request may have reached OpenCode. Look for the exact message once;
	// never resend.
	checkCtx, cancelCheck := context.WithTimeout(context.WithoutCancel(ctx), webRequestTimeout)
	defer cancelCheck()
	if present, err := client.webUserMessageExists(checkCtx, c.id, messageID); err == nil && present {
		return nil
	}
	return fmt.Errorf("%w: %s", agentapi.ErrSubmissionUncertain, client.safeText(postErr.Error()))
}

// checkIdle refuses a prompt while OpenCode runs a turn in the session.
func (c *webConversation) checkIdle(ctx context.Context, client *apiClient) error {
	statusCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	statuses, err := client.webStatuses(statusCtx)
	if err != nil {
		return fmt.Errorf("check OpenCode session status: %w", err)
	}
	if webBusy(statuses, c.id) {
		return agentapi.ErrBusy
	}
	return nil
}

// webFileParts maps referenced files and uploads to OpenCode file parts. A
// file's source links it to its @path in the text, in UTF-16 offsets as
// OpenCode's own clients count. An upload goes inline as a data: URL;
// OpenCode inlines text/plain into the prompt and passes images and PDFs to
// models that take them.
func webFileParts(prompt agentapi.Prompt) []map[string]any {
	parts := []map[string]any{}
	for _, f := range prompt.Files {
		mime := "text/plain"
		if f.Dir {
			mime = "application/x-directory"
		}
		part := map[string]any{"type": "file", "mime": mime, "filename": f.Rel, "url": (&url.URL{Scheme: "file", Path: f.Path}).String()}
		token := "@" + f.Rel
		if start := strings.Index(prompt.Text, token); start >= 0 {
			from := len(utf16.Encode([]rune(prompt.Text[:start])))
			part["source"] = map[string]any{"type": "file", "path": f.Rel, "text": map[string]any{
				"value": token, "start": from, "end": from + len(utf16.Encode([]rune(token))),
			}}
		}
		parts = append(parts, part)
	}
	for _, b := range prompt.Attachments {
		parts = append(parts, map[string]any{"type": "file", "mime": b.MIME, "filename": b.Name,
			"url": "data:" + b.MIME + ";base64," + base64.StdEncoding.EncodeToString(b.Data)})
	}
	return parts
}

// Commands lists every command OpenCode offers: init, review, custom and
// MCP commands, and skills.
func (c *webConversation) Commands(ctx context.Context) ([]agentapi.Command, error) {
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	listed, err := client.webCommands(callCtx)
	if err != nil {
		return nil, err
	}
	out := make([]agentapi.Command, 0, len(listed))
	for _, cmd := range listed {
		kind := agentapi.CommandPrompt
		if cmd.Source == "skill" {
			kind = agentapi.CommandSkill
		}
		out = append(out, agentapi.Command{Name: cmd.Name, Description: cmd.Description, Kind: kind, InputHint: strings.Join(cmd.Hints, " ")})
	}
	return out, nil
}

// webCommandWait bounds how long RunCommand waits for OpenCode to take the
// command; the turn itself runs on.
const webCommandWait = webRequestTimeout

// RunCommand posts the command in the background, because OpenCode answers
// only when the turn ends. The command is accepted once its user message
// exists; the call is never repeated. OpenCode stores the expanded
// template as the user message; this conversation shows "/name arguments"
// for it instead.
func (c *webConversation) RunCommand(ctx context.Context, name string, args agentapi.Prompt) error {
	// OpenCode runs every !`cmd` in the expanded template without asking,
	// and the arguments are expanded into it.
	if strings.Contains(args.Text, "!`") {
		return fmt.Errorf("command arguments must not contain !`")
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	client, err := c.client()
	if err != nil {
		return err
	}
	if err := c.checkIdle(ctx, client); err != nil {
		return err
	}
	messageID, err := newAscendingMessageID()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.commandText[messageID] = strings.TrimSpace("/" + name + " " + args.Text)
	c.mu.Unlock()
	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	serverCtx := c.server.ctx
	go func() {
		status, err := client.webCommand(serverCtx, c.id, messageID, name, args.Text, webFileParts(args))
		done <- result{status, err}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(webCommandWait)
	defer deadline.Stop()
	for {
		select {
		case r := <-done:
			switch {
			case r.err == nil && successfulStatus(r.status), c.sawUserMessage(messageID):
				return nil
			case r.err != nil:
				return c.commandUncertain(ctx, client, messageID, r.err)
			case r.status == 404:
				return fmt.Errorf("%w: OpenCode session %s", agentapi.ErrConversationNotFound, c.id)
			default:
				return fmt.Errorf("OpenCode rejected /%s with HTTP %d", name, r.status)
			}
		case <-tick.C:
			if c.sawUserMessage(messageID) {
				return nil
			}
		case <-deadline.C:
			return c.commandUncertain(ctx, client, messageID, errors.New("OpenCode did not confirm the command in time"))
		case <-ctx.Done():
			return c.commandUncertain(ctx, client, messageID, ctx.Err())
		}
	}
}

func (c *webConversation) sawUserMessage(messageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.roles[messageID] == "user"
}

// commandUncertain looks for the command's user message once; it never
// resends.
func (c *webConversation) commandUncertain(ctx context.Context, client *apiClient, messageID string, cause error) error {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webRequestTimeout)
	defer cancel()
	if present, err := client.webUserMessageExists(checkCtx, c.id, messageID); err == nil && present {
		return nil
	}
	return fmt.Errorf("%w: %s", agentapi.ErrSubmissionUncertain, client.safeText(cause.Error()))
}

// Steer is not offered: this unregistered adapter refuses prompts while a
// turn runs, so nothing is folded into a running turn.
func (c *webConversation) Steer(context.Context, string) error {
	return agentapi.ErrUnsupported
}

func (c *webConversation) CancelSubagent(context.Context, string) error {
	return agentapi.ErrUnsupported
}

func (c *webConversation) PromptSubagent(context.Context, string, string) error {
	return agentapi.ErrUnsupported
}

// SetTitle is unsupported: the adapter does not title Tasks with a model yet.
func (c *webConversation) SetTitle(context.Context, string) error {
	return agentapi.ErrUnsupported
}

func (c *webConversation) Cancel(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	return client.webAbort(callCtx, c.id)
}

func (c *webConversation) Respond(ctx context.Context, interactionID string, answer agentapi.Answer) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	c.mu.Lock()
	interaction, ok := c.pending[interactionID]
	c.mu.Unlock()
	if !ok {
		return agentapi.ErrInteractionGone
	}
	callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	var (
		state      agentapi.InteractionState
		resolution string
	)
	switch interaction.Kind {
	case agentapi.InteractionPermission:
		state, resolution, ok = webPermissionResolution(answer.Decision)
		if !ok {
			return fmt.Errorf("invalid OpenCode permission decision %q", displaytext.Sanitize(answer.Decision))
		}
		err = client.webReplyPermission(callCtx, interactionID, answer.Decision)
	case agentapi.InteractionQuestion:
		if answer.Reject {
			state, resolution = agentapi.InteractionRejected, "Dismissed"
			err = client.webRejectQuestion(callCtx, interactionID)
			break
		}
		if len(answer.Answers) != len(interaction.Questions) {
			return fmt.Errorf("OpenCode question expects %d answers, got %d", len(interaction.Questions), len(answer.Answers))
		}
		answers := make([][]string, len(answer.Answers))
		for index, values := range answer.Answers {
			answers[index] = append([]string{}, values...)
		}
		state, resolution = agentapi.InteractionAnswered, webAnswerResolution(answers)
		err = client.webReplyQuestion(callCtx, interactionID, answers)
	default:
		return agentapi.ErrInteractionGone
	}
	if errors.Is(err, errWebNotFound) {
		c.resolve(interactionID, agentapi.InteractionExpired, webExpiredResolution)
		return agentapi.ErrInteractionGone
	}
	if err != nil {
		return err
	}
	c.resolve(interactionID, state, resolution)
	return nil
}

func (c *webConversation) Diff(ctx context.Context) ([]agentapi.FileDiff, error) {
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	diffs, err := client.webDiff(callCtx, c.id)
	if err != nil {
		return nil, err
	}
	result := make([]agentapi.FileDiff, 0, len(diffs))
	for _, diff := range diffs {
		result = append(result, agentapi.FileDiff{
			Path:      diff.File,
			Status:    diff.Status,
			Additions: int(diff.Additions),
			Deletions: int(diff.Deletions),
			Patch:     diff.Patch,
		})
	}
	return result, nil
}

// Close detaches without aborting the turn or deleting the session. The
// server stops when this was its last conversation.
func (c *webConversation) Close(context.Context) error {
	c.mu.Lock()
	c.closed = true
	release := !c.released
	c.released = true
	c.mu.Unlock()
	if release {
		c.server.unregister(c)
		c.server.provider.release(c.server)
	}
	return nil
}

func (c *webConversation) markClosed() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// exit reports that the conversation became unusable and closes it.
func (c *webConversation) exit(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.sink.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: reason})
}

func (c *webConversation) statusSequence() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusSeq
}

func (c *webConversation) resolve(id string, state agentapi.InteractionState, resolution string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.resolveLocked(id, state, resolution)
	}
}
