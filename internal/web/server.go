package web

import (
	"bytes"
	"crypto/sha256"
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
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The placeholder keeps source-only checkouts buildable; release tags include dist.
//
//go:embed all:dist*
var embedded embed.FS

const (
	maxBodyBytes      = 1 << 20
	maxLoginBytes     = 4 << 10
	loginReadWait     = 10 * time.Second
	heartbeatInterval = 15 * time.Second
	streamWriteWait   = 15 * time.Second
	maxLoggedValue    = 512
	contentSecurity   = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
)

// ServerConfig configures the HTTP interface.
type ServerConfig struct {
	Manager *Manager
	// Token is the access token browsers present at /api/login.
	Token string
	// Listen is the address the service binds. Beyond loopback, a Host that
	// is an IP literal (such as the LAN address) is also accepted.
	Listen string
	// PublicOrigins are origins (scheme://host[:port]) of same-host reverse
	// proxies whose Host header is accepted besides loopback.
	PublicOrigins []string
	// NoAuth treats every request as authenticated. The Host, cross-origin,
	// content-type and body checks still apply.
	NoAuth  bool
	Version string
	// Assets overrides the embedded frontend (tests).
	Assets fs.FS
	// LogHeaders logs one record per request with its headers (credentials
	// redacted) and the outcome of the checks in ServeHTTP.
	LogHeaders bool
}

// Server is the HTTP handler for the web interface.
type Server struct {
	m         *Manager
	token     string
	noAuth    bool
	headerLog bool
	ipHosts   bool
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
		m: cfg.Manager, token: cfg.Token, noAuth: cfg.NoAuth, headerLog: cfg.LogHeaders, ipHosts: BeyondLoopback(cfg.Listen), hosts: map[string]bool{},
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
		if _, err := fs.Stat(sub, "index.html"); err != nil {
			return nil, errors.New("web assets are not built; run make build or make install, or install a release tag")
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
	mux.HandleFunc("PATCH /api/projects/{id}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.handleRemoveProject)
	mux.HandleFunc("GET /api/fs/dirs", s.handleListDirs)
	mux.HandleFunc("POST /api/fs/dirs", s.handleMakeDir)
	mux.HandleFunc("GET /api/sessions", s.handleList)
	mux.HandleFunc("POST /api/sessions", s.handleCreate)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleDetail)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handlePatch)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/sessions/{id}/subagents/{agent_id}", s.handleSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/subagents/{agent_id}/cancel", s.handleCancelSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/subagents/{agent_id}/prompt", s.handlePromptSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/sessions/{id}/command", s.handleCommand)
	mux.HandleFunc("GET /api/sessions/{id}/commands", s.handleCommands)
	mux.HandleFunc("GET /api/sessions/{id}/files", s.handleFiles)
	mux.HandleFunc("POST /api/sessions/{id}/attachments", s.handleUpload)
	mux.HandleFunc("GET /api/sessions/{id}/attachments/{attachment_id}", s.handleAttachment)
	mux.HandleFunc("POST /api/sessions/{id}/queue/resume", s.handleQueueResume)
	mux.HandleFunc("POST /api/sessions/{id}/queue/clear", s.handleQueueClear)
	mux.HandleFunc("DELETE /api/sessions/{id}/queue/{request_id}", s.handleQueueCancel)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/sessions/{id}/close", s.handleClose)
	mux.HandleFunc("POST /api/sessions/{id}/settle", s.handleStage((*Manager).Settle))
	mux.HandleFunc("POST /api/sessions/{id}/reopen", s.handleStage((*Manager).Reopen))
	mux.HandleFunc("POST /api/sessions/{id}/archive", s.handleStage((*Manager).Archive))
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
	// service only answers for loopback, configured public origins and,
	// beyond loopback, IP literals.
	if !s.allowedHost(r.Host) {
		s.refuse(w, r, http.StatusForbidden, "host not allowed")
		return
	}
	if !safeMethod(r.Method) {
		if err := s.csrf.Check(r); err != nil {
			s.refuse(w, r, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		// A DELETE without a body has no content to type; every other
		// state change must be JSON, except an attachment upload, whose body
		// is the file. Its type, like JSON, is not one a cross-site form can
		// send, and it gets its own size cap.
		bodyless := r.Method == http.MethodDelete && r.ContentLength == 0
		limit := int64(maxBodyBytes)
		switch {
		case r.Method == http.MethodPost && uploadPath(r.URL.Path):
			if !hasMediaType(r.Header.Get("Content-Type"), "application/octet-stream") {
				s.refuse(w, r, http.StatusUnsupportedMediaType, "attachment uploads must use Content-Type: application/octet-stream")
				return
			}
			limit = maxUploadBytes
		case !bodyless && !jsonContentType(r.Header.Get("Content-Type")):
			s.refuse(w, r, http.StatusUnsupportedMediaType, "requests must use Content-Type: application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	if api && r.URL.Path != "/api/auth" && r.URL.Path != "/api/login" && !s.authenticated(r) {
		s.refuse(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	s.logRequest(r, "allowed", 0)
	s.mux.ServeHTTP(w, r)
}

// refuse answers a request the checks turn away.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.logRequest(r, msg, status)
	writeError(w, status, msg)
}

// logRequest is --log-headers: one record per request, written where the
// checks decide. Credential headers are redacted, values are capped, and
// bodies are never read (/api/login carries the token in its body). The JSON
// logger escapes CR and LF, so a header value cannot forge a record.
func (s *Server) logRequest(r *http.Request, outcome string, status int) {
	if !s.headerLog {
		return
	}
	headers := make(map[string][]string, len(r.Header))
	for name, values := range r.Header {
		logged := make([]string, len(values))
		for i, v := range values {
			logged[i] = redactHeaderValue(name, v)
		}
		headers[name] = logged
	}
	args := []any{"method", r.Method, "path", capLogValue(r.URL.RequestURI()), "remote", r.RemoteAddr,
		"host", capLogValue(r.Host), "headers", headers, "outcome", outcome}
	if status != 0 {
		args = append(args, "status", status)
	}
	log.Info("web request", args...)
}

// credentialHints are name fragments of headers that can carry a credential:
// Cookie, Authorization and Proxy-Authorization, and custom ones such as
// X-Api-Key, X-Auth-Token or a proxy's JWT assertion.
var credentialHints = []string{"auth", "cookie", "token", "key", "secret", "session", "jwt", "signature", "password", "credential"}

// redactHeaderValue is the logged form of one header value: a placeholder
// when the header can carry a credential, otherwise the value, capped.
func redactHeaderValue(name, value string) string {
	lower := strings.ToLower(name)
	for _, hint := range credentialHints {
		if strings.Contains(lower, hint) {
			return "[redacted]"
		}
	}
	return capLogValue(value)
}

func capLogValue(v string) string {
	if len(v) > maxLoggedValue {
		return v[:maxLoggedValue] + "…"
	}
	return v
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
	// A domain name stays refused on any bind: that is what a rebound
	// website sends.
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || s.ipHosts)
}

func jsonContentType(value string) bool { return hasMediaType(value, "application/json") }

func hasMediaType(value, want string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == want
}

// uploadPath matches exactly POST /api/sessions/{id}/attachments.
func uploadPath(p string) bool {
	ok, _ := path.Match("/api/sessions/*/attachments", p)
	return ok
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
	// Compare digests: ConstantTimeCompare returns early on a length
	// mismatch, and an owner-set token's length is part of the secret.
	given, want := sha256.Sum256([]byte(strings.TrimSpace(body.Token))), sha256.Sum256([]byte(s.token))
	if subtle.ConstantTimeCompare(given[:], want[:]) != 1 {
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
		Dir      string        `json:"dir"`
		Name     string        `json:"name"`
		Defaults *TaskDefaults `json:"defaults"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	p, err := s.m.AddProject(body.Dir, body.Name, body.Defaults)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     *string       `json:"name"`
		Defaults *TaskDefaults `json:"defaults"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == nil && body.Defaults == nil {
		writeError(w, http.StatusBadRequest, "name or defaults is required")
		return
	}
	p, err := s.m.UpdateProject(r.PathValue("id"), body.Name, body.Defaults)
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

func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	list, err := listDirs(r.URL.Query().Get("path"), maxDirEntries)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleMakeDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	p, err := makeDir(body.Parent, body.Name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": p})
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

// handlePatch applies Task settings. The model settings change first: this is
// the part that can be refused, and a refused request should change nothing.
func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        *string `json:"name"`
		Model       *string `json:"model"`
		Effort      *string `json:"effort"`
		ContextSize *string `json:"context_size"`
		Mode        *string `json:"mode"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == nil && body.Model == nil && body.Effort == nil && body.ContextSize == nil && body.Mode == nil {
		writeError(w, http.StatusBadRequest, "name, model, effort, context_size or mode is required")
		return
	}
	if body.Name != nil {
		if _, err := cleanTaskName(*body.Name); err != nil {
			writeFailure(w, err)
			return
		}
	}
	if body.Mode != nil {
		if _, err := parseMode(*body.Mode); err != nil {
			writeFailure(w, err)
			return
		}
	}
	id := r.PathValue("id")
	var summary SessionSummary
	var err error
	if body.Model != nil || body.Effort != nil || body.ContextSize != nil {
		if summary, err = s.m.SetModel(id, body.Model, body.Effort, body.ContextSize); err != nil {
			writeFailure(w, err)
			return
		}
	}
	if body.Mode != nil {
		if summary, err = s.m.SetMode(id, *body.Mode); err != nil {
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
	var body PromptRequest
	if !decodeBody(w, r, &body) {
		return
	}
	sub, err := s.m.Submit(r.PathValue("id"), body)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sub)
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var body CommandRequest
	if !decodeBody(w, r, &body) {
		return
	}
	sub, err := s.m.Command(r.PathValue("id"), body)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sub)
}

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	commands, err := s.m.Commands(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]agentapi.Command{"commands": commands})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := defaultFileLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxFileLimit {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", maxFileLimit))
			return
		}
		limit = n
	}
	files, err := s.m.Files(r.Context(), r.PathValue("id"), q.Get("q"), limit)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// handleUpload stores the request body as one attachment named by the name
// query parameter.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "attachments can be at most 10 MiB")
			return
		}
		writeError(w, http.StatusBadRequest, "could not read the upload")
		return
	}
	att, err := s.m.Upload(r.PathValue("id"), r.URL.Query().Get("name"), data)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

// handleAttachment serves a stored upload with the type UAM sniffed, never
// as HTML. Images are shown inline; other files download.
func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	att, data, modified, err := s.m.Attachment(r.PathValue("id"), r.PathValue("attachment_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	h := w.Header()
	contentType, disposition := att.MIME, "attachment"
	if att.MIME == mimeText {
		contentType = "text/plain; charset=utf-8"
	}
	if isImage(att.MIME) {
		disposition = "inline"
	}
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": att.Name}); value != "" {
		h.Set("Content-Disposition", value)
	} else {
		h.Set("Content-Disposition", disposition)
	}
	http.ServeContent(w, r, "", modified, bytes.NewReader(data))
}

func (s *Server) handleQueueResume(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, s.m.ResumeQueue(r.PathValue("id")))
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, s.m.ClearQueue(r.PathValue("id")))
}

func (s *Server) handleQueueCancel(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, s.m.CancelQueued(r.PathValue("id"), r.PathValue("request_id")))
}

// writeNoContent answers 204, or err as a failure.
func writeNoContent(w http.ResponseWriter, err error) {
	if err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelSubagent(w http.ResponseWriter, r *http.Request) {
	subagent, err := s.m.CancelSubagent(r.PathValue("id"), r.PathValue("agent_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subagent)
}

func (s *Server) handlePromptSubagent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text      string `json:"text"`
		RequestID string `json:"request_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	sub, err := s.m.PromptSubagent(r.PathValue("id"), r.PathValue("agent_id"), body.Text, body.RequestID)
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

// handleStage serves a Task stage change, answering with the summary.
func (s *Server) handleStage(move func(*Manager, string) (SessionSummary, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		summary, err := move(s.m, r.PathValue("id"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
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
		if _, err := w.Write(frame); err != nil { // #nosec G705 -- text/event-stream contains JSON-escaped data and fixed event names, never HTML.
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
