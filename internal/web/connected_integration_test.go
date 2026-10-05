package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// These fixtures use real owner login, pairing, bearer dispatch and TLS proxying.
// Only providers are fake. Seeded durable records deliberately collide across hosts.
type connectedTestNode struct {
	name, token string
	m           *Manager
	prov        *agenttest.Provider
	srv         *Server
	http        *httptest.Server
	client      *http.Client
	cookie      *http.Cookie
	mu          sync.Mutex
	workloads   int
}

type connectedTestFleet struct {
	nodes           []*connectedTestNode
	taskID, project string
}

func newConnectedTestFleet(t *testing.T) *connectedTestFleet {
	t.Helper()
	f := &connectedTestFleet{taskID: mustUUID(t), project: mustUUID(t)}
	t.Cleanup(func() {
		// Cancel every outbound reader before closing any source HTTP server.
		for _, n := range f.nodes {
			if n.srv != nil {
				n.srv.Close()
			}
		}
		for _, n := range f.nodes {
			if n.http != nil {
				n.http.Close()
				n.client.CloseIdleConnections()
			}
			_ = n.m.Shutdown(context.Background())
		}
	})
	pool := x509.NewCertPool()
	for i, name := range []string{"A", "B", "C"} {
		st, dir := openTestStore(t), t.TempDir()
		now := time.Now().UTC()
		if err := st.Update(func(cfg *store.Config) error {
			cfg.WebProjects = map[string]store.WebProject{f.project: {ID: f.project, Name: name + " project", Dir: dir, CreatedAt: now}}
			cfg.Sessions[store.Key("fake", f.taskID)] = store.SessionRecord{
				ID: f.taskID, Agent: "fake", Name: name + " task", Mode: store.ModeSafe, Workdir: dir,
				CreatedAt: now, LastSeenAt: now, Status: store.StatusActive, Surface: store.SurfaceWeb,
				ProviderSessionID: name + "-conversation", Web: &store.WebState{Turn: StateIdle, ProjectID: f.project, UpdatedAt: now},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		prov := agenttest.NewProvider("fake", allCaps)
		prov.AddConversation(name+"-conversation", nil)
		n := &connectedTestNode{name: name, token: fmt.Sprintf("%064x", i+1), prov: prov, m: NewManager(st, []agentapi.Provider{prov})}
		if err := n.m.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, n)
		var err error
		n.srv, err = NewServer(ServerConfig{Manager: n.m, Token: n.token, Version: "connected-integration"})
		if err != nil {
			t.Fatal(err)
		}
		n.http = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, federationWorkloadPrefix) {
				n.mu.Lock()
				n.workloads++
				n.mu.Unlock()
			}
			n.srv.ServeHTTP(w, r)
		}))
		pool.AddCert(n.http.Certificate())
		n.client = n.http.Client()
		n.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		n.client.Timeout = 15 * time.Second
	}
	for _, n := range f.nodes {
		n.srv.connectionTLS = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		r, _ := n.request(t, http.MethodPost, "/api/login", map[string]string{"token": n.token}, http.StatusNoContent)
		if len(r.Cookies()) != 1 {
			t.Fatal("owner login did not issue one session cookie")
		}
		n.cookie = r.Cookies()[0]
	}
	return f
}

func (n *connectedTestNode) request(t *testing.T, method, path string, body any, want int) (*http.Response, []byte) {
	t.Helper()
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, n.http.URL+path, input)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", n.http.URL)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", "application/json")
	}
	if n.cookie != nil {
		req.AddCookie(n.cookie)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s on %s = %d %s, want %d", method, path, n.name, resp.StatusCode, data, want)
	}
	return resp, data
}

func (n *connectedTestNode) connect(t *testing.T, target *connectedTestNode) Connection {
	t.Helper()
	_, data := n.request(t, http.MethodPost, "/api/connections", map[string]any{"label": target.name, "base_url": target.http.URL, "token": target.token, "allow_private": true}, http.StatusCreated)
	var conn Connection
	if err := json.Unmarshal(data, &conn); err != nil {
		t.Fatal(err)
	}
	if conn.InstanceID != target.srv.connections.InstanceID() || !conn.Enabled || conn.Generation == 0 {
		t.Fatalf("paired unexpected owner: %+v", conn)
	}
	return conn
}

func connectedTestPath(conn Connection, path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return fmt.Sprintf("/api/connected/%s%s%suam_generation=%d", conn.ID, path, separator, conn.Generation)
}

func (f *connectedTestFleet) counts() [3]int {
	var counts [3]int
	for i, n := range f.nodes {
		n.mu.Lock()
		counts[i] = n.workloads
		n.mu.Unlock()
	}
	return counts
}

func TestConnectedIntegrationCyclesAndCollidingOwners(t *testing.T) {
	f := newConnectedTestFleet(t)
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	ab, ba, bc, ca := a.connect(t, b), b.connect(t, a), b.connect(t, c), c.connect(t, a)
	for _, route := range []struct {
		home, owner *connectedTestNode
		conn        *Connection
	}{{a, a, nil}, {a, b, &ab}, {b, a, &ba}, {b, c, &bc}, {c, a, &ca}} {
		path := "/api/sessions"
		if route.conn != nil {
			path = connectedTestPath(*route.conn, path)
		}
		before := f.counts()
		_, data := route.home.request(t, http.MethodGet, path, nil, http.StatusOK)
		var sessions []SessionSummary
		if err := json.Unmarshal(data, &sessions); err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 || sessions[0].ID != f.taskID || sessions[0].Name != route.owner.name+" task" {
			t.Fatalf("wrong owner or transitive tasks: %s", data)
		}
		want := before
		if route.conn != nil {
			for i, n := range f.nodes {
				if n == route.owner {
					want[i]++
				}
			}
		}
		if got := f.counts(); got != want {
			t.Fatalf("request crossed unexpected owners: %v -> %v, want %v", before, got, want)
		}
	}
	_, data := a.request(t, http.MethodGet, connectedTestPath(ab, "/api/projects"), nil, http.StatusOK)
	var projects struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(data, &projects); err != nil || len(projects.Projects) != 1 || projects.Projects[0].ID != f.project || projects.Projects[0].Name != "B project" {
		t.Fatalf("wrong project owner: %s (%v)", data, err)
	}
	a.request(t, http.MethodPatch, connectedTestPath(ab, "/api/sessions/"+f.taskID), map[string]string{"name": "changed on B"}, http.StatusOK)
	for _, n := range f.nodes {
		want := n.name + " task"
		if n == b {
			want = "changed on B"
		}
		if got := detail(t, n.m, f.taskID).Name; got != want {
			t.Fatalf("write escaped its owner: %s task = %q", n.name, got)
		}
	}
	before := f.counts()
	for _, forbidden := range []string{"/api/connections", connectedTestPath(bc, "/api/sessions")} {
		a.request(t, http.MethodGet, connectedTestPath(ab, forbidden), nil, http.StatusNotFound)
	}
	if got := f.counts(); got != before {
		t.Fatalf("forbidden registry/recursive request reached a target: %v -> %v", before, got)
	}
	connectedTestSubmit(t, a, "/api/sessions/"+f.taskID+"/prompt")
	connectedTestSubmit(t, a, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/prompt"))
	connectedTestSubmit(t, b, connectedTestPath(bc, "/api/sessions/"+f.taskID+"/prompt"))
	a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/cancel"), nil, http.StatusAccepted)
	for _, n := range f.nodes {
		want := 0
		if n == b {
			want = 1
		}
		if got := n.prov.Last().Cancels(); got != want {
			t.Fatalf("cancel crossed owners sharing a task ID: %s cancels=%d, want %d", n.name, got, want)
		}
	}
}

func connectedTestSubmit(t *testing.T, home *connectedTestNode, path string) {
	t.Helper()
	home.request(t, http.MethodPost, path, PromptRequest{Text: "keep working", RequestID: mustUUID(t), Mode: ModeSend}, http.StatusAccepted)
}

type connectedTestStream struct {
	lines chan string
	done  chan struct{}
}

func (n *connectedTestNode) stream(t *testing.T, path string) *connectedTestStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.http.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(n.cookie)
	// Streaming lifetime is controlled by cancellation, not the JSON client's timeout.
	client := *n.client
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	s := &connectedTestStream{lines: make(chan string, 64), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer close(s.lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case s.lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	s.waitFor(t, "event: snapshot")
	return s
}

func (s *connectedTestStream) waitFor(t *testing.T, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				t.Fatalf("stream ended before %q", text)
			}
			if strings.Contains(line, text) {
				return
			}
		case <-timer.C:
			t.Fatalf("stream did not deliver %q", text)
		}
	}
}

func (n *connectedTestNode) terminal(t *testing.T, path string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := http.Header{"Cookie": {n.cookie.String()}, "Origin": {n.http.URL}}
	conn, _, err := websocket.Dial(ctx, "wss://"+strings.TrimPrefix(n.http.URL, "https://")+path, &websocket.DialOptions{HTTPClient: n.client, HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func TestConnectedIntegrationDisableIsolatesChannelsAndPreservesTasks(t *testing.T) {
	sh, err := execpath.Resolve("sh")
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("SHELL", sh)
	t.Setenv("HOME", t.TempDir())
	f := newConnectedTestFleet(t)
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	ab, ac := a.connect(t, b), a.connect(t, c)
	for _, target := range []struct {
		n    *connectedTestNode
		conn Connection
	}{{b, ab}, {c, ac}} {
		setTerminal(t, target.n.m, true)
		connectedTestSubmit(t, a, connectedTestPath(target.conn, "/api/sessions/"+f.taskID+"/prompt"))
	}
	bstream := a.stream(t, connectedTestPath(ab, "/api/events?session="+url.QueryEscape(f.taskID)))
	cstream := a.stream(t, connectedTestPath(ac, "/api/events?session="+url.QueryEscape(f.taskID)))
	bterm := a.terminal(t, connectedTestPath(ab, "/api/projects/"+f.project+"/terminal"))
	cterm := a.terminal(t, connectedTestPath(ac, "/api/projects/"+f.project+"/terminal"))
	pid := shellPID(t, bterm)
	a.request(t, http.MethodPatch, "/api/connections/"+ab.ID, map[string]any{"enabled": false, "generation": ab.Generation}, http.StatusOK)
	select {
	case <-bstream.done:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled B stream stayed open")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := bterm.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("disabled B terminal stayed open")
			}
			break
		}
	}
	waitGone(t, pid)
	// The output differs from the typed command, so it is told apart from the
	// echoed input even when the prompt and the output share a line.
	send(t, cterm, websocket.MessageBinary, "echo C-still-$((40+2))\n")
	readOutput(t, cterm, regexp.MustCompile(`C-still-42\r?\n`))
	c.prov.Last().EmitDelta("after-disable", agentapi.ItemAssistant, "C continues after B disable")
	cstream.waitFor(t, "C continues after B disable")
	for _, n := range []*connectedTestNode{b, c} {
		conv := n.prov.Last()
		conv.EmitDelta("completed-after-disable", agentapi.ItemAssistant, n.name+" finishes accepted work")
		conv.EmitTurn(agentapi.TurnCompleted, "")
		if conv.Cancels() != 0 || conv.Closes() != 0 || len(conv.Sends()) != 1 || detail(t, n.m, f.taskID).State != StateCompleted {
			t.Fatalf("disable changed %s accepted task: cancels=%d closes=%d sends=%d", n.name, conv.Cancels(), conv.Closes(), len(conv.Sends()))
		}
	}
	_, data := a.request(t, http.MethodPatch, "/api/connections/"+ab.ID, map[string]bool{"enabled": true}, http.StatusOK)
	var enabled Connection
	if err := json.Unmarshal(data, &enabled); err != nil {
		t.Fatal(err)
	}
	before := f.counts()
	_, data = a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/cancel"), nil, http.StatusConflict)
	if !bytes.Contains(data, []byte(`"connection_changed"`)) || f.counts() != before {
		t.Fatalf("stale generation reached a workload: %s", data)
	}
	a.request(t, http.MethodGet, connectedTestPath(enabled, "/api/sessions"), nil, http.StatusOK)
}

func TestConnectedIntegrationRemoteRevocationPreservesHomeAndOtherOwner(t *testing.T) {
	f := newConnectedTestFleet(t)
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	ab, ac := a.connect(t, b), a.connect(t, c)
	connectedTestSubmit(t, a, "/api/sessions/"+f.taskID+"/prompt")
	connectedTestSubmit(t, a, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/prompt"))
	connectedTestSubmit(t, a, connectedTestPath(ac, "/api/sessions/"+f.taskID+"/prompt"))
	_, data := b.request(t, http.MethodGet, "/api/federation/grants", nil, http.StatusOK)
	// Match the public grant identity field rather than reading credentials from A.
	var wire struct {
		Grants []struct {
			ID     string `json:"id"`
			Client string `json:"client_instance_id"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(data, &wire); err != nil || len(wire.Grants) != 1 || wire.Grants[0].Client != a.srv.connections.InstanceID() {
		t.Fatalf("unexpected B grants: %s (%v)", data, err)
	}
	b.request(t, http.MethodDelete, "/api/federation/grants/"+wire.Grants[0].ID, nil, http.StatusNoContent)
	for _, path := range []string{"/api/sessions", "/api/sessions/" + f.taskID} {
		resp, data := a.request(t, http.MethodGet, connectedTestPath(ab, path), nil, http.StatusFailedDependency)
		if !bytes.Contains(data, []byte(`"remote_auth_required"`)) || len(resp.Cookies()) != 0 {
			t.Fatalf("remote revoke changed home authentication: %s", data)
		}
	}
	_, data = a.request(t, http.MethodGet, "/api/auth", nil, http.StatusOK)
	if !bytes.Contains(data, []byte(`"authenticated":true`)) {
		t.Fatalf("home owner session lost after remote revoke: %s", data)
	}
	a.request(t, http.MethodGet, "/api/sessions", nil, http.StatusOK)
	a.request(t, http.MethodGet, connectedTestPath(ac, "/api/sessions"), nil, http.StatusOK)
	for _, n := range f.nodes {
		conv := n.prov.Last()
		conv.EmitTurn(agentapi.TurnCompleted, "")
		if conv.Cancels() != 0 || conv.Closes() != 0 || detail(t, n.m, f.taskID).State != StateCompleted {
			t.Fatalf("grant revoke reached %s workload", n.name)
		}
	}
	// Both surviving owner routes still accept writes with the original A cookie.
	connectedTestSubmit(t, a, "/api/sessions/"+f.taskID+"/prompt")
	connectedTestSubmit(t, a, connectedTestPath(ac, "/api/sessions/"+f.taskID+"/prompt"))
}

// Settings written through a connection change only that instance, keep its
// safeguards, and never carry a saved secret back through the proxy.
func TestConnectedIntegrationSettingsStayWithOwnerAndRedactSecrets(t *testing.T) {
	f := newConnectedTestFleet(t)
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	ab := a.connect(t, b)
	const secret = "fixture-connected-byom-key"
	// The Terminal safeguard holds on B: off there, a command change is refused.
	if _, refused := a.request(t, http.MethodPut, connectedTestPath(ab, "/api/configuration/agents/reviewer"), map[string]string{"content": "---\nname: reviewer\n---\n", "revision": ""}, http.StatusForbidden); !bytes.Contains(refused, []byte("Terminal on")) {
		t.Fatalf("refused for another reason: %s", refused)
	}
	before := f.counts()
	_, data := a.request(t, http.MethodPatch, connectedTestPath(ab, "/api/settings"), map[string]any{
		"terminal":      true,
		"custom_models": []map[string]string{{"name": "direct", "base_url": "https://llm.example/v1", "model_id": "m", "api_key": secret}},
	}, http.StatusOK)
	if got := f.counts(); got != [3]int{before[0], before[1] + 1, before[2]} {
		t.Fatalf("settings write crossed owners: %v -> %v", before, got)
	}
	_, read := a.request(t, http.MethodGet, connectedTestPath(ab, "/api/settings"), nil, http.StatusOK)
	for _, body := range [][]byte{data, read} {
		if bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte(`"api_key":`)) || !bytes.Contains(body, []byte(`"key_present":true`)) {
			t.Fatalf("connected settings exposed or lost the key: %s", body)
		}
	}
	if !b.m.Settings().Terminal || len(b.m.Settings().CustomModels) != 1 {
		t.Fatalf("B did not keep its settings: %+v", b.m.Settings())
	}
	for _, n := range []*connectedTestNode{a, c} {
		if got := n.m.Settings(); got.Terminal || len(got.CustomModels) != 0 {
			t.Fatalf("%s settings changed by a write to B: %+v", n.name, got)
		}
	}
}
