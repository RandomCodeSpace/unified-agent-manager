//go:build linux

package opencode

import (
	"os/exec"
	"syscall"
)

// setParentDeathSignal makes the kernel send SIGTERM to the server when the
// thread that started it exits, so a crashed uam does not orphan the server.
// Linux ties this to the forking OS thread; the Go runtime keeps threads alive
// unless a goroutine exits while locked to one, which uam does not do.
func setParentDeathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}
