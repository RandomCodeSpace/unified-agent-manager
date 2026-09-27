package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadRaw writes raw as the config file and loads it.
func loadRaw(t *testing.T, raw string) Config {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A bad web selection costs a record only the bad values, never the record.
func TestLoadClearsInvalidWebSelectionKeepsRecord(t *testing.T) {
	long := strings.Repeat("e", maxWebModelBytes+1)
	record := func(id, web string) string {
		return `{"id":"` + id + `","agent":"copilot","mode":"safe","status":"active","surface":"web","web":` + web + `}`
	}
	cfg := loadRaw(t, `{"schema_version":4,"sessions":{
		"copilot:aaaaaaaa":`+record("aaaaaaaa-1111-4222-8333-444455556666", `{"project_id":"p;rm","model":"m\u0001","effort":"`+long+`","context_size":"huge","title":"kept"}`)+`,
		"copilot:bbbbbbbb":`+record("bbbbbbbb-1111-4222-8333-444455556666", `{"project_id":"p1","model":"gpt-5","effort":"high","context_size":"bogus"}`)+`,
		"copilot:cccccccc":`+record("cccccccc-1111-4222-8333-444455556666", `{"project_id":"p1","model":"gpt-5","effort":"high","context_size":"long_context"}`)+`}}`)
	if len(cfg.Sessions) != 3 {
		t.Fatalf("sessions = %+v, want all three kept", cfg.Sessions)
	}
	cases := []struct {
		key                                    string
		project, model, effort, context, title string
	}{
		{"copilot:aaaaaaaa", "", "", "", "", "kept"},
		{"copilot:bbbbbbbb", "p1", "gpt-5", "high", "", ""},
		{"copilot:cccccccc", "p1", "gpt-5", "high", "long_context", ""},
	}
	for _, tc := range cases {
		web := cfg.Sessions[tc.key].Web
		if web == nil || web.ProjectID != tc.project || web.Model != tc.model || web.Effort != tc.effort || web.ContextSize != tc.context || web.Title != tc.title {
			t.Errorf("%s web = %+v, want project %q model %q effort %q context %q title %q", tc.key, web, tc.project, tc.model, tc.effort, tc.context, tc.title)
		}
	}
}

// Web projects whose ID or directory could not be used safely are dropped.
func TestLoadDropsInvalidWebProjects(t *testing.T) {
	cfg := loadRaw(t, `{"schema_version":4,"web_projects":{
		"good":{"id":"good","dir":"/srv/good"},
		"mismatch":{"id":"other","dir":"/srv/a"},
		"empty-id":{"id":"","dir":"/srv/b"},
		"p;rm":{"id":"p;rm","dir":"/srv/c"},
		"relative":{"id":"relative","dir":"srv/d"},
		"no-dir":{"id":"no-dir","dir":""},
		"control":{"id":"control","dir":"/srv/\u0007e"}}}`)
	if len(cfg.WebProjects) != 1 || cfg.WebProjects["good"].Dir != "/srv/good" {
		t.Fatalf("web projects = %+v, want only good", cfg.WebProjects)
	}
}
