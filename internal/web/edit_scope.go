package web

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// editScopeDenyLocked refuses an edit-tool call (edit, create, apply_patch)
// that writes outside the Thread's folder: changes there would not show in
// this Thread's Changes or its Project. The conversation's own Copilot
// session folder (its plan and files) and the temporary directory stay
// writable. Shell commands are not checked: their targets are not known
// before they run. The caller holds m.mu.
func (m *Manager) editScopeDenyLocked(s *webSession, use agentapi.ToolUse) string {
	for _, path := range trailPaths(use) {
		path = resolvedPath(path)
		if s.isPlanPath(path) || withinDir(s.workdir, path) || withinDir(resolvedPath(os.TempDir()), path) {
			continue
		}
		if dir := m.sessionStateDirLocked(s); dir != "" && withinDir(dir, path) {
			continue
		}
		next := "Tell the owner; this thread cannot start threads."
		if s.spawnedBy == "" && s.routineID == "" {
			next = "To change it, start a thread there with " + createTaskToolName + " and path set to its folder; uam asks the owner before it adds a new project."
		}
		return fmt.Sprintf("uam scope: %s is outside this thread's folder %s, so the change would not be tracked here. Do not write it from this thread. %s", path, s.workdir, next)
	}
	return ""
}

// sessionStateDirLocked is the Copilot session folder of s's conversation;
// "" without one.
func (m *Manager) sessionStateDirLocked(s *webSession) string {
	if s.convID == "" || strings.ContainsAny(s.convID, `/\`) {
		return ""
	}
	scope, err := m.configurationScope("")
	if err != nil {
		return ""
	}
	return resolvedPath(filepath.Join(scope.base, "session-state", s.convID))
}

// withinDir reports whether path is dir or below it; both are absolute and
// clean.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolvedPath resolves the symbolic links of path's deepest existing
// ancestor, so a link into or out of a folder is judged by its target. The
// part that does not exist yet is kept as written.
func resolvedPath(path string) string {
	path = filepath.Clean(path)
	rest := ""
	for dir := path; ; {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, fs.ErrNotExist) || parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
