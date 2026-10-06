package agentapi

import (
	"context"
	"errors"
)

var (
	// ErrCLIIncompatible wraps the provider SDK's refusal of a runtime
	// release, with the SDK's reason. Nothing was installed.
	ErrCLIIncompatible = errors.New("this uam cannot drive that release")
	// ErrCLIBusy refuses an update while conversations are still open on
	// the runtime. Nothing was installed.
	ErrCLIBusy = errors.New("conversations are still open on the runtime")
)

// CLIRelease is a provider runtime's installed and newest release.
type CLIRelease struct {
	// Installed is the version the runtime runs.
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
	// release the SDK refuses fails with ErrCLIIncompatible. Before the
	// running runtime is replaced, new starts wait and quiesce is called to
	// close idle conversations; its error, or a conversation still open
	// (ErrCLIBusy), stops the update before anything is installed. The next
	// use starts the new runtime. ctx bounds the whole update.
	UpdateCLI(ctx context.Context, version string, quiesce func() error) error
}
