package web

import (
	"context"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// failureHint is one line for the agent after a view of a missing path or a
// patch whose lines no longer match: the one file the path most likely meant,
// or that another Thread edited the file this turn. It says only what uam can
// show, never file content, and "" otherwise. The CLI texts matched are
// "Path does not exist" and "Failed to apply patch: Error: Failed to find
// expected lines in <path>:"; a reworded text gives no hint.
func (m *Manager) failureHint(ctx context.Context, s *webSession, use agentapi.ToolUse, failure string) string {
	switch use.Tool {
	case "view":
		if strings.Contains(strings.ToLower(failure), "does not exist") {
			return m.viewHint(ctx, s, use)
		}
	case "apply_patch":
		if strings.Contains(failure, "expected lines") {
			return m.patchHint(s, use, failure)
		}
	}
	return ""
}

func (m *Manager) hintableLocked(s *webSession) bool {
	return !m.closed && !s.removed && s.base != StateClosed && m.sessions[s.id] == s
}

// viewHint names the one file of the Thread's folder that a missing path
// inside it means, by its path there or else its bare name.
func (m *Manager) viewHint(ctx context.Context, s *webSession, use agentapi.ToolUse) string {
	raw, _ := use.Args["path"].(string)
	m.mu.Lock()
	live := m.hintableLocked(s)
	workdir, roots := s.workdir, []string{s.workdir, s.realWorkdir}
	recorded := slices.Collect(maps.Keys(s.edits))
	recorded = append(recorded, s.nativePaths...)
	m.mu.Unlock()
	if !live || raw == "" || workdir == "" {
		return ""
	}
	name := raw
	if filepath.IsAbs(raw) {
		name = ""
		for _, root := range roots {
			if r, err := filepath.Rel(root, filepath.Clean(raw)); root != "" && err == nil && filepath.IsLocal(r) {
				name = r
				break
			}
		}
	}
	if name == "" || readableTaskFile(workdir, name) {
		return ""
	}
	files := []resolvedFile{{Path: name}}
	if base := path.Base(filepath.ToSlash(filepath.Clean(name))); base != filepath.ToSlash(filepath.Clean(name)) {
		files = append(files, resolvedFile{Path: base})
	}
	// An unfinished search proves nothing unique, so a slow folder gives no hint.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m.findTargets(ctx, s, workdir, roots, recorded, files)
	for _, f := range files {
		if f.Target != "" {
			m.count("hints_view_path")
			return "uam: did you mean " + filepath.Join(workdir, filepath.FromSlash(f.Target)) + "?"
		}
	}
	return ""
}

// patchHint says another Thread of the Project edited a patched file the
// failure names after this turn began and after this Thread's own last edit
// of it, unless a trail note already said so for this turn.
func (m *Manager) patchHint(s *webSession, use agentapi.ToolUse, failure string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hintableLocked(s) || s.turnStart.IsZero() {
		return ""
	}
	now := m.now()
	for _, p := range trailPaths(use) {
		if !strings.Contains(failure, p) || s.trailTurn.Equal(s.turnStart) && s.trailPaths[p] {
			continue
		}
		since := s.turnStart
		for q, at := range s.edits {
			if editPath(s.workdir, q) == p && at.After(since) {
				since = at
			}
		}
		for _, edit := range m.siblingEditsLocked(s, p) {
			if edit.At.After(since) && !edit.At.After(now) {
				m.count("hints_patch_changed")
				return "uam: another Thread edited " + p + " this turn; view it again."
			}
		}
	}
	return ""
}
