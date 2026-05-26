package main

import (
	"os"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/cli"
)

func main() {
	// Dispatch internal subcommands before the standard CLI entry so they
	// bypass log init and TUI wiring. These are not user-facing in the
	// usual sense — `daemon` is the user-managed supervisor; `internal-host`
	// is forked by the supervisor; `attach --raw` is the placeholder raw
	// stdio attach (v0.1.13: errors out).
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			cli.RunDaemon(os.Args[2:])
			return
		case "internal-host":
			cli.RunInternalHost(os.Args[2:])
			return
		case "attach":
			if len(os.Args) > 2 && os.Args[2] == "--raw" {
				cli.RunAttachRaw(os.Args[3:])
				return
			}
		}
	}
	cli.Main()
}
