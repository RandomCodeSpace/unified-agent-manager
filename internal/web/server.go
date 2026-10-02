package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
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
	// frameDocument is the only response with another policy: the document
	// Mermaid renders in (ADR 0004, "Diagrams in a sandboxed frame"). The page
	// embeds it with sandbox="allow-scripts" and no allow-same-origin, so its
	// origin is opaque: it holds no cookie and, with connect-src 'none', can
	// reach no route. Its requests carry no cookie either, so a sign-in proxy in
	// front of the service would answer a separate script with a redirect to
	// its login page: the one script is inline, and framePolicy allows exactly
	// that script by its hash. Inline styles are allowed because Mermaid's SVG
	// needs them.
	frameDocument = "diagram-frame.html"
	// viewSecurity replaces the policy for a file of a Task's directory opened
	// in its own tab or the app's preview, such as an HTML report: its scripts run
	// and it loads what it likes, but without allow-same-origin its origin is
	// opaque, so it holds no cookie, cannot read this service's responses and
	// cannot reach the opener or navigate the top level.
	viewSecurity = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads; frame-ancestors 'self'"
	// fileKeyRoute is the view route under a file key (fileKey), the one
	// route that checks its own credential instead of the cookie.
	fileKeyRoute = "GET /api/sessions/{id}/files/key/{key}/{path...}"
	// indexDocument is the app's page, served for every client-side route.
	indexDocument = "index.html"
	// serviceWorker is the app's service worker; it must sit at the root to
	// control the whole app.
	serviceWorker = "sw.js"
	noCache       = "no-cache"
)

// Header names.
const (
	headerAcceptEncoding     = "Accept-Encoding"
	headerCacheControl       = "Cache-Control"
	headerContentDisposition = "Content-Disposition"
	headerContentEncoding    = "Content-Encoding"
	headerContentSecurity    = "Content-Security-Policy"
	headerContentType        = "Content-Type"
	headerContentTypeOptions = "X-Content-Type-Options"
	headerFrameOptions       = "X-Frame-Options"
)

// ServerConfig configures the HTTP interface.
type ServerConfig struct {
	Manager *Manager
	// Token is the required access token browsers present at /api/login.
	Token string
	// PublicOrigins are validated origins (scheme://host[:port]) of same-host
	// reverse proxies. Authentication is bound to the request Host.
	PublicOrigins []string
	Version       string
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
	headerLog bool
	csrf      *http.CrossOriginProtection
	assets    fs.FS
	version   string
	mux       *http.ServeMux
	heartbeat time.Duration
	grants    *tempGrants
	// terminalOrigins are the public origins as WebSocket origin patterns.
	terminalOrigins []string
	// frameSecurity and frameETag are framePolicy's for the embedded frame document.
	frameSecurity, frameETag string
}

// NewServer validates cfg and builds the handler.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Manager == nil || cfg.Token == "" {
		return nil, errors.New("web server needs a manager and an access token")
	}
	if err := ValidateToken(cfg.Token); err != nil {
		return nil, fmt.Errorf("web server access token: %w", err)
	}
	s := &Server{
		m: cfg.Manager, token: cfg.Token, headerLog: cfg.LogHeaders,
		csrf: http.NewCrossOriginProtection(), assets: cfg.Assets, version: cfg.Version, heartbeat: heartbeatInterval,
	}
	for _, origin := range cfg.PublicOrigins {
		normalized, err := NormalizePublicOrigin(origin)
		if err != nil {
			return nil, err
		}
		s.terminalOrigins = append(s.terminalOrigins, originPattern(normalized))
	}
	if s.assets == nil {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, fmt.Errorf("embedded web assets: %w", err)
		}
		if _, err := fs.Stat(sub, indexDocument); err != nil {
			return nil, errors.New("web assets are not built; run make build or make install, or install a release tag")
		}
		s.assets = sub
	}
	frame, _ := fs.ReadFile(s.assets, frameDocument)
	s.frameSecurity, s.frameETag = framePolicy(frame)
	s.grants = newTempGrants(s.m)
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
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("PATCH /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/settings/custom-models/discover", s.handleDiscoverModels)
	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("GET /api/providers/{provider}/account", s.handleAccount)
	mux.HandleFunc("POST /api/providers/{provider}/account/sign-in", s.handleSignIn)
	mux.HandleFunc("POST /api/providers/{provider}/account/sign-out", s.handleSignOut)
	mux.HandleFunc("GET /api/utility", s.handleUtility)
	mux.HandleFunc("GET /api/push", s.handlePushKey)
	mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("POST /api/push/unsubscribe", s.handlePushUnsubscribe)
	mux.HandleFunc("GET /api/fs/dirs", s.handleListDirs)
	mux.HandleFunc("POST /api/fs/dirs", s.handleMakeDir)
	mux.HandleFunc("GET /api/projects/{id}/files", s.handleFileList((*Manager).ProjectFiles))
	mux.HandleFunc("GET /api/projects/{id}/previous", s.handlePrevious)
	mux.HandleFunc("GET /api/projects/{id}/terminal", s.handleTerminal)
	mux.HandleFunc("GET /api/previous/counts", s.handlePreviousCounts)
	mux.HandleFunc("POST /api/projects/{id}/previous/{conversation_id}/import", s.handleImport)
	mux.HandleFunc("GET /api/sessions", s.handleList)
	mux.HandleFunc("POST /api/sessions", s.handleCreate)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleDetail)
	mux.HandleFunc("GET /api/sessions/{id}/history", s.handleHistoryPage)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handlePatch)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/sessions/{id}/subagents", s.handleSubagents)
	mux.HandleFunc("GET /api/sessions/{id}/subagents/{agent_id}", s.handleSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/subagents/{agent_id}/cancel", s.handleCancelSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/background-tasks/{task_id}/cancel", s.handleCancelBackgroundTask)
	mux.HandleFunc("POST /api/sessions/{id}/subagents/{agent_id}/prompt", s.handlePromptSubagent)
	mux.HandleFunc("POST /api/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/sessions/{id}/command", s.handleCommand)
	mux.HandleFunc("GET /api/sessions/{id}/commands", s.handleCommands)
	mux.HandleFunc("GET /api/sessions/{id}/files", s.handleFileList((*Manager).Files))
	mux.HandleFunc("POST /api/sessions/{id}/files/resolve", s.handleResolveFiles)
	mux.HandleFunc("GET /api/sessions/{id}/files/tree", s.handleTree)
	mux.HandleFunc("GET /api/sessions/{id}/files/raw", s.handleRawImage)
	mux.HandleFunc("GET /api/sessions/{id}/files/view/{path...}", s.handleViewFile)
	mux.HandleFunc(fileKeyRoute, s.handleViewFile)
	mux.HandleFunc("POST /api/sessions/{id}/file-grants", s.handleCreateGrant)
	mux.HandleFunc("DELETE /api/sessions/{id}/file-grants/{grant_id}", s.handleDeleteGrant)
	mux.HandleFunc(grantKeyRoute, s.handleGrantFile)
	mux.HandleFunc("POST /api/sessions/{id}/attachments", s.handleUpload)
	mux.HandleFunc("GET /api/sessions/{id}/attachments/{attachment_id}", s.handleAttachment)
	mux.HandleFunc("POST /api/sessions/{id}/queue/resume", s.handleQueueResume)
	mux.HandleFunc("POST /api/sessions/{id}/queue/clear", s.handleQueueClear)
	mux.HandleFunc("DELETE /api/sessions/{id}/queue/{request_id}", s.handleQueueCancel)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/sessions/{id}/close", s.handleClose)
	mux.HandleFunc("POST /api/sessions/{id}/settle", s.handleSettle)
	mux.HandleFunc("POST /api/sessions/{id}/reopen", s.handleStage((*Manager).Reopen))
	mux.HandleFunc("POST /api/sessions/{id}/archive", s.handleStage((*Manager).Archive))
	mux.HandleFunc("POST /api/sessions/{id}/interactions/{iid}", s.handleAnswer)
	mux.HandleFunc("GET /api/sessions/{id}/changes", s.handleChanges)
	mux.HandleFunc("GET /api/sessions/{id}/changes/file", s.handleFileChange)
	mux.HandleFunc("GET /api/sessions/{id}/git", s.handleGitState)
	mux.HandleFunc("POST /api/sessions/{id}/git/init", s.handleGitInit)
	mux.HandleFunc("POST /api/sessions/{id}/git/commit", s.handleGitCommit)
	mux.HandleFunc("POST /api/sessions/{id}/git/push", s.handleGitRemote((*Manager).GitPush))
	mux.HandleFunc("POST /api/sessions/{id}/git/pull", s.handleGitRemote((*Manager).GitPull))
	mux.HandleFunc("POST /api/sessions/{id}/git/message", s.handleCommitMessage)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/events/detail", s.handleDetailEvents)
	mux.HandleFunc("GET /api/sessions/{id}/items/{item_id}", s.handleItemBody)
	mux.HandleFunc("GET /api/sessions/{id}/subagents/{agent_id}/history", s.handleHistoryPage)
	s.boardRoutes(mux)
	s.chartRoutes(mux)
	s.routineRoutes(mux)
	s.assistRoutes(mux)
	s.mcpRoutes(mux)
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
	h.Set(headerContentSecurity, contentSecurity)
	h.Set(headerContentTypeOptions, "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set(headerFrameOptions, "DENY")
	api := isAPI(r.URL.Path)
	if api {
		h.Set(headerCacheControl, "no-store")
	}
	// Any Host can reach sign-in and static assets. Cookies and file keys
	// are Host-bound, so another Host cannot reuse their authentication.
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
			if !hasMediaType(r.Header.Get(headerContentType), "application/octet-stream") {
				s.refuse(w, r, http.StatusUnsupportedMediaType, "attachment uploads must use Content-Type: application/octet-stream")
				return
			}
			limit = maxUploadBytes
		case !bodyless && !jsonContentType(r.Header.Get(headerContentType)):
			s.refuse(w, r, http.StatusUnsupportedMediaType, "requests must use Content-Type: application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	if api && r.URL.Path != "/api/auth" && r.URL.Path != "/api/login" && !s.authenticated(r) && !s.fileKeyRequest(r) {
		s.refuse(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	s.logRequest(r, "allowed", 0)
	serveCompressed(w, r, s.mux)
}

// fileKeyRequest reports whether r is for the file key route, whose handler
// checks the key.
func (s *Server) fileKeyRequest(r *http.Request) bool {
	_, pattern := s.mux.Handler(r)
	return pattern == fileKeyRoute || pattern == grantKeyRoute
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
	args := []any{"method", r.Method, "path", capLogValue(redactKeyURL(r.URL.RequestURI())), "remote", r.RemoteAddr,
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
			return redactedValue
		}
	}
	return capLogValue(redactKeyURL(value))
}

func capLogValue(v string) string {
	if len(v) > maxLoggedValue {
		return v[:maxLoggedValue] + "…"
	}
	return v
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
	w.Header().Set(headerContentType, "application/json; charset=utf-8")
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
	if !errors.As(err, &webErr) {
		writeError(w, status, msg)
		return
	}
	body := map[string]any{"error": msg}
	if webErr.ProjectID != "" {
		body["project_id"] = webErr.ProjectID
	}
	if webErr.Code != "" {
		body["code"] = webErr.Code
	}
	if len(webErr.Refs) > 0 {
		body["refs"] = webErr.Refs
	}
	if webErr.Cards != nil {
		body["cards"] = webErr.Cards
	}
	writeJSON(w, status, body)
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
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.authenticated(r), "required": true})
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
	meta := Meta{Version: s.version, Providers: s.m.Providers(), RecentWorkdirs: s.m.RecentWorkdirs()}
	if roots, err := s.grants.rootsForUse(); err == nil {
		meta.TempRoot, meta.TempRootAliases = roots.canonical, roots.aliases
	}
	writeJSON(w, http.StatusOK, meta)
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

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name *string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == nil {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	p, err := s.m.UpdateProject(r.PathValue("id"), *body.Name)
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

func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.m.Settings())
}

// handleUpdateSettings refuses a key it does not know, so a misspelt
// setting is an error rather than a silent no-op.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !decodeBody(w, r, &body) {
		return
	}
	var patch SettingsPatch
	for key, raw := range body {
		switch key {
		case "send_default":
			patch.SendDefault = new(string)
			if json.Unmarshal(raw, patch.SendDefault) != nil {
				writeError(w, http.StatusBadRequest, "send_default must be a string")
				return
			}
		case "terminal":
			patch.Terminal = new(bool)
			if json.Unmarshal(raw, patch.Terminal) != nil || string(raw) == "null" {
				writeError(w, http.StatusBadRequest, "terminal must be true or false")
				return
			}
		case "planner":
			patch.Planner = new(bool)
			if json.Unmarshal(raw, patch.Planner) != nil || string(raw) == "null" {
				writeError(w, http.StatusBadRequest, "planner must be true or false")
				return
			}
		case "hidden_models":
			if patch.HiddenModels = hiddenModelsBody(raw); patch.HiddenModels == nil {
				writeError(w, http.StatusBadRequest, "hidden_models must map providers to lists of model IDs")
				return
			}
		case "title_model":
			if json.Unmarshal(raw, &patch.TitleModel) != nil || patch.TitleModel == nil {
				writeError(w, http.StatusBadRequest, "title_model must map providers to model IDs")
				return
			}
		case "custom_models":
			// key_present is output only; any other unknown entry key is ignored.
			var list []store.WebCustomModel
			if json.Unmarshal(raw, &list) != nil || list == nil {
				writeError(w, http.StatusBadRequest, "custom_models must be a list of custom models")
				return
			}
			patch.CustomModels = &list
		case "suggest_replies":
			patch.SuggestReplies = new(bool)
			if json.Unmarshal(raw, patch.SuggestReplies) != nil || string(raw) == "null" {
				writeError(w, http.StatusBadRequest, "suggest_replies must be true or false")
				return
			}
		case "task_defaults":
			patch.TaskDefaults = new(TaskDefaults)
			if json.Unmarshal(raw, patch.TaskDefaults) != nil || string(raw) == "null" {
				writeError(w, http.StatusBadRequest, "task_defaults must be an object with provider, model, effort, context_size and mode")
				return
			}
		case "utility_daily_limit":
			// null puts the default back.
			patch.UtilityLimit = new(*int)
			if json.Unmarshal(raw, patch.UtilityLimit) != nil {
				writeError(w, http.StatusBadRequest, "utility_daily_limit must be a whole number or null")
				return
			}
		case "compact_threshold":
			// null puts the default back.
			patch.CompactionThreshold = new(*int)
			if json.Unmarshal(raw, patch.CompactionThreshold) != nil {
				writeError(w, http.StatusBadRequest, "compact_threshold must be a whole number or null")
				return
			}
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown setting %q", key))
			return
		}
	}
	settings, err := s.m.UpdateSettings(patch)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleDiscoverModels(w http.ResponseWriter, r *http.Request) {
	var req DiscoverRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := s.m.DiscoverModels(r.Context(), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// hiddenModelsBody decodes a hidden_models object, or returns nil when raw is
// not an object of string lists (null included).
func hiddenModelsBody(raw json.RawMessage) map[string][]string {
	var byProvider map[string]json.RawMessage
	if json.Unmarshal(raw, &byProvider) != nil || byProvider == nil {
		return nil
	}
	out := make(map[string][]string, len(byProvider))
	for provider, list := range byProvider {
		var ids []string
		if json.Unmarshal(list, &ids) != nil || ids == nil {
			return nil
		}
		out[provider] = ids
	}
	return out
}

func (s *Server) handleUsage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.m.AccountUsage())
}

func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := listDirs(q.Get("path"), q.Get("hidden") == "1", maxDirEntries)
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

func (s *Server) handlePrevious(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.Previous(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePreviousCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := s.m.PreviousCounts(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

// handleImport imports a previous conversation as a Task. The body, if any,
// is ignored.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	summary, err := s.m.Import(r.Context(), r.PathValue("id"), r.PathValue("conversation_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, summary)
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
	if r.URL.Query().Get("view") == compactRepresentation {
		d, err := s.m.CompactDetail(id)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
		return
	}
	detail, err := s.m.Detail(id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if r.URL.Query().Get("history") == "recent" {
		detail = recentDetail(detail)
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
	if r.URL.Query().Get("view") == compactRepresentation {
		d, err := s.m.CompactSubagent(id, r.PathValue("agent_id"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
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

// handleFileList serves the @ picker's listing of a Task's or a Project's
// directory; list is Files or ProjectFiles.
func (s *Server) handleFileList(list func(*Manager, context.Context, string, string, int) (FileList, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		files, err := list(s.m, r.Context(), r.PathValue("id"), q.Get("q"), limit)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, files)
	}
}

// handleTree lists one folder of the Task's directory, named by the dir
// query parameter relative to it, for the Files panel.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.Tree(r.Context(), r.PathValue("id"), r.URL.Query().Get("dir"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRawImage serves an image file of the Task's directory, named by the
// path query parameter, for the images a reply's markdown refers to by path.
// The type is what the bytes sniffed as, never HTML. Unlike the other API
// routes it may be revalidated: the browser sends If-Modified-Since or
// If-None-Match and gets a 304 while the file is unchanged.
func (s *Server) handleRawImage(w http.ResponseWriter, r *http.Request) {
	img, err := s.m.RawImage(r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer func() { _ = img.File.Close() }()
	h := w.Header()
	h.Set(headerContentType, img.MIME)
	h.Set(headerContentTypeOptions, "nosniff")
	h.Set(headerCacheControl, "private, no-cache")
	h.Set("ETag", fmt.Sprintf(`"%x-%x"`, img.Info.Size(), img.Info.ModTime().UnixNano()))
	if value := mime.FormatMediaType("inline", map[string]string{"filename": img.Info.Name()}); value != "" {
		h.Set(headerContentDisposition, value)
	} else {
		h.Set(headerContentDisposition, "inline")
	}
	http.ServeContent(w, r, "", img.Info.ModTime(), img.File)
}

// handleViewFile serves any file of the Task's directory, named by the path
// after view/ so that a page's relative links resolve to its siblings. Every
// response is sandboxed (viewSecurity); browser support for embedding a type
// varies, and a type the route does not display is a download. Files change,
// so each load revalidates. The cookie-checked view
// route redirects to the same path under a file key, which the sandboxed
// page's own requests then carry.
func (s *Server) handleViewFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if key := r.PathValue("key"); key != "" {
		if !s.validFileKey(key, r.Host, id) || (secureRequest(r) && !s.validFileCookie(r, id)) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
	} else {
		exp := time.Now().Add(fileKeyTTL).Unix()
		key := fileKey(s.token, r.Host, id, exp)
		prefix := "/api/sessions/" + url.PathEscape(id) + "/files/key/"
		target := prefix + key + "/" + viewPath(r.PathValue("path"))
		if secureRequest(r) {
			setFileCookie(w, fileCookie(s.token, r.Host, id, exp), prefix, int(fileKeyTTL/time.Second))
		}
		if r.URL.Query().Get("download") == "1" {
			target += "?download=1"
		}
		http.Redirect(w, r, target, http.StatusFound) // #nosec G710 -- a path under /api/sessions/ built from the route's escaped values; never another host.
		return
	}
	f, err := s.m.ViewFile(id, r.PathValue("path"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer func() { _ = f.File.Close() }()
	h := w.Header()
	h.Set(headerContentType, f.MIME)
	h.Set(headerCacheControl, "private, no-cache")
	h.Set("ETag", fmt.Sprintf(`"%x-%x"`, f.Info.Size(), f.Info.ModTime().UnixNano()))
	h.Set(headerContentSecurity, viewSecurity)
	h.Set(headerFrameOptions, "SAMEORIGIN")
	disposition := "inline"
	if f.MIME == "application/octet-stream" || r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": f.Info.Name()}); value != "" {
		h.Set(headerContentDisposition, value)
	} else {
		h.Set(headerContentDisposition, disposition)
	}
	http.ServeContent(w, r, "", f.Info.ModTime(), f.File)
}

// viewPath escapes a view route's file path segment by segment, keeping the
// separators so a page's relative links resolve to its siblings. A dot
// segment is escaped as well, so it stays part of the path the route passes on.
func viewPath(p string) string {
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		if segment == "." || segment == ".." {
			segments[i] = strings.ReplaceAll(segment, ".", "%2E")
		} else {
			segments[i] = url.PathEscape(segment)
		}
	}
	return strings.Join(segments, "/")
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
	var model *string
	if r.URL.Query().Has("model") {
		value := r.URL.Query().Get("model")
		model = &value
	}
	att, err := s.m.upload(r.PathValue("id"), r.URL.Query().Get("name"), data, model)
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
	if isImage(att.MIME) && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	h.Set(headerContentType, contentType)
	h.Set(headerContentTypeOptions, "nosniff")
	// A tool image may have no name.
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": att.Name}); value != "" && att.Name != "" {
		h.Set(headerContentDisposition, value)
	} else {
		h.Set(headerContentDisposition, disposition)
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
	sub, snapshot, err := s.m.subscribeView(id, r.URL.Query().Get("tool_output") == "delta", r.URL.Query().Get("history") == "recent", r.URL.Query().Get("view") == compactRepresentation)
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer s.m.Unsubscribe(sub)
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set(headerContentType, "text/event-stream")
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

// framePolicy returns the frame document's policy, which allows the document's
// one inline script by its SHA-256 (no script without one), and a weak
// validator for the document; weak, since it may be sent gzipped.
func framePolicy(doc []byte) (policy, etag string) {
	script := "'none'"
	if _, rest, ok := bytes.Cut(doc, []byte("<script>")); ok {
		if code, _, ok := bytes.Cut(rest, []byte("</script>")); ok {
			sum := sha256.Sum256(code)
			script = "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		}
	}
	sum := sha256.Sum256(doc)
	return "default-src 'none'; script-src " + script + "; style-src 'self' 'unsafe-inline'; img-src data:; font-src 'self'; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'",
		fmt.Sprintf(`W/"%x"`, sum[:12])
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
		name = indexDocument
	}
	if s.serveAsset(w, r, name) {
		return
	}
	if path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	if !s.serveAsset(w, r, indexDocument) {
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
	switch name {
	case indexDocument:
		w.Header().Set(headerCacheControl, noCache)
	case serviceWorker:
		// The service worker (push and notification clicks only, no caching):
		// revalidated, so a new version reaches every browser.
		w.Header().Set(headerCacheControl, noCache)
	case "manifest.webmanifest":
		// The web app manifest: its type is not in Go's built-in table, and it is
		// revalidated so a changed name or icon reaches installed apps.
		w.Header().Set(headerContentType, "application/manifest+json")
		w.Header().Set(headerCacheControl, noCache)
	case frameDocument:
		// The document carries the whole renderer: revalidated, it costs a 304.
		h := w.Header()
		h.Set(headerContentSecurity, s.frameSecurity)
		h.Set(headerFrameOptions, "SAMEORIGIN")
		h.Set(headerCacheControl, noCache)
		h.Set("ETag", s.frameETag)
	default:
		switch ext := path.Ext(name); {
		case strings.HasPrefix(name, "assets/"):
			// Vite names every file under assets/ by its content hash.
			w.Header().Set(headerCacheControl, "public, max-age=31536000, immutable")
		case path.Dir(name) == "." && (ext == ".png" || ext == ".svg"):
			// App icons keep their names: cached for a day, not for good.
			w.Header().Set(headerCacheControl, "public, max-age=86400")
		}
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
	return true
}
