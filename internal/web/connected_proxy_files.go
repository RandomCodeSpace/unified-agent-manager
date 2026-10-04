package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	connectedViewKeyRoute  = "GET /api/connected/{connection}/files/{id}/{key}/{path...}"
	connectedGrantKeyRoute = "GET /api/connected/{connection}/grants/{id}/{grant_id}/{key}"
)

// These are the only connected routes allowed through without the home owner
// cookie. Their handlers require a signed, generation-bound file credential.
func (s *Server) connectedFileKeyRequest(r *http.Request) bool {
	_, pattern := s.mux.Handler(r)
	return pattern == connectedViewKeyRoute || pattern == connectedGrantKeyRoute
}

func connectedFileScope(target connectionTarget, task, grant string) string {
	return "connected|" + target.ID + "|" + target.InstanceID + "|" + strconv.FormatUint(target.Generation, 10) + "|" + task + "|" + grant
}

func (s *Server) redirectConnectedView(w http.ResponseWriter, r, local *http.Request, target connectionTarget) {
	parts := strings.SplitN(strings.TrimPrefix(local.URL.Path, "/api/sessions/"), "/files/view/", 2)
	if len(parts) != 2 || !connectedID(parts[0]) || !connectedCleanPath("/"+parts[1]) {
		writeError(w, http.StatusBadRequest, "invalid connected file path")
		return
	}
	task, filePath := parts[0], parts[1]
	expires := time.Now().Add(fileKeyTTL).Unix()
	scope := connectedFileScope(target, task, "")
	prefix := "/api/connected/" + target.ID + "/files/" + task + "/"
	key := fileKey(s.token, r.Host, scope, expires)
	if secureRequest(r) {
		setFileCookie(w, fileCookie(s.token, r.Host, scope, expires), prefix, int(fileKeyTTL/time.Second))
	}
	location := prefix + key + "/" + viewPath(filePath)
	if r.URL.Query().Get("download") == "1" {
		location += "?download=1"
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) connectedFileTarget(w http.ResponseWriter, r *http.Request, grant string) (connectionTarget, bool) {
	if s.connections == nil || !connectedID(r.PathValue("connection")) || !connectedID(r.PathValue("id")) || (grant != "" && !connectedID(grant)) {
		writeFailure(w, connectionError(http.StatusForbidden, "connection_file_forbidden", "invalid connected file authorization"))
		return connectionTarget{}, false
	}
	target, _, err := s.connections.Acquire(r.PathValue("connection"))
	if err != nil {
		writeFailure(w, err)
		return connectionTarget{}, false
	}
	scope := connectedFileScope(target, r.PathValue("id"), grant)
	if !s.validFileKey(r.PathValue("key"), r.Host, scope) || (secureRequest(r) && !s.validFileCookie(r, scope)) {
		writeFailure(w, connectionError(http.StatusForbidden, "connection_file_forbidden", "invalid or expired connected file authorization"))
		return connectionTarget{}, false
	}
	return target, true
}

func (s *Server) handleConnectedViewKey(w http.ResponseWriter, r *http.Request) {
	target, ok := s.connectedFileTarget(w, r, "")
	if !ok {
		return
	}
	filePath := r.PathValue("path")
	if !connectedCleanPath("/" + filePath) {
		writeError(w, http.StatusBadRequest, "invalid connected file path")
		return
	}
	local := r.Clone(r.Context())
	local.URL.Path = "/api/sessions/" + r.PathValue("id") + "/files/view/" + filePath
	local.URL.RawPath = "/api/sessions/" + r.PathValue("id") + "/files/view/" + viewPath(filePath)
	local.URL.RawQuery = connectedDownloadQuery(r)
	current, lifetime, err := s.connections.Acquire(target.ID)
	if err != nil || current.Generation != target.Generation {
		writeError(w, http.StatusConflict, "connection changed")
		return
	}
	s.proxyConnected(w, r, local, current, lifetime, "GET /api/sessions/{id}/files/view/{path...}")
}

func connectedDownloadQuery(r *http.Request) string {
	if r.URL.Query().Get("download") == "1" {
		return "download=1"
	}
	return ""
}

func (s *Server) rewriteConnectedGrant(response *http.Response, r, local *http.Request, target connectionTarget) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGrantBody+1))
	_ = response.Body.Close()
	if err != nil || len(data) > maxGrantBody {
		return errors.New("invalid remote file grant")
	}
	var grant fileGrant
	if json.Unmarshal(data, &grant) != nil || !connectedID(grant.ID) || !grant.ExpiresAt.After(time.Now()) || grant.ExpiresAt.After(time.Now().Add(grantTTL+time.Second)) {
		return errors.New("invalid remote file grant")
	}
	task := strings.TrimSuffix(strings.TrimPrefix(local.URL.Path, "/api/sessions/"), "/file-grants")
	if !connectedID(task) {
		return errors.New("invalid remote file grant task")
	}
	scope := connectedFileScope(target, task, grant.ID)
	expires := grant.ExpiresAt.Unix()
	prefix := "/api/connected/" + target.ID + "/grants/" + task + "/" + grant.ID + "/"
	grant.URL = prefix + fileKey(s.token, r.Host, scope, expires)
	if secureRequest(r) {
		cookie := &http.Cookie{Name: fileCookieName, Value: fileCookie(s.token, r.Host, scope, expires), Path: prefix,
			MaxAge: int(time.Until(grant.ExpiresAt).Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode}
		response.Header.Add("Set-Cookie", cookie.String())
	}
	data, err = json.Marshal(grant)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.Header.Set("Content-Length", strconv.Itoa(len(data)))
	return nil
}

func (s *Server) handleConnectedGrantKey(w http.ResponseWriter, r *http.Request) {
	target, ok := s.connectedFileTarget(w, r, r.PathValue("grant_id"))
	if !ok {
		return
	}
	local := r.Clone(r.Context())
	local.URL.Path = "/api/sessions/" + r.PathValue("id") + "/file-grants/" + r.PathValue("grant_id") + "/remote"
	local.URL.RawPath = ""
	local.URL.RawQuery = connectedDownloadQuery(r)
	current, lifetime, err := s.connections.Acquire(target.ID)
	if err != nil || current.Generation != target.Generation {
		writeError(w, http.StatusConflict, "connection changed")
		return
	}
	s.proxyConnected(w, r, local, current, lifetime, grantKeyRoute)
}

// The remote dispatcher calls this only after workload bearer authentication.
// Reuse the existing file resolver and serve the bytes without B's browser-only
// redirect/cookie handshake. A supplies its own scoped browser authorization.
func (s *Server) handleRemoteViewFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.m.ViewFile(r.PathValue("id"), r.PathValue("path"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer func() { _ = f.File.Close() }()
	connectedFileHeaders(w.Header(), r, f)
	w.Header().Set("ETag", `"`+strconv.FormatInt(f.Info.Size(), 16)+"-"+strconv.FormatInt(f.Info.ModTime().UnixNano(), 16)+`"`)
	http.ServeContent(w, r, "", f.Info.ModTime(), f.File)
}

// A target-side temporary grant retains its checked descriptor, expiry,
// cancellation, replacement detection and per-task limits.
func (s *Server) handleRemoteGrantFile(w http.ResponseWriter, r *http.Request) {
	f, ctx, release, err := s.grants.open(r.Context(), r.Host, r.PathValue("id"), r.PathValue("grant_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer release()
	connectedFileHeaders(w.Header(), r, f)
	reader := grantReader{ctx: ctx, reader: io.NewSectionReader(f.File, 0, f.Info.Size())}
	http.ServeContent(grantResponse{w}, r, f.Info.Name(), f.Info.ModTime(), reader)
}

func connectedFileHeaders(h http.Header, r *http.Request, f *ServedFile) {
	h.Set("Content-Type", f.MIME)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", viewSecurity)
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("X-Content-Type-Options", "nosniff")
	disposition := "inline"
	if f.MIME == "application/octet-stream" || r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Info.Name()}))
}

// Home file capabilities also occur in Referer and arbitrary URL-valued
// headers. Recognize escaped paths and redact malformed connected URLs whole.
func redactConnectedKeyURL(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil {
		// Decode valid escapes individually for recognition only: a bad
		// escape elsewhere must not hide an encoded connection prefix.
		// Ordinary non-URL headers retain the existing logging behavior.
		var decoded strings.Builder
		for i := 0; i < len(value); i++ {
			if value[i] == '%' && i+2 < len(value) {
				if part, err := url.PathUnescape(value[i : i+3]); err == nil {
					decoded.WriteString(part)
					i += 2
					continue
				}
			}
			decoded.WriteByte(value[i])
		}
		if strings.Contains(decoded.String(), "/api/connected/") {
			return redactedValue, true
		}
		return "", false
	}
	parts := strings.Split(u.Path, "/")
	for i := 0; i+5 < len(parts); i++ {
		if parts[i] != "api" || parts[i+1] != "connected" {
			continue
		}
		keyIndex := i + 5
		switch parts[i+3] {
		case "files":
		case "grants":
			keyIndex++
		default:
			continue
		}
		if keyIndex >= len(parts) {
			return redactedValue, true
		}
		parts[keyIndex] = redactedValue
		u.Path, u.RawPath = strings.Join(parts, "/"), ""
		return u.String(), true
	}
	return "", false
}
