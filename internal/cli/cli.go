package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

// Main is the process entrypoint shared by the root command and cmd/uam.
func Main() {
	flag.Usage = Usage
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version and exit")
	flag.Parse()
	args := flag.Args()
	if *showVersion {
		args = []string{"version"}
	}

	// Help and version are deliberately independent from both the cache logger
	// and the persistent store. They must remain usable when either location is
	// unavailable (for example during installation diagnostics).
	if handled, err := runBeforeLogger(args); handled {
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "uam: %v\n", err)
			os.Exit(1)
		}
		return
	}

	closer, err := log.Init()
	if err != nil {
		log.UseStderr(os.Stderr)
		fmt.Fprintf(os.Stderr, "uam: failed to initialize logger: %v\n", err)
	} else {
		defer func() { _ = closer.Close() }()
	}

	ctx := context.Background()
	if err := Run(ctx, args); err != nil && !errors.Is(err, context.Canceled) {
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		log.Error("run exited with error", "err", err)
		fmt.Fprintf(os.Stderr, "uam: %v\n", err)
		os.Exit(1)
	}
}

// Usage prints the supported web service commands.
func Usage() {
	fmt.Fprintln(os.Stderr, `uam — Copilot web workspace

usage:
  uam                              show this help
  uam web [--listen 127.0.0.1:8260] [--public-origin <url>]... [--log-headers]
                                   start the authenticated web service
  uam web status [--json]           show service status
  uam web stop                      stop the web service
  uam web token set                 set the access token from stdin
  uam help
  uam version`)
}

// Run executes web service commands without opening the terminal session store.
func Run(ctx context.Context, args []string) error {
	if handled, err := runBeforeLogger(args); handled {
		return err
	}
	switch args[0] {
	case "web":
		return runWeb(ctx, args[1:])
	case "__web":
		return runWebDaemon(args[1:])
	case "new", "dispatch", "attach", "last", "ls", "list", "stop", "restart", "rm", "kill-all", "profile", "doctor", "notify-closed", "__host", "__attach", "__opencode":
		return fmt.Errorf("%s: terminal support has been removed; use uam web", args[0])
	default:
		return fmt.Errorf("unknown command %q; run uam help", args[0])
	}
}

// Help and version must work without a writable cache or config directory.
func runBeforeLogger(args []string) (bool, error) {
	if len(args) == 0 {
		Usage()
		return true, nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		Usage()
		return true, nil
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return true, nil
	default:
		return false, nil
	}
}

func ignoreHelp(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}
