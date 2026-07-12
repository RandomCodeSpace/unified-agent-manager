//go:build darwin

package session

import "golang.org/x/sys/unix"

// procStartTime returns the process start timestamp in microseconds since the
// Unix epoch, or 0 when Darwin cannot provide a stable identity.
func procStartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info == nil {
		return 0
	}
	started := info.Proc.P_starttime
	if started.Sec <= 0 {
		return 0
	}
	return started.Sec*1_000_000 + int64(started.Usec)
}
