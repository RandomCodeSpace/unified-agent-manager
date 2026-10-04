package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func pairFixture(t *testing.T, target *testServer) federationPairResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": testToken, "client_instance_id": uuid.NewString(), "label": "home"})
	w := target.do("POST", "/api/federation/pair", string(body))
	var pair federationPairResponse
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &pair) != nil || pair.GrantID == "" || pair.Credential == "" {
		t.Fatalf("pair failed: status %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("pairing changed browser-cookie or CORS contract")
	}
	return pair
}
func federationAuth(pair federationPairResponse) []reqOpt {
	return []reqOpt{withHeader("Authorization", "Bearer "+pair.Credential), withHeader(headerUAMInstance, pair.InstanceID)}
}

func TestFederationPairAndBoundary(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	body := `{"token":"` + testToken + `","client_instance_id":"` + uuid.NewString() + `","label":"home"}`
	for _, tc := range []struct {
		name, body string
		opts       []reqOpt
		want       int
	}{
		{"wrong key", strings.Replace(body, testToken, "bad", 1), nil, 401},
		{"foreign origin", body, []reqOpt{withHeader("Origin", "https://evil.example")}, 403},
		{"wrong identity", body, []reqOpt{withHeader(headerUAMInstance, uuid.NewString())}, 409},
		{"self", `{"token":"` + testToken + `","client_instance_id":"` + ts.srv.connections.InstanceID() + `","label":"self"}`, nil, 409},
	} {
		w := ts.do("POST", "/api/federation/pair", tc.body, tc.opts...)
		if w.Code != tc.want {
			t.Errorf("%s status=%d want=%d", tc.name, w.Code, tc.want)
		}
	}
	if len(ts.srv.connections.data.Grants) != 0 {
		t.Fatal("rejected pairing persisted a grant")
	}
	pair := pairFixture(t, ts)
	auth := federationAuth(pair)
	for _, path := range []string{"/api/connections", "/api/federation/grants"} {
		if w := ts.do("GET", path, "", auth...); w.Code != 401 {
			t.Errorf("grant reached owner route %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/connections", "/api/federation/grants", "/api/auth", "/api/login", "/api/connected/anything/api/sessions", "/api/federation/workload/api/sessions"} {
		if w := ts.do("GET", federationWorkloadPrefix+strings.TrimPrefix(path, "/"), "", auth...); w.Code != 403 {
			t.Errorf("grant escaped workload at %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/meta", "/api/sessions", "/api/settings", "/api/projects"} {
		w := ts.do("GET", federationWorkloadPrefix+strings.TrimPrefix(path, "/"), "", auth...)
		if w.Code != 200 || w.Header().Get(headerUAMInstance) != pair.InstanceID {
			t.Errorf("workload %s status=%d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), pair.Credential) || strings.Contains(w.Body.String(), testToken) {
			t.Fatal("workload response disclosed a credential")
		}
	}
	if w := ts.do("GET", federationWorkloadPrefix+"api/sessions", "", withHeader("Authorization", "Bearer "+testToken), withHeader(headerUAMInstance, pair.InstanceID)); w.Code != 401 {
		t.Fatal("master accepted as workload grant")
	}
	if w := ts.do("GET", "/api/federation/grants", "", withCookie(ts)); w.Code != 200 || strings.Contains(w.Body.String(), "verifier") || strings.Contains(w.Body.String(), pair.Credential) {
		t.Fatal("owner grant list is unavailable or contains a secret")
	}
}

func TestFederationExpectedIdentityBeforeTaskCreation(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	pair := pairFixture(t, ts)
	project := addProject(t, ts.m, t.TempDir())
	body := `{"provider":"fake","project_id":"` + project + `","name":"remote task"}`
	for _, identity := range []string{"", uuid.NewString()} {
		w := ts.do("POST", federationWorkloadPrefix+"api/sessions", body, withHeader("Authorization", "Bearer "+pair.Credential), withHeader(headerUAMInstance, identity))
		if w.Code != 409 || len(ts.m.List()) != 0 {
			t.Fatalf("identity mismatch changed tasks: status %d", w.Code)
		}
	}
	w := ts.do("POST", federationWorkloadPrefix+"api/sessions", body, federationAuth(pair)...)
	var sum SessionSummary
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &sum) != nil || sum.ID == "" || len(ts.m.List()) != 1 {
		t.Fatalf("scoped grant cannot create a task: %d", w.Code)
	}
	// Provider account controls are within workload authority, even when this fake
	// provider does not implement accounts. The owning handler decides that case.
	local := ts.do("POST", "/api/providers/fake/account/sign-out", "", withCookie(ts))
	remote := ts.do("POST", federationWorkloadPrefix+"api/providers/fake/account/sign-out", "", federationAuth(pair)...)
	if remote.Code != local.Code || remote.Code == 403 {
		t.Fatalf("provider account route is not scoped consistently: local=%d remote=%d", local.Code, remote.Code)
	}
}

func TestFederationRevokeStopsStreamWithoutStoppingTask(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	pair := pairFixture(t, ts)
	host := httptest.NewServer(ts.srv)
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", host.URL+federationWorkloadPrefix+"api/events?session="+sum.ID, nil)
	req.Header.Set("Authorization", "Bearer "+pair.Credential)
	req.Header.Set(headerUAMInstance, pair.InstanceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	finished := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(finished) }()
	w := ts.do("DELETE", "/api/federation/grants/"+pair.GrantID, "", withCookie(ts))
	if w.Code != 204 {
		t.Fatalf("revoke status %d", w.Code)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("revocation kept stream open")
	}
	if conv.Cancels() != 0 || conv.Closes() != 0 {
		t.Fatal("revocation stopped server-owned task")
	}
	if w := ts.do("GET", federationWorkloadPrefix+"api/sessions", "", federationAuth(pair)...); w.Code != 401 {
		t.Fatal("revoked grant reused")
	}
}

func TestConnectionManagementPairingAndRepair(t *testing.T) {
	remote := newTestServer(t, ServerConfig{})
	targetHTTP := httptest.NewTLSServer(remote.srv)
	defer targetHTTP.Close()
	home := newTestServer(t, ServerConfig{})
	defer home.srv.Close() // Stop notice readers before closing their TLS target.
	roots := x509.NewCertPool()
	roots.AddCert(targetHTTP.Certificate())
	home.srv.connectionTLS = &tls.Config{RootCAs: roots}
	createBody := func(token string, private bool) string {
		b, _ := json.Marshal(map[string]any{"label": "remote", "base_url": targetHTTP.URL, "token": token, "allow_private": private})
		return string(b)
	}
	if w := home.do("POST", "/api/connections", createBody(testToken, true)); w.Code != 401 {
		t.Fatal("registry creation did not require owner cookie")
	}
	if w := home.do("POST", "/api/connections", createBody(testToken, false), withCookie(home)); w.Code != 400 {
		t.Fatalf("private destination did not require approval: %d", w.Code)
	}
	if w := home.do("POST", "/api/connections", createBody(strings.Repeat("x", 64), true), withCookie(home)); w.Code != 424 {
		t.Fatalf("remote auth could sign out home: %d", w.Code)
	}
	w := home.do("POST", "/api/connections", createBody(testToken, true), withCookie(home))
	var c Connection
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatalf("create status %d: %s", w.Code, w.Body)
	}
	if c.Generation != 1 || !c.HasKey || c.InstanceID != remote.srv.connections.InstanceID() || strings.Contains(w.Body.String(), testToken) {
		t.Fatal("invalid or unredacted registration")
	}
	old, live, err := home.srv.connections.Acquire(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Credential == testToken || strings.Contains(w.Body.String(), old.Credential) {
		t.Fatal("master stored or grant exposed")
	}
	if w := home.do("POST", "/api/connections", createBody(testToken, true), withCookie(home)); w.Code != 409 {
		t.Fatal("duplicate target identity accepted")
	}
	repairBody, _ := json.Marshal(map[string]any{"token": testToken, "generation": 1})
	w = home.do("PATCH", "/api/connections/"+c.ID, string(repairBody), withCookie(home))
	if w.Code != 200 {
		t.Fatalf("repair status %d", w.Code)
	}
	next, _, _ := home.srv.connections.Acquire(c.ID)
	if next.Credential == old.Credential || next.Generation != 2 {
		t.Fatal("repair did not rotate grant and generation")
	}
	select {
	case <-live.Done():
	default:
		t.Fatal("repair kept old lifetime")
	}
	if _, _, err := remote.srv.connections.authenticate(old.Credential); err == nil {
		t.Fatal("repair left old target grant active")
	}
	if w := home.do("PATCH", "/api/connections/"+c.ID, `{"enabled":false,"generation":1}`, withCookie(home)); w.Code != 409 {
		t.Fatal("stale mutation accepted")
	}
	if w := home.do("PATCH", "/api/connections/"+c.ID, `{"enabled":false,"generation":2}`, withCookie(home)); w.Code != 200 {
		t.Fatal("disable failed")
	}
	if _, _, err := home.srv.connections.Acquire(c.ID); err == nil {
		t.Fatal("disabled connection is usable")
	}
	if w := home.do("DELETE", "/api/connections/"+c.ID, "", withCookie(home)); w.Code != 204 || len(home.srv.connections.List()) != 0 {
		t.Fatal("remove did not forget connection")
	}
	if _, _, err := remote.srv.connections.authenticate(next.Credential); err == nil {
		t.Fatal("remove left target grant active")
	}
}

func TestFederationTerminalGrantRevocation(t *testing.T) {
	target, host, project := newTerminalServer(t, ServerConfig{})
	pair := pairFixture(t, target)
	endpoint := "ws" + strings.TrimPrefix(host.URL, "http") + federationWorkloadPrefix + "api/projects/" + project + "/terminal"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+pair.Credential)
	headers.Set(headerUAMInstance, uuid.NewString())
	if conn, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: headers}); err == nil {
		_ = conn.CloseNow()
		t.Fatal("mismatched owner opened terminal")
	} else if response == nil || response.StatusCode != 409 {
		t.Fatalf("mismatched identity response: %v", err)
	}
	target.m.mu.Lock()
	count := len(target.m.terminals)
	target.m.mu.Unlock()
	if count != 0 {
		t.Fatal("failed grant check opened a shell")
	}
	headers.Set(headerUAMInstance, pair.InstanceID)
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	pid := shellPID(t, conn)
	if err := target.srv.connections.revoke(pair.GrantID); err != nil {
		t.Fatal(err)
	}
	// The grant boundary closes the hijacked connection, including an idle terminal.
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatal("revocation did not close terminal promptly")
	}
	waitGone(t, pid)
}
