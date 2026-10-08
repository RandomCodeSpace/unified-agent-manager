package web

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestCompressionNegotiation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"identity", false}, {"br", false}, {"gzip", true},
		{"br, gzip, deflate", true}, {" GZip ; q=0.5 ", true}, {"gzip;q=1", true},
		{"gzip;q=0.001", true}, {"gzip;q=1.000", true}, {"gzip;q=0", false},
		{"gzip;q=0.000", false}, {"gzip;q=0.", false}, {"gzip;q=1.", true},
		{"xgzip", false}, {"gzip2", false}, {"gzip;q=0, *;q=1", false},
		{"*;q=1, gzip;q=0", false}, {"*", true}, {"*;q=0", false},
		{"*;q=0, gzip;q=1", true}, {"gzip;q=bad, *", false},
		{"gzip;q=NaN", false}, {"gzip;q=Inf", false}, {"gzip;q=-1", false},
		{"gzip;q=2", false}, {"gzip;q=1.001", false}, {"gzip;q=.5", false},
		{"gzip;q=0.1234", false}, {"gzip;q=1e0", false}, {"gzip;q=+1", false},
		{"gzip;q=", false}, {`gzip;q="0.5"`, false}, {"gzip;foo=1", false}, {"gzip;q=0;q=1", false}, {"gzip;q=0, gzip", false},
		{"gzip, gzip;q=0", false}, {"*;q=no", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := acceptsCoding([]string{tc.value}, "gzip"); got != tc.want {
				t.Fatalf("acceptsCoding(%q, gzip) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
	if acceptsCoding([]string{"*", "gzip;q=0"}, "gzip") || !acceptsCoding([]string{"br", "gzip"}, "gzip") {
		t.Fatal("negotiation ignored a second header field")
	}
	for value, want := range map[string]bool{
		"gzip, deflate, br, zstd": true, "br": true, "BR;q=0.5": true, "br;q=0": false, "gzip": false,
		"brotli": false, "*": true, "*, br;q=0": false, "br;q=0, *": false,
	} {
		if got := acceptsCoding([]string{value}, "br"); got != want {
			t.Fatalf("acceptsCoding(%q, br) = %v, want %v", value, got, want)
		}
	}
}

func gunzipResponse(t *testing.T, body io.Reader) []byte {
	t.Helper()
	reader, err := gzip.NewReader(body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestCompressionRepresentations(t *testing.T) {
	for _, mediaType := range []string{
		"application/json", "application/problem+json; charset=utf-8", "text/html; charset=utf-8",
		"text/css", "text/plain", "text/event-stream", "text/javascript", "application/javascript",
		"application/x-javascript", "image/svg+xml", "application/manifest+json",
	} {
		t.Run(mediaType, func(t *testing.T) {
			body := strings.Repeat("compressible response ", 100)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			serveCompressed(w, r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", mediaType)
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				w.Header().Add("Vary", "Origin")
				w.Header().Add("Vary", "Accept-Language")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, body)
			}))
			if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Content-Length") != "" {
				t.Fatalf("gzip metadata = %v", w.Header())
			}
			if got := strings.Join(w.Header().Values("Vary"), ", "); got != "Origin, Accept-Language, Accept-Encoding" {
				t.Fatalf("Vary = %q", got)
			}
			if got := string(gunzipResponse(t, w.Body)); got != body {
				t.Fatalf("decoded body = %q", got)
			}
		})
	}
}

func TestCompressionIdentityAndBodyless(t *testing.T) {
	for _, tc := range []struct {
		name, method, accept, mediaType, encoding, disposition, ranged string
		status                                                         int
		body                                                           string
		gzip                                                           bool
	}{
		{name: "absent", body: "plain"},
		{name: "refused", accept: "gzip;q=0, *", body: "plain"},
		{name: "binary", accept: "gzip", mediaType: "image/png", body: "PNG"},
		{name: "font", accept: "gzip", mediaType: "font/woff2", body: "font"},
		{name: "encoded", accept: "gzip", encoding: "br", body: "encoded"},
		{name: "download", accept: "gzip", disposition: "attachment; filename=x.txt", body: "download"},
		{name: "task file", accept: "gzip", disposition: "inline; filename=x.svg", mediaType: "image/svg+xml", body: "<svg/>"},
		{name: "range request", accept: "gzip", ranged: "bytes=0-3", body: "part"},
		{name: "partial", accept: "gzip", status: http.StatusPartialContent, body: "part"},
		{name: "no content", accept: "gzip", status: http.StatusNoContent},
		{name: "reset content", accept: "gzip", status: http.StatusResetContent},
		{name: "not modified", accept: "gzip", status: http.StatusNotModified},
		{name: "head", method: http.MethodHead, accept: "gzip", body: "must not be written", gzip: true},
		{name: "empty", accept: "gzip", gzip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.method == "" {
				tc.method = http.MethodGet
			}
			if tc.mediaType == "" {
				tc.mediaType = "text/plain"
			}
			if tc.status == 0 {
				tc.status = http.StatusOK
			}
			r := httptest.NewRequest(tc.method, "/", nil)
			r.Header.Set("Accept-Encoding", tc.accept)
			r.Header.Set("Range", tc.ranged)
			w := httptest.NewRecorder()
			serveCompressed(w, r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				h := w.Header()
				h.Set("Content-Type", tc.mediaType)
				h.Set("Content-Length", strconv.Itoa(len(tc.body)))
				h.Set("ETag", `"unchanged"`)
				if tc.encoding != "" {
					h.Set("Content-Encoding", tc.encoding)
				}
				if tc.disposition != "" {
					h.Set("Content-Disposition", tc.disposition)
				}
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = io.WriteString(w, tc.body)
				}
				if tc.method == http.MethodHead && w.(*compressedResponse).gzip != nil {
					t.Error("HEAD allocated a compressor")
				}
			}))
			wantEncoding := tc.encoding
			if tc.gzip {
				wantEncoding = "gzip"
			}
			if got := w.Header().Get("Content-Encoding"); got != wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, wantEncoding)
			}
			if w.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("identity negotiation omitted Vary")
			}
			if w.Code != tc.status || w.Header().Get("ETag") != `"unchanged"` {
				t.Fatal("status or validator changed")
			}
			if tc.gzip && w.Header().Get("Content-Length") != "" {
				t.Fatal("identity length retained")
			}
			if !tc.gzip && w.Header().Get("Content-Length") != strconv.Itoa(len(tc.body)) {
				t.Fatal("identity length changed")
			}
			got := w.Body.Bytes()
			want := tc.body
			if tc.method == http.MethodHead {
				want = ""
			} else if tc.gzip {
				got = gunzipResponse(t, bytes.NewReader(got))
			}
			if string(got) != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
		})
	}
	for _, value := range []string{"Accept-Encoding", "Origin, accept-encoding", "*"} {
		h := http.Header{"Vary": {value}}
		varyAcceptEncoding(h)
		if got := h.Values("Vary"); len(got) != 1 || got[0] != value {
			t.Fatalf("existing Vary changed: %v", got)
		}
	}
}

func TestCompressionStaticHTTPAndSecurity(t *testing.T) {
	assets := frameAssets()
	assets["icon.svg"] = &fstest.MapFile{Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}
	assets["empty.css"] = &fstest.MapFile{}
	ts := newTestServer(t, ServerConfig{Assets: assets})
	httpSrv := httptest.NewServer(ts.srv)
	defer httpSrv.Close()
	transport := &http.Transport{DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, path := range []string{"/", "/assets/app.js", "/assets/style.css", "/icon.svg", "/empty.css", "/diagram-frame.html", "/api/auth"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+path, func(t *testing.T) {
				req, _ := http.NewRequest(method, httpSrv.URL+path, nil)
				req.Header.Set("Accept-Encoding", "gzip")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" || resp.Uncompressed {
					t.Fatalf("response = %d %v", resp.StatusCode, resp.Header)
				}
				wantCSP, wantFrame := contentSecurity, "DENY"
				if path == "/diagram-frame.html" {
					wantCSP, wantFrame = ts.srv.frameSecurity, "SAMEORIGIN"
				}
				if resp.Header.Get("Content-Security-Policy") != wantCSP || resp.Header.Get("X-Frame-Options") != wantFrame {
					t.Fatal("security policy changed")
				}
				identity := ts.do(http.MethodGet, path, "")
				if resp.Header.Get("Cache-Control") != identity.Header().Get("Cache-Control") {
					t.Fatal("cache policy changed")
				}
				if method == http.MethodHead {
					body, err := io.ReadAll(resp.Body)
					if err != nil || len(body) != 0 || resp.Header.Get("Content-Length") != "" || resp.ContentLength != -1 {
						t.Fatalf("HEAD body=%q err=%v length=%d headers=%v", body, err, resp.ContentLength, resp.Header)
					}
				} else if got := gunzipResponse(t, resp.Body); !bytes.Equal(got, identity.Body.Bytes()) {
					t.Fatalf("decoded response differs for %s", path)
				}
			})
		}
	}
	for _, tc := range []struct {
		srv  *testServer
		path string
		opts []reqOpt
		want int
	}{
		{ts, "/api/sessions", nil, http.StatusUnauthorized},
		{ts, "/api/sessions", []reqOpt{withCookie(ts), withHost("evil.example")}, http.StatusUnauthorized},
	} {
		w := tc.srv.do(http.MethodGet, tc.path, "", append(tc.opts, withHeader("Accept-Encoding", "gzip"))...)
		if w.Code != tc.want || w.Header().Get("Content-Encoding") != "" || w.Header().Get("Content-Security-Policy") != contentSecurity {
			t.Fatalf("security refusal changed: %d %v", w.Code, w.Header())
		}
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The build's compressed copies of static files are sent as they are, by
// Accept-Encoding, with the file's own type, caching and validators; anything
// else keeps the identity bytes or the per-response gzip.
func TestStaticPrecompressedCopies(t *testing.T) {
	assets := frameAssets()
	app, frame, style, index := assets["assets/app.js"].Data, assets["diagram-frame.html"].Data, assets["assets/style.css"].Data, assets["index.html"].Data
	// The brotli copies are opaque here: the service never decodes or checks them.
	assets["assets/app.js.br"] = &fstest.MapFile{Data: []byte("brotli app")}
	assets["assets/app.js.gz"] = &fstest.MapFile{Data: gzipBytes(t, app)}
	assets["assets/style.css.gz"] = &fstest.MapFile{Data: gzipBytes(t, style)}
	assets["diagram-frame.html.br"] = &fstest.MapFile{Data: []byte("brotli frame")}
	assets["index.html.br"] = &fstest.MapFile{Data: []byte("brotli index")}
	assets["LICENSE"] = &fstest.MapFile{Data: []byte(strings.Repeat("license text ", 20))}
	assets["LICENSE.br"] = &fstest.MapFile{Data: []byte("not sent: no type to send it as")}
	ts := newTestServer(t, ServerConfig{Assets: assets})
	etag := ts.do(http.MethodGet, "/diagram-frame.html", "").Header().Get("ETag")
	for _, tc := range []struct {
		name, method, path, accept, ranged, match string
		status                                    int
		encoding                                  string
		body                                      []byte
	}{
		{name: "brotli first", path: "/assets/app.js", accept: "gzip, deflate, br, zstd", encoding: "br", body: []byte("brotli app")},
		{name: "gzip copy", path: "/assets/app.js", accept: "gzip", encoding: "gzip", body: assets["assets/app.js.gz"].Data},
		{name: "brotli refused", path: "/assets/app.js", accept: "br;q=0, gzip", encoding: "gzip", body: assets["assets/app.js.gz"].Data},
		{name: "no encoding", path: "/assets/app.js", body: app},
		{name: "identity", path: "/assets/app.js", accept: "identity", body: app},
		{name: "head", method: http.MethodHead, path: "/assets/app.js", accept: "br", encoding: "br"},
		{name: "range", path: "/assets/app.js", accept: "br, gzip", ranged: "bytes=0-6", status: http.StatusPartialContent, body: app[:7]},
		{name: "missing brotli copy", path: "/assets/style.css", accept: "br", body: style},
		{name: "gzip copy without brotli", path: "/assets/style.css", accept: "br, gzip", encoding: "gzip", body: assets["assets/style.css.gz"].Data},
		{name: "missing gzip copy", path: "/diagram-frame.html", accept: "gzip", encoding: "gzip", body: gzipBytes(t, frame)},
		{name: "frame", path: "/diagram-frame.html", accept: "br", encoding: "br", body: []byte("brotli frame")},
		{name: "frame revalidated", path: "/diagram-frame.html", accept: "br", match: etag, status: http.StatusNotModified},
		{name: "app route", path: "/sessions/abc", accept: "br", encoding: "br", body: []byte("brotli index")},
		{name: "index", path: "/", accept: "gzip", encoding: "gzip", body: gzipBytes(t, index)},
		{name: "untyped", path: "/LICENSE", accept: "br, gzip", encoding: "gzip", body: gzipBytes(t, assets["LICENSE"].Data)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method, status := cmp.Or(tc.method, http.MethodGet), cmp.Or(tc.status, http.StatusOK)
			opts := []reqOpt{withHeader("Accept-Encoding", tc.accept)}
			if tc.ranged != "" {
				opts = append(opts, withHeader("Range", tc.ranged))
			}
			if tc.match != "" {
				opts = append(opts, withHeader("If-None-Match", tc.match))
			}
			w := ts.do(method, tc.path, "", opts...)
			identity := ts.do(http.MethodGet, tc.path, "")
			h := w.Header()
			if w.Code != status || h.Get("Content-Encoding") != tc.encoding || h.Get("Vary") != "Accept-Encoding" {
				t.Fatalf("%s %s = %d %q, Vary %q", method, tc.path, w.Code, h.Get("Content-Encoding"), h.Get("Vary"))
			}
			// Per-response gzip carries the original body: compare what it decodes to.
			got := w.Body.Bytes()
			if tc.encoding == "gzip" && !bytes.Equal(got, tc.body) {
				got, tc.body = gunzipResponse(t, bytes.NewReader(got)), gunzipResponse(t, bytes.NewReader(tc.body))
			}
			if !bytes.Equal(got, tc.body) {
				t.Fatalf("body = %q, want %q", got, tc.body)
			}
			for _, header := range []string{"Cache-Control", "Content-Security-Policy", "X-Frame-Options", "ETag"} {
				if h.Get(header) != identity.Header().Get(header) {
					t.Fatalf("%s = %q, identity has %q", header, h.Get(header), identity.Header().Get(header))
				}
			}
			if status == http.StatusOK && h.Get("Content-Type") != identity.Header().Get("Content-Type") {
				t.Fatalf("Content-Type = %q, identity has %q", h.Get("Content-Type"), identity.Header().Get("Content-Type"))
			}
			// An encoded body goes without a length, as per-response gzip does.
			if want := strconv.Itoa(len(tc.body)); status == http.StatusOK && tc.encoding == "" && h.Get("Content-Length") != want || tc.encoding != "" && h.Get("Content-Length") != "" {
				t.Fatalf("Content-Length = %q for %q", h.Get("Content-Length"), tc.encoding)
			}
		})
	}
}

func TestCompressionTaskFileKeepsIdentityValidatorsAndRanges(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	view := authenticatedFileView(t, ts)
	sum, _ := createSession(t, ts.m, ts.prov)
	const body = "plain task file content"
	if err := os.WriteFile(filepath.Join(sum.Workdir, "notes.txt"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/sessions/" + sum.ID + "/files/view/notes.txt"
	identity := view(http.MethodGet, path)
	gzipOpt := withHeader("Accept-Encoding", "gzip")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := view(method, path, gzipOpt)
		if w.Code != 200 || w.Header().Get("Content-Encoding") != "" || w.Header().Get("Content-Disposition") == "" {
			t.Fatalf("file response = %d %v", w.Code, w.Header())
		}
		for _, key := range []string{"ETag", "Last-Modified", "Content-Length", "Content-Security-Policy", "Content-Type"} {
			if w.Header().Get(key) != identity.Header().Get(key) {
				t.Fatalf("file %s changed", key)
			}
		}
	}
	if etag := identity.Header().Get("ETag"); etag == "" || strings.HasPrefix(etag, "W/") {
		t.Fatalf("expected strong file ETag, got %q", etag)
	}
	w := view(http.MethodGet, path, gzipOpt, withHeader("If-None-Match", identity.Header().Get("ETag")))
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatal("file revalidation changed")
	}
	w = view(http.MethodGet, path, gzipOpt, withHeader("Range", "bytes=0-4"))
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Encoding") != "" || w.Body.String() != body[:5] {
		t.Fatalf("file range = %d %v %q", w.Code, w.Header(), w.Body.String())
	}
}

func TestCompressionStreamsFlushBeforeHandlerReturns(t *testing.T) {
	for _, stream := range []string{"main", "detail"} {
		t.Run(stream, func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{})
			ts.srv.heartbeat = 25 * time.Millisecond
			sum, conv := createSession(t, ts.m, ts.prov)
			conv.EmitItem(agentapi.Item{ID: "body", Kind: agentapi.ItemAssistant, Text: "initial"})
			done := make(chan struct{})
			httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); ts.srv.ServeHTTP(w, r) }))
			defer httpSrv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			path := "/api/events?session=" + sum.ID + "&view=compact"
			if stream == "detail" {
				path = "/api/events/detail?session=" + sum.ID + "&item=" + url.QueryEscape(`["","body"]`)
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			ts.login(t, req.URL.Host)(req)
			transport := &http.Transport{DisableCompression: true}
			defer transport.CloseIdleConnections()
			resp, err := (&http.Client{Transport: transport}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Accel-Buffering") != "no" || resp.Uncompressed {
				t.Fatalf("stream response = %d %v", resp.StatusCode, resp.Header)
			}
			decoded, err := gzip.NewReader(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = decoded.Close() }()
			reader := bufio.NewReader(decoded)
			next := func() string {
				var frame strings.Builder
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Fatalf("read live gzip frame: %v", err)
					}
					frame.WriteString(line)
					if line == "\n" {
						select {
						case <-done:
							t.Fatal("frame arrived only after handler returned")
						default:
						}
						return frame.String()
					}
				}
			}
			want := "event: snapshot\n"
			if stream == "detail" {
				want = "event: detail_ready\n"
			}
			sawInitialBody := false
			for frame := next(); !strings.Contains(frame, want); frame = next() {
				sawInitialBody = sawInitialBody || (strings.Contains(frame, "event: body\n") && strings.Contains(frame, `"text":"initial"`))
			}
			event, itemID := "item", "tiny"
			if stream == "detail" {
				if !sawInitialBody {
					t.Fatal("detail_ready arrived without the watched body")
				}
				event, itemID = "body", "body"
			}
			conv.EmitItem(agentapi.Item{ID: itemID, Kind: agentapi.ItemAssistant, Text: "x"})
			sawTiny, sawHeartbeat := false, false
			for !sawTiny || !sawHeartbeat {
				frame := next()
				sawTiny = sawTiny || (strings.Contains(frame, "event: "+event+"\n") && strings.Contains(frame, `"id":"`+itemID+`"`) && (stream == "main" || strings.Contains(frame, `"text":"x"`)))
				sawHeartbeat = sawHeartbeat || frame == ": keep-alive\n\n"
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stream did not stop after cancellation")
			}
			ts.m.mu.Lock()
			remaining := len(ts.m.subs)
			ts.m.mu.Unlock()
			if remaining != 0 || conv.Cancels() != 0 || conv.Closes() != 0 {
				t.Fatalf("disconnect subscribers=%d cancels=%d closes=%d", remaining, conv.Cancels(), conv.Closes())
			}
		})
	}
}

type compressionFailureWriter struct {
	streamFailureWriter
	deadlines, flushes, failAfterFlush int
	flushed                            chan struct{}
}

func (w *compressionFailureWriter) SetWriteDeadline(time.Time) error { w.deadlines++; return nil }
func (w *compressionFailureWriter) FlushError() error {
	if err := w.streamFailureWriter.FlushError(); err != nil {
		return err
	}
	w.flushes++
	if w.flushes == w.failAfterFlush {
		w.failWrite = w.writes + 1
	}
	if w.flushes == 1 {
		close(w.flushed)
	}
	return nil
}

func TestCompressionStreamErrorsReleaseSubscriptions(t *testing.T) {
	for _, failure := range []string{"snapshot", "flush", "event", "heartbeat", "detail"} {
		t.Run(failure, func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{})
			ts.srv.heartbeat = time.Hour
			sum, conv := createSession(t, ts.m, ts.prov)
			w := &compressionFailureWriter{streamFailureWriter: streamFailureWriter{header: make(http.Header), ready: make(chan struct{})}, flushed: make(chan struct{})}
			path := "/api/events?session=" + sum.ID
			switch failure {
			case "snapshot":
				w.failWrite = 1
			case "flush":
				w.failFlush = true
			case "event":
				w.failAfterFlush = 1
			case "heartbeat":
				w.failAfterFlush = 1
				ts.srv.heartbeat = time.Millisecond
			case "detail":
				w.failAfterFlush = 1
				path = "/api/events/detail?session=" + sum.ID + "&item=" + url.QueryEscape(`["","missing"]`)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			r.Host = "127.0.0.1:8260"
			ts.login(t, r.Host)(r)
			r.Header.Set("Accept-Encoding", "gzip")
			done := make(chan struct{})
			go func() { defer close(done); ts.srv.ServeHTTP(w, r) }()
			if failure == "event" {
				select {
				case <-w.flushed:
				case <-time.After(time.Second):
					t.Fatal("snapshot did not flush")
				}
				conv.EmitItem(agentapi.Item{ID: "live", Kind: agentapi.ItemAssistant, Text: "x"})
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("failed stream did not return")
			}
			ts.m.mu.Lock()
			remaining := len(ts.m.subs)
			ts.m.mu.Unlock()
			if remaining != 0 || w.deadlines == 0 || conv.Closes() != 0 || conv.Cancels() != 0 {
				t.Fatalf("subscribers=%d deadlines=%d closes=%d cancels=%d", remaining, w.deadlines, conv.Closes(), conv.Cancels())
			}
		})
	}
}

type compressionDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
	err      error
}

func (w *compressionDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.err
}

func TestCompressionResponseControllerDelegatesDeadline(t *testing.T) {
	wantErr := errors.New("deadline failed")
	underlying := &compressionDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), err: wantErr}
	w := &compressedResponse{ResponseWriter: underlying}
	deadline := time.Now().Add(time.Second)
	if err := http.NewResponseController(w).SetWriteDeadline(deadline); !errors.Is(err, wantErr) || !underlying.deadline.Equal(deadline) {
		t.Fatalf("deadline delegation = %v, %v", underlying.deadline, err)
	}
}
