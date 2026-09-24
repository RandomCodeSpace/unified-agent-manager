package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The packaging smoke invokes both modes in fresh source and release trees.
func TestEmbeddedFrontendPackaging(t *testing.T) {
	expect := os.Getenv("UAM_EXPECT_WEB_ASSETS")
	if expect == "" {
		if _, err := fs.Stat(embedded, "dist/index.html"); err == nil {
			expect = "built"
		} else {
			expect = "absent"
		}
	}
	s, err := NewServer(ServerConfig{Manager: &Manager{}, Token: "packaging-test"})
	if expect == "absent" {
		if err == nil || !strings.Contains(err.Error(), "web assets are not built; run make build or make install") {
			t.Fatalf("unbuilt source should explain how to build the UI, got %v", err)
		}
		return
	}
	if expect != "built" {
		t.Fatalf("unknown UAM_EXPECT_WEB_ASSETS: %q", expect)
	}
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	index := get("/")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `<div id="root">`) {
		t.Fatalf("embedded index: %d %s", index.Code, index.Body)
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^" ]+)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("embedded index must reference compiled JS and CSS")
	}
	for _, asset := range assets {
		if w := get(asset[1]); w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("embedded asset %s: %d, %d bytes", asset[1], w.Code, w.Body.Len())
		}
	}
	// The diagram frame: its own policy, and a classic script, since a module
	// script from the sandboxed frame's opaque origin cannot pass CORS.
	frame := get("/diagram-frame.html")
	if frame.Code != http.StatusOK || frame.Header().Get("Content-Security-Policy") != frameSecurity {
		t.Fatalf("embedded frame: %d %q", frame.Code, frame.Header().Get("Content-Security-Policy"))
	}
	if strings.Contains(frame.Body.String(), `type="module"`) {
		t.Fatalf("the frame must load a classic script, got %s", frame.Body)
	}
	script := regexp.MustCompile(`<script src="(/assets/diagram-frame-[^" ]+\.js)"></script>`).FindStringSubmatch(frame.Body.String())
	if script == nil {
		t.Fatalf("embedded frame must reference its hashed script: %s", frame.Body)
	}
	if w := get(script[1]); w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != contentSecurity || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("embedded frame script %s: %d %q %q", script[1], w.Code, w.Header().Get("Content-Security-Policy"), w.Header().Get("Content-Type"))
	}
}
