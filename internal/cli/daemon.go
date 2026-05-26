package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipcclient"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/supervisor"
)

// RunDaemon handles `uam daemon start|stop|status|logs ...`. Designed to
// be invoked from cmd/uam/main.go before the TUI dispatcher runs.
func RunDaemon(args []string) {
	if len(args) < 1 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: uam daemon <start|stop|status|logs>")
		os.Exit(2)
	}
	switch args[0] {
	case "start":
		runDaemonStart(args[1:])
	case "stop":
		runDaemonStop(args[1:])
	case "status":
		runDaemonStatus(args[1:])
	case "logs":
		runDaemonLogs(args[1:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown daemon subcommand: %s\n", args[0])
		os.Exit(2)
	}
}

func runDaemonStart(args []string) {
	fs := flag.NewFlagSet("daemon start", flag.ExitOnError)
	detach := fs.Bool("detach", false, "double-fork into the background and return immediately")
	_ = fs.Parse(args)
	if *detach {
		if err := doubleForkDaemon(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "daemon start --detach:", err)
			os.Exit(1)
		}
		return
	}
	sup, err := supervisor.New(supervisor.Options{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "supervisor.New:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-stop
		sup.Shutdown()
		cancel()
	}()
	if err := sup.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(os.Stderr, "supervisor.Run:", err)
		os.Exit(1)
	}
}

func runDaemonStop(args []string) {
	_ = args
	sockPath := ipcclient.DefaultSocketPath()
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "daemon stop: daemon not running:", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindShutdown, ID: 1}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "daemon stop: write:", err)
		os.Exit(1)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ipc.ReadFrame(conn); err != nil {
		// EOF after shutdown is normal.
		if !strings.Contains(err.Error(), "EOF") {
			_, _ = fmt.Fprintln(os.Stderr, "daemon stop: read:", err)
		}
	}
	fmt.Println("daemon stopped")
}

func runDaemonStatus(args []string) {
	_ = args
	sockPath := ipcclient.DefaultSocketPath()
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		fmt.Println("daemon: not running")
		return
	}
	defer func() { _ = conn.Close() }()
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindHello, ID: 1}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "daemon status: write:", err)
		os.Exit(1)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := ipc.ReadFrame(conn)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "daemon status: read:", err)
		os.Exit(1)
	}
	var out struct {
		Pid int `json:"pid"`
	}
	_ = json.Unmarshal(resp.Payload, &out)
	fmt.Printf("daemon: running pid=%d socket=%s\n", out.Pid, sockPath)
}

func runDaemonLogs(args []string) {
	_ = args
	// The supervisor logs to stderr; for now point users at the runtime dir.
	rd := supervisor.DefaultRuntimeDir()
	fmt.Printf("logs live in %s (stderr of the daemon process).\n", rd)
}

// doubleForkDaemon spawns a detached copy of ourselves running
// `uam daemon start` (without --detach) and returns once the control
// socket is reachable.
func doubleForkDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	// #nosec G204 -- exe is our own resolved path; not user input.
	cmd := exec.Command(exe, "daemon", "start")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork+exec: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	sockPath := ipcclient.DefaultSocketPath()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become reachable within 5s")
}

// readDaemonPid is reserved for future use (e.g., signal-based stop).
func readDaemonPid() (int, error) {
	rd := supervisor.DefaultRuntimeDir()
	// #nosec G304 -- path constructed from the runtime dir; not user input.
	b, err := os.ReadFile(filepath.Join(rd, "uam.pid"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// Stub for future use; keeps the function reachable to silence "unused"
// without exporting it.
var _ = readDaemonPid
