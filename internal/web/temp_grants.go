package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
)

const (
	grantKeyRoute = "GET /api/sessions/{id}/file-grants/{grant_id}/{key}"
	grantTTL      = 5 * time.Minute
	maxGrantBody  = 8 << 10
	maxTaskGrants = 8
	maxGrants     = 32
	maxGrantOps   = 16
)

type fileGrant struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	ExpiresAt time.Time `json:"expires_at"`
}

type tempGrant struct {
	id, taskID, host, relative string
	file                       *os.File
	info                       os.FileInfo
	expires                    time.Time
	ctx                        context.Context
	cancel                     context.CancelFunc
	timer                      *time.Timer
	comparisons                int
	revoked                    bool
}

// tempGrants is volatile and bounded. If both locks are needed, Manager.mu
// precedes mu; filesystem checks never run under Manager.mu. Publication uses
// the Task pointer, so successful deletion cannot be followed by a late grant.
type tempGrants struct {
	manager *Manager
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	entries map[string]*tempGrant
	ops     chan struct{}
	roots   *grantRoots
	rootErr error
	// Deterministic filesystem/lifetime seams; tests set them before use.
	beforeLeaf, beforeRewalk, beforeRead func()
	beforePublish                        func(*os.File)
	ttl                                  time.Duration
}

func newTempGrants(m *Manager) *tempGrants {
	ctx, cancel := context.WithCancel(context.Background())
	g := &tempGrants{manager: m, ctx: ctx, cancel: cancel, entries: make(map[string]*tempGrant),
		ops: make(chan struct{}, maxGrantOps), ttl: grantTTL}
	// Pin configuration before NewServer returns: a later rename cannot
	// cause the first request to mistake a replacement for the runtime root.
	g.roots, g.rootErr = openGrantRoots(os.TempDir(), daemonruntime.DefaultDir())
	m.mu.Lock()
	if m.closed {
		g.closed = true
		cancel()
		if g.roots != nil {
			g.roots.close()
		}
	} else {
		if m.fileGrants == nil {
			m.fileGrants = make(map[*tempGrants]struct{})
		}
		m.fileGrants[g] = struct{}{}
	}
	m.mu.Unlock()
	return g
}

// Missing roots disable grants (503), never weaken their path policy.
func (g *tempGrants) rootsForUse() (*grantRoots, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.rootErr != nil || g.roots == nil {
		return nil, newError(http.StatusServiceUnavailable, "temporary file protection is unavailable")
	}
	return g.roots, nil
}

func (g *tempGrants) operation(ctx context.Context) (context.Context, func(), error) {
	select {
	case g.ops <- struct{}{}:
	default:
		return nil, nil, newError(http.StatusTooManyRequests, "too many temporary file operations")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.ctx, cancel)
	release := func() { stop(); cancel(); <-g.ops }
	if g.ctx.Err() != nil || ctx.Err() != nil {
		release()
		return nil, nil, errGrantUnavailable
	}
	return ctx, release, nil
}

func (g *tempGrants) create(ctx context.Context, host, taskID, candidate string) (fileGrant, error) {
	ctx, release, err := g.operation(ctx)
	if err != nil {
		return fileGrant{}, err
	}
	defer release()
	task, err := g.manager.lookup(taskID)
	if err != nil {
		return fileGrant{}, err
	}
	roots, err := g.rootsForUse()
	if err != nil {
		return fileGrant{}, err
	}
	rel, err := roots.relative(candidate)
	if err != nil {
		return fileGrant{}, err
	}
	f, err := roots.checkedOpen(ctx, rel, g.beforeLeaf, g.beforeRewalk)
	if err != nil {
		return fileGrant{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
		}
	}()
	typed, err := viewType(f, rel)
	if err != nil {
		return fileGrant{}, errGrantUnavailable
	}
	info, err := grantFileInfo(f)
	if err != nil {
		return fileGrant{}, err
	}
	if g.beforePublish != nil {
		g.beforePublish(f)
	}
	g.manager.mu.Lock()
	defer g.manager.mu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.manager.closed || task.removed || g.manager.sessions[taskID] != task || ctx.Err() != nil {
		return fileGrant{}, errGrantUnavailable
	}
	count := 0
	for _, entry := range g.entries {
		if entry.taskID == taskID {
			count++
		}
	}
	if count >= maxTaskGrants || len(g.entries) >= maxGrants {
		return fileGrant{}, newError(http.StatusTooManyRequests, "temporary file grant limit reached")
	}
	id := rand.Text()
	// The signed expiry is in Unix seconds, so registry and key expire at
	// the same instant, without a fractional-second mismatch.
	expires := time.Now().Add(g.ttl).Truncate(time.Second)
	grantCtx, cancel := context.WithCancel(g.ctx)
	entry := &tempGrant{id: id, taskID: taskID, host: strings.ToLower(host), relative: rel,
		file: f, info: info, expires: expires, ctx: grantCtx, cancel: cancel}
	g.entries[id] = entry
	entry.timer = time.AfterFunc(time.Until(expires), func() { g.revoke(host, taskID, id) })
	keep = true
	return fileGrant{ID: id, Name: filepath.Base(rel), MIME: typed.MIME, Size: info.Size(), ExpiresAt: expires}, nil
}

// revokeLocked cancels every active reader, including readers whose comparison
// lease already ended. A retained descriptor closes when the last comparison
// finishes; each reader owns a separate descriptor and cancellation context.
func (g *tempGrants) revokeLocked(entry *tempGrant) {
	delete(g.entries, entry.id)
	entry.revoked = true
	entry.cancel()
	if entry.timer != nil {
		entry.timer.Stop()
	}
	if entry.comparisons == 0 {
		_ = entry.file.Close()
	}
}

func (g *tempGrants) revoke(host, taskID, id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if entry := g.entries[id]; entry != nil && entry.host == strings.ToLower(host) && entry.taskID == taskID {
		g.revokeLocked(entry)
	}
}

// revokeTask is called while Manager.mu still holds the successful removal.
func (g *tempGrants) revokeTask(taskID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, entry := range g.entries {
		if entry.taskID == taskID {
			g.revokeLocked(entry)
		}
	}
}

func (g *tempGrants) endComparison(entry *tempGrant) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry.comparisons--
	if entry.revoked && entry.comparisons == 0 {
		_ = entry.file.Close()
	}
}

func (g *tempGrants) open(ctx context.Context, host, taskID, id string) (*ServedFile, context.Context, func(), error) {
	ctx, releaseOp, err := g.operation(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	g.mu.Lock()
	entry := g.entries[id]
	if entry == nil || entry.taskID != taskID || entry.host != strings.ToLower(host) || !time.Now().Before(entry.expires) {
		if entry != nil && !time.Now().Before(entry.expires) {
			g.revokeLocked(entry)
		}
		g.mu.Unlock()
		releaseOp()
		return nil, nil, nil, errGrantUnavailable
	}
	entry.comparisons++
	g.mu.Unlock()
	requestCtx := ctx
	ctx, cancel := context.WithCancel(entry.ctx)
	stop := context.AfterFunc(requestCtx, cancel)
	if requestCtx.Err() != nil {
		cancel()
	}
	release := func() { stop(); cancel(); releaseOp() }
	roots, err := g.rootsForUse()
	var f *os.File
	if err == nil {
		f, err = roots.checkedOpen(ctx, entry.relative, g.beforeLeaf, g.beforeRewalk)
	}
	if err == nil {
		info, statErr := grantFileInfo(f)
		if statErr != nil || !os.SameFile(entry.info, info) || ctx.Err() != nil || requestCtx.Err() != nil {
			err = errGrantUnavailable
		}
	}
	g.endComparison(entry)
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		if ctx.Err() == nil && requestCtx.Err() == nil {
			g.revoke(host, taskID, id)
		}
		release()
		return nil, nil, nil, err
	}
	typed, err := viewType(f, entry.relative)
	if err == nil {
		typed.Info, err = grantFileInfo(f)
	}
	if err != nil || ctx.Err() != nil || requestCtx.Err() != nil {
		_ = f.Close()
		if ctx.Err() == nil && requestCtx.Err() == nil {
			g.revoke(host, taskID, id)
		}
		release()
		return nil, nil, nil, errGrantUnavailable
	}
	// Closing the descriptor on cancellation also bounds its lifetime while
	// a slow client blocks ServeContent in ResponseWriter.Write.
	stopFile := context.AfterFunc(ctx, func() { _ = f.Close() })
	return typed, ctx, func() { stopFile(); _ = f.Close(); release() }, nil
}

func (g *tempGrants) close() {
	g.manager.mu.Lock()
	delete(g.manager.fileGrants, g)
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		g.cancel()
		for _, entry := range g.entries {
			g.revokeLocked(entry)
		}
		if g.roots != nil {
			g.roots.close()
		}
	}
	g.mu.Unlock()
	g.manager.mu.Unlock()
}

// Close cancels grants before HTTP shutdown waits for readers to drain. It is
// safe to call more than once; a Server must not be reused after Close.
func (s *Server) Close() { s.grants.close() }

func grantKey(token, host, taskID, grantID string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return e + "." + grantKeyMAC(token, host, taskID, grantID, e)
}

func grantKeyMAC(token, host, taskID, grantID, exp string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("uam-web-temp-file-v1|" + strings.ToLower(host) + "|" + taskID + "|" + grantID + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validGrantKey(key, host, taskID, grantID string) bool {
	exp, mac, ok := strings.Cut(key, ".")
	until, err := strconv.ParseInt(exp, 10, 64)
	return ok && err == nil && time.Now().Unix() < until &&
		subtle.ConstantTimeCompare([]byte(mac), []byte(grantKeyMAC(s.token, host, taskID, grantID, exp))) == 1
}

func (s *Server) handleCreateGrant(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGrantBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "temporary file request is too large")
		return
	}
	if !utf8.Valid(data) {
		writeError(w, http.StatusBadRequest, "invalid temporary file request")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "temporary file request is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid temporary file request")
		}
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid temporary file request")
		return
	}
	grant, err := s.grants.create(r.Context(), r.Host, r.PathValue("id"), body.Path)
	if err != nil {
		writeFailure(w, err)
		return
	}
	key := grantKey(s.token, r.Host, r.PathValue("id"), grant.ID, grant.ExpiresAt.Unix())
	grant.URL = "/api/sessions/" + url.PathEscape(r.PathValue("id")) + "/file-grants/" + grant.ID + "/" + key
	writeJSON(w, http.StatusCreated, grant)
}

func (s *Server) handleDeleteGrant(w http.ResponseWriter, r *http.Request) {
	s.grants.revoke(r.Host, r.PathValue("id"), r.PathValue("grant_id"))
	w.WriteHeader(http.StatusNoContent)
}

type grantReader struct {
	ctx    context.Context
	reader io.ReadSeeker
}

func (r grantReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (r grantReader) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Seek(offset, whence)
}

func (s *Server) handleGrantFile(w http.ResponseWriter, r *http.Request) {
	id, grantID := r.PathValue("id"), r.PathValue("grant_id")
	if !s.validGrantKey(r.PathValue("key"), r.Host, id, grantID) {
		writeError(w, http.StatusUnauthorized, "invalid or expired temporary file key")
		return
	}
	f, ctx, release, err := s.grants.open(r.Context(), r.Host, id, grantID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer release()
	if s.grants.beforeRead != nil {
		s.grants.beforeRead()
	}
	if ctx.Err() != nil {
		writeError(w, http.StatusNotFound, "temporary file is unavailable")
		return
	}
	w.Header().Set("Content-Type", f.MIME)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", viewSecurity)
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" || f.MIME == "application/octet-stream" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Info.Name()}))
	// Capture the checked size: a later append cannot turn a bounded grant
	// into an unbounded response. In-place writes remain visible.
	reader := grantReader{ctx: ctx, reader: io.NewSectionReader(f.File, 0, f.Info.Size())}
	http.ServeContent(grantResponse{w}, r, f.Info.Name(), f.Info.ModTime(), reader)
}

// ServeContent clears Cache-Control on errors such as an unsatisfiable Range.
// Every grant response must remain outside persistent caches, including those
// errors; enforce that policy at the final header write.
type grantResponse struct{ http.ResponseWriter }

func (w grantResponse) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(status)
}

// Scrub grant URLs in request targets and headers, including opaque-frame
// Referer values. Existing Task file-key policy is intentionally unchanged.
func redactGrantURL(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		// A malformed header value must not bypass redaction merely because
		// it is not parseable as a URL.
		if strings.Contains(value, "file-grants") {
			return "[redacted]"
		}
		return value
	}
	parts := strings.Split(u.Path, "/")
	for i := 0; i+4 < len(parts); i++ {
		if parts[i] == "api" && parts[i+1] == "sessions" && parts[i+3] == "file-grants" && i+5 < len(parts) {
			parts[i+5] = "[redacted]"
			u.Path, u.RawPath = strings.Join(parts, "/"), ""
			return u.String()
		}
	}
	return value
}
