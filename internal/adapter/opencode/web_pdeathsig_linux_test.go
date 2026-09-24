//go:build linux

package opencode

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestSetParentDeathSignalSurvivesManagedStart(t *testing.T) {
	command := exec.Command("/bin/true")
	setParentDeathSignal(command)
	process, err := startManagedProcess(command)
	if err != nil {
		t.Fatal(err)
	}
	_ = process.waitError()
	if attrs := command.SysProcAttr; attrs.Pdeathsig != syscall.SIGTERM || !attrs.Setpgid {
		t.Fatalf("SysProcAttr = %#v, want Pdeathsig SIGTERM with Setpgid", attrs)
	}
}
