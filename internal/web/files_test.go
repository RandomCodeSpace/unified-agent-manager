package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rawImageTask makes a Task whose directory holds test images, a symbolic
// link to one, and links leading out of it. The directory is reached through
// a symlink so the Task's workdir itself needs resolving.
func rawImageTask(t *testing.T, ts *testServer) (SessionSummary, string, string) {
	t.Helper()
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(realDir, "shots"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	png := pngBytes(t)
	files := map[string][]byte{
		filepath.Join(realDir, "sky-dodge.png"):  png,
		filepath.Join(realDir, "shots", "a.PNG"): png,
		filepath.Join(realDir, "tiny.webp"):      webpBytes,
		filepath.Join(realDir, "fake.png"):       []byte("<html><script>alert(1)</script></html>"),
		filepath.Join(realDir, "notes.txt"):      []byte("plain text"),
		filepath.Join(realDir, "logo.svg"):       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		filepath.Join(realDir, "wrong-ext.jpg"):  png,
		filepath.Join(realDir, "empty.png"):      nil,
		filepath.Join(outside, "secret.png"):     png,
		filepath.Join(realDir, "shots", "x.txt"): []byte("x"),
	}
	for name, data := range files {
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(realDir, "inside-link.png"):  filepath.Join(realDir, "sky-dodge.png"),
		filepath.Join(realDir, "outside-link.png"): filepath.Join(outside, "secret.png"),
		filepath.Join(realDir, "outside-dir"):      outside,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: addProject(t, ts.m, link), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	return sum, realDir, outside
}

func rawURL(id, p string) string {
	return "/api/sessions/" + id + "/files/raw?path=" + url.QueryEscape(p)
}

func TestRawImageServesImagesInsideTheTaskDirectory(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, realDir, _ := rawImageTask(t, ts)
	png := pngBytes(t)

	allowed := map[string]struct {
		body []byte
		mime string
	}{
		"sky-dodge.png":                           {png, "image/png"},
		"./sky-dodge.png":                         {png, "image/png"},
		"shots/a.PNG":                             {png, "image/png"},
		"shots/../sky-dodge.png":                  {png, "image/png"},
		"tiny.webp":                               {webpBytes, "image/webp"},
		"inside-link.png":                         {png, "image/png"},
		filepath.Join(realDir, "sky-dodge.png"):   {png, "image/png"},
		filepath.Join(realDir, "inside-link.png"): {png, "image/png"},
		filepath.Join(sum.Workdir, "shots/a.PNG"): {png, "image/png"},
	}
	for p, want := range allowed {
		w := ts.do(http.MethodGet, rawURL(sum.ID, p), "", auth)
		h := w.Header()
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want.body) || h.Get("Content-Type") != want.mime ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "private, no-cache" ||
			h.Get("Last-Modified") == "" || h.Get("ETag") == "" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline; filename=") ||
			h.Get("Content-Security-Policy") != contentSecurity {
			t.Errorf("GET %s = %d %v %q", p, w.Code, h, w.Body.String()[:min(w.Body.Len(), 80)])
		}
	}

	// A revalidation with the served ETag is a 304 without a body.
	first := ts.do(http.MethodGet, rawURL(sum.ID, "sky-dodge.png"), "", auth)
	w := ts.do(http.MethodGet, rawURL(sum.ID, "sky-dodge.png"), "", auth, withHeader("If-None-Match", first.Header().Get("ETag")))
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("revalidate = %d %d bytes", w.Code, w.Body.Len())
	}
	if w := ts.do(http.MethodHead, rawURL(sum.ID, "sky-dodge.png"), "", auth); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("HEAD = %d", w.Code)
	}
}

func TestRawImageRefusesEverythingElse(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, realDir, outside := rawImageTask(t, ts)

	rejected := map[string]int{
		"":                                   http.StatusBadRequest,
		"sky\x00dodge.png":                   http.StatusBadRequest,
		strings.Repeat("a", 5000) + ".png":   http.StatusBadRequest,
		"missing.png":                        http.StatusNotFound,
		"shots":                              http.StatusNotFound,
		".":                                  http.StatusNotFound,
		"../outside/secret.png":              http.StatusNotFound,
		"shots/../../outside/secret.png":     http.StatusNotFound,
		"..%2Foutside%2Fsecret.png":          http.StatusNotFound,
		"outside-link.png":                   http.StatusNotFound,
		"outside-dir/secret.png":             http.StatusNotFound,
		filepath.Join(outside, "secret.png"): http.StatusNotFound,
		"/etc/passwd":                        http.StatusNotFound,
		"/etc/hostname.png":                  http.StatusNotFound,
		"/proc/self/cmdline":                 http.StatusNotFound,
		"/proc/self/cwd/sky-dodge.png":       http.StatusNotFound,
		"/dev/null":                          http.StatusNotFound,
		"/dev/zero.png":                      http.StatusNotFound,
		"notes.txt":                          http.StatusUnsupportedMediaType,
		"logo.svg":                           http.StatusUnsupportedMediaType,
		"fake.png":                           http.StatusUnsupportedMediaType,
		"wrong-ext.jpg":                      http.StatusUnsupportedMediaType,
		"empty.png":                          http.StatusUnsupportedMediaType,
		filepath.Join(realDir, "notes.txt"):  http.StatusUnsupportedMediaType,
	}
	for p, want := range rejected {
		w := ts.do(http.MethodGet, rawURL(sum.ID, p), "", auth)
		if w.Code != want || !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), realDir) {
			t.Errorf("GET %q = %d %s, want %d", p, w.Code, w.Body, want)
		}
	}

	// A file over the cap is refused before any byte is read.
	big := filepath.Join(realDir, "big.png")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxServedImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if w := ts.do(http.MethodGet, rawURL(sum.ID, "big.png"), "", auth); w.Code != http.StatusUnsupportedMediaType || !strings.Contains(w.Body.String(), "20 MiB") {
		t.Fatalf("over cap = %d %s", w.Code, w.Body)
	}

	// The route follows the API's checks: session, host-bound cookie and method.
	if w := ts.do(http.MethodGet, rawURL("nope", "sky-dodge.png"), "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown task = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, rawURL(sum.ID, "sky-dodge.png"), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("without cookie = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, rawURL(sum.ID, "sky-dodge.png"), "", auth, withHost("evil.example")); w.Code != http.StatusUnauthorized {
		t.Fatalf("foreign host with another host's cookie = %d", w.Code)
	}
	// The route is GET only; another method falls through to the API's 404.
	if w := ts.do(http.MethodPost, rawURL(sum.ID, "sky-dodge.png"), "{}", auth); w.Code != http.StatusNotFound || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("POST = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	// Another Task's directory does not hold the file.
	other, _ := createSession(t, ts.m, ts.prov)
	if w := ts.do(http.MethodGet, rawURL(other.ID, filepath.Join(realDir, "sky-dodge.png")), "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("another task = %d", w.Code)
	}
}

// viewTask makes a Task whose directory holds a page with sibling assets,
// files of other kinds, a directory and links leading out of it.
func viewTask(t *testing.T, ts *testServer) (SessionSummary, string) {
	t.Helper()
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(realDir, "out"), filepath.Join(realDir, "assets"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		"out/report.html":     []byte(`<link rel="stylesheet" href="style.css"><script src="app.js"></script><img src="../shot.png">`),
		"out/style.css":       []byte("h1 { color: red }"),
		"out/app.js":          []byte("document.title = 'ran'"),
		"assets/x.css":        []byte("body { margin: 0 }"),
		"shot.png":            pngBytes(t),
		"logo.svg":            []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"doc.pdf":             []byte("%PDF-1.4\n"),
		"notes.md":            []byte("# Notes"),
		"Makefile":            []byte("all:\n\techo hi\n"),
		"blob.bin":            []byte("head\x00tail"),
		"a b.txt":             []byte("spaced"),
		"../outside/secret.x": []byte("secret"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(realDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"inside-link.md": filepath.Join(realDir, "notes.md"),
		"outside-link.x": filepath.Join(outside, "secret.x"),
		"outside-dir":    outside,
	} {
		if err := os.Symlink(target, filepath.Join(realDir, link)); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: addProject(t, ts.m, realDir), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	return sum, realDir
}

func viewURL(id, p string) string { return "/api/sessions/" + id + "/files/view/" + p }

// Follow the production cookie-to-file-key redirect, then serve without a cookie.
func authenticatedFileView(t *testing.T, ts *testServer) func(string, string, ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	auth := ts.login(t, "127.0.0.1:8260")
	return func(method, target string, opts ...reqOpt) *httptest.ResponseRecorder {
		w := ts.do(method, target, "", append([]reqOpt{auth}, opts...)...)
		if w.Code == http.StatusFound {
			return ts.do(method, w.Header().Get("Location"), "", opts...)
		}
		return w
	}
}

func TestViewFileServesAnyFileOfTheTaskDirectorySandboxed(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, realDir := viewTask(t, ts)
	view := authenticatedFileView(t, ts)

	served := map[string]struct{ mime, disposition string }{
		"out/report.html": {"text/html; charset=utf-8", "inline"},
		"out/style.css":   {"text/css; charset=utf-8", "inline"},
		"out/app.js":      {"text/javascript; charset=utf-8", "inline"},
		"assets/x.css":    {"text/css; charset=utf-8", "inline"},
		"shot.png":        {"image/png", "inline"},
		"logo.svg":        {"image/svg+xml", "inline"},
		"doc.pdf":         {"application/pdf", "inline"},
		"notes.md":        {"text/plain; charset=utf-8", "inline"},
		"inside-link.md":  {"text/plain; charset=utf-8", "inline"},
		"Makefile":        {"text/plain; charset=utf-8", "inline"},
		"a%20b.txt":       {"text/plain; charset=utf-8", "inline"},
		"blob.bin":        {"application/octet-stream", "attachment"},
	}
	for p, want := range served {
		w := view(http.MethodGet, viewURL(sum.ID, p))
		h := w.Header()
		name, _ := url.PathUnescape(p)
		body, _ := os.ReadFile(filepath.Join(realDir, name))
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), body) || h.Get("Content-Type") != want.mime ||
			h.Get("Content-Security-Policy") != viewSecurity || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("X-Frame-Options") != "SAMEORIGIN" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "private, no-cache" ||
			!strings.HasPrefix(h.Get("Content-Disposition"), want.disposition+"; filename=") {
			t.Errorf("GET %s = %d %v %q", p, w.Code, h, w.Body.String()[:min(w.Body.Len(), 80)])
		}
	}
	if w := view(http.MethodGet, viewURL(sum.ID, "notes.md"), withHeader("Range", "bytes=2-")); w.Code != http.StatusPartialContent || w.Body.String() != "Notes" {
		t.Fatalf("range = %d %q", w.Code, w.Body)
	}

	for _, p := range []string{
		"", "out", "out/", "missing.html", "outside-link.x", "outside-dir/secret.x",
		"..%2Foutside%2Fsecret.x", "out%2F..%2F..%2Foutside%2Fsecret.x", "%2Fetc%2Fpasswd", "%00",
	} {
		w := view(http.MethodGet, viewURL(sum.ID, p))
		if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), realDir) {
			t.Errorf("GET %q = %d %s", p, w.Code, w.Body)
		}
	}
	// A literal .. is cleaned away by the mux before any handler runs.
	if w := view(http.MethodGet, viewURL(sum.ID, "../../../../etc/passwd")); w.Code == http.StatusOK && strings.Contains(w.Body.String(), "root:") {
		t.Fatalf("dot-dot = %d", w.Code)
	}
	if w := view(http.MethodGet, viewURL("nope", "notes.md")); w.Code != http.StatusNotFound {
		t.Fatalf("unknown task = %d", w.Code)
	}
}

func TestViewFilePreviewMetadataRangeAndDownload(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, dir := viewTask(t, ts)
	view := authenticatedFileView(t, ts)
	text := strings.Repeat("a", 64<<10) + "tail"
	for name, data := range map[string]string{"large.txt": text, "empty.txt": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w := view(http.MethodHead, viewURL(sum.ID, "large.txt"))
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(text)) || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("preview metadata = %d, body bytes %d, size %q, type %q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"), w.Header().Get("Content-Type"))
	}
	w = view(http.MethodGet, viewURL(sum.ID, "large.txt"), withHeader("Range", "bytes=0-65535"), withHeader("Accept-Encoding", "gzip"))
	if w.Code != http.StatusPartialContent || w.Body.String() != text[:64<<10] || w.Header().Get("Content-Range") != "bytes 0-65535/65540" || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("preview range = %d, body bytes %d, range %q, encoding %q", w.Code, w.Body.Len(), w.Header().Get("Content-Range"), w.Header().Get("Content-Encoding"))
	}
	w = view(http.MethodGet, viewURL(sum.ID, "empty.txt"), withHeader("Range", "bytes=0-65535"))
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "0" {
		t.Fatalf("empty preview range = %d, body bytes %d, size %q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}

	auth := ts.login(t, "127.0.0.1:8260")
	w = ts.do(http.MethodHead, viewURL(sum.ID, "a%20b.txt")+"?download=1", "", auth)
	target, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusFound || target.Query().Get("download") != "1" || !strings.HasSuffix(target.EscapedPath(), "/a%20b.txt") {
		t.Fatal("download redirect did not retain the escaped file path and download flag")
	}
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		w = ts.do(method, target.String(), "")
		if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") != `attachment; filename="a b.txt"` || w.Header().Get("Content-Length") != "6" {
			t.Fatalf("%s download = %d, disposition %q, size %q", method, w.Code, w.Header().Get("Content-Disposition"), w.Header().Get("Content-Length"))
		}
		if method == http.MethodHead && w.Body.Len() != 0 || method == http.MethodGet && w.Body.String() != "spaced" {
			t.Fatalf("%s download body bytes = %d", method, w.Body.Len())
		}
	}
	if w = view(http.MethodGet, viewURL(sum.ID, "a%20b.txt")+"?download=0"); !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline;") {
		t.Fatal("only download=1 should force a download")
	}

	// Framing is limited to an authorized file response, never the app, its
	// authenticated API, a failed file lookup or a refused credential.
	for _, response := range []*httptest.ResponseRecorder{
		ts.do(http.MethodGet, "/", ""),
		ts.do(http.MethodGet, "/api/meta", "", auth),
		ts.do(http.MethodHead, viewURL(sum.ID, "large.txt"), ""),
		ts.do(http.MethodGet, target.String(), "", withHost("other.example")),
		view(http.MethodGet, viewURL(sum.ID, "missing.txt")),
	} {
		if response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("Content-Security-Policy") != contentSecurity {
			t.Fatalf("non-view response changed framing policy: status %d", response.Code)
		}
	}
	if w = ts.do(http.MethodHead, viewURL(sum.ID, "large.txt"), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metadata = %d", w.Code)
	}
	if w = ts.do(http.MethodGet, target.String(), "", withHost("other.example")); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-host download = %d", w.Code)
	}
}

// Over HTTPS a file key opens files only with the companion cookie the view
// route sets, so a key copied out of a view URL is useless elsewhere.
func TestViewFileOverHTTPSNeedsTheCompanionCookie(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := viewTask(t, ts)
	other, _ := createSession(t, ts.m, ts.prov)
	https := withHeader("X-Forwarded-Proto", "https")
	auth := withCookie(ts)

	w := ts.do(http.MethodGet, viewURL(sum.ID, "out/report.html"), "", auth, https)
	prefix := "/api/sessions/" + sum.ID + "/files/key/"
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, prefix) {
		t.Fatalf("view = %d %q", w.Code, loc)
	}
	var companion *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == fileCookieName {
			companion = c
		}
	}
	if companion == nil || companion.Path != prefix || !companion.HttpOnly || !companion.Secure ||
		companion.SameSite != http.SameSiteNoneMode || companion.MaxAge != int(fileKeyTTL/time.Second) {
		t.Fatalf("companion cookie = %+v", companion)
	}
	key := strings.SplitN(strings.TrimPrefix(loc, prefix), "/", 2)[0]
	withFile := func(value string) reqOpt {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: fileCookieName, Value: value}) }
	}
	exp := time.Now().Add(time.Hour).Unix()
	for name, c := range map[string]struct {
		target string
		opts   []reqOpt
		want   int
	}{
		"key with its cookie":              {loc, []reqOpt{https, withFile(companion.Value)}, http.StatusOK},
		"sibling in another folder":        {prefix + key + "/shot.png", []reqOpt{https, withFile(companion.Value)}, http.StatusOK},
		"key without the cookie":           {loc, []reqOpt{https}, http.StatusUnauthorized},
		"key with the sign-in cookie only": {loc, []reqOpt{https, auth}, http.StatusUnauthorized},
		"cookie of another Task":           {loc, []reqOpt{https, withFile(fileCookie(testToken, "127.0.0.1:8260", other.ID, exp))}, http.StatusUnauthorized},
		"cookie of another host":           {loc, []reqOpt{https, withFile(fileCookie(testToken, "evil.example", sum.ID, exp))}, http.StatusUnauthorized},
		"expired cookie":                   {loc, []reqOpt{https, withFile(fileCookie(testToken, "127.0.0.1:8260", sum.ID, time.Now().Add(-time.Second).Unix()))}, http.StatusUnauthorized},
		"cookie of another secret":         {loc, []reqOpt{https, withFile(fileCookie("another-secret-token-0123456789", "127.0.0.1:8260", sum.ID, exp))}, http.StatusUnauthorized},
		"tampered cookie":                  {loc, []reqOpt{https, withFile(companion.Value + "0")}, http.StatusUnauthorized},
		"file key as the cookie":           {loc, []reqOpt{https, withFile(key)}, http.StatusUnauthorized},
		// Plain HTTP (loopback, SSH) cannot hold a Secure cookie: the key alone opens.
		"plain HTTP key alone": {loc, nil, http.StatusOK},
	} {
		if w := ts.do(http.MethodGet, c.target, "", c.opts...); w.Code != c.want {
			t.Errorf("%s = %d, want %d", name, w.Code, c.want)
		}
	}
	// Plain HTTP sets no companion cookie.
	w = ts.do(http.MethodGet, viewURL(sum.ID, "out/report.html"), "", auth)
	for _, c := range w.Result().Cookies() {
		if c.Name == fileCookieName {
			t.Fatalf("plain HTTP set %+v", c)
		}
	}
}

// Keys and cookies signed before this release's labels no longer open anything.
func TestViewFileRefusesKeysFromBeforeTheCurrentLabel(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := viewTask(t, ts)
	e := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	old := e + "." + legacyMAC("uam-web-file-v1|127.0.0.1:8260|"+sum.ID+"|"+e)
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/files/key/"+old+"/notes.md", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("old file key = %d", w.Code)
	}
}

func TestViewFileUnderAuthenticationRedirectsToAFileKey(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, _ := viewTask(t, ts)
	other, _ := createSession(t, ts.m, ts.prov)

	if w := ts.do(http.MethodGet, viewURL(sum.ID, "out/report.html"), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("without cookie = %d", w.Code)
	}
	w := ts.do(http.MethodGet, viewURL(sum.ID, "a%20b.txt"), "", auth)
	loc := w.Header().Get("Location")
	prefix := "/api/sessions/" + sum.ID + "/files/key/"
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, prefix) || !strings.HasSuffix(loc, "/a%20b.txt") {
		t.Fatalf("with cookie = %d %q", w.Code, loc)
	}
	key := strings.TrimSuffix(strings.TrimPrefix(loc, prefix), "/a%20b.txt")
	keyURL := func(id, key, p string) string { return "/api/sessions/" + id + "/files/key/" + key + "/" + p }

	// The key stands in for the cookie, for the page and its siblings.
	for _, p := range []string{"a%20b.txt", "out/report.html", "assets/x.css", "shot.png"} {
		if w := ts.do(http.MethodGet, keyURL(sum.ID, key, p), ""); w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != viewSecurity {
			t.Errorf("keyed %s = %d", p, w.Code)
		}
	}
	if w := ts.do(http.MethodHead, keyURL(sum.ID, key, "notes.md"), ""); w.Code != http.StatusOK {
		t.Fatalf("keyed HEAD = %d", w.Code)
	}
	// Confinement still applies under a key.
	if w := ts.do(http.MethodGet, keyURL(sum.ID, key, "outside-link.x"), ""); w.Code != http.StatusNotFound {
		t.Fatalf("keyed outside link = %d", w.Code)
	}

	expired := fileKey(testToken, "127.0.0.1:8260", sum.ID, time.Now().Add(-time.Minute).Unix())
	// The last hex digit changed, never replaced by itself.
	flip := "0"
	if strings.HasSuffix(key, "0") {
		flip = "1"
	}
	valid := time.Now().Add(time.Hour).Unix()
	refused := map[string]string{
		"expired":      keyURL(sum.ID, expired, "notes.md"),
		"other task":   keyURL(other.ID, key, "notes.md"),
		"tampered":     keyURL(sum.ID, key[:len(key)-1]+flip, "notes.md"),
		"extended":     keyURL(sum.ID, strconv.FormatInt(valid, 10)+key[strings.IndexByte(key, '.'):], "notes.md"),
		"no mac":       keyURL(sum.ID, strconv.FormatInt(valid, 10), "notes.md"),
		"other secret": keyURL(sum.ID, fileKey("another-token-of-24-chars-plus", "127.0.0.1:8260", sum.ID, valid), "notes.md"),
	}
	for name, target := range refused {
		if w := ts.do(http.MethodGet, target, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	// Any Host reaches the route with sign-in on; the key MAC binds the host.
	for _, host := range []string{"localhost:8260", "evil.example"} {
		if w := ts.do(http.MethodGet, keyURL(sum.ID, key, "notes.md"), "", withHost(host)); w.Code != http.StatusUnauthorized {
			t.Fatalf("other host %q = %d", host, w.Code)
		}
	}
	// The key opens only this GET route: not another method, not another route.
	if w := ts.do(http.MethodPost, keyURL(sum.ID, key, "notes.md"), "{}"); w.Code != http.StatusUnauthorized {
		t.Fatalf("keyed POST = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/files/raw?path=shot.png&key="+key, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("key on the raw route = %d", w.Code)
	}
}

// The view redirect is built from the route's values: the escaped Task id,
// a key for it and the file path escaped segment by segment. The companion
// cookie is scoped to the same prefix, and the key route accepts the target.
func TestViewFileRedirectTargetIsBuiltFromTheRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	https := withHeader("X-Forwarded-Proto", "https")
	for _, c := range []struct{ name, id, escapedID, path, want string }{
		{"plain", "task", "task", "out/report.html", "out/report.html"},
		{"escaped name", "task", "task", "a%20b.txt", "a%20b.txt"},
		{"nested escaped segments", "task", "task", "my%20dir/sub/r%23%3F.html", "my%20dir/sub/r%23%3F.html"},
		{"encoded separators", "task", "task", "dir%2Fx.html", "dir/x.html"},
		{"encoded dot segments", "task", "task", "..%2Foutside%2Fsecret.x", "%2E%2E/outside/secret.x"},
		{"empty path", "task", "task", "", ""},
		{"id with a space", "a b", "a%20b", "x.html", "x.html"},
		{"id with a slash", "a/b", "a%2Fb", "x.html", "x.html"},
		{"id with a question mark", "a?b", "a%3Fb", "x.html", "x.html"},
	} {
		w := ts.do(http.MethodGet, "/api/sessions/"+c.escapedID+"/files/view/"+c.path+"?download=1", "", auth, https)
		prefix := "/api/sessions/" + c.escapedID + "/files/key/"
		loc := w.Header().Get("Location")
		key, rest, _ := strings.Cut(strings.TrimPrefix(loc, prefix), "/")
		if w.Code != http.StatusFound || !strings.HasPrefix(loc, prefix) || !ts.srv.validFileKey(key, "127.0.0.1:8260", c.id) || rest != c.want+"?download=1" {
			t.Errorf("%s: redirect = %d %q", c.name, w.Code, loc)
			continue
		}
		var cookiePath string
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == fileCookieName {
				cookiePath = cookie.Path
			}
		}
		if cookiePath != prefix {
			t.Errorf("%s: companion cookie path = %q, want %q", c.name, cookiePath, prefix)
		}
		// Over plain HTTP the key alone opens; no such Task, so not found.
		if w := ts.do(http.MethodGet, loc, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: following %q = %d", c.name, loc, w.Code)
		}
	}
}
