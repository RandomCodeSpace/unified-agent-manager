package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	connectedViewKeyRoute  = "GET /api/connected/{connection}/files/{id}/{key}/{path...}"
	connectedGrantKeyRoute = "GET /api/connected/{connection}/grants/{id}/{grant_id}/{key}"
	// connectedArtifactKeyRoute is artifactKeyRoute for a connected
	// instance's Thread, under a key this side issued.
	connectedArtifactKeyRoute = "GET /api/connected/{connection}/artifact-files/{id}/{artifact}/{version}/{key}/{path...}"
	// headerArtifactFolder marks a connected instance's answer for a
	// version's folder named without its trailing slash.
	headerArtifactFolder = "X-UAM-Artifact-Folder"
	connectedPathPrefix  = "/api/connected/"
	sessionsPathPrefix   = "/api/sessions/"
	filesViewSegment     = "/files/view/"
)

// These are the only connected routes allowed through without the home owner
// cookie. Their handlers require a signed, generation-bound file credential.
func (s *Server) connectedFileKeyRequest(r *http.Request) bool {
	_, pattern := s.mux.Handler(r)
	return pattern == connectedViewKeyRoute || pattern == connectedGrantKeyRoute || pattern == connectedArtifactKeyRoute
}

// connectedArtifactGrant names one artifact version in a connected file
// scope, as a grant ID names one temporary file.
func connectedArtifactGrant(artifact, version string) string {
	return "artifact-" + artifact + "-" + version
}

// redirectConnectedArtifact is redirectConnectedView for a version of a
// connected Thread's artifact: a key this side issued, scoped to that version.
func (s *Server) redirectConnectedArtifact(w http.ResponseWriter, r, local *http.Request, target connectionTarget) {
	// /api/sessions/{id}/artifacts/{artifact}/v/{version}/{path...}, which the route matched.
	parts := strings.SplitN(strings.TrimPrefix(local.URL.Path, sessionsPathPrefix), "/", 6)
	if len(parts) != 6 || parts[1] != "artifacts" || parts[3] != "v" {
		writeError(w, http.StatusBadRequest, "invalid connected artifact path")
		return
	}
	task, artifact, version, filePath := parts[0], parts[2], parts[4], parts[5]
	n, err := strconv.Atoi(version)
	if !connectedID(task) || !artifactIDRE.MatchString(artifact) || err != nil || n < 1 || strconv.Itoa(n) != version || !connectedCleanPath("/"+filePath) {
		writeError(w, http.StatusBadRequest, "invalid connected artifact path")
		return
	}
	committed, err := s.connectedArtifactCommitted(r.Context(), target, task, artifact, n)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !committed {
		writeError(w, http.StatusNotFound, "artifact version not found")
		return
	}
	expires := time.Now().Add(fileKeyTTL).Unix()
	scope := connectedFileScope(target, task, connectedArtifactGrant(artifact, version))
	prefix := connectedPathPrefix + target.ID + "/artifact-files/" + task + "/" + artifact + "/" + version + "/"
	session := s.requestFileSession(r)
	if s.secureRequest(r) {
		setFileCookie(w, fileCookie(s.signKey, r.Host, session, scope, expires), prefix, int(fileKeyTTL/time.Second))
	}
	location := prefix + fileKey(s.signKey, r.Host, session, scope, expires) + "/" + viewPath(filePath)
	if r.URL.Query().Get("download") == "1" {
		location += "?download=1"
	}
	http.Redirect(w, r, location, http.StatusFound) // #nosec G710 -- fixed /api/connected/ prefix, validated route IDs and escaped file path; never another host.
}

// connectedArtifactCommitted reports whether the connected instance lists
// version of its Thread task's artifact, so a key is signed only for a
// snapshot it committed. It is false only when the instance answered that it
// has no such Thread or version; any other failure is an error.
func (s *Server) connectedArtifactCommitted(ctx context.Context, target connectionTarget, task, artifact string, version int) (bool, error) {
	transport, err := s.proxyTransport(target)
	if err != nil {
		return false, err
	}
	// The deadline covers the headers and the body.
	ctx, cancel := context.WithTimeout(ctx, fleetCheckTimeout)
	defer cancel()
	unavailable := newError(http.StatusBadGateway, "remote instance is unavailable")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.BaseURL+federationWorkloadPrefix+"api/sessions/"+task+"/artifacts", nil)
	if err != nil {
		return false, unavailable
	}
	req.Header.Set("Authorization", "Bearer "+target.Credential)
	req.Header.Set(headerUAMInstance, target.InstanceID)
	response, err := transport.RoundTrip(req)
	if err != nil {
		return false, unavailable
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	default:
		return false, unavailable
	}
	var got struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, &got) != nil {
		return false, unavailable
	}
	return slices.ContainsFunc(got.Artifacts, func(a Artifact) bool {
		return a.ID == artifact && slices.ContainsFunc(a.Versions, func(v ArtifactVersion) bool { return v.Version == version })
	}), nil
}

func (s *Server) handleConnectedArtifactKey(w http.ResponseWriter, r *http.Request) {
	artifact, version, filePath := r.PathValue("artifact"), r.PathValue("version"), r.PathValue("path")
	if n, ok := artifactVersionParam(r); !artifactIDRE.MatchString(artifact) || !ok || n == 0 || !connectedCleanPath("/"+filePath) {
		writeError(w, http.StatusBadRequest, "invalid connected artifact path")
		return
	}
	target, ok := s.connectedFileTarget(w, r, connectedArtifactGrant(artifact, version))
	if !ok {
		return
	}
	local := r.Clone(r.Context())
	base := sessionsPathPrefix + r.PathValue("id") + "/artifacts/" + artifact + "/v/" + version + "/"
	local.URL.Path = base + filePath
	local.URL.RawPath = base + viewPath(filePath)
	local.URL.RawQuery = connectedDownloadQuery(r)
	current, lifetime, err := s.connections.Acquire(target.ID)
	if err != nil || current.Generation != target.Generation {
		writeError(w, http.StatusConflict, "connection changed")
		return
	}
	s.proxyConnected(w, r, local, current, lifetime, artifactViewRoute)
}

// handleRemoteArtifactFile serves a version's file to the instance this one
// is connected to, after workload bearer authentication, as
// handleRemoteViewFile does; an empty path is the version's page.
func (s *Server) handleRemoteArtifactFile(w http.ResponseWriter, r *http.Request) {
	id, artifact := r.PathValue("id"), r.PathValue("artifact")
	version, ok := artifactVersionParam(r)
	if !ok || version == 0 {
		writeError(w, http.StatusNotFound, "artifact version not found")
		return
	}
	p := r.PathValue("path")
	if p == "" {
		v, err := s.m.artifactVersion(id, artifact, version)
		if err != nil {
			writeFailure(w, err)
			return
		}
		p = v.Entry
	}
	f, err := s.m.ArtifactFile(id, artifact, version, p)
	if errors.Is(err, errViewFolder) {
		w.Header().Set(headerArtifactFolder, "1")
	}
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer func() { _ = f.File.Close() }()
	connectedFileHeaders(w.Header(), r, f)
	w.Header().Set("Content-Security-Policy", artifactPolicy(f.MIME))
	w.Header().Set("ETag", `"`+strconv.FormatInt(f.Info.Size(), 16)+"-"+strconv.FormatInt(f.Info.ModTime().UnixNano(), 16)+`"`)
	http.ServeContent(w, r, "", f.Info.ModTime(), f.File)
}

func connectedFileScope(target connectionTarget, task, grant string) string {
	return "connected|" + target.ID + "|" + target.InstanceID + "|" + strconv.FormatUint(target.Generation, 10) + "|" + task + "|" + grant
}

func (s *Server) redirectConnectedView(w http.ResponseWriter, r, local *http.Request, target connectionTarget) {
	parts := strings.SplitN(strings.TrimPrefix(local.URL.Path, sessionsPathPrefix), filesViewSegment, 2)
	if len(parts) != 2 || !connectedID(parts[0]) || !connectedCleanPath("/"+parts[1]) {
		writeError(w, http.StatusBadRequest, "invalid connected file path")
		return
	}
	task, filePath := parts[0], parts[1]
	expires := time.Now().Add(fileKeyTTL).Unix()
	scope := connectedFileScope(target, task, "")
	prefix := connectedPathPrefix + target.ID + "/files/" + task + "/"
	session := s.requestFileSession(r)
	key := fileKey(s.signKey, r.Host, session, scope, expires)
	if s.secureRequest(r) {
		setFileCookie(w, fileCookie(s.signKey, r.Host, session, scope, expires), prefix, int(fileKeyTTL/time.Second))
	}
	location := prefix + key + "/" + viewPath(filePath)
	if r.URL.Query().Get("download") == "1" {
		location += "?download=1"
	}
	http.Redirect(w, r, location, http.StatusFound) // #nosec G710 -- fixed /api/connected/ prefix, validated route IDs and escaped file path; never another host.
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
	if !s.validFileKey(r.PathValue("key"), r.Host, scope) || (s.secureRequest(r) && !s.validFileCookie(r, scope)) {
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
	local.URL.Path = sessionsPathPrefix + r.PathValue("id") + filesViewSegment + filePath
	local.URL.RawPath = sessionsPathPrefix + r.PathValue("id") + filesViewSegment + viewPath(filePath)
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
	if json.Unmarshal(data, &grant) != nil || !connectedID(grant.ID) || grant.ExpiresAt.IsZero() {
		return errors.New("invalid remote file grant")
	}
	// The other instance enforces its own expiry. This side's key lasts at most
	// a grant's lifetime by this clock, so clock skew neither refuses the grant
	// nor stretches it.
	if limit := time.Now().Add(grantTTL); !grant.ExpiresAt.After(time.Now()) || grant.ExpiresAt.After(limit) {
		grant.ExpiresAt = limit
	}
	task := strings.TrimSuffix(strings.TrimPrefix(local.URL.Path, sessionsPathPrefix), "/file-grants")
	if !connectedID(task) {
		return errors.New("invalid remote file grant task")
	}
	scope := connectedFileScope(target, task, grant.ID)
	expires := grant.ExpiresAt.Unix()
	prefix := connectedPathPrefix + target.ID + "/grants/" + task + "/" + grant.ID + "/"
	session := s.requestFileSession(r)
	grant.URL = prefix + fileKey(s.signKey, r.Host, session, scope, expires)
	if s.secureRequest(r) {
		cookie := &http.Cookie{ // #nosec G124 -- SameSite=None is required for opaque sandbox requests; Secure and HttpOnly are always set, with a signed URL and one grant-scoped path.
			Name: fileCookieName, Value: fileCookie(s.signKey, r.Host, session, scope, expires), Path: prefix,
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
	local.URL.Path = sessionsPathPrefix + r.PathValue("id") + "/file-grants/" + r.PathValue("grant_id") + "/remote"
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
	h.Set("Content-Security-Policy", viewPolicy(f.MIME))
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("X-Content-Type-Options", "nosniff")
	disposition := "inline"
	if f.MIME == "application/octet-stream" || r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Info.Name()}))
}

// decodeEscapes decodes a value's valid escapes one by one, for recognizing
// a key route only: a bad escape elsewhere must not hide an encoded one.
func decodeEscapes(value string) string {
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
	return decoded.String()
}

// Home file capabilities also occur in Referer and arbitrary URL-valued
// headers. Recognize escaped paths and redact malformed connected URLs whole.
func redactConnectedKeyURL(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil {
		// Ordinary non-URL headers retain the existing logging behavior.
		if strings.Contains(decodeEscapes(value), connectedPathPrefix) {
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
		case "artifact-files":
			keyIndex += 2
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
