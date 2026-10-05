package web

import (
	"context"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const connectedProxyRoute = "/api/connected/{connection}/{path...}"

func (s *Server) connectedRoutes(mux *http.ServeMux) {
	mux.HandleFunc(connectedProxyRoute, s.handleConnectedProxy)
	mux.HandleFunc(connectedViewKeyRoute, s.handleConnectedViewKey)
	mux.HandleFunc(connectedGrantKeyRoute, s.handleConnectedGrantKey)
}

// connectedLocalRequest preserves escaping while dropping exactly the home
// connection prefix. Never clean an ambiguous path into an authorized route.
func connectedLocalRequest(r *http.Request) (*http.Request, bool) {
	id := r.PathValue("connection")
	if !connectedID(id) {
		return nil, false
	}
	prefix := "/api/connected/" + id + "/"
	escaped, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix)
	if !ok {
		return nil, false
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil || !strings.HasPrefix(decoded, "api/") || !connectedCleanPath("/"+decoded) {
		return nil, false
	}
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		return nil, false
	}
	local := r.Clone(r.Context())
	local.URL.Path, local.URL.RawPath = "/"+decoded, "/"+escaped
	local.RequestURI = local.URL.RequestURI()
	return local, true
}

func connectedCleanPath(p string) bool {
	if strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return p == path.Clean(p) || strings.TrimSuffix(p, "/") == path.Clean(p)
}

func connectedID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handleConnectedProxy(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	local, ok := connectedLocalRequest(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid connected workload path")
		return
	}
	_, pattern := s.mux.Handler(local)
	if !connectedWorkloadPatternAllowed(pattern) {
		writeError(w, http.StatusNotFound, "connected workload route not found")
		return
	}
	terminal := pattern == "GET /api/projects/{id}/terminal"
	if terminal && !s.connectedTerminalOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if r.Header.Get("Upgrade") != "" && (!terminal || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket")) {
		writeError(w, http.StatusBadRequest, "unsupported connection upgrade")
		return
	}
	if s.connections == nil {
		writeError(w, http.StatusServiceUnavailable, "connected instances are unavailable")
		return
	}
	target, lifetime, err := s.connections.Acquire(r.PathValue("connection"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	versions := r.URL.Query()["uam_generation"]
	var generation uint64
	if len(versions) == 1 {
		if parsed, err := strconv.ParseUint(versions[0], 10, 64); err == nil {
			generation = parsed
		}
	}
	if generation == 0 || generation != target.Generation {
		writeFailure(w, connectionError(http.StatusConflict, "connection_changed", "the connected instance changed; refresh its connection"))
		return
	}
	if connectedAccountGated(pattern) {
		if err := s.refuseFleetAccount(r.Context(), target); err != nil {
			writeFailure(w, err)
			return
		}
	}
	query := local.URL.Query()
	query.Del("uam_generation")
	local.URL.RawQuery = query.Encode()
	if pattern == "GET /api/sessions/{id}/files/view/{path...}" {
		s.redirectConnectedView(w, r, local, target)
		return
	}
	// Temporary grants are only readable through the home grant wrapper. The
	// remote key returned by B must not become a second browser auth channel.
	if pattern == grantKeyRoute {
		writeError(w, http.StatusNotFound, "use the connected file grant URL")
		return
	}
	s.proxyConnected(w, r, local, target, lifetime, pattern)
}

// ReverseProxy does not run websocket.Accept on A. Apply the same origin
// policy before contacting B, whose bearer-only handshake has no browser Origin.
func (s *Server) connectedTerminalOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if strings.EqualFold(r.Host, u.Host) {
		return true
	}
	for _, allowed := range s.terminalOrigins {
		if match, _ := path.Match(strings.ToLower(allowed), strings.ToLower(u.Scheme+"://"+u.Host)); match {
			return true
		}
	}
	return false
}

func (s *Server) proxyConnected(w http.ResponseWriter, r, local *http.Request, target connectionTarget, lifetime context.Context, pattern string) {
	transport, err := s.connectionTransport(target)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if idle, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer idle.CloseIdleConnections()
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(lifetime, cancel)
	defer func() { stop(); cancel() }()
	if lifetime.Err() != nil {
		writeError(w, http.StatusConflict, "connection changed")
		return
	}
	base, err := url.Parse(target.BaseURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "remote instance is unavailable")
		return
	}
	homeHeaders := w.Header().Clone()
	proxy := httputil.ReverseProxy{
		Transport: transport,
		ErrorLog:  log.New(io.Discard, "", 0),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(base)
			p.Out.URL.Path = "/api/federation/workload" + local.URL.Path
			p.Out.URL.RawPath = "/api/federation/workload" + local.URL.EscapedPath()
			p.Out.URL.RawQuery = local.URL.RawQuery
			if pattern == "GET /api/events" {
				query := p.Out.URL.Query()
				query.Del("page")
				p.Out.URL.RawQuery = query.Encode()
			}
			p.Out.Header = connectedRequestHeaders(p.Out.Header)
			p.Out.Header.Set("Authorization", "Bearer "+target.Credential)
			p.Out.Header.Set("X-UAM-Instance-ID", target.InstanceID)
			p.Out.Header.Set("Accept-Encoding", "identity")
		},
		ModifyResponse: func(response *http.Response) error {
			if response.StatusCode == http.StatusUnauthorized {
				// This response belongs to B. A's own 401 remains reserved for
				// the home login, including native uploads and blob consumers.
				_ = response.Body.Close()
				body := `{"error":"connected instance requires authorization","code":"remote_auth_required"}`
				response.StatusCode = http.StatusFailedDependency
				response.Header = http.Header{"Content-Type": {"application/json"}}
				response.Body = io.NopCloser(strings.NewReader(body))
				response.ContentLength = int64(len(body))
			}
			if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != http.StatusNotModified {
				return errors.New("remote redirect refused")
			}
			if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
				return errors.New("unexpected remote encoding")
			}
			if err := connectedResponseHeaders(response, pattern); err != nil {
				return err
			}
			if pattern == "POST /api/sessions/{id}/file-grants" && response.StatusCode == http.StatusCreated {
				if err := s.rewriteConnectedGrant(response, r, local, target); err != nil {
					return err
				}
			}
			// ReverseProxy copies trailers after ModifyResponse. Drop both
			// their announcement and the values populated while reading.
			response.Trailer = nil
			if response.StatusCode != http.StatusSwitchingProtocols {
				response.Body = &connectedResponseBody{ReadCloser: response.Body, response: response}
			}
			// ReverseProxy adds response headers. Replace the home defaults
			// with the selected route policy rather than emitting two CSPs,
			// whose intersection would prohibit the sandboxed file frame.
			for name := range response.Header {
				w.Header().Del(name)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// ReverseProxy clears headers after informational responses.
			// Preserve the home error policy if the final response fails.
			for name, values := range homeHeaders {
				w.Header()[name] = append([]string(nil), values...)
			}
			writeError(w, http.StatusBadGateway, "remote instance is unavailable")
		},
	}
	proxy.ServeHTTP(connectedResponseWriter{w}, r.WithContext(ctx))
}

// Informational responses bypass ReverseProxy.ModifyResponse. They carry no
// workload data and must not expose upstream cookies or security headers.
type connectedResponseWriter struct{ http.ResponseWriter }

func (w connectedResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w connectedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type connectedResponseBody struct {
	io.ReadCloser
	response *http.Response
}

func (b *connectedResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.response.Trailer = nil
	}
	return n, err
}

func (b *connectedResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.response.Trailer = nil
	return err
}

// Forward only protocol and workload headers. In particular, no browser
// cookie, Origin, forwarding assertion or alternate credential reaches B.
func connectedRequestHeaders(in http.Header) http.Header {
	out := make(http.Header)
	for _, name := range []string{
		"Accept", "Accept-Language", "Content-Type", "Range", "If-Range", "If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "Last-Event-ID",
		"Connection", "Upgrade", "Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions",
	} {
		if values := in.Values(name); len(values) != 0 {
			out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	return out
}

func connectedResponseHeaders(response *http.Response, pattern string) error {
	if response.StatusCode == http.StatusSwitchingProtocols {
		if pattern != "GET /api/projects/{id}/terminal" || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") {
			return errors.New("unexpected remote upgrade")
		}
	} else if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotModified {
		typeName, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil {
			return errors.New("invalid remote content type")
		}
		eventStream := (pattern == "GET /api/events" || pattern == "GET /api/events/detail") && response.StatusCode == http.StatusOK && typeName == "text/event-stream"
		if !connectedResourcePattern(pattern) && typeName != "application/json" && !eventStream {
			return errors.New("unexpected remote content type")
		}
	}
	headers := make(http.Header)
	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Content-Disposition", "Retry-After", "X-Accel-Buffering",
		"Connection", "Upgrade", "Sec-WebSocket-Accept", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions",
	} {
		if values := response.Header.Values(name); len(values) != 0 {
			headers[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	headers.Set("Cache-Control", "no-store")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("Content-Security-Policy", contentSecurity)
	headers.Set("X-Frame-Options", "DENY")
	if connectedResourcePattern(pattern) {
		headers.Set("Content-Security-Policy", viewSecurity)
		headers.Set("X-Frame-Options", "SAMEORIGIN")
	}
	response.Header = headers
	return nil
}

func connectedResourcePattern(pattern string) bool {
	switch pattern {
	case "GET /api/sessions/{id}/files/view/{path...}", "GET /api/sessions/{id}/files/raw", "GET /api/sessions/{id}/attachments/{attachment_id}", "GET /api/sessions/{id}/export", grantKeyRoute:
		return true
	default:
		return false
	}
}
