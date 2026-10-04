package web

import (
	"crypto/sha256"
	"encoding/base64"
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
	m, _, _ := newTestManager(t)
	s, err := NewServer(ServerConfig{Manager: m, Token: testToken})
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
	t.Cleanup(s.Close)
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
	// The diagram frame: one classic inline script, since the frame's own
	// requests carry no cookie a sign-in proxy would accept, and a policy that
	// allows exactly that script by its hash.
	frame := get("/diagram-frame.html")
	body := frame.Body.String()
	scripts := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllStringSubmatch(body, -1)
	if frame.Code != http.StatusOK || len(scripts) != 1 || strings.TrimSpace(scripts[0][1]) != "" || len(scripts[0][2]) == 0 {
		t.Fatalf("embedded frame must hold exactly one inline classic script: %d, %d scripts", frame.Code, len(scripts))
	}
	sum := sha256.Sum256([]byte(scripts[0][2]))
	if policy := frame.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"';") {
		t.Fatalf("embedded frame policy does not pin its script: %q", policy)
	}
}
