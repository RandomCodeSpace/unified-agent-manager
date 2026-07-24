package session

import (
	"fmt"
	"math"
)

func attachInputFD(fd uintptr) (int32, error) {
	if fd > math.MaxInt32 {
		return 0, fmt.Errorf("terminal input file descriptor %d exceeds poll range", fd)
	}
	return int32(fd), nil // #nosec G115 -- fd is bounded to MaxInt32 above.
}
