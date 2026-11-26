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
	"path"
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
	stagingPrefix       = ".publish-"
	maxArtifactFiles    = 500
	maxArtifactBytes    = 25 << 20
	maxArtifactVersions = 10
	maxThreadArtifacts  = 20
	maxArtifactTitle    = 120
	maxArtifactDepth    = 32
	// artifactViewRoute is a version's page under the cookie; it redirects
	// to the same path under a key for that version (artifactKeyRoute).
	artifactViewRoute = "GET /api/sessions/{id}/artifacts/{artifact}/v/{version}/{path...}"
	// artifactKeyRoute serves one version's files under a file key scoped to
	// that version, the key in the path so the page's relative links keep it.
	artifactKeyRoute = "GET /api/sessions/{id}/artifact-files/{artifact}/{version}/{key}/{path...}"
)

// artifactSecurity is viewSecurity for a published page: the same sandbox,
// and its scripts, styles, images, fonts and frames come only from this
// host (in a sandboxed document 'self' is the response URL's origin), so the
// page loads its own snapshot's files and nothing from other hosts.
// Residual: 'self' admits any path on the uam origin, and popups and
// top-level navigation stay unrestricted; a path-scoped source would need
// the browser's host, which a Host-rewriting proxy hides from r.Host.
const artifactSecurity = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads; default-src 'self' data: blob:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'none'; form-action 'none'; frame-ancestors 'self'"

// artifactPolicy is viewPolicy for a published version's file.
func artifactPolicy(mime string) string {
	if policy := viewPolicy(mime); policy != viewSecurity {
		return policy
	}
	return artifactSecurity
}

var artifactIDRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

var publishTool = agentapi.HostTool{
	Name: publishToolName,
	Description: "Publish a page you built in the thread's directory so the owner can open it from uam: a folder with index.html at its top (with its own CSS, scripts, images and fonts) or one file. " +
		"uam copies a snapshot of it, so later edits do not change what was published; publish again with the same title or id for a new version. " +
		fmt.Sprintf("A snapshot leaves out .git, node_modules and .env files, links and hard-linked files and holds at most %d files and folders and %d MiB; an artifact keeps its last %d versions. ", maxArtifactFiles, maxArtifactBytes>>20, maxArtifactVersions) +
		"The page runs sandboxed: use relative paths and classic scripts. ES modules, fetch of its own files, localStorage, network calls and files from other hosts do not work. " +
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
// Source is the published path in the Thread's directory. Last is the
// highest version number it ever had, so a deleted version's number, and
// any key for it, never names other bytes.
type Artifact struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Source   string            `json:"source"`
	Versions []ArtifactVersion `json:"versions"`
	Last     int               `json:"last,omitempty"`
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

var (
	errArtifactTooLarge = fmt.Errorf("the snapshot would hold more than %d files and folders or %d MiB; publish only the built page's own folder", maxArtifactFiles, maxArtifactBytes>>20)
	errArtifactTooDeep  = fmt.Errorf("the snapshot would nest folders more than %d deep; publish only the built page's own folder", maxArtifactDepth)
)

// snapshotter copies one publish's file or folder. Every read goes through
// one os.Root of the Thread's directory, opened once, and each name is
// checked with Lstat and then opened and compared with what Lstat saw, so
// no link is followed and nothing outside that directory is read.
type snapshotter struct {
	ctx context.Context
	// entries counts the files and folders visited, against maxArtifactFiles.
	entries, files int
	size           int64
	// skipped are the paths left out as links, hard links or special files.
	skipped []string
}

// snapshot copies the file or folder p (relative to workdir, or absolute
// inside it) into dst. It returns the entry page's path in dst and the
// published path.
func (s *snapshotter) snapshot(workdir, p, dst string) (entry, source string, err error) {
	if p == "" || len(p) > 4096 || !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return "", "", errors.New("path is required")
	}
	outside := errors.New("path must name a file or folder inside the thread's directory, other than the directory itself")
	rel, ok := threadRel(workdir, p)
	if !ok {
		return "", "", outside
	}
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return "", "", outside
	}
	dir := root
	defer func() { _ = dir.Close() }()
	parts := strings.Split(rel, string(filepath.Separator))
	for i, name := range parts {
		if skippedName(name) {
			return "", "", fmt.Errorf("%s is not published: .git, node_modules and .env files are left out", p)
		}
		info, err := dir.Lstat(name)
		switch {
		case err != nil:
			return "", "", fmt.Errorf("%s does not exist", p)
		case info.Mode()&fs.ModeSymlink != 0:
			return "", "", fmt.Errorf("%s is a symbolic link; publish the file or folder itself", p)
		case i == len(parts)-1 && info.Mode().IsRegular():
			reason, err := s.copyFile(dir, name, info, filepath.Join(dst, name))
			if err == nil && reason != "" {
				err = fmt.Errorf("%s is not published: it %s", p, reason)
			}
			return name, filepath.ToSlash(rel), err
		case !info.IsDir():
			return "", "", fmt.Errorf("%s is not a file or folder", p)
		}
		next, err := openSnapshotDir(dir, name, info)
		_ = dir.Close()
		if dir = next; err != nil {
			return "", "", fmt.Errorf("%s cannot be read", p)
		}
	}
	if err := s.copyDir(dir, dst, "", 1); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(filepath.Join(dst, indexDocument)); err != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s has no %s at its top; publish a folder with one, or a single file", p, indexDocument)
	}
	return indexDocument, filepath.ToSlash(rel), nil
}

// threadRel returns p's path inside workdir, spelled from workdir as
// configured or from its resolved path, so either spelling of an alias works.
func threadRel(workdir, p string) (string, bool) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	bases := []string{filepath.Clean(workdir)}
	if real, err := filepath.EvalSymlinks(workdir); err == nil {
		bases = append(bases, real)
	}
	for _, base := range bases {
		if rel, err := filepath.Rel(base, filepath.Clean(p)); err == nil && rel != "." && filepath.IsLocal(rel) {
			return rel, true
		}
	}
	return "", false
}

// openSnapshotDir opens the folder name of dir that Lstat saw as info, and
// fails if what it opened is another one, such as a link put in its place.
func openSnapshotDir(dir *os.Root, name string, info fs.FileInfo) (*os.Root, error) {
	sub, err := dir.OpenRoot(name)
	if err != nil {
		return dir, err
	}
	if opened, err := sub.Stat("."); err != nil || !os.SameFile(info, opened) {
		_ = sub.Close()
		return dir, errors.New("the folder changed while it was read")
	}
	return sub, nil
}

// copyDir copies the folder src into dst, reading its entries a batch at a
// time; rel is src's path in the snapshot, for skipped.
func (s *snapshotter) copyDir(src *os.Root, dst, rel string, depth int) error {
	if depth > maxArtifactDepth {
		return errArtifactTooDeep
	}
	d, err := src.Open(".")
	if err != nil {
		return fmt.Errorf("%s cannot be read", rel)
	}
	defer func() { _ = d.Close() }()
	for {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		batch, err := d.ReadDir(64)
		for _, e := range batch {
			if s.entries++; s.entries > maxArtifactFiles {
				return errArtifactTooLarge
			}
			name := e.Name()
			if skippedName(name) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return fmt.Errorf("%s cannot be read", path.Join(rel, name))
			}
			target := filepath.Join(dst, name)
			switch {
			case info.IsDir():
				err = s.copySubdir(src, name, info, target, path.Join(rel, name), depth)
			case info.Mode().IsRegular():
				var reason string
				if reason, err = s.copyFile(src, name, info, target); reason != "" {
					s.skipped = append(s.skipped, path.Join(rel, name)+" ("+reason+")")
				}
			default:
				s.skipped = append(s.skipped, path.Join(rel, name)+" (a link or special file)")
			}
			if err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s cannot be read", rel)
		}
	}
}

func (s *snapshotter) copySubdir(src *os.Root, name string, info fs.FileInfo, dst, rel string, depth int) error {
	sub, err := openSnapshotDir(src, name, info)
	if err != nil {
		return fmt.Errorf("%s cannot be read", rel)
	}
	defer func() { _ = sub.Close() }()
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	return s.copyDir(sub, dst, rel, depth+1)
}

// copyFile copies the regular file name of dir, which Lstat saw as info, to
// dst, failing past the snapshot's bytes. A file that is no longer the one
// Lstat saw, or that has other hard links (which could carry another
// file's bytes), copies nothing; reason says why.
func (s *snapshotter) copyFile(dir *os.Root, name string, info fs.FileInfo, dst string) (reason string, err error) {
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("%s cannot be read", name)
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "changed while it was read", nil
	}
	if st, ok := opened.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return "has other hard links", nil
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a fresh staging directory of uam's own.
	if err != nil {
		return "", err
	}
	budget := maxArtifactBytes - s.size
	n, err := io.Copy(out, io.LimitReader(f, budget+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > budget {
		err = errArtifactTooLarge
	}
	s.files++
	s.size += n
	return "", err
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
	// Held from before staging, so concurrent calls cannot pass the caps.
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	m.mu.Lock()
	workdir := s.workdir
	m.mu.Unlock()
	root := m.artifactRoot(taskID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		log.Warn("make the artifacts directory failed", "session", taskID, "error", err)
		return "", errors.New("uam could not store the artifact; try again")
	}
	list := reconcileArtifacts(root)
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
	staging, err := os.MkdirTemp(root, stagingPrefix)
	if err != nil {
		return "", errors.New("uam could not store the artifact; try again")
	}
	defer func() { _ = os.RemoveAll(staging) }()
	call.ReportProgress("Copying the files")
	snap := snapshotter{ctx: ctx}
	entry, source, err := snap.snapshot(workdir, args.Path, staging)
	if err != nil {
		return "", err
	}
	version := a.Last + 1
	if len(a.Versions) > 0 {
		version = max(version, a.Versions[len(a.Versions)-1].Version+1)
	}
	dir := artifactVersionDir(root, a.ID, version)
	if err := os.MkdirAll(filepath.Join(root, a.ID), 0o700); err != nil {
		return "", errors.New("uam could not store the artifact; try again")
	}
	if err := os.Rename(staging, dir); err != nil {
		return "", errors.New("uam could not store the artifact; try again")
	}
	a.Title, a.Source, a.Last = title, source, version
	a.Versions = append(a.Versions, ArtifactVersion{Version: version, At: time.Now().UTC(), Entry: entry, Files: snap.files, Bytes: snap.size, CallID: call.CallID})
	var pruned []int
	for len(a.Versions) > maxArtifactVersions {
		pruned = append(pruned, a.Versions[0].Version)
		a.Versions = a.Versions[1:]
	}
	// The metadata commits the version; old versions go only after it.
	if err := writePrivateJSON(m.uploadRoot(), filepath.Join(root, a.ID, artifactMetaFile), a); err != nil {
		// Only metadata read back without the version proves it uncommitted;
		// an unreadable one is left for reconcileArtifacts.
		if stored, rerr := readArtifact(root, a.ID); rerr == nil && !slices.ContainsFunc(stored.Versions, func(v ArtifactVersion) bool { return v.Version == version }) {
			_ = os.RemoveAll(dir)
		}
		// The rename may have committed it before a later sync failed.
		m.artifactsChanged(taskID, len(readArtifacts(root)))
		log.Warn("store an artifact failed", "session", taskID, "error", err)
		return "", errors.New("uam could not store the artifact; try again")
	}
	for _, v := range pruned {
		_ = os.RemoveAll(artifactVersionDir(root, a.ID, v))
	}
	if !m.artifactsChanged(taskID, len(list)) {
		removeUploads(m.taskUploadDir(taskID))
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	text := fmt.Sprintf("Published %q as version %d (%d files, %d KiB); the owner opens it from the card in this conversation. Its artifact id is %q: publish again with this id or the same title for a new version.",
		title, version, snap.files, (snap.size+1023)>>10, a.ID)
	if n := len(snap.skipped); n > 0 {
		shown := snap.skipped[:min(n, 10)]
		text += fmt.Sprintf(" Left out %d entries: %s", n, strings.Join(shown, ", "))
		if n > len(shown) {
			text += ", …"
		}
		text += "."
	}
	return text, nil
}

// reconcileArtifacts removes what an interrupted publish left under root
// (staging folders, versions the metadata does not list, an artifact whose
// first metadata was never written) and returns the artifacts. The caller
// holds the Thread's artifactMu.
func reconcileArtifacts(root string) []Artifact {
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			_ = os.RemoveAll(dir)
			continue
		}
		if !artifactIDRE.MatchString(e.Name()) {
			continue
		}
		a, err := readArtifact(root, e.Name())
		if err != nil {
			if _, err := os.Lstat(filepath.Join(dir, artifactMetaFile)); errors.Is(err, fs.ErrNotExist) {
				_ = os.RemoveAll(dir)
			}
			continue
		}
		versions, _ := os.ReadDir(dir)
		for _, v := range versions {
			if v.IsDir() && !slices.ContainsFunc(a.Versions, func(k ArtifactVersion) bool { return "v"+strconv.Itoa(k.Version) == v.Name() }) {
				_ = os.RemoveAll(filepath.Join(dir, v.Name()))
			}
		}
	}
	return readArtifacts(root)
}

// artifactsChanged records that the Thread taskID has n artifacts, for its
// header, and that its artifacts changed, so open lists and cards read them
// again. It is false when the Thread was removed meanwhile.
func (m *Manager) artifactsChanged(taskID string, n int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	if s == nil || s.removed {
		return false
	}
	before := m.summaryLocked(s)
	s.artifacts = n
	s.artifactsRevision++
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
	s, err := m.lookup(taskID)
	if err != nil {
		return err
	}
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
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
		a.Last = max(a.Last, a.Versions[len(a.Versions)-1].Version)
		a.Versions = slices.Delete(a.Versions, i, i+1)
		if err = writePrivateJSON(m.uploadRoot(), filepath.Join(root, id, artifactMetaFile), a); err == nil {
			err = os.RemoveAll(artifactVersionDir(root, id, version))
		}
	}
	// Also on failure: the metadata may have changed before a later step failed.
	m.artifactsChanged(taskID, len(readArtifacts(root)))
	if err != nil {
		log.Warn("delete an artifact failed", "session", taskID, "error", err)
		return newError(http.StatusInternalServerError, "uam could not delete the artifact")
	}
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
// the Thread's directory. The version must be in the artifact's metadata.
func (m *Manager) ArtifactFile(taskID, id string, version int, rel string) (*ServedFile, error) {
	if version < 1 {
		return nil, newError(http.StatusNotFound, msgFileNotFound)
	}
	if _, err := m.artifactVersion(taskID, id, version); err != nil {
		return nil, err
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
	s.serveView(w, r, f, err, artifactPolicy, "/api/sessions/"+url.PathEscape(id)+"/artifact-files/"+url.PathEscape(artifact)+"/"+strconv.Itoa(version)+"/"+url.PathEscape(r.PathValue("key"))+"/")
}
