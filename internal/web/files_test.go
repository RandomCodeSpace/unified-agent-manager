package web

import (
	"bytes"
	"net/http"
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
	real := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(real, "shots"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	png := pngBytes(t)
	files := map[string][]byte{
		filepath.Join(real, "sky-dodge.png"):  png,
		filepath.Join(real, "shots", "a.PNG"): png,
		filepath.Join(real, "tiny.webp"):      webpBytes,
		filepath.Join(real, "fake.png"):       []byte("<html><script>alert(1)</script></html>"),
		filepath.Join(real, "notes.txt"):      []byte("plain text"),
		filepath.Join(real, "logo.svg"):       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		filepath.Join(real, "wrong-ext.jpg"):  png,
		filepath.Join(real, "empty.png"):      nil,
		filepath.Join(outside, "secret.png"):  png,
		filepath.Join(real, "shots", "x.txt"): []byte("x"),
	}
	for name, data := range files {
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(real, "inside-link.png"):  filepath.Join(real, "sky-dodge.png"),
		filepath.Join(real, "outside-link.png"): filepath.Join(outside, "secret.png"),
		filepath.Join(real, "outside-dir"):      outside,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: addProject(t, ts.m, link), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	return sum, real, outside
}

func rawURL(id, p string) string {
	return "/api/sessions/" + id + "/files/raw?path=" + url.QueryEscape(p)
}

func TestRawImageServesImagesInsideTheTaskDirectory(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, real, _ := rawImageTask(t, ts)
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
		filepath.Join(real, "sky-dodge.png"):      {png, "image/png"},
		filepath.Join(real, "inside-link.png"):    {png, "image/png"},
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
	sum, real, outside := rawImageTask(t, ts)

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
		filepath.Join(real, "notes.txt"):     http.StatusUnsupportedMediaType,
	}
	for p, want := range rejected {
		w := ts.do(http.MethodGet, rawURL(sum.ID, p), "", auth)
		if w.Code != want || !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), real) {
			t.Errorf("GET %q = %d %s, want %d", p, w.Code, w.Body, want)
		}
	}

	// A file over the cap is refused before any byte is read.
	big := filepath.Join(real, "big.png")
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

	// The route follows the API's checks: session, cookie, Host and method.
	if w := ts.do(http.MethodGet, rawURL("nope", "sky-dodge.png"), "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown task = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, rawURL(sum.ID, "sky-dodge.png"), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("without cookie = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, rawURL(sum.ID, "sky-dodge.png"), "", auth, withHost("evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("foreign host = %d", w.Code)
	}
	// The route is GET only; another method falls through to the API's 404.
	if w := ts.do(http.MethodPost, rawURL(sum.ID, "sky-dodge.png"), "{}", auth); w.Code != http.StatusNotFound || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("POST = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	// Another Task's directory does not hold the file.
	other, _ := createSession(t, ts.m, ts.prov)
	if w := ts.do(http.MethodGet, rawURL(other.ID, filepath.Join(real, "sky-dodge.png")), "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("another task = %d", w.Code)
	}
}

// viewTask makes a Task whose directory holds a page with sibling assets,
// files of other kinds, a directory and links leading out of it.
func viewTask(t *testing.T, ts *testServer) (SessionSummary, string) {
	t.Helper()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(real, "out"), filepath.Join(real, "assets"), outside} {
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
		if err := os.WriteFile(filepath.Join(real, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"inside-link.md": filepath.Join(real, "notes.md"),
		"outside-link.x": filepath.Join(outside, "secret.x"),
		"outside-dir":    outside,
	} {
		if err := os.Symlink(target, filepath.Join(real, link)); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: addProject(t, ts.m, real), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	return sum, real
}

func viewURL(id, p string) string { return "/api/sessions/" + id + "/files/view/" + p }

func TestViewFileServesAnyFileOfTheTaskDirectorySandboxed(t *testing.T) {
	ts := newTestServer(t, ServerConfig{NoAuth: true})
	sum, real := viewTask(t, ts)

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
		w := ts.do(http.MethodGet, viewURL(sum.ID, p), "")
		h := w.Header()
		name, _ := url.PathUnescape(p)
		body, _ := os.ReadFile(filepath.Join(real, name))
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), body) || h.Get("Content-Type") != want.mime ||
			h.Get("Content-Security-Policy") != viewSecurity || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "private, no-cache" ||
			!strings.HasPrefix(h.Get("Content-Disposition"), want.disposition+"; filename=") {
			t.Errorf("GET %s = %d %v %q", p, w.Code, h, w.Body.String()[:min(w.Body.Len(), 80)])
		}
	}
	if w := ts.do(http.MethodGet, viewURL(sum.ID, "notes.md"), "", withHeader("Range", "bytes=2-")); w.Code != http.StatusPartialContent || w.Body.String() != "Notes" {
		t.Fatalf("range = %d %q", w.Code, w.Body)
	}

	for _, p := range []string{
		"", "out", "out/", "missing.html", "outside-link.x", "outside-dir/secret.x",
		"..%2Foutside%2Fsecret.x", "out%2F..%2F..%2Foutside%2Fsecret.x", "%2Fetc%2Fpasswd", "%00",
	} {
		w := ts.do(http.MethodGet, viewURL(sum.ID, p), "")
		if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), real) {
			t.Errorf("GET %q = %d %s", p, w.Code, w.Body)
		}
	}
	// A literal .. is cleaned away by the mux before any handler runs.
	if w := ts.do(http.MethodGet, viewURL(sum.ID, "../../../../etc/passwd"), ""); w.Code == http.StatusOK && strings.Contains(w.Body.String(), "root:") {
		t.Fatalf("dot-dot = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, viewURL("nope", "notes.md"), ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown task = %d", w.Code)
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
	if w := ts.do(http.MethodGet, keyURL(sum.ID, key, "notes.md"), "", withHost("localhost:8260")); w.Code != http.StatusUnauthorized {
		t.Fatalf("other host = %d", w.Code)
	}
	// The key opens only this GET route: not another method, not another route.
	if w := ts.do(http.MethodPost, keyURL(sum.ID, key, "notes.md"), "{}"); w.Code != http.StatusUnauthorized {
		t.Fatalf("keyed POST = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/files/raw?path=shot.png&key="+key, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("key on the raw route = %d", w.Code)
	}
}
