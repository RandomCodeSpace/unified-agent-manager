package session

import (
	"errors"
	"fmt"
)

const errorCodeBusy = "busy"

// ErrSessionBusy identifies operations rejected while a controller owns the PTY.
var ErrSessionBusy = errors.New("session controller is attached")

// SessionBusyError preserves the rejected operation across the host protocol.
type SessionBusyError struct {
	Operation string
}

func (err *SessionBusyError) Error() string {
	return fmt.Sprintf("%s: %s", err.Operation, ErrSessionBusy)
}

func (err *SessionBusyError) Is(target error) bool {
	return target == ErrSessionBusy
}
