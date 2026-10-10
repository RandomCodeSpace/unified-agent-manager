package web

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func trailPaths(use agentapi.ToolUse) []string {
	var paths []string
	switch use.Tool {
	case "edit", "create":
		if path, _ := use.Args["path"].(string); path != "" {
			paths = append(paths, path)
		}
	case "apply_patch":
		patch, _ := use.Args["patch"].(string)
		for line := range strings.SplitSeq(patch, "\n") {
			for _, prefix := range []string{"*** Update File: ", "*** Add File: "} {
				if path, ok := strings.CutPrefix(line, prefix); ok && strings.TrimSpace(path) != "" {
					paths = append(paths, strings.TrimSpace(path))
				}
			}
		}
	}
	for i, path := range paths {
		paths[i] = editPath(use.Workdir, path)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// preToolUse is bound to the Task on create and resume, including its subagents.
func (m *Manager) preToolUse(_ context.Context, s *webSession, use agentapi.ToolUse) agentapi.ToolVerdict {
	paths := trailPaths(use)
	if len(paths) == 0 {
		return agentapi.ToolVerdict{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || s.removed || m.sessions[s.id] != s {
		return agentapi.ToolVerdict{}
	}
	if !s.trailTurn.Equal(s.turnStart) {
		s.trailTurn = s.turnStart
		s.trailPaths = nil
	}
	var notes []string
	now := m.now()
	for _, path := range paths {
		if s.trailPaths[path] || s.isPlanPath(path) {
			continue
		}
		n := 0
		for _, edit := range m.siblingEditsLocked(s, path) {
			sibling := edit.Session
			if sibling.stage != StageActive || (!turnRunning(sibling.base) && sibling.runningSubagents() == 0) || now.Sub(edit.At) > 30*time.Minute || edit.At.After(now) {
				continue
			}
			display := path
			if relative, err := filepath.Rel(s.workdir, path); err == nil {
				display = relative
			}
			notes = append(notes, fmt.Sprintf("uam trail: Task %q edited %s %d min ago and is still running. Re-read it first.", firstNonEmpty(sibling.name, sibling.title, "New task"), display, int(now.Sub(edit.At)/time.Minute)))
			n++
			if n == 3 {
				break
			}
		}
		if n > 0 {
			if s.trailPaths == nil {
				s.trailPaths = map[string]bool{}
			}
			s.trailPaths[path] = true
		}
	}
	return agentapi.ToolVerdict{Context: strings.Join(notes, "\n")}
}
