package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectLocationStoredAndStreamed(t *testing.T) {
	st := openTestStore(t)
	m := startManager(t, st)
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	got, err := m.UpdateSettings(SettingsPatch{DefaultProjectDir: setting(dir + "/")})
	if err != nil || got.DefaultProjectDir != dir {
		t.Fatalf("save location = %+v, %v", got, err)
	}
	var streamed Settings
	if err := json.Unmarshal(frameOf(t, sub, "settings").data["settings"], &streamed); err != nil || streamed.DefaultProjectDir != dir {
		t.Fatalf("streamed location = %+v, %v", streamed, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := startManager(t, st)
	if got := loaded.Settings().DefaultProjectDir; got != dir {
		t.Fatalf("location after restart = %q, want %q", got, dir)
	}
	if _, err := loaded.UpdateSettings(SettingsPatch{DefaultProjectDir: setting("")}); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil || cfg.WebSettings.DefaultProjectDir != "" {
		t.Fatalf("cleared location = %q, %v", cfg.WebSettings.DefaultProjectDir, err)
	}
}

func TestProjectLocationRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	dir, explicit := t.TempDir(), t.TempDir()
	patch := func(body string, status int) {
		t.Helper()
		if w := ts.do(http.MethodPatch, "/api/settings", body, auth); w.Code != status {
			t.Fatalf("PATCH %s = %d %s, want %d", body, w.Code, w.Body, status)
		}
	}
	listing := func(query, want string) {
		t.Helper()
		w := ts.do(http.MethodGet, "/api/fs/dirs"+query, "", auth)
		var got DirList
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Path != want {
			t.Fatalf("listing %s = %d %s, want %q", query, w.Code, w.Body, want)
		}
	}
	patch(fmt.Sprintf(`{"default_project_dir":%q}`, dir), http.StatusOK)
	listing("", dir)
	listing("?path="+url.QueryEscape(explicit), explicit)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value  string
		status int
	}{
		{`null`, http.StatusBadRequest},
		{`42`, http.StatusBadRequest},
		{`true`, http.StatusBadRequest},
		{`"relative"`, http.StatusBadRequest},
		{`"/tmp/control\n"`, http.StatusBadRequest},
		{fmt.Sprintf("%q", file), http.StatusBadRequest},
		{fmt.Sprintf("%q", filepath.Join(dir, "missing")), http.StatusNotFound},
	} {
		patch(`{"send_default":"queue","default_project_dir":`+tc.value+`}`, tc.status)
		if got := ts.m.Settings(); got.DefaultProjectDir != dir || got.SendDefault != "steer" {
			t.Fatalf("invalid location changed settings: %+v", got)
		}
	}
	patch(`{"default_project_dir":""}`, http.StatusOK)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	listing("", filepath.Clean(home))
}

func TestConnectedProjectLocationStaysWithInstance(t *testing.T) {
	f := newConnectedTestFleet(t)
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	ab := a.connect(t, b)
	dirA, dirB := t.TempDir(), t.TempDir()
	a.request(t, http.MethodPatch, "/api/settings", map[string]string{"default_project_dir": dirA}, http.StatusOK)
	a.request(t, http.MethodPatch, connectedTestPath(ab, "/api/settings"), map[string]string{"default_project_dir": dirB}, http.StatusOK)
	if a.m.Settings().DefaultProjectDir != dirA || b.m.Settings().DefaultProjectDir != dirB || c.m.Settings().DefaultProjectDir != "" {
		t.Fatal("project locations crossed instance ownership")
	}
	for endpoint, want := range map[string]string{"/api/fs/dirs": dirA, connectedTestPath(ab, "/api/fs/dirs"): dirB} {
		_, data := a.request(t, http.MethodGet, endpoint, nil, http.StatusOK)
		var got DirList
		if err := json.Unmarshal(data, &got); err != nil || got.Path != want {
			t.Fatalf("%s listed %q, want %q: %v", endpoint, got.Path, want, err)
		}
	}
}
