package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const (
	maxFileRefs      = 20
	defaultFileLimit = 50
	maxFileLimit     = 200
	maxListBytes     = 4 << 20
	// binarySniffBytes is how much of a file is searched for a NUL byte, as
	// git does to call a file binary.
	binarySniffBytes = 8000
)

// FileEntry is one project path the composer can reference.
type FileEntry struct {
	Path string `json:"path"`
	// Type is "file" or "directory".
	Type string `json:"type"`
}

// FileList answers GET /api/sessions/{id}/files. Reason says why the list
// is empty or cut short.
type FileList struct {
	Files  []FileEntry `json:"files"`
	Reason string      `json:"reason"`
}

// Files lists up to limit paths in the Task's directory whose path matches
// q, for the @ picker. Git lists them, so .gitignore applies; parent
// directories are added, symbolic links are left out.
func (m *Manager) Files(ctx context.Context, id, q string, limit int) (FileList, error) {
	s, err := m.lookup(id)
	if err != nil {
		return FileList{}, err
	}
	m.mu.Lock()
	workdir := s.workdir
	m.mu.Unlock()
	return listFiles(ctx, workdir, q, limit)
}

func listFiles(ctx context.Context, workdir, q string, limit int) (FileList, error) {
	out := FileList{Files: []FileEntry{}}
	repo, reason, err := openRepo(ctx, workdir)
	if err != nil {
		return out, err
	}
	if repo == nil {
		out.Reason = reason
		return out, nil
	}
	// Run in the workdir, not the top level: paths come out relative to it
	// and only files under it are listed.
	raw, code, stderr, err := runGit(ctx, repo.git, workdir, maxListBytes+1, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, newError(http.StatusBadGateway, "git ls-files failed: %s", gitMessage(stderr))
	}
	if len(raw) > maxListBytes {
		raw = raw[:bytes.LastIndexByte(raw[:maxListBytes], 0)+1]
		out.Reason = "the project has more files than uam lists; some are missing"
	}
	candidates := map[string]bool{}
	for _, p := range strings.Split(string(raw), "\x00") {
		if p == "" || candidates[p] {
			continue
		}
		candidates[p] = true
		for dir := path.Dir(p); dir != "." && !candidates[dir]; dir = path.Dir(dir) {
			candidates[dir] = true
		}
	}
	q = strings.ToLower(q)
	type match struct {
		path  string
		score int
	}
	var matches []match
	for p := range candidates {
		if score, ok := matchScore(p, q); ok {
			matches = append(matches, match{p, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return out, newError(http.StatusBadGateway, "could not open the project directory: %s", shortError(err))
	}
	defer func() { _ = root.Close() }()
	for _, mt := range matches {
		if len(out.Files) == limit {
			break
		}
		// Git lists what it tracks; the tree on disk decides. Links, special
		// files and paths that are gone are left out.
		info, err := root.Lstat(mt.path)
		switch {
		case err != nil:
		case info.IsDir():
			out.Files = append(out.Files, FileEntry{Path: mt.path, Type: "directory"})
		case info.Mode().IsRegular():
			out.Files = append(out.Files, FileEntry{Path: mt.path, Type: "file"})
		}
	}
	return out, nil
}

// matchScore ranks p for the lower-case query q: a base name starting with
// q first, then a base name containing it, then a path containing it.
func matchScore(p, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	lower := strings.ToLower(p)
	base := lower[strings.LastIndexByte(lower, '/')+1:]
	switch {
	case strings.HasPrefix(base, q):
		return 0, true
	case strings.Contains(base, q):
		return 1, true
	case strings.Contains(lower, q):
		return 2, true
	}
	return 0, false
}

// checkFiles validates the files a prompt references, relative to workdir,
// and returns them with absolute paths. Every path is resolved inside the
// directory through os.Root; a bad one is a 400 that names it.
func checkFiles(workdir string, rels []string) ([]agentapi.File, error) {
	if len(rels) == 0 {
		return nil, nil
	}
	if len(rels) > maxFileRefs {
		return nil, newError(http.StatusBadRequest, "a prompt can reference at most %d files", maxFileRefs)
	}
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return nil, newError(http.StatusConflict, "the project directory %s cannot be opened", workdir)
	}
	defer func() { _ = root.Close() }()
	out := make([]agentapi.File, 0, len(rels))
	for _, rel := range rels {
		if slices.ContainsFunc(out, func(f agentapi.File) bool { return f.Rel == rel }) {
			continue
		}
		dir, reason := checkFile(root, rel)
		if reason != "" {
			return nil, newError(http.StatusBadRequest, "file %q %s", clipRunes(displaytext.Sanitize(rel), maxDetailRunes), reason)
		}
		out = append(out, agentapi.File{Path: filepath.Join(workdir, filepath.FromSlash(rel)), Rel: rel, Dir: dir})
	}
	return out, nil
}

// checkFile reports whether rel names a directory, or why it cannot be
// referenced: absolute, leaving the directory, through or at a symbolic
// link, missing, special, or binary (a NUL byte in its first bytes).
func checkFile(root *os.Root, rel string) (dir bool, reason string) {
	switch {
	case rel == "" || !utf8.ValidString(rel) || strings.ContainsRune(rel, 0):
		return false, "is not a valid path"
	case path.IsAbs(rel) || filepath.IsAbs(rel):
		return false, "is absolute; use a path relative to the project"
	case slices.Contains(strings.Split(rel, "/"), ".."):
		return false, "leaves the project directory"
	case path.Clean(rel) != rel || !filepath.IsLocal(rel):
		return false, "is not a clean relative path"
	}
	parts := strings.Split(rel, "/")
	var info fs.FileInfo
	for i := range parts {
		var err error
		info, err = root.Lstat(strings.Join(parts[:i+1], "/"))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return false, "does not exist"
		case err != nil:
			return false, "cannot be read"
		case info.Mode()&fs.ModeSymlink != 0:
			return false, "is a symbolic link or inside one"
		case i < len(parts)-1 && !info.IsDir():
			return false, "does not exist"
		}
	}
	if info.IsDir() {
		return true, ""
	}
	if !info.Mode().IsRegular() {
		return false, "is not a regular file or directory"
	}
	// O_NONBLOCK keeps a FIFO swapped in after the check from blocking;
	// O_NOFOLLOW refuses a link swapped in.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, "cannot be read"
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return false, "is not a regular file or directory"
	}
	head, err := io.ReadAll(io.LimitReader(f, binarySniffBytes))
	if err != nil {
		return false, "cannot be read"
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false, "is a binary file"
	}
	return false, ""
}

// maxServedImageBytes caps an image served from a Task's directory.
const maxServedImageBytes = 20 << 20

// imageExtensions maps the file extensions the raw route serves to the type
// the bytes must sniff as. SVG is left out: DetectContentType cannot tell it
// apart from other XML, and opened as a document it runs script.
var imageExtensions = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
}

// ServedImage is an image file of a Task's directory, open for reading.
type ServedImage struct {
	File *os.File
	Info fs.FileInfo
	MIME string
}

// RawImage opens the image at p, absolute or relative to the Task's
// directory, for the raw file route. Symbolic links are resolved first and
// the real file must lie inside the Task's real directory; anything else,
// missing files included, is a 404 that says nothing more. Only a regular
// file whose extension and bytes agree on png, jpeg, gif or webp, at most
// maxServedImageBytes, is served.
func (m *Manager) RawImage(id, p string) (*ServedImage, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	workdir := s.workdir
	m.mu.Unlock()
	return openImage(workdir, p)
}

func openImage(workdir, p string) (*ServedImage, error) {
	notFound := newError(http.StatusNotFound, "file not found")
	if p == "" || len(p) > 4096 || !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return nil, newError(http.StatusBadRequest, "path is required")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	realDir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return nil, notFound
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return nil, notFound
	}
	rel, err := filepath.Rel(realDir, real)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return nil, notFound
	}
	// Nothing that is not a regular file with an image name is opened, so a
	// name linked to a device or FIFO never reaches open(2).
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return nil, notFound
	}
	if _, ok := imageExtensions[strings.ToLower(filepath.Ext(real))]; !ok {
		return nil, newError(http.StatusUnsupportedMediaType, "only png, jpeg, gif and webp images are served")
	}
	// os.Root confines the open itself, so a link swapped in after the
	// resolution above still cannot leave the directory.
	root, err := os.OpenRoot(realDir)
	if err != nil {
		return nil, notFound
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, notFound
	}
	img, err := checkImage(f, filepath.Ext(real))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return img, nil
}

func checkImage(f *os.File, ext string) (*ServedImage, error) {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, newError(http.StatusNotFound, "file not found")
	}
	want, ok := imageExtensions[strings.ToLower(ext)]
	if !ok {
		return nil, newError(http.StatusUnsupportedMediaType, "only png, jpeg, gif and webp images are served")
	}
	if info.Size() > maxServedImageBytes {
		return nil, newError(http.StatusUnsupportedMediaType, "images larger than 20 MiB are not served")
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, newError(http.StatusNotFound, "file not found")
	}
	if got := http.DetectContentType(head[:n]); got != want {
		return nil, newError(http.StatusUnsupportedMediaType, "the file is not a %s image", strings.TrimPrefix(want, "image/"))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, newError(http.StatusNotFound, "file not found")
	}
	return &ServedImage{File: f, Info: info, MIME: want}, nil
}
