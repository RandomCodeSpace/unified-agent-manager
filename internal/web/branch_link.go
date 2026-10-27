package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// maxBranchLink bounds a Project's branch link.
const maxBranchLink = 2048

// branchPlaceholder is where a branch link takes the branch name.
const branchPlaceholder = "{branch}"

// cleanBranchLink checks a Project's branch link: empty, or an https
// address with a host, no user or password, and {branch} exactly once.
func cleanBranchLink(raw string) (string, error) {
	link := strings.TrimSpace(raw)
	if link == "" {
		return "", nil
	}
	if len(link) > maxBranchLink {
		return "", newError(http.StatusBadRequest, "the branch link is longer than %d characters", maxBranchLink)
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "", newError(http.StatusBadRequest, "the branch link must be an https address with a host")
	}
	if u.User != nil {
		return "", newError(http.StatusBadRequest, "the branch link must not hold a user name or password")
	}
	if strings.Count(link, branchPlaceholder) != 1 {
		return "", newError(http.StatusBadRequest, "the branch link must contain {branch} exactly once, where the branch name goes")
	}
	return link, nil
}

// loadedBranchLink keeps a saved branch link that still checks; one that
// does not counts as never set, so it is detected again.
func loadedBranchLink(link *string) *string {
	if link == nil {
		return nil
	}
	if clean, err := cleanBranchLink(*link); err != nil || clean != *link {
		return nil
	}
	return link
}

// branchLinkFromRemote suggests a branch link for a git remote URL: GitHub's
// layout on github.com, GitLab's on every other host.
func branchLinkFromRemote(remote string) (string, bool) {
	host, path, ok := parseRemote(remote)
	if !ok {
		return "", false
	}
	tree := "/-/tree/"
	if host == "github.com" {
		tree = "/tree/"
	}
	link, err := cleanBranchLink((&url.URL{Scheme: "https", Host: host, Path: "/" + path}).String() + tree + branchPlaceholder)
	return link, err == nil
}

// detectBranchLink suggests a branch link from dir's origin remote.
func detectBranchLink(ctx context.Context, dir string) (string, bool) {
	git, err := execpath.Resolve("git")
	if err != nil {
		return "", false
	}
	out, code, _, err := runGit(ctx, git, dir, 4096, "remote", "get-url", "origin")
	if err != nil || code != 0 {
		return "", false
	}
	return branchLinkFromRemote(string(out))
}

// linkedBranch names the branch a link should open: branch when origin has
// it, else origin's default branch, main when git cannot tell. It reads
// local refs only, never the network.
func linkedBranch(ctx context.Context, dir, branch string) string {
	git, err := execpath.Resolve("git")
	if err != nil {
		return ""
	}
	if _, code, _, err := runGit(ctx, git, dir, 4096, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil && code == 0 {
		return branch
	}
	out, code, _, err := runGit(ctx, git, dir, 4096, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if def, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "origin/"); err == nil && code == 0 && ok && def != "" {
		return def
	}
	return "main"
}

// fillBranchLink puts branch into link, each path segment escaped.
func fillBranchLink(link, branch string) string {
	segments := strings.Split(branch, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Replace(link, branchPlaceholder, strings.Join(segments, "/"), 1)
}

// branchLinkLocked is the address of the branch s's link opens, and that
// branch; empty until checkBranchLink read the current branch.
func (m *Manager) branchLinkLocked(s *webSession) (string, string) {
	p := m.projects[s.projectID]
	if p == nil || p.BranchLink == nil || *p.BranchLink == "" || s.branch == "" || s.linkFor != s.branch || s.linkOpens == "" {
		return "", ""
	}
	return fillBranchLink(*p.BranchLink, s.linkOpens), s.linkOpens
}

// checkBranchLink runs when a Thread is opened: it detects its Project's
// branch link once if none was ever set, then reads whether the Thread's
// branch is on origin, so its link opens a branch that exists there.
func (m *Manager) checkBranchLink(ctx context.Context, id string) {
	m.mu.Lock()
	s := m.sessions[id]
	var p *Project
	if s != nil && !s.removed {
		p = m.projects[s.projectID]
	}
	if p == nil {
		m.mu.Unlock()
		return
	}
	projectID, projectDir, detect := p.ID, p.Dir, p.BranchLink == nil && !p.linkTried
	p.linkTried = true
	workdir, branch := s.workdir, s.branch
	m.mu.Unlock()
	if detect {
		m.detectProjectBranchLink(ctx, projectID, projectDir)
	}
	m.mu.Lock()
	linked := p.BranchLink != nil && *p.BranchLink != ""
	m.mu.Unlock()
	if !linked || branch == "" {
		return
	}
	opens := linkedBranch(ctx, workdir, branch)
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.removed {
		return
	}
	before := m.summaryLocked(s)
	s.linkFor, s.linkOpens = branch, opens
	m.changedLocked(s, before)
}

var errBranchLinkSet = errors.New("branch link already set")

// detectProjectBranchLink saves the branch link detected from the Project's
// origin, unless one was set meanwhile.
func (m *Manager) detectProjectBranchLink(ctx context.Context, id, dir string) {
	link, ok := detectBranchLink(ctx, dir)
	if !ok {
		return
	}
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	if err := m.store.Update(func(cfg *store.Config) error {
		stored, ok := cfg.WebProjects[id]
		if !ok || stored.BranchLink != nil {
			return errBranchLinkSet
		}
		stored.BranchLink = &link
		cfg.WebProjects[id] = stored
		return nil
	}); err != nil {
		if !errors.Is(err, errBranchLinkSet) {
			log.Warn("web project: save detected branch link", "project", id, "err", err)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.projects[id]; p != nil && p.BranchLink == nil {
		next := *p
		next.BranchLink = &link
		m.setProjectLocked(p, next)
	}
}

// setProjectLocked replaces *p with next and publishes it, and the
// Project's Threads whose branch link it changed.
func (m *Manager) setProjectLocked(p *Project, next Project) {
	before := map[*webSession]SessionSummary{}
	for _, s := range m.sessions {
		if s.projectID == p.ID && !s.removed {
			before[s] = m.summaryLocked(s)
		}
	}
	*p = next
	m.publishProjectLocked(*p)
	for s, summary := range before {
		m.changedLocked(s, summary)
	}
}

// handleDetectBranchLink suggests the Project's branch link from its origin
// remote. It never saves.
func (s *Server) handleDetectBranchLink(w http.ResponseWriter, r *http.Request) {
	s.m.mu.Lock()
	p := s.m.projects[r.PathValue("id")]
	var dir string
	if p != nil {
		dir = p.Dir
	}
	s.m.mu.Unlock()
	if p == nil {
		writeFailure(w, errProjectNotFound)
		return
	}
	link, ok := detectBranchLink(r.Context(), dir)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "git has no origin remote here that uam can read")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"branch_link": link})
}
