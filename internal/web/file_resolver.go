package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/toolargs"
)

const (
	maxResolvePaths     = toolargs.MaxPaths
	maxResolvePathBytes = toolargs.MaxPathBytes
	maxResolveBodyBytes = 512 << 10
)

// resolvedFile answers one path. Exists says whether Path itself names a
// readable file of the Thread's folder. When it does not, Target may name the
// one file a bare name (`gen.py`) or a partial path (`lib/gen.py`) means, with
// Kind "file" and Exists false, so a client that predates Target links nothing.
type resolvedFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
}

func validResolvePath(path string) bool { return toolargs.ValidPath(path) }

func (s *Server) handleResolveFiles(w http.ResponseWriter, r *http.Request) {
	// Read the complete bounded envelope so an oversized trailing suffix also
	// returns 413, and reject invalid UTF-8 before JSON replaces it with U+FFFD.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxResolveBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
		}
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &req) != nil || len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "paths must be a nonempty array of file paths")
		return
	}
	paths := make([]string, 0, min(len(req.Paths), maxResolvePaths))
	seen := make(map[string]bool)
	for _, path := range req.Paths {
		if !validResolvePath(path) {
			writeError(w, http.StatusBadRequest, "invalid file path")
			return
		}
		if !seen[path] {
			if len(paths) == maxResolvePaths {
				writeError(w, http.StatusBadRequest, "at most 64 unique paths are allowed")
				return
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	files, err := s.m.resolveFiles(r.Context(), r.PathValue("id"), paths)
	if err != nil {
		if r.Context().Err() == nil {
			writeFailure(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Files []resolvedFile `json:"files"`
	}{files})
}

func (m *Manager) resolveFiles(ctx context.Context, id string, paths []string) ([]resolvedFile, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return nil, newError(http.StatusNotFound, msgSessionNotFound)
	}
	workdir, roots := s.workdir, []string{s.workdir, s.realWorkdir}
	recorded := slices.Collect(maps.Keys(s.edits))
	recorded = append(recorded, s.nativePaths...)
	m.mu.Unlock()

	files := make([]resolvedFile, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file := resolvedFile{Path: path, Kind: "unavailable"}
		// A name the connected file route refuses is never offered as a link,
		// so a reference behaves the same on this instance and through another.
		if !strings.ContainsAny(path, "\\\r\n") && readableTaskFile(workdir, path) {
			file.Exists, file.Kind = true, "file"
		}
		files = append(files, file)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.findTargets(ctx, s, workdir, roots, recorded, files)
	return files, nil
}

// Bounds of the folder search for a name the record does not settle: the
// entries one search visits, and how long its answers are reused.
const (
	maxTargetWalk = 200_000
	targetTTL     = 30 * time.Second
)

// targetAnswer is a folder search's answer for one name: the file, or "".
type targetAnswer struct {
	target string
	until  time.Time
}

// findTargets sets the Target of each missing relative path that names
// exactly one file, never a guess between several. The Thread's record (the
// files its edit tools touched and its native session diff listed) decides
// first: one recorded match is the file, and several leave the name unlinked.
// Without a recorded match, the Thread's folder is searched for the one file
// whose path ends in the name, skipping .git and node_modules.
func (m *Manager) findTargets(ctx context.Context, s *webSession, workdir string, roots, recorded []string, files []resolvedFile) {
	var search []string
	for i := range files {
		f := &files[i]
		if f.Exists || filepath.IsAbs(f.Path) || strings.ContainsAny(f.Path, "\\\r\n") {
			continue
		}
		name := filepath.ToSlash(filepath.Clean(f.Path))
		if !filepath.IsLocal(name) {
			continue
		}
		switch matches := recordedMatches(workdir, roots, recorded, name); len(matches) {
		case 0:
			search = append(search, name)
		case 1:
			f.Kind, f.Target = "file", matches[0]
		}
	}
	if len(search) == 0 {
		return
	}
	now := time.Now()
	answers := make(map[string]string, len(search))
	var walk []string
	m.mu.Lock()
	for _, name := range search {
		if a, ok := s.targets[name]; ok && a.until.After(now) {
			answers[name] = a.target
		} else {
			walk = append(walk, name)
		}
	}
	m.mu.Unlock()
	if len(walk) > 0 {
		found, complete := searchFolder(ctx, workdir, walk)
		// An unfinished search proves no name unique. One stopped by its bound
		// is kept as that, so a huge folder is not walked again per request;
		// one a cancelled request stopped is not kept.
		keep := complete || ctx.Err() == nil
		if !complete {
			found = nil
		}
		m.mu.Lock()
		if s.targets == nil || len(s.targets) > 4096 {
			s.targets = map[string]targetAnswer{}
		}
		for _, name := range walk {
			answers[name] = found[name]
			if keep {
				s.targets[name] = targetAnswer{found[name], now.Add(targetTTL)}
			}
		}
		m.mu.Unlock()
	}
	for i := range files {
		f := &files[i]
		if f.Exists || f.Target != "" {
			continue
		}
		if target := answers[filepath.ToSlash(filepath.Clean(f.Path))]; target != "" && readableTaskFile(workdir, target) {
			f.Kind, f.Target = "file", target
		}
	}
}

// recordedMatches lists the distinct readable files of the record, relative
// to workdir, whose path is name or ends in "/" + name. An absolute record
// path counts through either root: workdir or its real location.
func recordedMatches(workdir string, roots, recorded []string, name string) []string {
	var matches []string
	for _, p := range recorded {
		rel := p
		if filepath.IsAbs(p) {
			rel = ""
			for _, root := range roots {
				if r, err := filepath.Rel(root, filepath.Clean(p)); root != "" && err == nil && filepath.IsLocal(r) {
					rel = r
					break
				}
			}
			if rel == "" {
				continue
			}
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if (rel == name || strings.HasSuffix(rel, "/"+name)) && !slices.Contains(matches, rel) && readableTaskFile(workdir, rel) {
			matches = append(matches, rel)
		}
	}
	return matches
}

// searchFolder walks workdir once for names and maps each to the one regular
// file whose relative path is the name or ends in "/" + name; a name with no
// file or several maps to "". complete is false when the walk stopped early
// (its bound, a cancelled request), which leaves uniqueness unproven.
func searchFolder(ctx context.Context, workdir string, names []string) (map[string]string, bool) {
	root, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return nil, false
	}
	byBase := map[string][]string{}
	for _, name := range names {
		base := path.Base(name)
		byBase[base] = append(byBase[base], name)
	}
	found := map[string]string{}
	several := map[string]bool{}
	visited := 0
	errStop := errors.New("stop")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if visited++; visited > maxTargetWalk || ctx.Err() != nil {
			return errStop
		}
		if d.IsDir() {
			if p != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		candidates := byBase[d.Name()]
		if len(candidates) == 0 || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for _, name := range candidates {
			if rel != name && !strings.HasSuffix(rel, "/"+name) {
				continue
			}
			if _, ok := found[name]; ok {
				several[name] = true
			} else {
				found[name] = rel
			}
		}
		return nil
	})
	if err != nil {
		return nil, false
	}
	for name := range several {
		found[name] = ""
	}
	return found, true
}

// The opened descriptor proves readability and type at this instant. It does
// not authorize a later view, which repeats its own checks. No content is read.
func readableTaskFile(workdir, path string) bool {
	root, rel, err := resolveTaskFile(workdir, path)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular()
}
