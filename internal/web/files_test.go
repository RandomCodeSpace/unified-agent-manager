package web

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
