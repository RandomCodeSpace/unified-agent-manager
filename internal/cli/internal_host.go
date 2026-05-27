package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/host"
)

// RunInternalHost handles `uam internal-host --config <json>`. Invoked
// by the supervisor via fork+exec; not user-facing.
func RunInternalHost(args []string) {
	fs := flag.NewFlagSet("internal-host", flag.ExitOnError)
	configJSON := fs.String("config", "", "JSON-encoded host.Config")
	_ = fs.Parse(args)
	if *configJSON == "" {
		_, _ = fmt.Fprintln(os.Stderr, "internal-host: --config required")
		os.Exit(2)
	}
	var cfg host.Config
	if err := json.Unmarshal([]byte(*configJSON), &cfg); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "internal-host: bad config:", err)
		os.Exit(2)
	}
	h, err := host.New(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "internal-host:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()
	if err := h.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(os.Stderr, "internal-host:", err)
		os.Exit(1)
	}
}
