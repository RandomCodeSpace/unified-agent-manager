package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const federationWorkloadPrefix = "/api/federation/workload/"

var federationCoreCapabilities = []string{"workload-grants-v1", "expected-instance-v1", "local-workload-v1", "events-v1"}

type federationDescriptor struct {
	InstanceID    string   `json:"instance_id"`
	ProtocolMajor int      `json:"protocol_major"`
	Version       string   `json:"version"`
	Capabilities  []string `json:"capabilities"`
}

type federationPairResponse struct {
	federationDescriptor
	GrantID    string `json:"grant_id"`
	Credential string `json:"credential"`
}

func (s *Server) federationDescriptor() federationDescriptor {
	capabilities := append(slices.Clone(federationCoreCapabilities), "files-v1", "terminal-v1", "configuration-v1", "provider-accounts-v1", "planner-v1", "routines-v1", "usage-v1")
	if s.notices != nil {
		capabilities = append(capabilities, "notices-v1")
	}
	return federationDescriptor{InstanceID: s.connections.InstanceID(), ProtocolMajor: 1, Version: s.version, Capabilities: capabilities}
}

func (s *Server) connectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/connections", s.handleConnections)
	mux.HandleFunc("POST /api/connections", s.handleAddConnection)
	mux.HandleFunc("PATCH /api/connections/{connection}", s.handleUpdateConnection)
	mux.HandleFunc("DELETE /api/connections/{connection}", s.handleRemoveConnection)
	mux.HandleFunc("POST /api/federation/pair", s.handleFederationPair)
	mux.HandleFunc("GET /api/federation/grants", s.handleWorkloadGrants)
	mux.HandleFunc("DELETE /api/federation/grants/{grant}", s.handleRevokeWorkloadGrant)
	// Self-revocation is separately authorized by the scoped bearer boundary.
	mux.HandleFunc("DELETE /api/federation/grants/self", s.handleRevokeSelf)
	s.federationMux = http.NewServeMux()
	s.federationMux.HandleFunc("GET /api/sessions/{id}/files/view/{path...}", s.handleRemoteViewFile)
	s.federationMux.HandleFunc(grantKeyRoute, s.handleRemoteGrantFile)
	s.federationMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) })
}

func cleanConnectionLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" || utf8.RuneCountInString(label) > 80 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return "", connectionError(400, "invalid_connection", "instance label must contain 1 to 80 printable characters")
	}
	return label, nil
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		InstanceID  string       `json:"instance_id"`
		Connections []Connection `json:"connections"`
	}{s.connections.InstanceID(), s.connections.List()})
}

func (s *Server) handleAddConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label        string `json:"label"`
		BaseURL      string `json:"base_url"`
		Token        string `json:"token"`
		AllowPrivate bool   `json:"allow_private"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	label, err := cleanConnectionLabel(body.Label)
	if err != nil {
		writeFailure(w, err)
		return
	}
	base, err := normalizeConnectionURL(body.BaseURL)
	if err != nil {
		writeFailure(w, err)
		return
	}
	target := connectionTarget{Connection: Connection{ID: uuid.NewString(), Label: label, BaseURL: base, Enabled: true, AllowPrivate: body.AllowPrivate}}
	target, err = s.pairConnection(r.Context(), target, body.Token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	out, err := s.connections.put(target, 0)
	if err != nil {
		s.revokeRemote(target)
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label        *string `json:"label"`
		BaseURL      *string `json:"base_url"`
		Token        *string `json:"token"`
		Enabled      *bool   `json:"enabled"`
		AllowPrivate *bool   `json:"allow_private"`
		Generation   *uint64 `json:"generation"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	old, err := s.connections.lookup(r.PathValue("connection"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	if body.Generation != nil && *body.Generation != old.Generation {
		writeFailure(w, connectionError(409, "connection_changed", "connection changed; reload before saving"))
		return
	}
	next := cloneTarget(old)
	if body.Label != nil {
		next.Label, err = cleanConnectionLabel(*body.Label)
		if err != nil {
			writeFailure(w, err)
			return
		}
	}
	if body.Enabled != nil {
		next.Enabled = *body.Enabled
	}
	if body.BaseURL != nil {
		next.BaseURL, err = normalizeConnectionURL(*body.BaseURL)
		if err != nil {
			writeFailure(w, err)
			return
		}
	}
	if body.AllowPrivate != nil {
		next.AllowPrivate = *body.AllowPrivate
	}
	repair := body.Token != nil || next.BaseURL != old.BaseURL || next.AllowPrivate != old.AllowPrivate
	if repair {
		if body.Token == nil {
			writeFailure(w, connectionError(400, "invalid_connection", "enter the target access key to change its URL or private-address approval"))
			return
		}
		next, err = s.pairConnection(r.Context(), next, *body.Token)
		if err != nil {
			writeFailure(w, err)
			return
		}
	}
	out, err := s.connections.put(next, old.Generation)
	if err != nil {
		if repair {
			s.revokeRemote(next)
		}
		writeFailure(w, err)
		return
	}
	if repair && old.Credential != next.Credential {
		s.revokeRemote(old)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRemoveConnection(w http.ResponseWriter, r *http.Request) {
	old, err := s.connections.lookup(r.PathValue("connection"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	if err := s.connections.remove(old.ID, old.Generation); err != nil {
		writeFailure(w, err)
		return
	}
	// Forgetting is durable even when the removed destination is unreachable.
	s.revokeRemote(old)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pairConnection(parent context.Context, target connectionTarget, master string) (connectionTarget, error) {
	master = strings.TrimSpace(master)
	if ValidateToken(master) != nil {
		return target, connectionError(400, "invalid_connection", "enter a valid target access key")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	allowed, err := connectionAddresses(ctx, target.BaseURL, target.AllowPrivate)
	if err != nil {
		return target, err
	}
	target.AllowedAddresses = allowed
	transport, err := s.connectionTransport(target)
	if err != nil {
		return target, err
	}
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	body, _ := json.Marshal(struct {
		Token            string `json:"token"`
		ClientInstanceID string `json:"client_instance_id"`
		Label            string `json:"label"`
	}{master, s.connections.InstanceID(), target.Label})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.BaseURL+"/api/federation/pair", bytes.NewReader(body))
	if err != nil {
		return target, connectionError(400, "invalid_connection", "invalid target URL")
	}
	req.Header.Set("Content-Type", "application/json")
	if target.InstanceID != "" {
		req.Header.Set(headerUAMInstance, target.InstanceID)
	}
	response, err := transport.RoundTrip(req)
	if err != nil {
		return target, connectionError(502, "remote_unavailable", "could not securely connect to the target instance")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return target, connectionError(424, "remote_auth_required", "the target refused the supplied access key")
	case http.StatusConflict:
		return target, connectionError(409, "identity_mismatch", "the target refused the expected instance identity")
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return target, connectionError(409, "unsupported_remote", "the target does not support connected instances")
	case http.StatusCreated:
	default:
		return target, connectionError(502, "remote_unavailable", "the target could not create a workload grant")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	var paired federationPairResponse
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &paired) != nil || !validInstanceID(paired.InstanceID) || !validInstanceID(paired.GrantID) || paired.ProtocolMajor != 1 || len(paired.Credential) > 512 || !strings.HasPrefix(paired.Credential, paired.GrantID+".") {
		return target, connectionError(409, "unsupported_remote", "the target returned an unsupported pairing response")
	}
	for _, capability := range federationCoreCapabilities {
		if !slices.Contains(paired.Capabilities, capability) {
			return target, connectionError(409, "unsupported_remote", "the target is missing a required workload capability")
		}
	}
	if target.InstanceID != "" && paired.InstanceID != target.InstanceID {
		return target, connectionError(409, "identity_mismatch", "the target instance identity changed")
	}
	target.InstanceID = paired.InstanceID
	target.Credential = paired.Credential
	target.Version = paired.Version
	target.ProtocolMajor = paired.ProtocolMajor
	target.Capabilities = slices.Clone(paired.Capabilities)
	return target, nil
}

func (s *Server) revokeRemote(target connectionTarget) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	transport, err := s.connectionTransport(target)
	if err != nil {
		return
	}
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.BaseURL+"/api/federation/grants/self", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+target.Credential)
	req.Header.Set(headerUAMInstance, target.InstanceID)
	response, err := transport.RoundTrip(req)
	if err == nil {
		response.Body.Close()
	}
}

func (s *Server) handleFederationPair(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBytes)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(loginReadWait))
	var body struct {
		Token            string `json:"token"`
		ClientInstanceID string `json:"client_instance_id"`
		Label            string `json:"label"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(credentialHash(strings.TrimSpace(body.Token))), []byte(credentialHash(s.token))) != 1 {
		writeFailure(w, connectionError(401, "remote_auth_required", "invalid access key"))
		return
	}
	if !validInstanceID(body.ClientInstanceID) {
		writeFailure(w, connectionError(400, "invalid_connection", "invalid client instance identity"))
		return
	}
	if body.ClientInstanceID == s.connections.InstanceID() {
		writeFailure(w, connectionError(409, "duplicate_instance", "cannot attach this instance to itself"))
		return
	}
	if expected := r.Header.Get(headerUAMInstance); expected != "" && expected != s.connections.InstanceID() {
		writeFailure(w, connectionError(409, "identity_mismatch", "target instance identity changed"))
		return
	}
	label, err := cleanConnectionLabel(body.Label)
	if err != nil {
		writeFailure(w, err)
		return
	}
	grant, credential, err := s.connections.issue(body.ClientInstanceID, label)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, federationPairResponse{s.federationDescriptor(), grant.ID, credential})
}

func (s *Server) handleWorkloadGrants(w http.ResponseWriter, r *http.Request) {
	type grantInfo struct {
		ID               string    `json:"id"`
		ClientInstanceID string    `json:"client_instance_id"`
		Label            string    `json:"label"`
		CreatedAt        time.Time `json:"created_at"`
	}
	out := []grantInfo{}
	s.connections.mu.Lock()
	for _, g := range s.connections.data.Grants {
		out = append(out, grantInfo{g.ID, g.ClientInstanceID, g.Label, g.CreatedAt})
	}
	s.connections.mu.Unlock()
	slices.SortFunc(out, func(a, b grantInfo) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, http.StatusOK, struct {
		Grants []grantInfo `json:"grants"`
	}{out})
}
func (s *Server) handleRevokeWorkloadGrant(w http.ResponseWriter, r *http.Request) {
	if err := s.connections.revoke(r.PathValue("grant")); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) handleRevokeSelf(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	grant, _, err := s.connections.authenticate(token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if err := s.connections.revoke(grant.ID); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveFederation handles only the explicit remote boundary. Ordinary cookie
// routes still use the standalone authentication and CSRF path unchanged.
func (s *Server) serveFederation(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/federation/pair" {
		s.mux.ServeHTTP(w, r)
		return true
	}
	workload := strings.HasPrefix(r.URL.Path, federationWorkloadPrefix)
	source := r.URL.Path == "/api/federation/notices" && r.Method == http.MethodGet
	revoke := r.URL.Path == "/api/federation/grants/self" && r.Method == http.MethodDelete
	if !workload && !source && !revoke {
		return false
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeFailure(w, connectionError(401, "remote_auth_required", "workload authentication required"))
		return true
	}
	_, life, err := s.connections.authenticate(token)
	if err != nil {
		writeFailure(w, err)
		return true
	}
	if r.Header.Get(headerUAMInstance) != s.connections.InstanceID() {
		writeFailure(w, connectionError(409, "identity_mismatch", "expected instance identity is required and must match"))
		return true
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(life, cancel)
	defer stop()
	if life.Err() != nil {
		writeFailure(w, connectionError(401, "remote_auth_required", "workload credential was revoked"))
		return true
	}
	request := r.Clone(ctx)
	writer := &workloadResponseWriter{ResponseWriter: w, ctx: ctx}
	w.Header().Set(headerUAMInstance, s.connections.InstanceID())
	if !workload {
		s.mux.ServeHTTP(writer, request)
		return true
	}
	localPath := "/" + strings.TrimPrefix(r.URL.Path, federationWorkloadPrefix)
	raw := strings.ToLower(r.URL.EscapedPath())
	if path.Clean(localPath) != strings.TrimSuffix(localPath, "/") || strings.ContainsAny(localPath, "\\\x00") || strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") {
		writeError(w, 400, "invalid workload path")
		return true
	}
	request.URL = &url.URL{Path: localPath, RawQuery: r.URL.RawQuery}
	request.RequestURI = request.URL.RequestURI()
	_, pattern := s.mux.Handler(request)
	if !connectedWorkloadPatternAllowed(pattern) {
		writeError(w, 403, "this route is not available to connected instances")
		return true
	}
	// The remote credential has already authenticated the WebSocket before a shell opens.
	// Origin headers are retained; the single-hop home proxy sends server requests without them.
	serveCompressed(writer, request, s.federationMux)
	return true
}

type workloadResponseWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w *workloadResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *workloadResponseWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *workloadResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		context.AfterFunc(w.ctx, func() { _ = conn.Close() })
	}
	return conn, rw, err
}
