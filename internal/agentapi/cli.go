package agentapi

import (
	"context"
	"errors"
)

var (
	// ErrCLIIncompatible wraps the provider SDK's refusal of a runtime
	// release, with the SDK's reason. Nothing was installed.
	ErrCLIIncompatible = errors.New("this uam cannot drive that release")
	// ErrCLIBusy refuses a restart while conversations are still open on
	// the runtime. Nothing was stopped.
	ErrCLIBusy = errors.New("conversations are still open on the runtime")
)

// CLIRelease is a provider runtime's installed and newest release.
type CLIRelease struct {
	// Installed is the version installed, which the next start runs.
	Installed string
	// Latest is the newest stable release; Newer is true when it is newer
	// than Installed. The adapter compares them: version schemes are its own.
	Latest string
	Newer  bool
	// Manual says why the runtime cannot be updated from here, such as an
	// install the adapter does not manage; empty when it can.
	Manual string
}

// CLIUpdater is implemented by a provider whose Capabilities.CLIUpdate is
// true: it updates its runtime, but only to a release its SDK accepts.
type CLIUpdater interface {
	// CLIRelease reads the installed and newest release. With an error,
	// Installed may still be set when only the newest could not be read.
	CLIRelease(ctx context.Context) (CLIRelease, error)
	// UpdateCLI installs version, a Latest that CLIRelease reported. A
	// release the SDK refuses fails with ErrCLIIncompatible. A running
	// runtime keeps serving, conversations opened meanwhile included; only
	// the start of a new one waits while the files are replaced. ctx bounds
	// the whole update.
	UpdateCLI(ctx context.Context, version string) error
	// RestartCLI stops a running runtime whose release was replaced (see
	// CLIOutdated), so the next use starts the installed one. New starts
	// wait meanwhile, and quiesce is called first to close idle
	// conversations; its error, or anything still running on the runtime
	// (ErrCLIBusy), leaves it running for a later call. With no such runtime
	// it does nothing and returns nil.
	RestartCLI(ctx context.Context, quiesce func() error) error
	// CLIOutdated reports whether a running runtime runs a release replaced
	// on disk since it started, by UpdateCLI or by an install made outside
	// uam; RestartCLI then restarts it like an update's.
	CLIOutdated() bool
	// RestartRuntime stops the running runtime whatever runs on it, so the
	// next use starts a new one with fresh connections. A running turn ends
	// as cancelled with reason as its note; an idle conversation ends and
	// reopens on its next use. New starts wait meanwhile. With no runtime
	// running it does nothing and returns nil.
	RestartRuntime(ctx context.Context, reason string) error
	// RuntimeStalls counts the turns the running runtime's model requests
	// stalled twice in since it started; zero after a restart.
	RuntimeStalls() int
}
