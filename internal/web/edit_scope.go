package web

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	for _, path := range toolPaths(use) {
		path = resolvedPath(rawJoin(use.Workdir, path))
		if m.writableLocked(s, path) {
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

// writableLocked reports whether s may write the resolved path: its folder,
// its plan, its Copilot session folder or the temporary directory. The
// caller holds m.mu.
func (m *Manager) writableLocked(s *webSession, path string) bool {
	// path is resolved, so the folder must be too: a worktree under a linked home or worktrees folder.
	if s.isPlanPath(path) || withinDir(resolvedPath(s.workdir), path) || withinDir(resolvedPath(os.TempDir()), path) {
		return true
	}
	dir := m.sessionStateDirLocked(s)
	return dir != "" && withinDir(dir, path)
}

// pathOutsideWorktreeLocked reports a request of a Thread in its own
// worktree that is never answered automatically, so the owner decides: to
// write a path outside what it may write, or to run a shell command on a
// path in the Project's own folder or another Thread's worktree. A shell
// request does not say whether it reads or writes, so it is not refused,
// and one on any other path (/dev/null, a cache) is still answered. The
// caller holds m.mu.
func (m *Manager) pathOutsideWorktreeLocked(s *webSession, ix *interaction) bool {
	if s.place == nil || s.place.Mode != placeWorktree || ix.PathAccess != "write" && ix.PathAccess != "shell" {
		return false
	}
	return slices.ContainsFunc(ix.Paths, func(p string) bool {
		path := resolvedPath(rawJoin(s.workdir, p))
		return !m.writableLocked(s, path) && (ix.PathAccess == "write" || m.othersFolderLocked(s, path))
	})
}

// othersFolderLocked reports whether the resolved path is in the folder of
// s's Project or in the worktree of another Thread of it: work s must not
// touch. The caller holds m.mu.
func (m *Manager) othersFolderLocked(s *webSession, path string) bool {
	if p := m.projects[s.projectID]; p != nil && withinDir(resolvedPath(p.Dir), path) {
		return true
	}
	for _, o := range m.sessions {
		if o != s && !o.removed && o.projectID == s.projectID && o.place != nil && o.place.Worktree != "" && withinDir(resolvedPath(o.place.Worktree), path) {
			return true
		}
	}
	return false
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

// rawJoin is path read from dir, its ".." left for resolvedPath: cleaned
// first, a ".." after a link would climb from the link, not its target.
func rawJoin(dir, path string) string {
	if filepath.IsAbs(path) || dir == "" {
		return path
	}
	return dir + string(filepath.Separator) + path
}

// resolvedPath resolves the symbolic links of path one name at a time, as
// the system does, so a link into or out of a folder is judged by its
// target and a ".." after a link climbs from the link's target, not from
// the link. The part that does not exist yet is kept as written.
func resolvedPath(path string) string {
	if !filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	names := strings.Split(path, string(filepath.Separator))
	cur := string(filepath.Separator)
	for i, name := range names {
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, name)
		real, err := filepath.EvalSymlinks(next)
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.Join(append([]string{next}, names[i+1:]...)...)
		}
		if err != nil {
			return filepath.Clean(path)
		}
		cur = real
	}
	return cur
}
