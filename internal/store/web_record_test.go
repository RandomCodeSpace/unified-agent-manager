package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebRecordRoundTripKeepsSurfaceStateAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"sessions":{"copilot:0f0e0d0c":{
		"id":"0f0e0d0c-1111-4222-8333-444455556666","agent":"copilot","name":"web task","mode":"safe","workdir":"/tmp/repo",
		"tmux_session":"","created_at":"` + now.Format(time.RFC3339) + `","last_seen_at":"` + now.Format(time.RFC3339) + `",
		"pinned":false,"group":"","sort_index":0,"status":"active","provider_session_id":"conv_1",
		"surface":"web","web":{"turn":"working","request_id":"req-1","request_status":"accepted","updated_at":"` + now.Format(time.RFC3339) + `","detail":"short"},
		"future_field":{"kept":true}}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions["copilot:0f0e0d0c"]
	if rec.Surface != SurfaceWeb || rec.Web == nil {
		t.Fatalf("web fields not loaded: %+v", rec)
	}
	if rec.Web.Turn != "working" || rec.Web.RequestID != "req-1" || rec.Web.RequestStatus != "accepted" || rec.Web.Detail != "short" || !rec.Web.UpdatedAt.Equal(now) {
		t.Fatalf("web state = %+v", *rec.Web)
	}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Sessions map[string]map[string]json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	fields := decoded.Sessions["copilot:0f0e0d0c"]
	if string(fields["surface"]) != `"web"` {
		t.Fatalf("surface not persisted: %s", data)
	}
	var web WebState
	if err := json.Unmarshal(fields["web"], &web); err != nil || web.RequestStatus != "accepted" || web.Turn != "working" {
		t.Fatalf("web state not persisted: %s (%v)", fields["web"], err)
	}
	var future map[string]bool
	if err := json.Unmarshal(fields["future_field"], &future); err != nil || !future["kept"] {
		t.Fatalf("unknown record field did not round-trip: %s", data)
	}
}

func TestOlderWebConfigRoundTripsWithoutProjectFields(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"sessions":{"copilot:0f0e0d0c":{
		"id":"0f0e0d0c-1111-4222-8333-444455556666","agent":"copilot","name":"web task","mode":"safe","workdir":"/tmp/repo",
		"tmux_session":"","created_at":"2026-09-01T00:00:00Z","last_seen_at":"2026-09-01T00:00:00Z",
		"pinned":false,"group":"","sort_index":0,"status":"active","provider_session_id":"conv_1",
		"surface":"web","web":{"turn":"completed","updated_at":"2026-09-01T00:00:00Z"}}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebProjects != nil {
		t.Fatalf("projects appeared from nowhere: %+v", cfg.WebProjects)
	}
	if web := cfg.Sessions["copilot:0f0e0d0c"].Web; web == nil || web.ProjectID != "" || web.Model != "" || web.Title != "" || web.Turn != "completed" || web.Stage != "" || !web.SettledAt.IsZero() {
		t.Fatalf("web state = %+v", web)
	}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"web_projects"`, `"project_id"`, `"model"`, `"title"`, `"stage"`, `"settled_at"`, `"archived_at"`} {
		if strings.Contains(string(data), key) {
			t.Fatalf("older config gained %s on save: %s", key, data)
		}
	}
}

func TestWebProjectsAndTaskFieldsPersist(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	project := WebProject{ID: "7d0c5a4e-1111-4222-8333-444455556666", Name: "repo", Dir: "/tmp/repo", CreatedAt: now}
	if err := s.Update(func(cfg *Config) error {
		cfg.WebProjects = map[string]WebProject{project.ID: project}
		cfg.Sessions["copilot:0f0e0d0c"] = SessionRecord{
			ID: "0f0e0d0c-1111-4222-8333-444455556666", Agent: "copilot", Mode: ModeSafe, Workdir: "/tmp/repo",
			Status: StatusActive, Surface: SurfaceWeb, ProviderSessionID: "conv_1",
			Web: &WebState{
				Turn: "idle", UpdatedAt: now, ProjectID: project.ID, Model: "gpt-5-mini", Effort: "high", ContextSize: "long_context", Title: "Fix the build",
				Stage: "archived", SettledAt: now.Add(-time.Hour), ArchivedAt: now,
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.WebProjects[project.ID]; got.ID != project.ID || got.Name != project.Name || got.Dir != project.Dir || !got.CreatedAt.Equal(project.CreatedAt) {
		t.Fatalf("project = %+v, want %+v", got, project)
	}
	web := cfg.Sessions["copilot:0f0e0d0c"].Web
	if web == nil || web.ProjectID != project.ID || web.Model != "gpt-5-mini" || web.Effort != "high" || web.ContextSize != "long_context" || web.Title != "Fix the build" ||
		web.Stage != "archived" || !web.SettledAt.Equal(now.Add(-time.Hour)) || !web.ArchivedAt.Equal(now) {
		t.Fatalf("web state = %+v", web)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["web_projects"]; !ok || len(cfg.unknown) != 0 {
		t.Fatalf("web_projects not a modeled top-level field: unknown=%v file=%s", cfg.unknown, data)
	}
}

// A newer uam may add fields inside web state and projects; saving here must
// keep them, also after a writer changed the modeled fields.
func TestNestedWebUnknownFieldsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},
		"web_projects":{"p1":{"id":"p1","name":"repo","dir":"/tmp/repo","created_at":"2026-09-01T00:00:00Z","archived":true}},
		"sessions":{"copilot:0f0e0d0c":{
		"id":"0f0e0d0c-1111-4222-8333-444455556666","agent":"copilot","name":"","mode":"safe","workdir":"/tmp/repo",
		"tmux_session":"","created_at":"2026-09-01T00:00:00Z","last_seen_at":"2026-09-01T00:00:00Z",
		"pinned":false,"group":"","sort_index":0,"status":"active","provider_session_id":"conv_1",
		"surface":"web","web":{"turn":"idle","updated_at":"2026-09-01T00:00:00Z","project_id":"p1","sort":{"pinned":3}}}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(cfg *Config) error {
		p := cfg.WebProjects["p1"]
		p.Name = "renamed"
		cfg.WebProjects["p1"] = p
		rec := cfg.Sessions["copilot:0f0e0d0c"]
		web := *rec.Web
		web.Update(WebState{Turn: "completed", ProjectID: "p1", Title: "t"})
		rec.Web = &web
		cfg.Sessions["copilot:0f0e0d0c"] = rec
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		WebProjects map[string]map[string]json.RawMessage `json:"web_projects"`
		Sessions    map[string]struct {
			Web map[string]json.RawMessage `json:"web"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	project := decoded.WebProjects["p1"]
	if string(project["archived"]) != "true" || string(project["name"]) != `"renamed"` {
		t.Fatalf("project fields after save: %s", data)
	}
	web := decoded.Sessions["copilot:0f0e0d0c"].Web
	var sort map[string]int
	if err := json.Unmarshal(web["sort"], &sort); err != nil || sort["pinned"] != 3 || string(web["turn"]) != `"completed"` || string(web["title"]) != `"t"` {
		t.Fatalf("web fields after save: %s", data)
	}
}

// Task defaults are additive: a Project written before them loads with none,
// a Project without them writes no key, and unknown Project fields survive.
func TestWebProjectDefaultsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},
		"web_projects":{
			"p1":{"id":"p1","name":"repo","dir":"/tmp/repo","created_at":"2026-09-01T00:00:00Z","archived":true},
			"p2":{"id":"p2","name":"other","dir":"/tmp/other","created_at":"2026-09-01T00:00:00Z"}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebProjects["p1"].Defaults != (WebTaskDefaults{}) {
		t.Fatalf("old project loaded defaults: %+v", cfg.WebProjects["p1"])
	}
	want := WebTaskDefaults{Provider: "copilot", Model: "gpt-5", Effort: "high", ContextSize: "long_context", Mode: "yolo"}
	if err := s.Update(func(cfg *Config) error {
		p := cfg.WebProjects["p1"]
		p.Defaults = want
		cfg.WebProjects["p1"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		WebProjects map[string]map[string]json.RawMessage `json:"web_projects"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	var saved map[string]string
	if p1 := decoded.WebProjects["p1"]; string(p1["archived"]) != "true" || json.Unmarshal(p1["defaults"], &saved) != nil || len(saved) != 5 ||
		saved["provider"] != "copilot" || saved["model"] != "gpt-5" || saved["effort"] != "high" || saved["context_size"] != "long_context" || saved["mode"] != "yolo" {
		t.Fatalf("p1 after save: %s", data)
	}
	if _, ok := decoded.WebProjects["p2"]["defaults"]; ok {
		t.Fatalf("project without defaults wrote them: %s", data)
	}
	if cfg, err = s.Load(); err != nil || cfg.WebProjects["p1"].Defaults != want {
		t.Fatalf("defaults after reload = %+v, %v", cfg.WebProjects["p1"].Defaults, err)
	}
}

func TestInvalidWebProjectDefaultsAreClearedOnLoad(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	project := func(id, defaults string) string {
		return `"` + id + `":{"id":"` + id + `","name":"x","dir":"/tmp/` + id + `","created_at":"2026-09-01T00:00:00Z","defaults":` + defaults + `}`
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"web_projects":{` + strings.Join([]string{
		project("unset", `{"provider":"copilot","model":"","effort":"","context_size":"","mode":"safe"}`),
		project("noprovider", `{"provider":"","model":"a","effort":"","context_size":"default","mode":"safe"}`),
		project("badmodel", `{"provider":"copilot","model":"a\u001b","effort":"","context_size":"default","mode":"safe"}`),
		project("badsize", `{"provider":"copilot","model":"a","effort":"","context_size":"huge","mode":"safe"}`),
		project("badmode", `{"provider":"copilot","model":"a","effort":"","context_size":"default","mode":"auto"}`),
		project("nomode", `{"provider":"copilot","model":"a","effort":"","context_size":"default"}`),
	}, ",") + `}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebProjects) != 6 {
		t.Fatalf("projects after load = %+v", cfg.WebProjects)
	}
	if got := cfg.WebProjects["unset"].Defaults; got != (WebTaskDefaults{Provider: "copilot", ContextSize: "default", Mode: "safe"}) {
		t.Fatalf("valid defaults after load = %+v", got)
	}
	for _, id := range []string{"noprovider", "badmodel", "badsize", "badmode", "nomode"} {
		if got := cfg.WebProjects[id].Defaults; got != (WebTaskDefaults{}) {
			t.Fatalf("%s defaults after load = %+v, want none", id, got)
		}
	}
}

func TestInvalidWebProjectsAndReferencesAreDroppedOnLoad(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},
		"web_projects":{
			"good":{"id":"good","name":"ok","dir":"/tmp/ok","created_at":"2026-09-01T00:00:00Z"},
			"rel":{"id":"rel","name":"x","dir":"relative","created_at":"2026-09-01T00:00:00Z"},
			"mismatch":{"id":"other","name":"x","dir":"/tmp/x","created_at":"2026-09-01T00:00:00Z"},
			"bad;id":{"id":"bad;id","name":"x","dir":"/tmp/x","created_at":"2026-09-01T00:00:00Z"}},
		"sessions":{"copilot:0f0e0d0c":{
		"id":"0f0e0d0c-1111-4222-8333-444455556666","agent":"copilot","name":"","mode":"safe","workdir":"/tmp/ok",
		"tmux_session":"","created_at":"2026-09-01T00:00:00Z","last_seen_at":"2026-09-01T00:00:00Z",
		"pinned":false,"group":"","sort_index":0,"status":"active","provider_session_id":"conv_1",
		"surface":"web","web":{"turn":"idle","updated_at":"2026-09-01T00:00:00Z","project_id":"x;rm","model":"bad\u001bmodel","effort":"high\u001b","context_size":"bogus","title":"t"}}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebProjects) != 1 || cfg.WebProjects["good"].Dir != "/tmp/ok" {
		t.Fatalf("projects after load = %+v", cfg.WebProjects)
	}
	rec, ok := cfg.Sessions["copilot:0f0e0d0c"]
	if !ok || rec.Web == nil || rec.Web.ProjectID != "" || rec.Web.Model != "" || rec.Web.Effort != "" || rec.Web.ContextSize != "" || rec.Web.Title != "t" {
		t.Fatalf("record after load = %+v web %+v", rec, rec.Web)
	}
}

func TestTerminalRecordOmitsWebFields(t *testing.T) {
	data, err := json.Marshal(SessionRecord{ID: "abc", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"surface"`) || strings.Contains(string(data), `"web"`) {
		t.Fatalf("terminal record gained web keys: %s", data)
	}
}

func TestPruneOldNeverDeletesSurfaceRecords(t *testing.T) {
	old := time.Now().Add(-365 * 24 * time.Hour)
	cfg := DefaultConfig()
	cfg.Sessions["claude:dead0001"] = SessionRecord{ID: "dead0001", Agent: "claude", SessionName: "uam-claude-dead0001", LastSeenAt: old}
	cfg.Sessions["copilot:web00001"] = SessionRecord{ID: "web00001", Agent: "copilot", Surface: SurfaceWeb, LastSeenAt: old}
	cfg.Sessions["opencode:future01"] = SessionRecord{ID: "future01", Agent: "opencode", Surface: "future", LastSeenAt: old}
	PruneOld(&cfg, time.Hour, func(string) bool { return false })
	if _, ok := cfg.Sessions["claude:dead0001"]; ok {
		t.Fatal("stale terminal record should be pruned")
	}
	for _, key := range []string{"copilot:web00001", "opencode:future01"} {
		if _, ok := cfg.Sessions[key]; !ok {
			t.Fatalf("record %s with a surface was pruned", key)
		}
	}
}
