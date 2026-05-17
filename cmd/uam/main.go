package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/randomcodespace/unified-agent-manager/internal/app"
	"github.com/randomcodespace/unified-agent-manager/internal/log"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "uam — unified agent manager")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "usage:")
		fmt.Fprintln(os.Stderr, "  uam          open the TUI (default)")
		fmt.Fprintln(os.Stderr, "  uam --help   show this help")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "subcommands (planned, not yet implemented):")
		fmt.Fprintln(os.Stderr, "  new, dispatch, attach, last, ls, peek, stop, rm")
	}
	flag.Parse()

	closer, err := log.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uam: failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer closer.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("run exited with error", "err", err)
		fmt.Fprintf(os.Stderr, "uam: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	p := tea.NewProgram(app.New(), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
