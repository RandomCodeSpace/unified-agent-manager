package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialEventSocket opens the event socket for stream, signed in when signedIn.
func dialEventSocket(t *testing.T, httpSrv *httptest.Server, stream string, signedIn bool, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u, err := url.Parse(httpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if header == nil {
		header = http.Header{}
	}
	if signedIn {
		header.Set("Cookie", cookieName+"="+validCookie(u.Host))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return websocket.Dial(ctx, "ws://"+u.Host+"/api/events/socket?path="+url.QueryEscape(stream), &websocket.DialOptions{HTTPHeader: header})
}

func readSocket(t *testing.T, conn *websocket.Conn) (websocket.MessageType, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return kind, string(data)
}

func TestEventSocketCarriesTheEventStream(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	httpSrv := httptest.NewServer(ts.srv)
	t.Cleanup(httpSrv.Close)
	conn, _, err := dialEventSocket(t, httpSrv, "/api/events?page=p1", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if kind, status := readSocket(t, conn); kind != websocket.MessageText || status != "200" {
		t.Fatalf("status message = %v %q, want text 200", kind, status)
	}
	var stream strings.Builder
	for !strings.Contains(stream.String(), "event: snapshot\ndata: ") {
		kind, data := readSocket(t, conn)
		if kind != websocket.MessageBinary {
			t.Fatalf("stream message type = %v, want binary", kind)
		}
		stream.WriteString(data)
	}
	if !strings.HasPrefix(stream.String(), "retry: 2000\n\n") {
		t.Fatalf("stream starts %q, want the handler's own frames", stream.String()[:min(40, stream.Len())])
	}
}

func TestEventSocketCarriesAConnectedInstanceStream(t *testing.T) {
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: snapshot\ndata: {\"seq\":7}\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	u, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream := "/api/connected/" + f.target.ID + "/api/events?session=task&uam_generation=" + strconv.FormatUint(f.target.Generation, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss://"+u.Host+"/api/events/socket?path="+url.QueryEscape(stream), &websocket.DialOptions{
		HTTPClient: f.client, HTTPHeader: http.Header{"Cookie": {cookieName + "=" + validCookie(u.Host)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if _, status := readSocket(t, conn); status != "200" {
		t.Fatalf("status = %q, want 200", status)
	}
	// The proxied stream arrives as it is flushed, not when it ends.
	if _, data := readSocket(t, conn); data != "event: snapshot\ndata: {\"seq\":7}\n\n" {
		t.Fatalf("stream = %q", data)
	}
}

func TestEventSocketPassesTheStreamStatusOn(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	httpSrv := httptest.NewServer(ts.srv)
	t.Cleanup(httpSrv.Close)
	for name, tc := range map[string]struct {
		stream   string
		signedIn bool
		want     string
	}{
		"signed out":      {"/api/events", false, "401"},
		"unknown session": {"/api/events?session=missing", true, "404"},
	} {
		t.Run(name, func(t *testing.T) {
			conn, _, err := dialEventSocket(t, httpSrv, tc.stream, tc.signedIn, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.CloseNow() }()
			if _, status := readSocket(t, conn); status != tc.want {
				t.Fatalf("status = %q, want %q", status, tc.want)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, data, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("after the status: %q, %v; want a normal close", data, err)
			}
		})
	}
}

func TestEventSocketRefusesOtherRoutesAndOrigins(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	httpSrv := httptest.NewServer(ts.srv)
	t.Cleanup(httpSrv.Close)
	for name, tc := range map[string]struct {
		stream string
		header http.Header
		want   int
	}{
		"not a stream":       {"/api/settings", nil, http.StatusBadRequest},
		"absolute URL":       {"http://example.test/api/events", nil, http.StatusBadRequest},
		"dot segments":       {"/api/connected/x/../../api/settings", nil, http.StatusBadRequest},
		"connected settings": {"/api/connected/c1/api/settings", nil, http.StatusBadRequest},
		"cross-site":         {"/api/events", http.Header{"Sec-Fetch-Site": {"cross-site"}}, http.StatusForbidden},
		"other origin":       {"/api/events", http.Header{"Origin": {"http://evil.test"}}, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			conn, resp, err := dialEventSocket(t, httpSrv, tc.stream, true, tc.header)
			if err == nil {
				_ = conn.CloseNow()
				t.Fatal("the socket opened")
			}
			if resp == nil || resp.StatusCode != tc.want {
				t.Fatalf("response = %v, want %d", resp, tc.want)
			}
		})
	}
}

func TestEventSocketTargets(t *testing.T) {
	for raw, want := range map[string]bool{
		"/api/events":                                          true,
		"/api/events?session=s1&page=p":                        true,
		"/api/events/detail?session=s1":                        true,
		"/api/connected-notifications/events":                  true,
		"/api/connected/c1/api/events?page=p":                  true,
		"/api/connected/c1/api/events/detail":                  true,
		"/api/connected/c1/api/connected-notifications/events": false,
		"/api/connected//api/events":                           false,
		"/api/events/socket":                                   false,
		"api/events":                                           false,
		"//host/api/events":                                    false,
		"/api/events#x":                                        false,
		"/api/%65vents":                                        false,
	} {
		if _, ok := eventSocketTarget(raw); ok != want {
			t.Errorf("eventSocketTarget(%q) = %v, want %v", raw, ok, want)
		}
	}
}

func FuzzEventSocketTarget(f *testing.F) {
	for _, seed := range []string{
		"/api/events?session=s1",
		"/api/connected/c1/api/events/detail",
		"/api/connected/c1/../api/events",
		"//host/api/events",
		"/api/%65vents",
		"http://x/api/events#f",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, ok := eventSocketTarget(raw)
		if !ok {
			return
		}
		if u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || path.Clean(u.Path) != u.Path {
			t.Fatalf("eventSocketTarget(%q) accepted %#v", raw, u)
		}
		switch u.Path {
		case "/api/events", "/api/events/detail", "/api/connected-notifications/events":
		default:
			rest, found := strings.CutPrefix(u.Path, "/api/connected/")
			id, inner, _ := strings.Cut(rest, "/")
			if !found || id == "" || (inner != "api/events" && inner != "api/events/detail") {
				t.Fatalf("eventSocketTarget(%q) accepted path %q", raw, u.Path)
			}
		}
		// The stream is served from RequestURI, so it must name the same target.
		again, ok := eventSocketTarget(u.RequestURI())
		if !ok || again.Path != u.Path || again.RawQuery != u.RawQuery {
			t.Fatalf("eventSocketTarget(%q): RequestURI %q does not round-trip", raw, u.RequestURI())
		}
	})
}
