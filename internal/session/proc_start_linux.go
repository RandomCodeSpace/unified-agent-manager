//go:build linux

package session

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procStartTime returns /proc/<pid>/stat field 22 (clock ticks since boot),
// or 0 when the process identity cannot be read.
func procStartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// comm (field 2) is parenthesized and may itself contain spaces or ')',
	// so split after the LAST ')'. starttime is overall field 22, i.e. index
	// 19 of the fields that follow comm.
	rest := string(data)
	if i := strings.LastIndexByte(rest, ')'); i >= 0 {
		rest = rest[i+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) < 20 {
		return 0
	}
	v, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	return v
}
