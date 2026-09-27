package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The web terminal (ADR 0004, "Web terminal (opt-in)"): while Settings →
// Terminal is on, a WebSocket at a Project runs a login shell in its
// directory on a PTY. The shell lives exactly as long as its socket; there
// is no reattach.
const (
	maxTerminals        = 8
	maxTerminalDim      = 500
	defaultTerminalCols = 80
	defaultTerminalRows = 24
	// terminalReadLimit caps one client message; terminalChunk is the most
	// output one server message carries.
	terminalReadLimit = 64 << 10
	terminalChunk     = 32 << 10
	// terminalKillWait is how long a hung-up shell has before its process
	// group is killed; terminalDrainWait is how long the output an exited
	// shell left behind has to arrive.
	terminalKillWait  = 3 * time.Second
	terminalDrainWait = time.Second
)

var errTerminalOff = newError(http.StatusNotFound, "the terminal is turned off in Settings")

// terminal is one open terminal. ctx ends, with the reason as its cause,
// when the service stops or the setting is turned off.
type terminal struct {
	projectID, dir string
	ctx            context.Context
	cancel         context.CancelCauseFunc
}

// openTerminal reserves one of maxTerminals for a Project; closeTerminal
// must follow.
func (m *Manager) openTerminal(projectID string) (*terminal, error) {
	m.mu.Lock()
	on, p := m.settings.Terminal, m.projects[projectID]
	var dir string
	if p != nil {
		dir = p.Dir
	}
	m.mu.Unlock()
	switch {
	case !on:
		return nil, errTerminalOff
	case p == nil:
		return nil, errProjectNotFound
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, newError(http.StatusConflict, "the project directory %s no longer exists", dir)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.closed:
		return nil, errShuttingDown
	case !m.settings.Terminal:
		return nil, errTerminalOff
	case len(m.terminals) >= maxTerminals:
		return nil, newError(http.StatusTooManyRequests, "too many terminals open")
	}
	t := &terminal{projectID: projectID, dir: dir}
	t.ctx, t.cancel = context.WithCancelCause(context.Background())
	if m.terminals == nil {
		m.terminals = make(map[*terminal]struct{})
	}
	m.terminals[t] = struct{}{}
	m.terminalWG.Add(1)
	return t, nil
}

func (m *Manager) closeTerminal(t *terminal) {
	t.cancel(nil)
	m.mu.Lock()
	delete(m.terminals, t)
	m.mu.Unlock()
	m.terminalWG.Done()
}

// closeTerminalsLocked ends every open terminal for reason.
func (m *Manager) closeTerminalsLocked(reason error) {
	for t := range m.terminals {
		t.cancel(reason)
	}
}

// handleTerminal upgrades to the WebSocket of a new terminal. The
// cross-origin check in ServeHTTP skips GET, which a WebSocket handshake is:
// a cross-site request is refused here, and Accept refuses an Origin other
// than this Host or a public origin.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	t, err := s.m.openTerminal(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer s.m.closeTerminal(t)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.terminalOrigins})
	if err != nil {
		// Accept has answered.
		log.Debug("web terminal refused", "project", t.projectID, "error", err)
		return
	}
	q := r.URL.Query()
	t.run(conn, queryDim(q.Get("cols"), defaultTerminalCols), queryDim(q.Get("rows"), defaultTerminalRows))
}

// originPattern is a public origin as a coder/websocket origin pattern,
// which path.Match reads: the brackets of an IPv6 host are escaped.
func originPattern(origin string) string {
	return strings.NewReplacer(`\`, `\\`, `[`, `\[`, `*`, `\*`, `?`, `\?`).Replace(origin)
}

// run connects the shell and conn until the shell exits, the socket closes,
// the service stops or the setting is turned off. Each way out hangs up the
// shell's process group and reaps the shell.
func (t *terminal) run(conn *websocket.Conn, cols, rows uint16) {
	conn.SetReadLimit(terminalReadLimit)
	shell, err := terminalShell()
	if err != nil {
		log.Warn("web terminal found no shell", "project", t.projectID, "error", err)
		_ = conn.Close(websocket.StatusInternalError, "no shell found")
		return
	}
	cmd := exec.Command(shell, "-l") // #nosec G204 -- the service user's own absolute $SHELL, or bash or sh from fixed system directories.
	cmd.Dir = t.dir
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, readyEnv+"=") }),
		"TERM=xterm-256color", "COLORTERM=truecolor")
	started, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		log.Warn("web terminal shell did not start", "project", t.projectID, "error", err)
		_ = conn.Close(websocket.StatusInternalError, "the shell did not start")
		return
	}
	pid, opened := cmd.Process.Pid, time.Now()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	ptmx, err := pollable(started)
	if err != nil {
		log.Warn("web terminal PTY failed", "project", t.projectID, "error", err)
		hangUp(pid, exited)
		_ = conn.Close(websocket.StatusInternalError, "the shell did not start")
		return
	}
	log.Info("web terminal opened", "project", t.projectID, "pid", pid, "cols", cols, "rows", rows)

	gone := make(chan struct{})
	var goneOnce sync.Once
	socketGone := func() { goneOnce.Do(func() { close(gone) }) }
	output := make(chan struct{})
	go func() {
		defer close(output)
		sendOutput(conn, ptmx, socketGone)
	}()
	input := make(chan struct{})
	go func() {
		defer close(input)
		readInput(conn, ptmx)
		socketGone()
	}()

	shellExited := false
	select {
	case <-exited:
		shellExited = true
		select {
		case <-output:
		case <-time.After(terminalDrainWait):
		}
	case <-gone:
	case <-t.ctx.Done():
	}
	hangUp(pid, exited)
	// Through the poller, closing the PTY ends a read or write blocked on
	// it, even while a process outside the group still holds the terminal.
	_ = ptmx.Close()
	code := exitCode(cmd.ProcessState)
	switch {
	case shellExited:
		ctx, cancel := context.WithTimeout(context.Background(), streamWriteWait)
		if conn.Write(ctx, websocket.MessageText, []byte(`{"type":"exit","code":`+strconv.Itoa(code)+`}`)) == nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
		cancel()
	case t.ctx.Err() != nil:
		_ = conn.Close(websocket.StatusGoingAway, context.Cause(t.ctx).Error())
	}
	_ = conn.CloseNow()
	<-output
	<-input
	log.Info("web terminal closed", "project", t.projectID, "pid", pid, "code", code, "duration", time.Since(opened).Round(time.Millisecond).String())
}

// terminalShell is $SHELL when it is an absolute path to an executable, else
// bash, else sh, from fixed system directories.
func terminalShell() (string, error) {
	if shell := os.Getenv("SHELL"); execpath.ValidateAbsoluteExecutable(shell) == nil {
		return shell, nil
	}
	if bash, err := execpath.Resolve("bash"); err == nil {
		return bash, nil
	}
	return execpath.Resolve("sh")
}

// pollable returns a nonblocking duplicate of the PTY master f and closes f.
// creack/pty leaves the master blocking, and a read or write blocked on a
// blocking descriptor outlives Close; through the runtime poller, Close ends
// it.
func pollable(f *os.File) (*os.File, error) {
	defer func() { _ = f.Close() }()
	fd, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

// hangUp sends SIGHUP to the shell's process group, and SIGKILL when the
// shell has not exited within terminalKillWait, then waits until the shell
// is reaped. The group's ID is the shell's PID, which may be reused once the
// shell is reaped, so a reaped shell's group is not signalled.
func hangUp(pid int, exited <-chan struct{}) {
	signal := func(sig syscall.Signal) {
		select {
		case <-exited:
		default:
			_ = syscall.Kill(-pid, sig)
		}
	}
	signal(syscall.SIGHUP)
	select {
	case <-exited:
		return
	case <-time.After(terminalKillWait):
	}
	signal(syscall.SIGKILL)
	<-exited
}

// exitCode is the shell's exit status, or 128 plus the signal that ended
// it, as shells report it.
func exitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// sendOutput sends what the shell writes as binary messages until the PTY
// or the socket fails; gone reports the socket's failure. A slow client
// blocks the write and the shell then blocks on its terminal, so memory
// stays bounded.
func sendOutput(conn *websocket.Conn, ptmx *os.File, gone func()) {
	buf := make([]byte, terminalChunk)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 && conn.Write(context.Background(), websocket.MessageBinary, buf[:n]) != nil {
			gone()
			return
		}
		if err != nil {
			return
		}
	}
}

// readInput writes binary messages to the PTY as they are and applies resize
// messages until the socket closes. Other messages are ignored.
func readInput(conn *websocket.Conn, ptmx *os.File) {
	for {
		typ, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			// Input for a shell that is gone is dropped.
			_, _ = ptmx.Write(data)
			continue
		}
		var msg struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
			resize(ptmx, clampDim(msg.Cols), clampDim(msg.Rows))
		}
	}
}

// resize sets the PTY's size through the poller: pty.Setsize would make the
// descriptor blocking again.
func resize(ptmx *os.File, cols, rows uint16) {
	rc, err := ptmx.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		_ = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	})
}

// queryDim is a cols or rows query value, clamped; def when v is absent or
// not a number.
func queryDim(v string, def int) uint16 {
	n, err := strconv.Atoi(v)
	if err != nil {
		n = def
	}
	return clampDim(n)
}

// clampDim clamps a column or row count to 1..maxTerminalDim.
func clampDim(n int) uint16 {
	switch {
	case n < 1:
		return 1
	case n > maxTerminalDim:
		return maxTerminalDim
	}
	return uint16(n)
}
