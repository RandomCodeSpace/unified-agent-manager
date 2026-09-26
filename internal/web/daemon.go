package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	// DefaultListen is the loopback address `uam web` binds by default.
	DefaultListen = "127.0.0.1:8260"

	stateFileName = "web.json"
	lockFileName  = "web.lock"
	readyEnv      = "UAM_WEB_READY_FD"

	readyTimeout    = 45 * time.Second
	stopWait        = 15 * time.Second
	shutdownTimeout = 15 * time.Second
)

// DaemonState is web.json: how to find and verify the running service. It
// never contains the access token.
type DaemonState struct {
	PID           int      `json:"pid"`
	StartTime     int64    `json:"start_time"`
	Listen        string   `json:"listen"`
	PublicOrigins []string `json:"public_origins,omitempty"`
	// LegacyNoAuth detects unsupported insecure daemons written by older
	// versions. New daemons never set this field.
	LegacyNoAuth bool      `json:"no_auth,omitempty"`
	LogHeaders   bool      `json:"log_headers,omitempty"`
	Version      string    `json:"version"`
	StartedAt    time.Time `json:"started_at"`
}

// URL is the address browsers on this host use (see LocalAddr).
func (st DaemonState) URL() string { return "http://" + st.LocalAddr() + "/" }

// LocalAddr is the host:port clients on this host connect to: the listen
// address, with an unspecified host replaced by loopback on the same port
// (0.0.0.0 by 127.0.0.1, :: by ::1).
func (st DaemonState) LocalAddr() string {
	host, port, err := net.SplitHostPort(st.Listen)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsUnspecified() {
		return st.Listen
	}
	if ip.To4() != nil {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return net.JoinHostPort("::1", port)
}

// DaemonConfig configures `uam __web`.
type DaemonConfig struct {
	Listen        string
	PublicOrigins []string
	Providers     []agentapi.Provider
	Version       string
	// LogHeaders logs every request's headers (see ServerConfig).
	LogHeaders bool
}

// ValidateListen accepts an IP literal (loopback, unspecified or an interface
// address) or localhost, and returns host:port with an IP literal. Other host
// names are refused.
func ValidateListen(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid --listen %q: %w", addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return "", fmt.Errorf("invalid --listen %q: bad port", addr)
	}
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("--listen %q is not an IP address; use an IP literal such as 127.0.0.1 or 0.0.0.0 (host names other than localhost are refused)", addr)
	}
	return net.JoinHostPort(ip.String(), port), nil
}

// BeyondLoopback reports whether listen (host:port) binds a non-loopback IP,
// which other machines may reach.
func BeyondLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && !ip.IsLoopback()
}

// listenNetwork binds exactly the listen address's family: plain "tcp" would
// turn 0.0.0.0 into a dual-stack [::] listener.
func listenNetwork(listen string) string {
	host, _, _ := net.SplitHostPort(listen)
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "tcp6"
	}
	return "tcp4"
}

func statePath(dir string) string { return filepath.Join(dir, stateFileName) }

// ReadRunning returns the running service's state after verifying that its
// PID still names the same process. A stale file reports not running.
func ReadRunning(dir string) (DaemonState, bool) {
	var st DaemonState
	if err := daemonruntime.VerifyDir(dir); err != nil {
		return st, false
	}
	data, err := os.ReadFile(statePath(dir)) // #nosec G304 -- fixed file name inside the verified owner-only runtime dir.
	if err != nil {
		return st, false
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return DaemonState{}, false
	}
	if !processMatches(st.PID, st.StartTime) {
		return DaemonState{}, false
	}
	return st, true
}

// processMatches is fail-closed: both the recorded and live start identity
// must be known and equal before the PID is trusted, let alone signalled.
func processMatches(pid int, start int64) bool {
	if pid <= 0 || start == 0 || !daemonruntime.ProcAlive(pid) {
		return false
	}
	return daemonruntime.ProcStartTime(pid) == start
}

func writeStateFile(dir string, st DaemonState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, stateFileName+".tmp.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), statePath(dir))
}

// lockDaemon takes the single-instance lock for the service's lifetime.
func lockDaemon(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed file name inside the verified runtime dir.
	if err != nil {
		return nil, fmt.Errorf("open web lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("uam web is already running")
		}
		return nil, fmt.Errorf("lock web service: %w", err)
	}
	return f, nil
}

// lockFree reports whether no service holds the lock (it is released when
// the service process exits, however it exits).
func lockFree(dir string) bool {
	f, err := lockDaemon(dir)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Spawn starts `uam __web` detached, exactly like a session host: its own
// session, stdio on /dev/null, readiness reported on fd 3. It returns once
// the service is serving or with the error the service reported.
func Spawn(ctx context.Context, exe string, args []string) error {
	devIn, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devIn.Close() }()
	devOut, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devOut.Close() }()
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create readiness pipe: %w", err)
	}
	defer func() { _ = r.Close() }()
	cmd := exec.Command(exe, append([]string{"__web"}, args...)...) // #nosec G204 -- exe is the running uam binary; args are built without a shell.
	// The service must outlive the launching terminal and SSH session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devIn, devOut, devOut
	cmd.Env = append(os.Environ(), readyEnv+"=3")
	cmd.ExtraFiles = []*os.File{w}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return fmt.Errorf("spawn uam web service: %w", err)
	}
	_ = w.Close()
	// Reap it whenever it exits so it never lingers as a zombie of a
	// long-lived launcher.
	go func() { _ = cmd.Wait() }()
	return waitReady(ctx, r, cmd.Process)
}

func waitReady(ctx context.Context, r *os.File, proc *os.Process) error {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		ch <- result{line: strings.TrimSpace(line), err: err}
	}()
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		if res.line == "ok" {
			return nil
		}
		if msg, found := strings.CutPrefix(res.line, "error: "); found {
			return fmt.Errorf("start uam web: %s", msg)
		}
		if res.err != nil {
			return fmt.Errorf("start uam web: service exited before it was ready: %w", res.err)
		}
		return fmt.Errorf("start uam web: unexpected response %q", res.line)
	case <-timer.C:
		// os.Process.Kill is pidfd-backed and refuses a reaped child, so it
		// can never hit a recycled PID.
		_ = proc.Kill()
		return errors.New("start uam web: the service did not become ready")
	case <-ctx.Done():
		_ = proc.Kill()
		return ctx.Err()
	}
}

// Stop asks the verified running service to shut down gracefully and waits
// for it. It reports false when nothing was running.
func Stop(ctx context.Context, dir string) (bool, error) {
	st, running := ReadRunning(dir)
	if !running {
		if lockFree(dir) {
			_ = os.Remove(statePath(dir))
		}
		return false, nil
	}
	if err := syscall.Kill(st.PID, syscall.SIGTERM); err != nil {
		return true, fmt.Errorf("signal uam web (pid %d): %w", st.PID, err)
	}
	deadline := time.NewTimer(stopWait)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if lockFree(dir) || !processMatches(st.PID, st.StartTime) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-deadline.C:
			return true, fmt.Errorf("uam web (pid %d) did not stop within %s", st.PID, stopWait)
		case <-tick.C:
		}
	}
}

// RunDaemon is `uam __web`: it serves until SIGTERM or SIGINT, then shuts
// down gracefully. Startup errors are reported on the readiness pipe.
func RunDaemon(cfg DaemonConfig) error {
	var ready *os.File
	if os.Getenv(readyEnv) == "3" {
		ready = os.NewFile(3, "ready")
		// Provider runtimes started by this service must not inherit the
		// launcher's pipe (or learn about it): an inherited write end would
		// hide this process's exit from the waiting launcher.
		syscall.CloseOnExec(3)
		_ = os.Unsetenv(readyEnv)
	}
	err := runDaemon(cfg, ready)
	if err != nil && ready != nil {
		_, _ = fmt.Fprintf(ready, "error: %v\n", err)
		_ = ready.Close()
	}
	return err
}

func runDaemon(cfg DaemonConfig, ready *os.File) error {
	listen, err := ValidateListen(cfg.Listen)
	if err != nil {
		return err
	}
	origins := make([]string, 0, len(cfg.PublicOrigins))
	for _, o := range cfg.PublicOrigins {
		normalized, err := NormalizePublicOrigin(o)
		if err != nil {
			return err
		}
		origins = append(origins, normalized)
	}
	dir := daemonruntime.DefaultDir()
	if err := daemonruntime.EnsureDir(dir); err != nil {
		return err
	}
	lock, err := lockDaemon(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	token, err := LoadOrCreateToken(TokenPath())
	if err != nil {
		return err
	}
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	mgr := NewManager(st, cfg.Providers)
	if err := mgr.Start(context.Background()); err != nil {
		return err
	}
	srv, err := NewServer(ServerConfig{Manager: mgr, Token: token, PublicOrigins: origins, LogHeaders: cfg.LogHeaders, Version: cfg.Version})
	if err != nil {
		_ = mgr.Shutdown(context.Background())
		return err
	}
	ln, err := net.Listen(listenNetwork(listen), listen)
	if err != nil {
		_ = mgr.Shutdown(context.Background())
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	state := DaemonState{
		PID: os.Getpid(), StartTime: daemonruntime.ProcStartTime(os.Getpid()), Listen: ln.Addr().String(),
		PublicOrigins: origins, LogHeaders: cfg.LogHeaders, Version: cfg.Version, StartedAt: time.Now().UTC(),
	}
	if err := writeStateFile(dir, state); err != nil {
		_ = ln.Close()
		_ = mgr.Shutdown(context.Background())
		return fmt.Errorf("write %s: %w", stateFileName, err)
	}
	defer func() { _ = os.Remove(statePath(dir)) }()

	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.L().Handler(), slog.LevelWarn),
	}
	// Event streams never go idle on their own; end them so Shutdown can
	// finish. Browsers reconnect to the next service.
	httpSrv.RegisterOnShutdown(mgr.DropSubscribers)
	// Notify (not Ignore) keeps default dispositions for anything this
	// process starts: providers must not inherit an ignored SIGHUP.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	if ready != nil {
		_, _ = fmt.Fprintln(ready, "ok")
		_ = ready.Close()
	}
	log.Info("uam web started", "pid", state.PID, "listen", state.Listen)

	var runErr error
wait:
	for {
		select {
		case sig := <-signals:
			if sig == syscall.SIGHUP {
				log.Info("uam web ignoring SIGHUP")
				continue
			}
			log.Info("uam web stopping", "signal", sig.String())
			break wait
		case err := <-serveErr:
			if !errors.Is(err, http.ErrServerClosed) {
				runErr = fmt.Errorf("serve: %w", err)
				log.Error("uam web server failed", "error", err)
			}
			break wait
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Warn("uam web http shutdown", "error", err)
	}
	if err := mgr.Shutdown(ctx); err != nil {
		log.Warn("uam web manager shutdown", "error", err)
	}
	log.Info("uam web stopped", "pid", state.PID)
	return runErr
}
