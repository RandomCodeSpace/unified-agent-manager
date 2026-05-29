package tmux

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// ErrMalformedSessionLines is the sentinel returned by ParseListSessions when
// one or more lines could not be parsed. The parsed subset is still returned so
// a single bad line (e.g. a cwd containing '|') never blanks the whole list.
var ErrMalformedSessionLines = errors.New("one or more tmux session lines were malformed")

type SessionInfo struct {
	Name           string
	CreatedUnix    int64
	Attached       bool
	PanePID        int
	CurrentPath    string
	CurrentCommand string
}

const ListFormat = "#{session_name}|#{session_created}|#{session_attached}|#{pane_pid}|#{pane_current_path}|#{pane_current_command}"

func ParseSessionLine(line string) (SessionInfo, error) {
	// Right-anchored split: the first four fields and the trailing command are
	// fixed-position, so pane_current_path (field 5) keeps any embedded '|'.
	// SplitN caps the left split at five pieces, leaving path|command in head[4].
	head := strings.SplitN(line, "|", 5)
	if len(head) != 5 {
		return SessionInfo{}, fmt.Errorf("expected 6 fields, got %d", len(head))
	}
	cut := strings.LastIndex(head[4], "|")
	if cut < 0 {
		return SessionInfo{}, fmt.Errorf("expected 6 fields, got %d", len(head))
	}
	path, command := head[4][:cut], head[4][cut+1:]
	created, err := strconv.ParseInt(strings.TrimSpace(head[1]), 10, 64)
	if err != nil {
		return SessionInfo{}, fmt.Errorf("parse created: %w", err)
	}
	attachedInt, err := strconv.Atoi(strings.TrimSpace(head[2]))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("parse attached: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(head[3]))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("parse pane pid: %w", err)
	}
	return SessionInfo{
		Name:           strings.TrimSpace(head[0]),
		CreatedUnix:    created,
		Attached:       attachedInt != 0,
		PanePID:        pid,
		CurrentPath:    strings.TrimSpace(path),
		CurrentCommand: strings.TrimSpace(command),
	}, nil
}

func ParseListSessions(output string) ([]SessionInfo, error) {
	var sessions []SessionInfo
	malformed := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		info, err := ParseSessionLine(line)
		if err != nil {
			// Skip-and-log: one unparseable line must not blank the whole list.
			// The healthy subset is still returned with a sentinel error (F11).
			log.Warn("skipping malformed tmux session line", "line", line, "error", err)
			malformed++
			continue
		}
		sessions = append(sessions, info)
	}
	if malformed > 0 {
		return sessions, fmt.Errorf("%w: skipped %d line(s)", ErrMalformedSessionLines, malformed)
	}
	return sessions, nil
}
