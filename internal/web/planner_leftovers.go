package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// removePlannerFiles is a one-time cleanup of the removed planner's files
// (ADR 0007); a later release can drop it. Start runs it on dir, the folder
// of sessions.json. It deletes board.db with its -wal and -shm files and the
// lane worktrees under lanes/, then prunes each lane's repository so git
// forgets the worktree. Only regular files and a real lanes directory go:
// a symlink is refused, never followed. Missing files are fine, and a
// failure is logged without stopping startup.
func removePlannerFiles(ctx context.Context, dir string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("remove the planner's files failed", "dir", dir, "error", err)
		}
		return
	}
	defer func() { _ = root.Close() }()
	var removed []string
	for _, name := range []string{"board.db", "board.db-wal", "board.db-shm", "lanes"} {
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var repos []string
		switch {
		case err != nil:
			// Logged below.
		case name == "lanes" && info.IsDir():
			repos = laneRepos(root, dir)
			err = root.RemoveAll(name)
		case name != "lanes" && info.Mode().IsRegular():
			err = root.Remove(name)
		default:
			err = errors.New("refused: not a regular file or a real directory")
		}
		if err == nil {
			removed = append(removed, name)
		} else {
			log.Warn("remove the planner's file failed", "path", filepath.Join(dir, name), "error", err)
		}
		for _, repo := range repos {
			if err := pruneWorktrees(ctx, repo); err != nil {
				log.Warn("prune the planner's lane worktrees failed", "repo", repo, "error", err)
			}
		}
	}
	if len(removed) > 0 {
		log.Info("removed the planner's files", "dir", dir, "files", removed)
	}
}

// laneRepos are the common git directories of the lanes
// lanes/<project id>/<lane>, read from each lane's .git file,
// "gitdir: <repo>/.git/worktrees/<name>".
func laneRepos(root *os.Root, dir string) []string {
	var repos []string
	projects, _ := fs.ReadDir(root.FS(), "lanes")
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		lanes, _ := fs.ReadDir(root.FS(), path.Join("lanes", project.Name()))
		for _, lane := range lanes {
			rel := path.Join("lanes", project.Name(), lane.Name())
			if !lane.IsDir() {
				continue
			}
			data, err := fs.ReadFile(root.FS(), path.Join(rel, ".git"))
			gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
			if err != nil || !ok {
				continue
			}
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(dir, filepath.FromSlash(rel), gitdir)
			}
			common := filepath.Dir(filepath.Dir(gitdir))
			if filepath.Base(filepath.Dir(gitdir)) == "worktrees" && !slices.Contains(repos, common) {
				repos = append(repos, common)
			}
		}
	}
	return repos
}

// pruneWorktrees runs `git worktree prune` in a repository's common git
// directory, so it forgets the worktrees whose folder is gone.
func pruneWorktrees(ctx context.Context, gitDir string) error {
	git, err := lookGit()
	if err != nil {
		return err
	}
	_, code, stderr, err := runGit(ctx, git, gitDir, 4096, "worktree", "prune")
	if err == nil && code != 0 {
		err = fmt.Errorf("git exited with %d: %s", code, gitMessage(stderr))
	}
	return err
}
