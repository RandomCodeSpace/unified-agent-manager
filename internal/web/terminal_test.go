package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

// newTerminalServer serves a test server with the terminal on over HTTP, so
// that WebSockets connect, and returns a Project to open terminals in. The
// shell is sh with an empty home, so no profile of the test's user is read.
func newTerminalServer(t *testing.T, cfg ServerConfig) (*testServer, *httptest.Server, string) {
	t.Helper()
	sh, err := execpath.Resolve("sh")
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("SHELL", sh)
	t.Setenv("HOME", t.TempDir())
	ts := newTestServer(t, cfg)
	setTerminal(t, ts.m, true)
	httpSrv := httptest.NewServer(ts.srv)
	t.Cleanup(httpSrv.Close)
	return ts, httpSrv, addProject(t, ts.m, t.TempDir())
}

func setTerminal(t *testing.T, m *Manager, on bool) {
	t.Helper()
	if _, err := m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
}

// dialTerminal opens a terminal at project, signed in, with header added.
func dialTerminal(t *testing.T, httpSrv *httptest.Server, project, query string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u, err := url.Parse(httpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if header == nil {
		header = http.Header{}
	}
	header.Set("Cookie", cookieName+"="+validCookie(u.Host))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, resp, err := websocket.Dial(ctx, "ws://"+u.Host+"/api/projects/"+project+"/terminal"+query, &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func openTestTerminal(t *testing.T, httpSrv *httptest.Server, project, query string) *websocket.Conn {
	t.Helper()
	conn, _, err := dialTerminal(t, httpSrv, project, query, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func send(t *testing.T, conn *websocket.Conn, typ websocket.MessageType, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, typ, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

// readOutput reads output until it matches re and returns the last group.
func readOutput(t *testing.T, conn *websocket.Conn, re *regexp.Regexp) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out []byte
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %s after %q: %v", re, out, err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("waiting for %s: text message %s", re, data)
		}
		out = append(out, data...)
		if m := re.FindSubmatch(out); m != nil {
			return string(m[len(m)-1])
		}
	}
}

// readClose skips output until the socket closes and returns why.
func readClose(t *testing.T, conn *websocket.Conn) websocket.CloseError {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		var closed websocket.CloseError
		if errors.As(err, &closed) {
			return closed
		}
		if err != nil {
			t.Fatalf("socket failed: %v", err)
		}
		if typ == websocket.MessageText {
			t.Fatalf("text message %s before the close", data)
		}
	}
}

func shellPID(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	send(t, conn, websocket.MessageBinary, "echo shell:$$\n")
	pid, err := strconv.Atoi(readOutput(t, conn, regexp.MustCompile(`shell:(\d+)`)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); daemonruntime.ProcAlive(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still runs", pid)
		}
	}
}

// The terminal is off until Settings turns it on; the setting is checked,
// stored, streamed and kept across a restart.
func TestTerminalSetting(t *testing.T) {
	st := openTestStore(t)
	m := startManager(t, st)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m}
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts))
		if w.Code != want {
			t.Fatalf("PATCH %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	for _, body := range []string{`{"terminal":null}`, `{"terminal":"true"}`, `{"terminal":1}`} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, "terminal must be true or false") {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	if m.Settings().Terminal {
		t.Fatal("the terminal is on by default")
	}
	on := `{"send_default":"steer","terminal":true,"planner":false}`
	if got := patch(`{"terminal":true}`, http.StatusOK); got != on {
		t.Fatalf("PATCH terminal = %s", got)
	}
	if f := frameOf(t, sub, "settings"); string(f.data["settings"]) != on {
		t.Fatalf("settings frame = %s", f.data["settings"])
	}
	if cfg, err := st.Load(); err != nil || !cfg.WebSettings.Terminal {
		t.Fatalf("stored settings = %+v, %v", cfg.WebSettings, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !startManager(t, st).Settings().Terminal {
		t.Fatal("the terminal setting did not survive a restart")
	}
}

func TestTerminalRouteRefusals(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	project := addProject(t, ts.m, t.TempDir())
	target := "/api/projects/" + project + "/terminal"
	if w := ts.do(http.MethodGet, target, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, target, "", auth); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "turned off in Settings") {
		t.Fatalf("turned off = %d %s", w.Code, w.Body)
	}
	setTerminal(t, ts.m, true)
	if w := ts.do(http.MethodGet, target, "", auth, withHeader("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/projects/missing/terminal", "", auth); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "project not found") {
		t.Fatalf("unknown project = %d %s", w.Code, w.Body)
	}
	gone := t.TempDir()
	goneProject := addProject(t, ts.m, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if w := ts.do(http.MethodGet, "/api/projects/"+goneProject+"/terminal", "", auth); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no longer exists") {
		t.Fatalf("missing directory = %d %s", w.Code, w.Body)
	}
	for range maxTerminals {
		term, err := ts.m.openTerminal(project)
		if err != nil {
			t.Fatal(err)
		}
		defer ts.m.closeTerminal(term)
	}
	if w := ts.do(http.MethodGet, target, "", auth); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "too many terminals open") {
		t.Fatalf("terminal %d = %d %s", maxTerminals+1, w.Code, w.Body)
	}
}

// Accept takes this Host's origin and the public origins, and no other.
func TestTerminalOrigins(t *testing.T) {
	_, httpSrv, project := newTerminalServer(t, ServerConfig{PublicOrigins: []string{"https://uam.example.com", "https://[::1]:8443"}})
	for _, header := range []http.Header{
		{"Origin": {"https://evil.example"}},
		{"Origin": {"http://uam.example.com"}},
		{"Origin": {httpSrv.URL}, "Sec-Fetch-Site": {"cross-site"}},
	} {
		if _, resp, err := dialTerminal(t, httpSrv, project, "", header); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("dial with %v = %v, %v", header, resp, err)
		}
	}
	for _, origin := range []string{httpSrv.URL, "https://uam.example.com", "https://[::1]:8443"} {
		conn, _, err := dialTerminal(t, httpSrv, project, "", http.Header{"Origin": {origin}, "Sec-Fetch-Site": {"same-origin"}})
		if err != nil {
			t.Fatalf("dial from %s: %v", origin, err)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
}

// Input runs in the Project's directory, resizes apply clamped, and the
// shell's exit is reported before a normal close.
func TestTerminalRoundTripResizeAndExit(t *testing.T) {
	_, httpSrv, project := newTerminalServer(t, ServerConfig{})
	conn := openTestTerminal(t, httpSrv, project, "?cols=120&rows=30")
	send(t, conn, websocket.MessageBinary, "echo uam-term-ok\n")
	// The terminal echoes the typed line; the command's output repeats it.
	readOutput(t, conn, regexp.MustCompile(`(?s)uam-term-ok.*uam-term-ok`))
	send(t, conn, websocket.MessageBinary, "stty size\n")
	if got := readOutput(t, conn, regexp.MustCompile(`(\d+ \d+)\r`)); got != "30 120" {
		t.Fatalf("size = %q, want 30 120", got)
	}
	send(t, conn, websocket.MessageText, `{"type":"paste","cols":1,"rows":1}`)
	send(t, conn, websocket.MessageText, `{"type":"resize","cols":9999,"rows":40}`)
	send(t, conn, websocket.MessageBinary, "stty size\n")
	if got := readOutput(t, conn, regexp.MustCompile(`(\d+ \d+)\r`)); got != "40 500" {
		t.Fatalf("size after resize = %q, want 40 500", got)
	}
	send(t, conn, websocket.MessageBinary, "exit 3\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("no exit message: %v", err)
		}
		if typ == websocket.MessageText {
			if string(data) != `{"type":"exit","code":3}` {
				t.Fatalf("exit message = %s", data)
			}
			break
		}
	}
	if closed := readClose(t, conn); closed.Code != websocket.StatusNormalClosure {
		t.Fatalf("close after exit = %v", closed)
	}
}

// Closing the socket hangs up the shell, which ends the command it runs.
func TestTerminalCloseKillsShell(t *testing.T) {
	_, httpSrv, project := newTerminalServer(t, ServerConfig{})
	conn := openTestTerminal(t, httpSrv, project, "")
	shell := shellPID(t, conn)
	send(t, conn, websocket.MessageBinary, "sh -c 'echo job:$$; exec sleep 30'\n")
	job, err := strconv.Atoi(readOutput(t, conn, regexp.MustCompile(`job:(\d+)`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	waitGone(t, shell)
	waitGone(t, job)
}

// Turning the setting off and stopping the service close open terminals and
// end their shells; Shutdown returns only once they have ended.
func TestTerminalTurnedOffOrShutDown(t *testing.T) {
	ts, httpSrv, project := newTerminalServer(t, ServerConfig{})
	conn := openTestTerminal(t, httpSrv, project, "")
	shell := shellPID(t, conn)
	if w := ts.do(http.MethodPatch, "/api/settings", `{"terminal":false}`, withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("turn off = %d %s", w.Code, w.Body)
	}
	if closed := readClose(t, conn); closed.Code != websocket.StatusGoingAway || closed.Reason != errTerminalOff.Message {
		t.Fatalf("close when turned off = %v", closed)
	}
	waitGone(t, shell)

	setTerminal(t, ts.m, true)
	conn = openTestTerminal(t, httpSrv, project, "")
	shell = shellPID(t, conn)
	// A browser keeps reading, which answers the close handshake.
	ended := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				ended <- err
				return
			}
		}
	}()
	if err := ts.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if daemonruntime.ProcAlive(shell) {
		t.Fatal("the shell outlived Shutdown")
	}
	var closed websocket.CloseError
	if err := <-ended; !errors.As(err, &closed) || closed.Code != websocket.StatusGoingAway || closed.Reason != errShuttingDown.Message {
		t.Fatalf("close at shutdown = %v", err)
	}
}
