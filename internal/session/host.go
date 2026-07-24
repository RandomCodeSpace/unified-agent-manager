package session

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
)

// ProviderIdentityFileEnv names the provider-neutral identity handoff read by
// the host after the managed process exits.
const ProviderIdentityFileEnv = "UAM_PROVIDER_IDENTITY_FILE"

// Default PTY geometry for a detached session, matching the old
// `tmux new-session -x 200 -y 50` so unattached agents render wide output the
// same way they used to. The first attach resizes to the real terminal.
const (
	defaultCols = 200
	defaultRows = 50
)

// historyLines is the scrollback capacity of the host's terminal emulator.
// Deliberately larger than tmux's default 2000-line history costs nothing
// here (plain runes, no attributes) and gives peek deeper context.
const historyLines = 4000

const (
	minHistoryLines = 100
	maxHistoryLines = 100000
)

// killGrace phases the kill escalation: SIGHUP immediately (what tmux
// kill-session delivered), SIGTERM after one grace period, SIGKILL after two.
const killGrace = 1500 * time.Millisecond

// attachBufFrames is the per-client broadcast buffer. A client that falls
// this far behind the PTY stream (dead TCP-equivalent: a wedged terminal) is
// disconnected rather than allowed to stall the session.
const attachBufFrames = 512

const (
	markClosedRetryWindow = 2 * time.Second
	markClosedRetryBase   = 25 * time.Millisecond
	markClosedRetryMax    = 400 * time.Millisecond
)

// RunHost is the entry point of the detached per-session host process
// (`uam __host`). It starts the agent command under a PTY, mirrors all output
// into a terminal emulator (for peek/replay), serves the control socket, and
// on agent exit marks the persisted record closed before cleaning up its
// runtime files. It only returns on fatal startup errors or after the agent
// exits.
func RunHost(args []string) error {
	fs := flag.NewFlagSet("__host", flag.ContinueOnError)
	dir := fs.String("dir", DefaultDir(), "session runtime directory")
	name := fs.String("name", "", "session name")
	cwd := fs.String("cwd", "", "working directory for the agent")
	label := fs.String("label", "", "user-facing session label")
	providerIdentity := fs.String("provider", "", "managed-session provider identity")
	scrollbackLines := fs.Int("scrollback", historyLines, "terminal scrollback lines")
	var envs stringList
	fs.Var(&envs, "env", "KEY=VALUE environment entry (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	ready := readyPipe()
	err := runHost(*dir, hostLaunchSpec{
		name: *name, cwd: *cwd, label: *label, providerIdentity: *providerIdentity, scrollbackLines: *scrollbackLines, envs: envs, command: command,
	}, ready)
	if err != nil && ready != nil {
		// Surface the startup failure to the waiting parent before exiting.
		_, _ = fmt.Fprintf(ready, "error: %v\n", err)
		_ = ready.Close()
	}
	return err
}

// readyPipe returns the inherited readiness pipe (fd 3) when the host was
// spawned by a uam client, or nil when run by hand.
func readyPipe() *os.File {
	if os.Getenv("UAM_HOST_READY_FD") != "3" {
		return nil
	}
	return os.NewFile(3, "ready")
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type hostLaunchSpec struct {
	name             string
	cwd              string
	label            string
	providerIdentity string
	scrollbackLines  int
	envs             []string
	command          []string
}

type host struct {
	dir, name            string
	providerIdentityFile string

	mu        sync.Mutex
	controlMu sync.Mutex
	term      *vterm.Terminal
	ptmx      *os.File
	label     string
	state     State
	registry  *clientRegistry

	child *exec.Cmd
	// exited is closed once the agent process has been reaped; the kill
	// escalation stops there. cleaned is closed after shutdown has also
	// removed the runtime files, so a Kill reply means the session is fully
	// gone — a List immediately after must not see leftovers.
	exited      chan struct{}
	cleaned     chan struct{}
	uamStopping atomic.Bool
}

func runHost(dir string, spec hostLaunchSpec, ready *os.File) error {
	name := spec.name
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := validateProviderIdentity(spec.providerIdentity); err != nil {
		return err
	}
	if len(spec.command) == 0 {
		return errors.New("host requires a command")
	}
	scrollbackLines, err := validatedScrollbackLines(spec.scrollbackLines)
	if err != nil {
		return err
	}
	if err := EnsureDir(dir); err != nil {
		return err
	}
	if st, err := readState(dir, name); err == nil && st.hostAlive() {
		return fmt.Errorf("session %s already exists (host pid %d)", name, st.HostPID)
	}
	// Stale leftovers from a crashed host: safe to clear, the pid is gone.
	if err := removeSessionFiles(dir, name); err != nil {
		return fmt.Errorf("remove stale session files: %w", err)
	}

	ln, err := net.Listen("unix", SocketPath(dir, name))
	if err != nil {
		return fmt.Errorf("listen %s: %w", SocketPath(dir, name), err)
	}
	defer func() { _ = ln.Close() }()

	h := &host{
		dir:                  dir,
		name:                 name,
		providerIdentityFile: envValue(spec.envs, ProviderIdentityFileEnv),
		label:                spec.label,
		term:                 vterm.New(defaultCols, defaultRows, scrollbackLines),
		registry:             newClientRegistry(),
		exited:               make(chan struct{}),
		cleaned:              make(chan struct{}),
	}
	cmd := exec.Command(spec.command[0], spec.command[1:]...) // #nosec G204 -- argv comes from the trusted uam client that spawned this host; no shell is involved.
	cmd.Dir = spec.cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, spec.envs...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: defaultCols, Rows: defaultRows})
	if err != nil {
		return fmt.Errorf("start %s: %w", spec.command[0], err)
	}
	h.ptmx = ptmx
	h.child = cmd
	h.state = State{
		Name:             name,
		HostPID:          os.Getpid(),
		HostStart:        procStartTime(os.Getpid()),
		ChildPID:         cmd.Process.Pid,
		ChildStart:       procStartTime(cmd.Process.Pid),
		CreatedUnix:      time.Now().Unix(),
		Cwd:              spec.cwd,
		Label:            spec.label,
		ProviderIdentity: spec.providerIdentity,
		Command:          spec.command,
	}
	if err := writeState(dir, h.state); err != nil {
		h.signalChild(syscall.SIGKILL)
		return fmt.Errorf("write session state: %w", err)
	}
	if ready != nil {
		_, _ = fmt.Fprintln(ready, "ok")
		_ = ready.Close()
	}

	go h.acceptLoop(ln)
	go h.signalLoop()
	go h.freshenLoop()

	h.pumpPTY()

	// PTY EOF: the agent exited (or the pty was torn down). Reap it.
	exitCode := 0
	if waitErr := cmd.Wait(); waitErr != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	close(h.exited)
	// Release the socket path while it is still ours: closing the listener
	// unlinks it, and leaving that to the deferred Close would unlink AFTER
	// cleaned has signalled — i.e. after Kill has returned and a replacement
	// host (restart) may have created its own socket at the same path,
	// leaving that new host running but unreachable. Established connections
	// (the kill responder) are unaffected.
	_ = ln.Close()
	h.shutdown(exitCode)
	close(h.cleaned)
	// Give pending kill responders a moment to flush their replies before the
	// process (and every connection it owns) goes away.
	time.Sleep(50 * time.Millisecond)
	return nil
}

// pumpPTY copies agent output into the emulator and to every attached client
// until the PTY reaches EOF (agent exit).
func (h *host) pumpPTY() {
	buf := make([]byte, 32*1024)
	for {
		n, err := h.ptmx.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			h.mu.Lock()
			_, _ = h.term.Write(data)
			clients := h.registry.readyClients()
			h.mu.Unlock()
			for _, client := range clients {
				h.enqueueClient(client, serverMessage{kind: serverFramePTY, payload: data})
			}
		}
		if err != nil {
			return
		}
	}
}

// freshenInterval is how often the host bumps its runtime files' timestamps.
// The default runtime dir lives under the shared temp dir, where
// systemd-tmpfiles removes entries untouched for ~10 days; periodic touches
// keep a long-idle session's socket and state file from being aged out (the
// same cleanup famously eats idle tmux sockets).
const freshenInterval = 6 * time.Hour

// freshenLoop periodically re-stamps the state file and socket mtimes until
// the session shuts down. Best-effort: a failed touch only matters on systems
// that both age temp files and idle a session for days.
func (h *host) freshenLoop() {
	ticker := time.NewTicker(freshenInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.exited:
			return
		case <-ticker.C:
			now := time.Now()
			for _, path := range []string{statePath(h.dir, h.name), SocketPath(h.dir, h.name)} {
				if err := os.Chtimes(path, now, now); err != nil {
					log.Debug("freshen runtime file failed", "path", path, "error", err)
				}
			}
		}
	}
}

func (h *host) signalLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	<-ch
	// Forward termination to the agent; the normal exit path then runs.
	h.terminateChild()
}

func (h *host) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

func (h *host) handleConn(conn net.Conn) {
	if err := conn.SetReadDeadline(time.Now().Add(attachHandshakeTimeout)); err != nil {
		_ = conn.Close()
		return
	}
	br := bufio.NewReader(conn)
	var req request
	if err := readBoundedJSONLine(br, &req); err != nil {
		reason := "rejected"
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			reason = "timeout"
		}
		log.Diagnostic(log.DiagnosticEvent{Event: "attach.negotiation", Session: h.name, Reason: reason})
		_ = writeJSONLine(conn, response{Err: fmt.Sprintf("invalid request: %v", err)})
		_ = conn.Close()
		return
	}
	if err := conn.SetWriteDeadline(time.Now().Add(attachHandshakeTimeout)); err != nil {
		_ = conn.Close()
		return
	}
	switch req.Op {
	case opPeek:
		h.mu.Lock()
		data := h.term.Capture(req.Lines)
		h.mu.Unlock()
		_ = writeJSONLine(conn, response{OK: true, Data: data})
		_ = conn.Close()
	case opSend:
		err := h.writeOutOfBandInput([]byte(req.Text))
		_ = writeJSONLine(conn, errResponse(err))
		_ = conn.Close()
	case opResize:
		err := h.resizeOutOfBand(terminalSize{cols: req.Cols, rows: req.Rows})
		_ = writeJSONLine(conn, errResponse(err))
		_ = conn.Close()
	case opLabel:
		h.setLabel(req.Label)
		_ = writeJSONLine(conn, response{OK: true})
		_ = conn.Close()
	case opKill:
		h.uamStopping.Store(true)
		h.terminateChild()
		select {
		case <-h.cleaned:
			_ = writeJSONLine(conn, response{OK: true})
		case <-time.After(10 * time.Second):
			_ = writeJSONLine(conn, response{Err: "session did not exit"})
		}
		_ = conn.Close()
	case opAttach:
		h.handleAttach(conn, br, req)
	case opDoctor:
		report := h.runtimeDiagnostic()
		_ = writeJSONLine(conn, response{OK: true, Diagnostic: &report})
		_ = conn.Close()
	default:
		_ = writeJSONLine(conn, response{Err: fmt.Sprintf("unknown op %q", req.Op)})
		_ = conn.Close()
	}
}

func errResponse(err error) response {
	if err != nil {
		if errors.Is(err, ErrSessionBusy) {
			return response{Err: err.Error(), ErrorCode: errorCodeBusy}
		}
		return response{Err: err.Error()}
	}
	return response{OK: true}
}

func (h *host) setLabel(label string) {
	h.mu.Lock()
	h.label = label
	h.state.Label = label
	st := h.state
	title := []byte(titleSequence(label))
	clients := h.registry.readyClients()
	h.mu.Unlock()
	for _, client := range clients {
		h.enqueueClient(client, serverMessage{kind: serverFramePTY, payload: title})
	}
	if err := writeState(h.dir, st); err != nil {
		log.Warn("persist session label failed", "session", h.name, "error", err)
	}
}

// titleSequence sets the terminal title via OSC 0 — the native stand-in for
// tmux's set-titles-string showing the user-facing session label.
func titleSequence(label string) string {
	return "\x1b]0;" + displaytext.Sanitize(label) + "\x07"
}

func validSize(cols, rows int) bool {
	return cols > 0 && rows > 0 && cols <= 1000 && rows <= 1000
}

func resizeNudge(cols, rows int) (int, int, bool) {
	if !validSize(cols, rows) {
		return 0, 0, false
	}
	if rows > 1 {
		return cols, rows - 1, true
	}
	if cols > 1 {
		return cols - 1, rows, true
	}
	return 0, 0, false
}

func (h *host) applyResizeLocked(cols, rows int) {
	if !validSize(cols, rows) {
		return
	}
	h.term.Resize(cols, rows)
	h.applyPTYSizeLocked(cols, rows)
}

func (h *host) applyPTYSizeLocked(cols, rows int) {
	if !validSize(cols, rows) {
		return
	}
	_ = pty.Setsize(h.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) // #nosec G115 -- bounds checked above
}

func (h *host) handleAttach(conn net.Conn, br *bufio.Reader, req request) {
	version, err := negotiateAttachRequest(req)
	if err != nil {
		log.Diagnostic(log.DiagnosticEvent{Event: "attach.negotiation", Session: h.name, Reason: "rejected"})
		_ = writeJSONLine(conn, response{Err: err.Error()})
		_ = conn.Close()
		return
	}
	registration, err := attachRegistration(req, version)
	if err != nil {
		log.Diagnostic(log.DiagnosticEvent{
			Event: "attach.negotiation", Session: h.name, Protocol: int(version), Reason: "rejected",
		})
		_ = writeJSONLine(conn, response{Err: err.Error()})
		_ = conn.Close()
		return
	}
	client := &attachClient{
		conn: conn, out: make(chan serverMessage, attachBufFrames), done: make(chan struct{}), version: version,
		fallback: version == protocolV1 && !req.versionPresent,
	}
	attachResponse, err := h.registerAttachClient(client, registration)
	if err != nil {
		_ = writeJSONLine(conn, response{Err: err.Error()})
		_ = conn.Close()
		return
	}
	if err := writeAttachResponse(conn, attachResponse); err != nil {
		h.dropClientReason(client, "handshake_write")
		return
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		h.dropClientReason(client, "deadline_reset")
		return
	}
	h.initializeAttachClient(client, registration, attachResponse.Data)
	go h.attachWriter(client)
	h.attachReader(client, br)
}

func (h *host) attachWriter(client *attachClient) {
	for {
		select {
		case <-client.done:
			return
		case message := <-client.out:
			if err := h.writeServerMessage(client, message); err != nil {
				h.dropClientReason(client, "connection_write")
				return
			}
		}
	}
}

func (h *host) writeServerMessage(client *attachClient, message serverMessage) error {
	if client.version == protocolV1 {
		if message.kind != serverFramePTY {
			return nil
		}
		return writeAll(client.conn, message.payload)
	}
	if len(message.payload) == 0 {
		return writeFrame(client.conn, message.kind, nil)
	}
	for len(message.payload) > 0 {
		size := min(len(message.payload), maxFrameLen)
		if err := writeFrame(client.conn, message.kind, message.payload[:size]); err != nil {
			return err
		}
		message.payload = message.payload[size:]
	}
	return nil
}

func (h *host) attachReader(client *attachClient, br *bufio.Reader) {
	reason := "connection_drop"
	defer func() { h.dropClientReason(client, reason) }()
	for {
		kind, payload, err := readFrame(br)
		if err != nil {
			return
		}
		switch kind {
		case frameStdin:
			if !h.handleStdinFrame(client, payload) {
				reason = "malformed_frame"
				return
			}
		case frameResize:
			if !h.handleResizeFrame(client, payload) {
				reason = "malformed_frame"
				return
			}
		case frameRole:
			if !h.handleRoleCommand(client, payload) {
				reason = "malformed_frame"
				return
			}
		case frameDetach:
			reason = "detached"
			return
		default:
			reason = "malformed_frame"
			return
		}
	}
}

func (h *host) dropClient(client *attachClient) {
	h.dropClientReason(client, "dropped")
}

func (h *host) dropClientReason(client *attachClient, reason string) {
	h.controlMu.Lock()
	h.mu.Lock()
	_, registered := h.registry.clients[client]
	wasController := h.registry.controller == client
	changes := h.registry.remove(client)
	var promotedSize terminalSize
	var promotedClient *attachClient
	var promotedReplay []byte
	if len(changes) > 0 && h.registry.controller != nil {
		promotedClient = h.registry.controller
		promotedSize = promotedClient.latestSize
		if promotedSize.valid() && h.term != nil {
			h.term.Resize(promotedSize.cols, promotedSize.rows)
		}
		if promotedClient.ready && h.term != nil {
			promotedReplay = h.term.Redraw()
		}
	}
	h.mu.Unlock()
	if promotedSize.valid() {
		h.applyPTYSize(promotedSize)
	}
	h.controlMu.Unlock()
	client.drop()
	if !registered {
		return
	}
	log.Diagnostic(log.DiagnosticEvent{
		Event: "attach.lifecycle", Session: h.name, ClientID: client.id,
		Protocol: int(client.version), Role: string(client.assignedRole), Reason: reason,
	})
	h.enqueueRoleChanges(changes)
	if wasController && len(changes) == 0 && promotedClient == nil {
		log.Diagnostic(log.DiagnosticEvent{
			Event: "role.vacancy", Session: h.name, ClientID: client.id,
			Protocol: int(client.version), Role: string(client.assignedRole), Reason: "no_controller",
		})
	}
	for _, change := range changes {
		log.Diagnostic(log.DiagnosticEvent{
			Event: "role.promotion", Session: h.name, ClientID: change.clientID,
			Protocol: int(change.client.version), Role: string(change.role), Reason: change.reason,
		})
		log.Diagnostic(log.DiagnosticEvent{
			Event: "controller.failover", Session: h.name, ClientID: change.clientID,
			Protocol: int(change.client.version), Role: string(change.role), Reason: reason,
		})
	}
	if promotedClient != nil && len(promotedReplay) > 0 {
		h.enqueueClient(promotedClient, serverMessage{kind: serverFramePTY, payload: promotedReplay})
	}
}

// terminateChild escalates HUP → TERM → KILL against the agent's process
// group. SIGHUP first mirrors what tmux kill-session delivered, giving the
// agent a chance to save state.
func (h *host) terminateChild() {
	go func() {
		for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGKILL} {
			h.signalChild(sig)
			select {
			case <-h.exited:
				return
			case <-time.After(killGrace):
			}
		}
	}()
}

func (h *host) signalChild(sig syscall.Signal) {
	pid := h.state.ChildPID
	if pid <= 0 {
		return
	}
	// The child is its own session leader (pty.Start setsid), so its pid is
	// also its process-group id; the negative target signals the whole group.
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = syscall.Kill(pid, sig)
	}
}

// shutdown runs once the agent has been reaped: flag the persisted record
// closed (the native replacement for the tmux session-closed hook), tell any
// attached clients, and remove the runtime files.
func (h *host) shutdown(exitCode int) {
	h.mu.Lock()
	clients := h.registry.drain()
	h.mu.Unlock()
	for _, client := range clients {
		log.Diagnostic(log.DiagnosticEvent{
			Event: "attach.lifecycle", Session: h.name, ClientID: client.id,
			Protocol: int(client.version), Role: string(client.assignedRole), Reason: "host_shutdown",
		})
		client.drop()
	}
	providerID := readProviderIdentityHandoff(h.dir, h.name, h.providerIdentityFile)
	if err := removeSessionFiles(h.dir, h.name); err != nil {
		log.Warn("remove session files failed", "session", h.name, "error", err)
	}
	h.recordExit(exitCode, providerID)
}

func (h *host) recordExit(exitCode int, providerID string) {
	deadline := time.Now().Add(markClosedRetryWindow)
	delay := markClosedRetryBase
	var lastErr error
	for {
		st, err := store.Open(store.DefaultPath())
		if err == nil {
			var matched bool
			matched, err = st.TryRecordSessionExit(store.SessionExit{SessionName: h.name, ProviderSessionID: providerID, ExitCode: exitCode, UAMInitiated: h.uamStopping.Load()})
			if err == nil && matched {
				return
			}
		}
		if h.uamStopping.Load() && err == nil {
			return
		}
		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			h.logMarkClosedFailure(lastErr)
			return
		}
		time.Sleep(min(delay, remaining))
		delay = min(delay*2, markClosedRetryMax)
	}
}

func envValue(envs []string, key string) string {
	prefix := key + "="
	for i := len(envs) - 1; i >= 0; i-- {
		if strings.HasPrefix(envs[i], prefix) {
			return strings.TrimPrefix(envs[i], prefix)
		}
	}
	return ""
}

func readProviderIdentityHandoff(dir, name, path string) string {
	if path == "" {
		return ""
	}
	canonicalPath, err := ProviderIdentityPath(dir, name)
	if err != nil {
		return ""
	}
	canonicalPath, err = resolvePathParent(canonicalPath)
	if err != nil {
		return ""
	}
	path, err = resolvePathParent(path)
	if err != nil || path != canonicalPath {
		return ""
	}
	providerID, err := ReadProviderIdentity(dir, name)
	if err != nil {
		return ""
	}
	return providerID
}

func resolvePathParent(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(absolutePath))
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(absolutePath)), nil
}

func (h *host) logMarkClosedFailure(err error) {
	if err != nil {
		log.Warn("mark session closed failed after retry", "session", h.name, "error", err)
		return
	}
	log.Warn("mark session closed record not found before retry deadline", "session", h.name)
}
