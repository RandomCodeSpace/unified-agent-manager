package supervisor

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// AcquirePidfile opens path, takes LOCK_EX|LOCK_NB, and writes the current
// pid. Returns a release function that drops the lock and removes the
// file. Returns an error if another process already holds the lock.
func AcquirePidfile(path string) (release func(), err error) {
	// #nosec G304 -- path is supplied by the supervisor; it lives under
	// the runtime dir the supervisor owns.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("pidfile open: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("pidfile flock: another uam daemon is running (%w)", err)
	}
	if err := f.Truncate(0); err != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("pidfile truncate: %w", err)
	}
	if _, err := f.Write([]byte(strconv.Itoa(os.Getpid()) + "\n")); err != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("pidfile write: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}
