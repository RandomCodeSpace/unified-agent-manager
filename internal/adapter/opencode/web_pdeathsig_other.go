//go:build !linux

package opencode

import "os/exec"

// setParentDeathSignal is Linux-only; elsewhere Shutdown remains the only
// cleanup path.
func setParentDeathSignal(*exec.Cmd) {}
