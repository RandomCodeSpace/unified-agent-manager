package opencode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	openCodeServerUsername = "uam"
	serverStartupTimeout   = 5 * time.Second
	serverStartupAttempts  = 3
	serverPollInterval     = 25 * time.Millisecond
	serverRequestTimeout   = 250 * time.Millisecond
	childCleanupTimeout    = 1500 * time.Millisecond
	serverLogCapacity      = 64 << 10
	eventReconnectBase     = 25 * time.Millisecond
	eventReconnectMax      = 400 * time.Millisecond
	runtimeDirFlag         = "runtime-dir"
)

type supervisorOptions struct {
	Command           providerCommand
	Directory         string
	SessionName       string
	ProviderSessionID string
	Yolo              bool
	RuntimeDir        string
}

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("OpenCode attach exited with code %d", e.Code)
}

func (e *ExitError) ExitCode() int {
	return e.Code
}

type uniqueStringFlag struct {
	name  string
	value string
	set   bool
}

func (f *uniqueStringFlag) String() string {
	return f.value
}

func (f *uniqueStringFlag) Set(value string) error {
	if f.set {
		return fmt.Errorf("flag --%s may be specified only once", f.name)
	}
	f.value = value
	f.set = true
	return nil
}

func RunSupervisorCommand(args []string) error {
	opts, err := parseSupervisorOptions(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer stop()
	return runSupervisor(ctx, opts)
}

func parseSupervisorOptions(args []string) (supervisorOptions, error) {
	values, err := parseSupervisorFlags(args)
	if err != nil {
		return supervisorOptions{}, err
	}

	command, err := providerCommandFromFlags(values["path"].value, values["shell"].value, values["alias"].value)
	if err != nil {
		return supervisorOptions{}, err
	}
	directory := values["dir"].value
	if err := validateCanonicalDirectory(directory); err != nil {
		return supervisorOptions{}, fmt.Errorf("invalid OpenCode working directory: %w", err)
	}
	runtimeDir := values[runtimeDirFlag].value
	if err := validateCanonicalDirectory(runtimeDir); err != nil {
		return supervisorOptions{}, fmt.Errorf("invalid OpenCode runtime directory: %w", err)
	}
	if err := session.VerifyDir(runtimeDir); err != nil {
		return supervisorOptions{}, err
	}
	sessionName := values["name"].value
	if err := session.ValidateName(sessionName); err != nil {
		return supervisorOptions{}, err
	}
	providerSessionID := values["session"].value
	if providerSessionID != "" && (!providerIDRE.MatchString(providerSessionID) || !store.ValidProviderSessionID(providerSessionID)) {
		return supervisorOptions{}, fmt.Errorf("invalid OpenCode provider session ID")
	}
	yolo, err := parseSupervisorMode(values["mode"].value)
	if err != nil {
		return supervisorOptions{}, err
	}
	return supervisorOptions{
		Command:           command,
		Directory:         directory,
		SessionName:       sessionName,
		ProviderSessionID: providerSessionID,
		Yolo:              yolo,
		RuntimeDir:        runtimeDir,
	}, nil
}

func parseSupervisorFlags(args []string) (map[string]*uniqueStringFlag, error) {
	fs := flag.NewFlagSet("__opencode", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	values := map[string]*uniqueStringFlag{}
	for _, name := range []string{"path", "shell", "alias", "dir", "name", runtimeDirFlag, "mode", "session"} {
		value := &uniqueStringFlag{name: name}
		values[name] = value
		fs.Var(value, name, "")
	}
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("parse OpenCode supervisor flags: %w", err)
	}
	if fs.NArg() != 0 {
		return nil, fmt.Errorf("OpenCode supervisor does not accept positional arguments")
	}
	for _, name := range []string{"dir", "name", runtimeDirFlag, "mode"} {
		if !values[name].set || values[name].value == "" {
			return nil, fmt.Errorf("OpenCode supervisor requires --%s", name)
		}
	}
	return values, nil
}

func parseSupervisorMode(mode string) (bool, error) {
	switch mode {
	case "safe":
		return false, nil
	case "yolo":
		return true, nil
	default:
		return false, fmt.Errorf("OpenCode supervisor mode must be safe or yolo")
	}
}

func validateCanonicalDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("path must be absolute and canonical")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	return nil
}

type managedProcess struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu  sync.Mutex
	err error
}

func startManagedProcess(cmd *exec.Cmd) (*managedProcess, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &managedProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (p *managedProcess) waitError() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

type byteRing struct {
	mu       sync.Mutex
	data     []byte
	capacity int
}

func newByteRing(capacity int) *byteRing {
	return &byteRing{capacity: capacity}
}

func (r *byteRing) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	written := len(data)
	if r.capacity <= 0 {
		return written, nil
	}
	if len(data) >= r.capacity {
		r.data = append(r.data[:0], data[len(data)-r.capacity:]...)
		return written, nil
	}
	overflow := len(r.data) + len(data) - r.capacity
	if overflow > 0 {
		copy(r.data, r.data[overflow:])
		r.data = r.data[:len(r.data)-overflow]
	}
	r.data = append(r.data, data...)
	return written, nil
}

func (r *byteRing) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.data...)
}

type runningServer struct {
	process *managedProcess
	client  *apiClient
	baseURL string
	logs    *byteRing
}

func runSupervisor(ctx context.Context, opts supervisorOptions) error {
	password, err := randomServerPassword()
	if err != nil {
		return err
	}
	env := serverEnvironment(os.Environ(), openCodeServerUsername, password)
	startupCtx, cancelStartup := context.WithTimeout(ctx, serverStartupTimeout)
	server, err := startOpenCodeServer(startupCtx, opts, env, password)
	cancelStartup()
	if err != nil {
		return err
	}
	var attach *managedProcess
	defer func() {
		terminateAndReap(attach, server.process)
	}()

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	events := make(chan eventEnvelope, 128)
	ready := make(chan struct{})
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- subscribeWithReconnect(streamCtx, server.client, ready, events)
	}()
	select {
	case <-ready:
	case <-server.process.done:
		return serverFailureError("before event subscription became ready", server, password)
	case err := <-streamDone:
		return sanitizedSupervisorError("OpenCode event subscription failed", err, password)
	case <-ctx.Done():
		return ctx.Err()
	}

	root, err := selectRootSession(ctx, opts, server.client)
	if err != nil {
		return err
	}
	if err := session.WriteProviderIdentity(opts.RuntimeDir, opts.SessionName, root.ID); err != nil {
		return fmt.Errorf("write OpenCode provider identity: %w", err)
	}
	eventState := newSupervisorEventState(opts, root.ID)

	attachCommand := opts.Command.command(context.Background(), "attach", server.baseURL, "--dir", opts.Directory, "--session", root.ID)
	attachCommand.Dir = opts.Directory
	attachCommand.Env = env
	attachCommand.Stdin = os.Stdin
	attachCommand.Stdout = os.Stdout
	attachCommand.Stderr = os.Stderr
	attach, err = startManagedProcess(attachCommand)
	if err != nil {
		return sanitizedSupervisorError("start OpenCode attach", err, password)
	}

	for {
		if outcome, ready := readySupervisorOutcome(ctx, server, attach, password); ready {
			return outcome
		}
		select {
		case <-attach.done:
		case <-server.process.done:
		case <-ctx.Done():
		case event := <-events:
			if outcome, ready := readySupervisorOutcome(ctx, server, attach, password); ready {
				return outcome
			}
			if err := eventState.handle(ctx, server.client, event); err != nil {
				return err
			}
		case err := <-streamDone:
			if outcome, ready := readySupervisorOutcome(ctx, server, attach, password); ready {
				return outcome
			}
			return sanitizedSupervisorError("OpenCode event stream stopped", err, password)
		}
	}
}

func readySupervisorOutcome(ctx context.Context, server *runningServer, attach *managedProcess, password string) (error, bool) {
	select {
	case <-server.process.done:
		return serverFailureError("while attach was active", server, password), true
	default:
	}
	select {
	case <-attach.done:
		return attachExitError(attach.waitError()), true
	default:
	}
	if err := ctx.Err(); err != nil {
		return err, true
	}
	return nil, false
}

func randomServerPassword() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate OpenCode server credential: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func serverEnvironment(base []string, username, password string) []string {
	result := make([]string, 0, len(base)+2)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if key == "OPENCODE_SERVER_USERNAME" || key == "OPENCODE_SERVER_PASSWORD" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "OPENCODE_SERVER_USERNAME="+username, "OPENCODE_SERVER_PASSWORD="+password)
}

func startOpenCodeServer(ctx context.Context, opts supervisorOptions, env []string, password string) (*runningServer, error) {
	var lastError error
	for attempt := 1; attempt <= serverStartupAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		server, retry, err := startOpenCodeServerAttempt(opts, env, password)
		if err != nil {
			if !retry {
				return nil, err
			}
			lastError = err
			continue
		}
		retry, err = waitForOpenCodeServer(ctx, server, password)
		if err == nil {
			return server, nil
		}
		if !retry {
			return nil, err
		}
		lastError = err
	}
	if lastError == nil {
		lastError = fmt.Errorf("OpenCode server failed before readiness")
	}
	return nil, fmt.Errorf("OpenCode server failed after %d attempts: %w", serverStartupAttempts, lastError)
}

func startOpenCodeServerAttempt(opts supervisorOptions, env []string, password string) (*runningServer, bool, error) {
	port, err := reserveLoopbackPort()
	if err != nil {
		return nil, false, fmt.Errorf("reserve OpenCode loopback port: %w", err)
	}
	baseURL := "http://127.0.0.1:" + fmt.Sprint(port)
	logs := newByteRing(serverLogCapacity)
	command := opts.Command.command(context.Background(), "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port))
	command.Dir = opts.Directory
	command.Env = env
	command.Stdout = logs
	command.Stderr = logs
	process, err := startManagedProcess(command)
	if err != nil {
		return nil, true, sanitizedSupervisorError("start OpenCode server", err, password)
	}
	client, err := newAPIClient(baseURL, openCodeServerUsername, password, opts.Directory, &http.Client{})
	if err != nil {
		terminateAndReap(process)
		return nil, false, err
	}
	return &runningServer{process: process, client: client, baseURL: baseURL, logs: logs}, false, nil
}

func waitForOpenCodeServer(ctx context.Context, server *runningServer, password string) (bool, error) {
	for {
		requestCtx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
		health, healthErr := server.client.health(requestCtx)
		cancel()
		if healthErr == nil && health.Healthy {
			if err := validateServerVersion(health.Version); err != nil {
				terminateAndReap(server.process)
				return false, sanitizedSupervisorError("validate OpenCode server version", err, password)
			}
			return false, nil
		}
		switch waitForServerPoll(ctx, server.process) {
		case serverPollCanceled:
			terminateAndReap(server.process)
			return false, startupFailureError(ctx.Err(), server, password)
		case serverPollExited:
			return true, serverFailureError("before readiness", server, password)
		}
	}
}

type serverPollStatus uint8

const (
	serverPollContinue serverPollStatus = iota
	serverPollCanceled
	serverPollExited
)

func waitForServerPoll(ctx context.Context, process *managedProcess) serverPollStatus {
	timer := time.NewTimer(serverPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return serverPollCanceled
	case <-process.done:
		return serverPollExited
	case <-timer.C:
		return serverPollContinue
	}
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func validateServerVersion(value string) error {
	version, err := parseSemanticVersion(value)
	if err != nil || version.prerelease || version.compare(semanticVersion{major: 1, minor: 18, patch: 1}) < 0 {
		return fmt.Errorf("OpenCode server reported unsupported version %q; required version %s", displaytext.Sanitize(value), minimumVersion)
	}
	return nil
}

func selectRootSession(ctx context.Context, opts supervisorOptions, client *apiClient) (sessionInfo, error) {
	var (
		root sessionInfo
		err  error
	)
	if opts.ProviderSessionID == "" {
		root, err = client.createSession(ctx, opts.SessionName)
	} else {
		root, err = client.getSession(ctx, opts.ProviderSessionID)
		if errors.Is(err, errSessionNotFound) {
			return sessionInfo{}, fmt.Errorf("OpenCode session %s was not found; exact resume cannot continue", opts.ProviderSessionID)
		}
	}
	if err != nil {
		return sessionInfo{}, err
	}
	if !providerIDRE.MatchString(root.ID) || !store.ValidProviderSessionID(root.ID) {
		return sessionInfo{}, fmt.Errorf("OpenCode returned an invalid session ID")
	}
	if opts.ProviderSessionID != "" && root.ID != opts.ProviderSessionID {
		return sessionInfo{}, fmt.Errorf("OpenCode exact resume returned a different session ID")
	}
	if root.ParentID != "" {
		return sessionInfo{}, fmt.Errorf("OpenCode session %s is not a root session", root.ID)
	}
	if root.Directory != opts.Directory {
		return sessionInfo{}, fmt.Errorf("OpenCode session %s belongs to a different directory", root.ID)
	}
	return root, nil
}

func terminateAndReap(processes ...*managedProcess) {
	processes = managedProcesses(processes)
	signalProcessGroups(processes, syscall.SIGTERM)
	if !waitForProcessGroups(processes, childCleanupTimeout) {
		signalProcessGroups(processes, syscall.SIGKILL)
	}
	reapProcesses(processes)
}

func managedProcesses(processes []*managedProcess) []*managedProcess {
	result := make([]*managedProcess, 0, len(processes))
	for _, process := range processes {
		if process != nil {
			result = append(result, process)
		}
	}
	return result
}

func signalProcessGroups(processes []*managedProcess, signal syscall.Signal) {
	for _, process := range processes {
		_ = syscall.Kill(-process.cmd.Process.Pid, signal)
	}
}

func waitForProcessGroups(processes []*managedProcess, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		allExited := true
		for _, process := range processes {
			if err := syscall.Kill(-process.cmd.Process.Pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
				allExited = false
				break
			}
		}
		if allExited {
			return true
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return false
		}
	}
}

func reapProcesses(processes []*managedProcess) {
	for _, process := range processes {
		if process != nil {
			_ = process.waitError()
		}
	}
}

func attachExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return fmt.Errorf("wait for OpenCode attach: %w", err)
	}
	code := exitError.ExitCode()
	if code < 1 || code > 255 {
		code = 1
	}
	return &ExitError{Code: code}
}

func startupFailureError(cause error, server *runningServer, password string) error {
	detail := safeServerLogExcerpt(server.logs.Bytes(), password)
	if detail == "" {
		return fmt.Errorf("OpenCode server readiness timed out: %w", cause)
	}
	return fmt.Errorf("OpenCode server readiness timed out: %w (server output: %s)", cause, detail)
}

func serverFailureError(when string, server *runningServer, password string) error {
	waitErr := server.process.waitError()
	detail := safeServerLogExcerpt(server.logs.Bytes(), password)
	if detail == "" {
		return sanitizedSupervisorError("OpenCode server exited "+when, waitErr, password)
	}
	return sanitizedSupervisorError("OpenCode server exited "+when+" (server output: "+detail+")", waitErr, password)
}

func sanitizedSupervisorError(operation string, err error, password string) error {
	detail := ""
	if err != nil {
		detail = safeServerLogExcerpt([]byte(err.Error()), password)
	}
	if detail == "" {
		return fmt.Errorf("%s", displaytext.Sanitize(operation))
	}
	return fmt.Errorf("%s: %s", displaytext.Sanitize(operation), detail)
}

func safeServerLogExcerpt(data []byte, password string) string {
	value := displaytext.Sanitize(string(data))
	for _, secret := range []string{password, displaytext.Sanitize(password)} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 512 {
		value = string(runes[len(runes)-512:])
	}
	return value
}

func reconnectBackoff(attempt int) time.Duration {
	delay := eventReconnectBase
	for index := 0; index < attempt && delay < eventReconnectMax; index++ {
		delay *= 2
	}
	if delay > eventReconnectMax {
		return eventReconnectMax
	}
	return delay
}

func subscribeWithReconnect(ctx context.Context, client *apiClient, initialReady chan<- struct{}, events chan<- eventEnvelope) error {
	readySent := false
	for attempt := 0; ; attempt++ {
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- client.subscribe(ctx, ready, events)
		}()
		select {
		case <-ready:
			if !readySent {
				close(initialReady)
				readySent = true
			}
			if err := <-done; ctx.Err() != nil {
				return ctx.Err()
			} else if err == nil {
				return fmt.Errorf("OpenCode event stream ended without an error")
			}
		case err := <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil {
				return fmt.Errorf("OpenCode event subscription ended before readiness")
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		timer := time.NewTimer(reconnectBackoff(attempt))
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}
	}
}

type sessionCreatedEvent struct {
	SessionID string      `json:"sessionID"`
	Info      sessionInfo `json:"info"`
}

type permissionAskedEvent struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
}

type supervisorEventState struct {
	opts          supervisorOptions
	activeRoot    string
	rootFor       map[string]string
	acceptedRoots map[string]struct{}
	replied       map[string]struct{}
}

func newSupervisorEventState(opts supervisorOptions, rootID string) *supervisorEventState {
	return &supervisorEventState{
		opts:          opts,
		activeRoot:    rootID,
		rootFor:       map[string]string{rootID: rootID},
		acceptedRoots: map[string]struct{}{rootID: {}},
		replied:       make(map[string]struct{}),
	}
}

func (s *supervisorEventState) handle(ctx context.Context, client *apiClient, event eventEnvelope) error {
	switch event.Type {
	case "session.created":
		return s.handleSessionCreated(event.Properties)
	case "permission.asked":
		return s.handlePermissionAsked(ctx, client, event.Properties)
	default:
		return nil
	}
}

func (s *supervisorEventState) handleSessionCreated(properties json.RawMessage) error {
	var created sessionCreatedEvent
	if err := decodeStrictJSON(properties, &created); err != nil {
		return nil
	}
	info := created.Info
	if created.SessionID == "" || created.SessionID != info.ID || !providerIDRE.MatchString(info.ID) || !store.ValidProviderSessionID(info.ID) || info.Directory != s.opts.Directory {
		return nil
	}
	if info.ParentID != "" {
		root, ok := s.rootFor[info.ParentID]
		if !ok {
			return nil
		}
		s.rootFor[info.ID] = root
		return nil
	}
	if _, accepted := s.acceptedRoots[info.ID]; accepted {
		return nil
	}
	if err := session.WriteProviderIdentity(s.opts.RuntimeDir, s.opts.SessionName, info.ID); err != nil {
		warning := displaytext.Sanitize(fmt.Sprintf(
			"OpenCode provider identity update for %s failed; continuing with %s: %v",
			info.ID,
			s.activeRoot,
			err,
		))
		_, _ = fmt.Fprintln(os.Stderr, warning)
		return nil
	}
	s.activeRoot = info.ID
	s.rootFor[info.ID] = info.ID
	s.acceptedRoots[info.ID] = struct{}{}
	return nil
}

func (s *supervisorEventState) handlePermissionAsked(ctx context.Context, client *apiClient, properties json.RawMessage) error {
	if !s.opts.Yolo {
		return nil
	}
	var asked permissionAskedEvent
	if err := decodeStrictJSON(properties, &asked); err != nil {
		return nil
	}
	if !permissionIDRE.MatchString(asked.ID) || !store.ValidProviderSessionID(asked.ID) || !providerIDRE.MatchString(asked.SessionID) || !store.ValidProviderSessionID(asked.SessionID) {
		return nil
	}
	root, ok := s.rootFor[asked.SessionID]
	if !ok || root != s.activeRoot {
		return nil
	}
	if _, duplicate := s.replied[asked.ID]; duplicate {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.replyPermission(requestCtx, asked.ID); err != nil {
		return fmt.Errorf("reply to OpenCode permission %s: %w", asked.ID, err)
	}
	s.replied[asked.ID] = struct{}{}
	return nil
}
