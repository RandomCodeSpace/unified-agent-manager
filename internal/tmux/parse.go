package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

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
	parts := strings.Split(line, "|")
	if len(parts) != 6 {
		return SessionInfo{}, fmt.Errorf("expected 6 fields, got %d", len(parts))
	}
	created, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return SessionInfo{}, fmt.Errorf("parse created: %w", err)
	}
	attachedInt, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("parse attached: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(parts[3]))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("parse pane pid: %w", err)
	}
	return SessionInfo{
		Name:           strings.TrimSpace(parts[0]),
		CreatedUnix:    created,
		Attached:       attachedInt != 0,
		PanePID:        pid,
		CurrentPath:    strings.TrimSpace(parts[4]),
		CurrentCommand: strings.TrimSpace(parts[5]),
	}, nil
}

func ParseListSessions(output string) ([]SessionInfo, error) {
	var sessions []SessionInfo
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		info, err := ParseSessionLine(line)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, info)
	}
	return sessions, nil
}
