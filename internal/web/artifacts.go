package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// uam_publish (docs/web.md "Published pages") publishes a file or folder of
// a Thread's directory as an artifact the owner opens from uam. Each publish
// copies an immutable snapshot, v<N>, into the Thread's upload directory
// beside its charts, so the page outlives later edits, goes with the Thread,
// and is served by a route that reaches only that snapshot's files.

const (
	publishToolName     = "uam_publish"
	artifactsDir        = "artifacts"
	artifactMetaFile    = "artifact.json"
	maxArtifactFiles    = 500
	maxArtifactBytes    = 25 << 20
	maxArtifactVersions = 10
	maxThreadArtifacts  = 20
	maxArtifactTitle    = 120
	// artifactViewRoute is a version's page under the cookie; it redirects
	// to the same path under a key for that version (artifactKeyRoute).
	artifactViewRoute = "GET /api/sessions/{id}/artifacts/{artifact}/v/{version}/{path...}"
	// artifactKeyRoute serves one version's files under a file key scoped to
	// that version, the key in the path so the page's relative links keep it.
	artifactKeyRoute = "GET /api/sessions/{id}/artifact-files/{artifact}/{version}/{key}/{path...}"
)

var artifactIDRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

var publishTool = agentapi.HostTool{
	Name: publishToolName,
	Description: "Publish a page you built in the thread's directory so the owner can open it from uam: a folder with index.html at its top (with its own CSS, scripts, images and fonts) or one file. " +
		"uam copies a snapshot of it, so later edits do not change what was published; publish again with the same title or id for a new version. " +
		fmt.Sprintf("A snapshot leaves out .git, node_modules and .env files and holds at most %d files and %d MiB; an artifact keeps its last %d versions. ", maxArtifactFiles, maxArtifactBytes>>20, maxArtifactVersions) +
		"The page runs sandboxed: use relative paths and classic scripts. ES modules, fetch of its own files, localStorage and network calls do not work. " +
		"To point the owner at a file without publishing it, use uam_show_file.",
	Parameters: toolSchema([]string{"path", "title"}, map[string]any{
		"path":  stringProp("The folder or file to publish, relative to the thread's directory or absolute inside it."),
		"title": stringProp(fmt.Sprintf("What the page is, up to %d characters. Publishing again with the same title adds a version.", maxArtifactTitle)),
		"id":    stringProp("An artifact id a previous uam_publish returned, to add a version to it whatever its title."),
	}),
}

// ArtifactVersion is one published snapshot. Entry is the page's path in
// it; CallID is the uam_publish call that made it.
type ArtifactVersion struct {
	Version int       `json:"version"`
	At      time.Time `json:"at"`
	Entry   string    `json:"entry"`
	Files   int       `json:"files"`
	Bytes   int64     `json:"bytes"`
	CallID  string    `json:"call_id,omitempty"`
}

// Artifact is a page a Thread published, with its versions oldest first.
// Source is the published path in the Thread's directory.
type Artifact struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Source   string            `json:"source"`
	Versions []ArtifactVersion `json:"versions"`
}

func (m *Manager) artifactRoot(taskID string) string {
	return filepath.Join(m.taskUploadDir(taskID), artifactsDir)
}

func artifactVersionDir(root, id string, version int) string {
	return filepath.Join(root, id, "v"+strconv.Itoa(version))
}

// readArtifacts returns the artifacts stored under root, the latest
// published first.
func readArtifacts(root string) []Artifact {
	entries, err := os.ReadDir(root)
	if err != nil {
		return []Artifact{}
	}
	out := []Artifact{}
	for _, e := range entries {
		if a, err := readArtifact(root, e.Name()); err == nil {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b Artifact) int {
		return b.Versions[len(b.Versions)-1].At.Compare(a.Versions[len(a.Versions)-1].At)
	})
	return out
}

func readArtifact(root, id string) (Artifact, error) {
	var a Artifact
	if !artifactIDRE.MatchString(id) {
		return a, newError(http.StatusNotFound, "artifact not found")
	}
	data, err := os.ReadFile(filepath.Join(root, id, artifactMetaFile)) // #nosec G304 -- uam's own directory; the name matched an artifact id.
	if err != nil || json.Unmarshal(data, &a) != nil || a.ID != id || len(a.Versions) == 0 {
		return Artifact{}, newError(http.StatusNotFound, "artifact not found")
	}
	return a, nil
}

// skippedName is a file or folder a snapshot leaves out: repository data,
// installed packages and environment files, which can hold secrets.
func skippedName(name string) bool {
	return name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".env")
}

var errArtifactTooLarge = fmt.Errorf("the snapshot would hold more than %d files or %d MiB; publish only the built page's own folder", maxArtifactFiles, maxArtifactBytes>>20)

// snapshot copies the file or folder p (relative to workdir, or absolute
// inside it) into dst. Only regular files are copied: symbolic links,
// devices and the skipped names are left out, and every read goes through
// an os.Root of workdir, so nothing outside it is read. It returns the
// entry page's path in dst, the published path and the counts.
func snapshot(ctx context.Context, workdir, p, dst string) (entry, source string, files int, size int64, err error) {
	if p == "" || len(p) > 4096 || !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return "", "", 0, 0, errors.New("path is required")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	outside := errors.New("path must name a file or folder inside the thread's directory, other than the directory itself")
	realDir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return "", "", 0, 0, outside
	}
	realPath, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("%s does not exist", p)
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", "", 0, 0, outside
	}
	root, err := os.OpenRoot(realDir)
	if err != nil {
		return "", "", 0, 0, outside
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(rel)
	switch {
	case err != nil:
		return "", "", 0, 0, fmt.Errorf("%s does not exist", p)
	case info.Mode().IsRegular():
		name := filepath.Base(rel)
		if skippedName(name) {
			return "", "", 0, 0, errors.New(".env files are not published")
		}
		n, err := copyRootFile(root, rel, filepath.Join(dst, name), maxArtifactBytes)
		return name, filepath.ToSlash(rel), 1, n, err
	case !info.IsDir():
		return "", "", 0, 0, fmt.Errorf("%s is not a file or folder", p)
	}
	site, err := root.OpenRoot(rel)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("%s cannot be read", p)
	}
	defer func() { _ = site.Close() }()
	err = fs.WalkDir(site.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if name == "." {
			return nil
		}
		if skippedName(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if d.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if files++; files > maxArtifactFiles {
			return errArtifactTooLarge
		}
		n, err := copyRootFile(site, name, target, maxArtifactBytes-size)
		size += n
		return err
	})
	if err != nil {
		return "", "", 0, 0, err
	}
	if info, err := os.Lstat(filepath.Join(dst, indexDocument)); err != nil || !info.Mode().IsRegular() {
		return "", "", 0, 0, fmt.Errorf("%s has no %s at its top; publish a folder with one, or a single file", p, indexDocument)
	}
	return indexDocument, filepath.ToSlash(rel), files, size, nil
}

// copyRootFile copies name in root to dst, failing past budget bytes. A
// name that is no longer a regular file when opened copies nothing.
func copyRootFile(root *os.Root, name, dst string, budget int64) (int64, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, fmt.Errorf("%s cannot be read", name)
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return 0, nil
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a fresh staging directory of uam's own.
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(f, budget+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > budget {
		err = errArtifactTooLarge
	}
	return n, err
}

func cleanArtifactTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > maxArtifactTitle || strings.IndexFunc(title, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("title must be 1 to %d printable characters", maxArtifactTitle)
	}
	return title, nil
}

// publishCall runs a uam_publish call of the Task taskID.
func (m *Manager) publishCall(ctx context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.publish(ctx, taskID, call)
	if err != nil {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	return agentapi.HostToolResult{Text: text}
}

func (m *Manager) publish(ctx context.Context, taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this thread")
	}
	if call.CallID == "" || len(call.CallID) > maxToolCallID {
		return "", errors.New("the call has no usable ID")
	}
	var args struct {
		Path  string `json:"path"`
		Title string `json:"title"`
		ID    string `json:"id"`
	}
	if err := decodeToolArgs(call.Arguments, &args); err != nil {
		return "", err
	}
	title, err := cleanArtifactTitle(args.Title)
	if err != nil {
		return "", err
	}
	if args.ID != "" && !artifactIDRE.MatchString(args.ID) {
		return "", errors.New("id must be an artifact id a previous uam_publish returned")
	}
	ctx, end, err := m.startCall(ctx, taskID)
	if err != nil {
		return "", err
	}
	defer end()
	s, err := m.lookup(taskID)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	workdir := s.workdir
	m.mu.Unlock()
	root := m.artifactRoot(taskID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		log.Warn("make the artifacts directory failed", "session", taskID, "error", err)
		return "", errors.New("uam could not store the artifact; try again")
	}
	staging, err := os.MkdirTemp(root, ".publish-")
	if err != nil {
		return "", errors.New("uam could not store the artifact; try again")
	}
	defer func() { _ = os.RemoveAll(staging) }()
	entry, source, files, size, err := snapshot(ctx, workdir, args.Path, staging)
	if err != nil {
		return "", err
	}

	m.artifactMu.Lock()
	defer m.artifactMu.Unlock()
	list := readArtifacts(root)
	i := slices.IndexFunc(list, func(a Artifact) bool { return a.ID == args.ID || args.ID == "" && a.Title == title })
	var a Artifact
	switch {
	case i >= 0:
		a = list[i]
	case args.ID != "":
		return "", fmt.Errorf("this thread has no artifact %q", args.ID)
	case len(list) >= maxThreadArtifacts:
		return "", fmt.Errorf("this thread already has %d artifacts; publish a new version of one (same title or id), or ask the owner to delete some", maxThreadArtifacts)
	default:
		id := make([]byte, 8)
		_, _ = rand.Read(id)
		a = Artifact{ID: hex.EncodeToString(id)}
		list = append(list, a)
	}
	version := 1
	if len(a.Versions) > 0 {
		version = a.Versions[len(a.Versions)-1].Version + 1
	}
	if err := os.MkdirAll(filepath.Join(root, a.ID), 0o700); err != nil {
		return "", errors.New("uam could not store the artifact; try again")
	}
	if err := os.Rename(staging, artifactVersionDir(root, a.ID, version)); err != nil {
		return "", errors.New("uam could not store the artifact; try again")
	}
	a.Title, a.Source = title, source
	a.Versions = append(a.Versions, ArtifactVersion{Version: version, At: time.Now().UTC(), Entry: entry, Files: files, Bytes: size, CallID: call.CallID})
	for len(a.Versions) > maxArtifactVersions {
		_ = os.RemoveAll(artifactVersionDir(root, a.ID, a.Versions[0].Version))
		a.Versions = a.Versions[1:]
	}
	if err := writePrivateJSON(m.uploadRoot(), filepath.Join(root, a.ID, artifactMetaFile), a); err != nil {
		_ = os.RemoveAll(artifactVersionDir(root, a.ID, version))
		log.Warn("store an artifact failed", "session", taskID, "error", err)
		return "", errors.New("uam could not store the artifact; try again")
	}
	if !m.artifactsChanged(taskID, len(list)) {
		removeUploads(m.taskUploadDir(taskID))
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	return fmt.Sprintf("Published %q as version %d (%d files, %d KiB); the owner opens it from the card in this conversation. Its artifact id is %q: publish again with this id or the same title for a new version.",
		title, version, files, (size+1023)>>10, a.ID), nil
}

// artifactsChanged records that the Thread taskID has n artifacts, for its
// header. It is false when the Thread was removed meanwhile.
func (m *Manager) artifactsChanged(taskID string, n int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	if s == nil || s.removed {
		return false
	}
	before := m.summaryLocked(s)
	s.artifacts = n
	m.changedLocked(s, before)
	return true
}

// Artifacts returns the Thread id's artifacts, the latest published first.
func (m *Manager) Artifacts(id string) ([]Artifact, error) {
	if _, err := m.lookup(id); err != nil {
		return nil, err
	}
	return readArtifacts(m.artifactRoot(id)), nil
}

// DeleteArtifact removes version of the Thread taskID's artifact id, or the
// whole artifact when version is 0 or its only one.
func (m *Manager) DeleteArtifact(taskID, id string, version int) error {
	if _, err := m.lookup(taskID); err != nil {
		return err
	}
	m.artifactMu.Lock()
	defer m.artifactMu.Unlock()
	root := m.artifactRoot(taskID)
	a, err := readArtifact(root, id)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(a.Versions, func(v ArtifactVersion) bool { return v.Version == version })
	switch {
	case version != 0 && i < 0:
		return newError(http.StatusNotFound, "artifact version not found")
	case version == 0 || len(a.Versions) == 1:
		err = os.RemoveAll(filepath.Join(root, id))
	default:
		a.Versions = slices.Delete(a.Versions, i, i+1)
		if err = writePrivateJSON(m.uploadRoot(), filepath.Join(root, id, artifactMetaFile), a); err == nil {
			err = os.RemoveAll(artifactVersionDir(root, id, version))
		}
	}
	if err != nil {
		log.Warn("delete an artifact failed", "session", taskID, "error", err)
		return newError(http.StatusInternalServerError, "uam could not delete the artifact")
	}
	m.artifactsChanged(taskID, len(readArtifacts(root)))
	return nil
}

// artifactVersion returns version of the Thread taskID's artifact id, or
// its latest when version is 0.
func (m *Manager) artifactVersion(taskID, id string, version int) (ArtifactVersion, error) {
	if _, err := m.lookup(taskID); err != nil {
		return ArtifactVersion{}, err
	}
	a, err := readArtifact(m.artifactRoot(taskID), id)
	if err != nil {
		return ArtifactVersion{}, err
	}
	if version == 0 {
		return a.Versions[len(a.Versions)-1], nil
	}
	if i := slices.IndexFunc(a.Versions, func(v ArtifactVersion) bool { return v.Version == version }); i >= 0 {
		return a.Versions[i], nil
	}
	return ArtifactVersion{}, newError(http.StatusNotFound, "artifact version not found")
}

// ArtifactFile opens rel in version of the Thread taskID's artifact id for
// the artifact routes, confined to that snapshot as the view route is to
// the Thread's directory.
func (m *Manager) ArtifactFile(taskID, id string, version int, rel string) (*ServedFile, error) {
	if _, err := m.lookup(taskID); err != nil {
		return nil, err
	}
	if !artifactIDRE.MatchString(id) || version < 1 {
		return nil, newError(http.StatusNotFound, msgFileNotFound)
	}
	return openView(artifactVersionDir(m.artifactRoot(taskID), id, version), rel)
}

// artifactVersionParam reads a route's version segment, 0 when it has none.
func artifactVersionParam(r *http.Request) (int, bool) {
	v := r.PathValue("version")
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n >= 1 && strconv.Itoa(n) == v
}

func artifactScope(task, artifact string, version int) string {
	return "artifact|" + task + "|" + artifact + "|" + strconv.Itoa(version)
}

func (s *Server) artifactRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/artifacts", s.handleArtifacts)
	mux.HandleFunc("DELETE /api/sessions/{id}/artifacts/{artifact}", s.handleDeleteArtifact)
	mux.HandleFunc("DELETE /api/sessions/{id}/artifacts/{artifact}/versions/{version}", s.handleDeleteArtifact)
	// The stable link: the latest version's page.
	mux.HandleFunc("GET /api/sessions/{id}/artifacts/{artifact}/{$}", s.handleOpenArtifact)
	mux.HandleFunc(artifactViewRoute, s.handleOpenArtifact)
	mux.HandleFunc(artifactKeyRoute, s.handleArtifactFile)
}

func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.Artifacts(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]Artifact{"artifacts": list})
}

func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	version, ok := artifactVersionParam(r)
	if !ok {
		writeError(w, http.StatusNotFound, "artifact version not found")
		return
	}
	if err := s.m.DeleteArtifact(r.PathValue("id"), r.PathValue("artifact"), version); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleOpenArtifact redirects a version's page, or the latest one's, to
// the same path under a key for that version, as the view route does.
func (s *Server) handleOpenArtifact(w http.ResponseWriter, r *http.Request) {
	id, artifact := r.PathValue("id"), r.PathValue("artifact")
	version, ok := artifactVersionParam(r)
	if !ok {
		writeError(w, http.StatusNotFound, "artifact version not found")
		return
	}
	v, err := s.m.artifactVersion(id, artifact, version)
	if err != nil {
		writeFailure(w, err)
		return
	}
	p := r.PathValue("path")
	if p == "" {
		p = v.Entry
	}
	exp := time.Now().Add(fileKeyTTL).Unix()
	session := s.requestFileSession(r)
	scope := artifactScope(id, artifact, v.Version)
	prefix := "/api/sessions/" + url.PathEscape(id) + "/artifact-files/" + artifact + "/" + strconv.Itoa(v.Version) + "/"
	if s.secureRequest(r) {
		setFileCookie(w, fileCookie(s.signKey, r.Host, session, scope, exp), prefix, int(fileKeyTTL/time.Second))
	}
	target := prefix + fileKey(s.signKey, r.Host, session, scope, exp) + "/" + viewPath(p)
	if r.URL.Query().Get("download") == "1" {
		target += "?download=1"
	}
	http.Redirect(w, r, target, http.StatusFound) // #nosec G710 -- a path under /api/sessions/ built from the route's checked values; never another host.
}

// handleArtifactFile serves a file of one version under that version's key.
func (s *Server) handleArtifactFile(w http.ResponseWriter, r *http.Request) {
	id, artifact := r.PathValue("id"), r.PathValue("artifact")
	version, ok := artifactVersionParam(r)
	if !ok || version == 0 || !s.validFileKey(r.PathValue("key"), r.Host, artifactScope(id, artifact, version)) ||
		(s.secureRequest(r) && !s.validFileCookie(r, artifactScope(id, artifact, version))) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	f, err := s.m.ArtifactFile(id, artifact, version, r.PathValue("path"))
	s.serveView(w, r, f, err)
}
