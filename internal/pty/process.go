//go:build unix

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// SpawnArgs is the input to Spawn.
type SpawnArgs struct {
	Argv []string
	Env  []string
	Cwd  string
}

// Child holds the spawned process.
type Child struct {
	Cmd *exec.Cmd
}

// Spawn fork+exec's argv with the PTY slave as the controlling tty.
// Master fd remains open in parent for reading/writing.
func Spawn(p *PTY, args SpawnArgs) (*Child, error) {
	if len(args.Argv) == 0 {
		return nil, fmt.Errorf("Spawn: empty argv")
	}
	cmd := exec.Command(args.Argv[0], args.Argv[1:]...) // #nosec G204 — caller-supplied agent argv, trusted at this boundary
	cmd.Dir = args.Cwd
	cmd.Env = append(os.Environ(), args.Env...)
	cmd.Stdin = p.Slave
	cmd.Stdout = p.Slave
	cmd.Stderr = p.Slave
	// Setctty interprets Ctty as the child-side fd index (post-dup), not
	// the parent-side fd. With slave assigned to Stdin/Stdout/Stderr, the
	// controlling tty in the child is fd 0.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Spawn: %w", err)
	}
	// Close slave in parent — the child has its own dup'd copies.
	_ = p.Slave.Close()
	p.Slave = nil
	return &Child{Cmd: cmd}, nil
}

// Wait blocks until the child exits.
func (c *Child) Wait() (*os.ProcessState, error) {
	if c.Cmd == nil || c.Cmd.Process == nil {
		return nil, fmt.Errorf("Wait: child not started")
	}
	if err := c.Cmd.Wait(); err != nil {
		return c.Cmd.ProcessState, err
	}
	return c.Cmd.ProcessState, nil
}

// Pid returns the child's process id.
func (c *Child) Pid() int {
	if c.Cmd == nil || c.Cmd.Process == nil {
		return 0
	}
	return c.Cmd.Process.Pid
}

// Kill sends SIGKILL to the child.
func (c *Child) Kill() error {
	if c.Cmd == nil || c.Cmd.Process == nil {
		return nil
	}
	return c.Cmd.Process.Kill()
}

// Terminate sends SIGTERM.
func (c *Child) Terminate() error {
	if c.Cmd == nil || c.Cmd.Process == nil {
		return nil
	}
	return c.Cmd.Process.Signal(syscall.SIGTERM)
}
