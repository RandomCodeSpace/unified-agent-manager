//go:build !linux && !darwin

package daemonruntime

// procStartTime reports that stable process identity is unavailable. Display
// liveness remains permissive, while PID-based fallback signaling fails closed.
func procStartTime(int) int64 { return 0 }
