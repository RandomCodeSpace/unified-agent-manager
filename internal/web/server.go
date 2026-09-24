package web

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

//go:embed all:dist
var embedded embed.FS

const (
	maxBodyBytes      = 1 << 20
	maxLoginBytes     = 4 << 10
	loginReadWait     = 10 * time.Second
	heartbeatInterval = 15 * time.Second
	streamWriteWait   = 15 * time.Second
	contentSecurity   = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
)

// ServerConfig configures the HTTP interface.
type ServerConfig struct {
	Manager *Manager
	// Token is the access token browsers present at /api/login.
	Token string
	// PublicOrigins are origins (scheme://host[:port]) of same-host reverse
	// proxies whose Host header is accepted besides loopback.
	PublicOrigins []string
	// NoAuth treats every request as authenticated. The Host, cross-origin,
	// content-type and body checks still apply.
	NoAuth  bool
	Version string
	// Assets overrides the embedded frontend (tests).
	Assets fs.FS
}

// Server is the HTTP handler for the web interface.
type Server struct {
	m         *Manager
	token     string
	noAuth    bool
	hosts     map[string]bool
	csrf      *http.CrossOriginProtection
	assets    fs.FS
	version   string
	mux       *http.ServeMux
	heartbeat time.Duration
}

// NewServer validates cfg and builds the handler.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Manager == nil || cfg.Token == "" {
		return nil, errors.New("web server needs a manager and an access token")
	}
	s := &Server{
		m: cfg.Manager, token: cfg.Token, noAuth: cfg.NoAuth, hosts: map[string]bool{},
		csrf: http.NewCrossOriginProtection(), assets: cfg.Assets, version: cfg.Version, heartbeat: heartbeatInterval,
	}
	for _, origin := range cfg.PublicOrigins {
		normalized, err := NormalizePublicOrigin(origin)
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(normalized)
		s.hosts[strings.ToLower(u.Host)] = true
	}
	if s.assets == nil {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, fmt.Errorf("embedded web assets: %w", err)
		}
		s.assets = sub
	}
	s.routes()
	return s, nil
}

// NormalizePublicOrigin validates a --public-origin value and returns it as
// scheme://host[:port].
func NormalizePublicOrigin(origin string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(origin, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid public origin %q: want http(s)://host[:port]", origin)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func (s *Server) routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth", s.handleAuth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("POST /api/projects", s.handleAddProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.handleRenameProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.handleRemoveProject)
	mux.HandleFunc("GET /api/sessions", s.handleList)
	mux.HandleFunc("POST /api/sessions", s.handleCreate)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleDetail)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handlePatch)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/sessions/{id}/subagents/{agent_id}", s.handleSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/sessions/{id}/close", s.handleClose)
	mux.HandleFunc("POST /api/sessions/{id}/interactions/{iid}", s.handleAnswer)
	mux.HandleFunc("GET /api/sessions/{id}/changes", s.handleChanges)
	mux.HandleFunc("GET /api/sessions/{id}/changes/file", s.handleFileChange)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/", s.handleStatic)
	s.mux = mux
}

func isAPI(p string) bool { return p == "/api" || strings.HasPrefix(p, "/api/") }

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// ServeHTTP applies the security checks every request passes before routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", contentSecurity)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	api := isAPI(r.URL.Path)
	if api {
		h.Set("Cache-Control", "no-store")
	}
	// A foreign Host means DNS rebinding or a misrouted request; the
	// service only answers for loopback and configured public origins.
	if !s.allowedHost(r.Host) {
		writeError(w, http.StatusForbidden, "host not allowed")
		return
	}
	if !safeMethod(r.Method) {
		if err := s.csrf.Check(r); err != nil {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		// A DELETE without a body has no content to type; every other
		// state change must be JSON.
		bodyless := r.Method == http.MethodDelete && r.ContentLength == 0
		if !bodyless && !jsonContentType(r.Header.Get("Content-Type")) {
			writeError(w, http.StatusUnsupportedMediaType, "requests must use Content-Type: application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}
	if api && r.URL.Path != "/api/auth" && r.URL.Path != "/api/login" && !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) allowedHost(hostport string) bool {
	host := strings.ToLower(hostport)
	if s.hosts[host] {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func jsonContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Debug("write web response failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeFailure(w http.ResponseWriter, err error) {
	status, msg := errorStatus(err)
	if status == http.StatusInternalServerError {
		log.Error("web request failed", "error", err)
	}
	var webErr *Error
	if errors.As(err, &webErr) && webErr.ProjectID != "" {
		writeJSON(w, status, map[string]string{"error": msg, "project_id": webErr.ProjectID})
		return
	}
	writeError(w, status, msg)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.authenticated(r), "required": !s.noAuth})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Login needs no cookie, so bound how long and how much a client may send
	// before it is judged; slow senders must not hold connections open.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(loginReadWait))
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBytes)
	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Token)), []byte(s.token)) != 1 {
		log.Warn("web login rejected", "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "invalid access token")
		return
	}
	s.setCookie(w, r, sessionCookie(s.token, r.Host, time.Now().Add(cookieMaxAge*time.Second).Unix()), cookieMaxAge)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	s.m.RefreshModels()
	writeJSON(w, http.StatusOK, Meta{Version: s.version, Providers: s.m.Providers(), RecentWorkdirs: s.m.RecentWorkdirs()})
}

func (s *Server) handleProjects(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]Project{"projects": s.m.Projects()})
}

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	p, err := s.m.AddProject(body.Dir, body.Name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	p, err := s.m.RenameProject(r.PathValue("id"), body.Name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleRemoveProject(w http.ResponseWriter, r *http.Request) {
	if err := s.m.RemoveProject(r.PathValue("id")); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.m.List())
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	summary, err := s.m.Create(req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, summary)
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.m.View(r.Context(), id); err != nil {
		writeFailure(w, err)
		return
	}
	detail, err := s.m.Detail(id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handlePatch applies {name?, model?}. The model changes first: it is the
// part that can be refused, and a refused request should change nothing.
func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  *string `json:"name"`
		Model *string `json:"model"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == nil && body.Model == nil {
		writeError(w, http.StatusBadRequest, "name or model is required")
		return
	}
	if body.Name != nil {
		if _, err := cleanTaskName(*body.Name); err != nil {
			writeFailure(w, err)
			return
		}
	}
	id := r.PathValue("id")
	var summary SessionSummary
	var err error
	if body.Model != nil {
		if summary, err = s.m.SetModel(id, *body.Model); err != nil {
			writeFailure(w, err)
			return
		}
	}
	if body.Name != nil {
		if summary, err = s.m.Rename(id, *body.Name); err != nil {
			writeFailure(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.m.Delete(r.PathValue("id")); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSubagent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.m.View(r.Context(), id); err != nil {
		writeFailure(w, err)
		return
	}
	detail, err := s.m.Subagent(id, r.PathValue("agent_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text      string `json:"text"`
		RequestID string `json:"request_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	sub, err := s.m.Submit(r.PathValue("id"), body.Text, body.RequestID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sub)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	summary, err := s.m.Cancel(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, summary)
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	summary, err := s.m.Close(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var answer agentapi.Answer
	if !decodeBody(w, r, &answer) {
		return
	}
	ix, err := s.m.Answer(r.PathValue("id"), r.PathValue("iid"), answer)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ix)
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	changes, err := s.m.Changes(r.Context(), r.PathValue("id"), r.URL.Query().Get("scope"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, changes)
}

func (s *Server) handleFileChange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	diff, err := s.m.FileChange(r.Context(), r.PathValue("id"), q.Get("scope"), q.Get("path"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// handleEvents streams events. The stream only observes: it ending, for any
// reason, has no effect on providers.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	if id != "" {
		if err := s.m.View(r.Context(), id); err != nil {
			writeFailure(w, err)
			return
		}
	}
	sub, snapshot, err := s.m.Subscribe(id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer s.m.Unsubscribe(sub)
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(frame []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteWait))
		if _, err := w.Write(frame); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write(append([]byte("retry: 2000\n\n"), snapshot...)) {
		return
	}
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Gone():
			return
		case frame := <-sub.Frames():
			sub.Sent(frame)
			if !write(frame) {
				return
			}
		case <-ticker.C:
			if !write([]byte(": keep-alive\n\n")) {
				return
			}
		}
	}
}

// handleStatic serves the embedded single-page application: real files as
// themselves, anything else that looks like an app route as index.html, and
// never a directory listing.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if s.serveAsset(w, r, name) {
		return
	}
	if path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	if !s.serveAsset(w, r, "index.html") {
		http.NotFound(w, r)
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := s.assets.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		return false
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
	return true
}
