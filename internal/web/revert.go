package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// RevertChange puts back one change (hunk), or the whole of path, in a file
// this Thread edited: the hunk as the task scope's patch shows it, applied in
// reverse; the whole file from HEAD, or removed when HEAD has no such file.
func (m *Manager) RevertChange(ctx context.Context, id, path, hunk string) error {
	if path == "" {
		return newError(http.StatusBadRequest, "path is required")
	}
	t, repo, unlock, err := m.openWriteRepo(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	m.mu.Lock()
	err = t.session.readOnlyLocked()
	edited := false
	resolve := repoPaths(repo.top, t.workdir)
	for p := range t.session.edits {
		if rel, ok := resolve(p); ok && rel == path {
			edited = true
			break
		}
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if !edited {
		return newError(http.StatusBadRequest, "path is not among the files this thread edited")
	}
	if hunk != "" {
		err = revertHunk(ctx, repo.top, path, hunk)
	} else {
		err = revertFile(ctx, repo, path)
	}
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.kickDiffLocked(t.session)
	m.mu.Unlock()
	m.refreshBranches(ctx, true, t.projectID)
	return nil
}

// revertHunk applies one hunk of path's diff against HEAD in reverse. The
// hunk is only hunk lines, so the patch names no file but path.
func revertHunk(ctx context.Context, top, path, hunk string) error {
	if strings.ContainsAny(path, "\n\r\t\"\\") {
		return newError(http.StatusBadRequest, "this file's name cannot be patched; revert the whole file")
	}
	if !strings.HasSuffix(hunk, "\n") {
		hunk += "\n"
	}
	lines := strings.Split(strings.TrimSuffix(hunk, "\n"), "\n")
	header := hunkHeaderRE.FindStringSubmatch(lines[0])
	if header == nil {
		return newError(http.StatusBadRequest, "hunk must start at its @@ line")
	}
	// Exactly the header's line counts: a line past them could start another
	// file's patch ("--- a/other"), which git apply would also apply.
	oldLeft, newLeft := hunkCount(header[1]), hunkCount(header[2])
	for _, line := range lines[1:] {
		switch {
		case line == "":
			return newError(http.StatusBadRequest, "hunk must be one hunk of the file's diff")
		case line[0] == '\\':
		case line[0] == ' ':
			oldLeft--
			newLeft--
		case line[0] == '-':
			oldLeft--
		case line[0] == '+':
			newLeft--
		default:
			return newError(http.StatusBadRequest, "hunk must be one hunk of the file's diff")
		}
		if oldLeft < 0 || newLeft < 0 {
			return newError(http.StatusBadRequest, "hunk must be one hunk of the file's diff")
		}
	}
	if oldLeft != 0 || newLeft != 0 {
		return newError(http.StatusBadRequest, "hunk must be one hunk of the file's diff")
	}
	patch := "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n" + hunk
	if _, err := runGitWrite(ctx, top, strings.NewReader(patch), "apply", "-R", "--check", "-"); err != nil {
		var gerr *gitError
		if errors.As(err, &gerr) {
			return newError(http.StatusConflict, "the file changed since this diff was read; refresh and try again")
		}
		return gitFailed("git apply failed", err)
	}
	if _, err := runGitWrite(ctx, top, strings.NewReader(patch), "apply", "-R", "-"); err != nil {
		return gitFailed("git apply failed", err)
	}
	return nil
}

// hunkHeaderRE reads a hunk header's line counts ("@@ -3,7 +3,8 @@").
var hunkHeaderRE = regexp.MustCompile(`^@@ -\d+(,\d+)? \+\d+(,\d+)? @@`)

// hunkCount is a header's line count: ",7" is 7, an omitted count is 1.
func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s[1:])
	return n
}

// revertFile restores path from HEAD, or removes it when HEAD has no such
// file (a file the Thread created). The removal stays inside the repository.
func revertFile(ctx context.Context, repo *gitRepo, path string) error {
	if repo.hasHead {
		_, err := runGitWrite(ctx, repo.top, nil, "cat-file", "-e", "HEAD:"+path)
		var gerr *gitError
		switch {
		case err == nil:
			if _, err := runGitWrite(ctx, repo.top, nil, "restore", "--source=HEAD", "--worktree", "--", path); err != nil {
				return gitFailed("git restore failed", err)
			}
			return nil
		case !errors.As(err, &gerr):
			return gitFailed("git cat-file failed", err)
		}
	}
	root, err := os.OpenRoot(repo.top)
	if err != nil {
		return newError(http.StatusBadGateway, "could not open the repository: %s", shortError(err))
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return newError(http.StatusConflict, "could not remove %s: %s", path, shortError(err))
	}
	return nil
}

func (s *Server) handleRevertChange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Hunk string `json:"hunk"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.m.RevertChange(r.Context(), r.PathValue("id"), body.Path, body.Hunk); err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
