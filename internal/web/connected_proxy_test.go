package web

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
)

type connectedProxyFixture struct {
	home     *testServer
	server   *httptest.Server
	client   *http.Client
	target   connectionTarget
	upstream *httptest.Server
}

func trustedConnectedClient(server *httptest.Server) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func newConnectedProxyFixture(t *testing.T, handler http.HandlerFunc) *connectedProxyFixture {
	t.Helper()
	f := &connectedProxyFixture{home: newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})}
	registry, err := openConnectionRegistry(context.Background(), t.TempDir(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.close)
	f.home.srv.connections = registry
	// Initial proxy tests also run before the separately owned Server wiring
	// is copied in. Avoid duplicate registrations when that wiring is present.
	probe := httptest.NewRequest("GET", "/api/connected/check/api/meta", nil)
	_, pattern := f.home.srv.mux.Handler(probe)
	if pattern != connectedProxyRoute {
		f.home.srv.connectedRoutes(f.home.srv.mux)
	}
	f.upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.target.Credential || r.Header.Get(headerUAMInstance) != f.target.InstanceID {
			http.Error(w, "wrong remote credentials", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(f.upstream.Close)
	pool := x509.NewCertPool()
	pool.AddCert(f.upstream.Certificate())
	f.home.srv.connectionTLS = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	f.target = connectionTarget{Connection: Connection{ID: mustUUID(t), InstanceID: mustUUID(t), BaseURL: f.upstream.URL, Enabled: true, AllowPrivate: true},
		Credential: "test-only-remote-credential", AllowedAddresses: []string{"127.0.0.1"}}
	registration, err := registry.put(f.target, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.target.Connection = registration
	f.server = httptest.NewTLSServer(f.home.srv)
	t.Cleanup(f.server.Close)
	f.client = trustedConnectedClient(f.server)
	t.Cleanup(f.client.CloseIdleConnections)
	return f
}

func (f *connectedProxyFixture) request(t *testing.T, method, localPath, body string, headers http.Header) *http.Response {
	t.Helper()
	u := f.server.URL + "/api/connected/" + f.target.ID + localPath
	r, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	query := r.URL.Query()
	if !query.Has("uam_generation") {
		query.Set("uam_generation", strconv.FormatUint(f.target.Generation, 10))
		r.URL.RawQuery = query.Encode()
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	if method != "GET" && method != "HEAD" && r.Header.Get("Content-Type") == "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(&http.Cookie{Name: cookieName, Value: validCookie(r.URL.Host)})
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func connectedBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return string(body)
}

func TestConnectedProxyRESTAndHeaderIsolation(t *testing.T) {
	var calls atomic.Int32
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/api/federation/workload/api/sessions" || r.URL.Query().Has("uam_generation") {
			t.Errorf("wrong owning route: %s %s", r.Method, r.URL.Path)
		}
		for _, name := range []string{"Cookie", "Origin", "Sec-Fetch-Site", "X-Forwarded-Host", "X-Api-Key", "Proxy-Authorization"} {
			if r.Header.Get(name) != "" {
				t.Errorf("leaked browser header %s", name)
			}
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"project_id":"project","request_id":"fixed"}` {
			t.Errorf("changed mutation body: %s", body)
		}
		w.Header().Set("Set-Cookie", "uam_web=attacker; Path=/")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Refresh", "0; url=https://evil.example")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Security-Policy", "default-src * 'unsafe-inline'")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"remote-task"}`)
	})
	r := f.request(t, "POST", "/api/sessions", `{"project_id":"project","request_id":"fixed"}`, http.Header{
		"X-Api-Key": {"browser-secret"}, "Proxy-Authorization": {"browser-secret"}, "X-Forwarded-Host": {"evil.example"}, "Origin": {f.server.URL},
	})
	if r.StatusCode != 201 || connectedBody(t, r) != `{"id":"remote-task"}` || calls.Load() != 1 {
		t.Fatalf("create status=%d calls=%d", r.StatusCode, calls.Load())
	}
	for _, name := range []string{"Set-Cookie", "Service-Worker-Allowed", "Refresh", "Access-Control-Allow-Origin"} {
		if r.Header.Get(name) != "" {
			t.Errorf("unsafe upstream response header %s", name)
		}
	}
	if r.Header.Get("Content-Security-Policy") != contentSecurity {
		t.Fatalf("changed home policy: %q", r.Header.Get("Content-Security-Policy"))
	}
	for _, target := range []string{"/api/connections", "/api/login", "/api/connected/other/api/sessions", "/api/federation/workload/api/sessions"} {
		response := f.request(t, "POST", target, `{}`, nil)
		if response.StatusCode != 404 {
			t.Errorf("forwarded forbidden route %s: %d", target, response.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("forbidden routes contacted upstream: %d", calls.Load())
	}
}

func TestConnectedProxyRejectsRedirectAndActiveJSON(t *testing.T) {
	for _, mode := range []string{"redirect", "html"} {
		t.Run(mode, func(t *testing.T) {
			f := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, "https://evil.example/secret", http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, "<script>steal()</script>")
			})
			r := f.request(t, "GET", "/api/sessions", "", nil)
			if r.StatusCode != 502 || r.Header.Get("Location") != "" || strings.Contains(connectedBody(t, r), "steal") {
				t.Fatalf("unsafe upstream response accepted: %d", r.StatusCode)
			}
		})
	}
}

func TestConnectedProxyRemoteUnauthorizedDoesNotSignOutHome(t *testing.T) {
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "upstream sign-in page")
	})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/sessions"}, {"GET", "/api/events"}, {"GET", "/api/sessions/task/attachments/image"},
		{"GET", "/api/sessions/task/export"}, {"POST", "/api/sessions/task/prompt"},
	} {
		r := f.request(t, tc.method, tc.path, `{}`, nil)
		if r.StatusCode != http.StatusFailedDependency || !strings.Contains(connectedBody(t, r), `"code":"remote_auth_required"`) {
			t.Errorf("%s %s auth status=%d", tc.method, tc.path, r.StatusCode)
		}
	}
	upload := f.request(t, "POST", "/api/sessions/task/attachments?name=test.png", "test-only-upload", http.Header{"Content-Type": {"application/octet-stream"}})
	if upload.StatusCode != http.StatusFailedDependency || !strings.Contains(connectedBody(t, upload), `"code":"remote_auth_required"`) {
		t.Fatalf("upload remote authentication status=%d", upload.StatusCode)
	}
	u := f.server.URL + "/api/connected/" + f.target.ID + "/api/sessions"
	r, err := f.client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("home authentication status=%d", r.StatusCode)
	}
}

func TestConnectedProxyFiltersInformationalHeadersAndTrailers(t *testing.T) {
	for _, finalType := range []string{"application/json", "text/html"} {
		t.Run(finalType, func(t *testing.T) {
			f := newConnectedProxyFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Set-Cookie", "upstream=secret")
				w.Header().Set("Link", "</foreign.js>; rel=preload")
				w.WriteHeader(http.StatusEarlyHints)
				w.Header().Del("Set-Cookie")
				w.Header().Del("Link")
				w.Header().Set("Content-Type", finalType)
				w.Header().Add("Trailer", "Set-Cookie")
				w.Header().Add("Trailer", "Content-Security-Policy")
				_, _ = io.WriteString(w, `{"ok":true}`)
				w.Header().Set("Set-Cookie", "trailer=secret")
				w.Header().Set("Content-Security-Policy", "default-src *")
			})
			r, err := http.NewRequest("GET", f.server.URL+"/api/connected/"+f.target.ID+"/api/sessions?uam_generation="+strconv.FormatUint(f.target.Generation, 10), nil)
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(&http.Cookie{Name: cookieName, Value: validCookie(r.URL.Host)})
			var informational atomic.Int32
			r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{Got1xxResponse: func(_ int, _ textproto.MIMEHeader) error {
				informational.Add(1)
				return nil
			}}))
			response, err := f.client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			_ = connectedBody(t, response)
			wantStatus := http.StatusOK
			if finalType == "text/html" {
				wantStatus = http.StatusBadGateway
			}
			if response.StatusCode != wantStatus || informational.Load() != 0 || len(response.Trailer) != 0 || response.Header.Get("Set-Cookie") != "" || response.Header.Get("Link") != "" || response.Header.Get("Content-Security-Policy") != contentSecurity {
				t.Fatalf("upstream metadata escaped filtering: status=%d informational=%d headers=%v trailers=%v", response.StatusCode, informational.Load(), response.Header, response.Trailer)
			}
		})
	}
}

func TestConnectedProxySSEStreamsAndCancelsGeneration(t *testing.T) {
	upstreamClosed := make(chan struct{})
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamClosed)
		if r.URL.Query().Has("page") || r.URL.Query().Get("session") != "task" {
			t.Errorf("upstream viewing query=%q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: snapshot\ndata: {\"seq\":7}\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	r := f.request(t, "GET", "/api/events?page=home-viewer&session=task", "", nil)
	if r.StatusCode != 200 {
		t.Fatalf("stream status=%d", r.StatusCode)
	}
	reader := bufio.NewReader(r.Body)
	if first, err := reader.ReadString('\n'); err != nil || first != "event: snapshot\n" {
		t.Fatalf("stream was buffered or changed: %q %v", first, err)
	}
	next := f.target
	next.Enabled = false
	if _, err := f.home.srv.connections.put(next, f.target.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-upstreamClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("disabled connection retained upstream stream")
	}
	if after := f.request(t, "GET", "/api/sessions", "", nil); after.StatusCode != http.StatusConflict {
		t.Fatalf("disabled connection accepted command: %d", after.StatusCode)
	}
}

func TestConnectedProxyWebSocketOriginAndGeneration(t *testing.T) {
	var accepted atomic.Int32
	closed := make(chan struct{})
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted.Add(1)
		defer func() { _ = conn.CloseNow(); close(closed) }()
		for {
			kind, message, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), kind, message); err != nil {
				return
			}
		}
	})
	u, _ := url.Parse(f.server.URL)
	headers := http.Header{"Cookie": {cookieName + "=" + validCookie(u.Host)}, "Origin": {"https://foreign.example"}}
	target := f.server.URL + "/api/connected/" + f.target.ID + "/api/projects/project/terminal?uam_generation=" + strconv.FormatUint(f.target.Generation, 10)
	conn, response, err := websocket.Dial(context.Background(), target, &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: headers})
	if conn != nil {
		_ = conn.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden || accepted.Load() != 0 {
		t.Fatalf("cross-site terminal reached target: response=%v error=%v accepted=%d", response, err, accepted.Load())
	}
	headers.Set("Origin", f.server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err = websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("terminal bytes")); err != nil {
		t.Fatal(err)
	}
	kind, body, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(body) != "terminal bytes" {
		t.Fatalf("terminal protocol changed: %v %q %v", kind, body, err)
	}
	next := f.target
	next.Enabled = false
	if _, err := f.home.srv.connections.put(next, f.target.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("disabled connection retained terminal socket")
	}
	if accepted.Load() != 1 {
		t.Fatalf("unexpected terminal count %d", accepted.Load())
	}
}

func TestConnectedProxyRejectsStaleGenerationBeforeForwarding(t *testing.T) {
	var calls atomic.Int32
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	for _, generation := range []string{"", "0", "2", "bad", "1&uam_generation=1"} {
		response := f.request(t, "POST", "/api/sessions?uam_generation="+generation, `{}`, nil)
		if response.StatusCode != http.StatusConflict || !strings.Contains(connectedBody(t, response), `"code":"connection_changed"`) {
			t.Errorf("generation %q status=%d", generation, response.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("stale or ambiguous authorization reached the target")
	}
}
