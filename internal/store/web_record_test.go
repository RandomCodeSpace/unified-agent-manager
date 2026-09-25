package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	for _, key := range []string{`"web_projects"`, `"project_id"`, `"model"`, `"title"`, `"stage"`, `"settled_at"`, `"archived_at"`, `"terminal_session"`} {
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
				Stage: "archived", SettledAt: now.Add(-time.Hour), ArchivedAt: now, TerminalSession: "0da22111-1111-4222-8333-444455556666",
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
		web.Stage != "archived" || !web.SettledAt.Equal(now.Add(-time.Hour)) || !web.ArchivedAt.Equal(now) || web.TerminalSession != "0da22111-1111-4222-8333-444455556666" {
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

// webSettingsField reads one web_settings key from the saved file; nil when absent.
func webSettingsField(t *testing.T, path, key string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		WebSettings map[string]json.RawMessage `json:"web_settings"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.WebSettings[key]
}

// Task defaults are one setting shared by every browser: absent loads as
// unset, a value survives a save, and invalid stored values are cleared.
func TestWebTaskDefaultsRoundTripAndLoadClean(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(settings string) {
		t.Helper()
		if err := os.WriteFile(s.Path(), []byte(`{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"web_settings":`+settings+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"send_default":"steer"}`)
	cfg, err := s.Load()
	if err != nil || cfg.WebSettings.TaskDefaults != (WebTaskDefaults{}) {
		t.Fatalf("absent task defaults loaded as %+v, %v", cfg.WebSettings.TaskDefaults, err)
	}
	want := WebTaskDefaults{Provider: "copilot", Model: "gpt-5", Effort: "high", ContextSize: "long_context", Mode: "yolo"}
	if err := s.Update(func(cfg *Config) error {
		cfg.WebSettings.TaskDefaults = want
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var saved WebTaskDefaults
	if raw := webSettingsField(t, s.Path(), "task_defaults"); json.Unmarshal(raw, &saved) != nil || saved != want {
		t.Fatalf("task defaults after save = %s", raw)
	}
	if cfg, err = s.Load(); err != nil || cfg.WebSettings.TaskDefaults != want {
		t.Fatalf("task defaults after reload = %+v, %v", cfg.WebSettings.TaskDefaults, err)
	}
	write(`{"task_defaults":{"provider":"copilot","model":"","effort":"","context_size":"","mode":"safe"}}`)
	if cfg, err = s.Load(); err != nil || cfg.WebSettings.TaskDefaults != (WebTaskDefaults{Provider: "copilot", ContextSize: "default", Mode: "safe"}) {
		t.Fatalf("unset context size after load = %+v, %v", cfg.WebSettings.TaskDefaults, err)
	}
	for name, defaults := range map[string]string{
		"noprovider": `{"provider":"","model":"a","effort":"","context_size":"default","mode":"safe"}`,
		"badmodel":   `{"provider":"copilot","model":"a\u001b","effort":"","context_size":"default","mode":"safe"}`,
		"badsize":    `{"provider":"copilot","model":"a","effort":"","context_size":"huge","mode":"safe"}`,
		"badmode":    `{"provider":"copilot","model":"a","effort":"","context_size":"default","mode":"auto"}`,
		"nomode":     `{"provider":"copilot","model":"a","effort":"","context_size":"default"}`,
	} {
		write(`{"task_defaults":` + defaults + `}`)
		if cfg, err = s.Load(); err != nil || cfg.WebSettings.TaskDefaults != (WebTaskDefaults{}) {
			t.Fatalf("%s task defaults after load = %+v, %v, want none", name, cfg.WebSettings.TaskDefaults, err)
		}
	}
}

// Per-Project defaults from before the setting migrate into it on load: the
// newest Project's valid ones when the setting is unset, never over a set one;
// every Project loses them and the next save writes none.
func TestWebProjectDefaultsMigrateIntoSettings(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	project := func(id, created, defaults string) string {
		return `"` + id + `":{"id":"` + id + `","name":"x","dir":"/tmp/` + id + `","created_at":"` + created + `","archived":true,"defaults":` + defaults + `}`
	}
	projects := `"web_projects":{` + strings.Join([]string{
		project("old", "2026-09-01T00:00:00Z", `{"provider":"copilot","model":"old","effort":"","context_size":"default","mode":"safe"}`),
		project("new", "2026-09-20T00:00:00Z", `{"provider":"copilot","model":"new","effort":"high","context_size":"","mode":"yolo"}`),
		project("newest-invalid", "2026-09-24T00:00:00Z", `{"provider":"","model":"bad","effort":"","context_size":"default","mode":"safe"}`),
		`"none":{"id":"none","name":"x","dir":"/tmp/none","created_at":"2026-09-25T00:00:00Z"}`,
	}, ",") + `}`
	write := func(settings string) {
		t.Helper()
		if err := os.WriteFile(s.Path(), []byte(`{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},`+settings+projects+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(``)
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.WebSettings.TaskDefaults; got != (WebTaskDefaults{Provider: "copilot", Model: "new", Effort: "high", ContextSize: "default", Mode: "yolo"}) {
		t.Fatalf("migrated task defaults = %+v", got)
	}
	for id, p := range cfg.WebProjects {
		if p.LegacyDefaults != (WebTaskDefaults{}) {
			t.Fatalf("project %s kept defaults after load: %+v", id, p.LegacyDefaults)
		}
	}
	if err := s.Update(func(cfg *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		WebProjects map[string]map[string]json.RawMessage `json:"web_projects"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded.WebProjects) != 4 {
		t.Fatalf("store after migration: %s, %v", data, err)
	}
	for id, p := range decoded.WebProjects {
		if _, has := p["defaults"]; has || (id != "none" && string(p["archived"]) != "true") {
			t.Fatalf("project %s after migration: %s", id, data)
		}
	}
	var migrated WebTaskDefaults
	if raw := webSettingsField(t, s.Path(), "task_defaults"); json.Unmarshal(raw, &migrated) != nil || migrated.Model != "new" {
		t.Fatalf("task defaults after migration = %s", raw)
	}
	// A set setting is kept over the Projects' defaults.
	write(`"web_settings":{"task_defaults":{"provider":"copilot","model":"chosen","effort":"","context_size":"default","mode":"safe"}},`)
	if cfg, err = s.Load(); err != nil || cfg.WebSettings.TaskDefaults.Model != "chosen" {
		t.Fatalf("set task defaults after load = %+v, %v", cfg.WebSettings.TaskDefaults, err)
	}
}

func TestWebSettingsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(raw string) {
		t.Helper()
		if err := os.WriteFile(s.Path(), []byte(`{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60}`+raw+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings := func() map[string]json.RawMessage {
		t.Helper()
		data, err := os.ReadFile(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		var out map[string]json.RawMessage
		if raw, ok := decoded["web_settings"]; ok {
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}

	write(``)
	if err := s.Update(func(cfg *Config) error {
		if cfg.WebSettings.SendDefault != "" {
			t.Fatalf("absent settings loaded as %+v", cfg.WebSettings)
		}
		cfg.UI.GroupByDir = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := settings(); got != nil {
		t.Fatalf("an unrelated write added web_settings: %v", got)
	}

	write(`,"web_settings":{"send_default":"steer","later_setting":"gpt-5-mini","title_model":{"copilot":"gpt-6-luna"}}`)
	if err := s.Update(func(cfg *Config) error {
		if cfg.WebSettings.SendDefault != WebSendSteer || cfg.WebSettings.TitleModel["copilot"] != "gpt-6-luna" {
			t.Fatalf("stored settings loaded as %+v", cfg.WebSettings)
		}
		cfg.WebSettings.SendDefault = WebSendQueue
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := settings()
	var titles map[string]string
	if err := json.Unmarshal(got["title_model"], &titles); err != nil || string(got["send_default"]) != `"queue"` || string(got["later_setting"]) != `"gpt-5-mini"` || titles["copilot"] != "gpt-6-luna" || len(titles) != 1 {
		t.Fatalf("web_settings after save = %s, %v", got, err)
	}
	if cfg, err := s.Load(); err != nil || cfg.WebSettings.SendDefault != WebSendQueue {
		t.Fatalf("send default after reload = %+v, %v", cfg.WebSettings, err)
	}

	for _, value := range []string{`"interrupt"`, `""`, `"Steer"`} {
		write(`,"web_settings":{"send_default":` + value + `}`)
		cfg, err := s.Load()
		var want string
		if err := json.Unmarshal([]byte(value), &want); err != nil {
			t.Fatal(err)
		}
		if err != nil || cfg.WebSettings.SendDefault != want {
			t.Fatalf("send default %s loaded as %+v, %v", value, cfg.WebSettings, err)
		}
		if err := s.Update(func(cfg *Config) error {
			cfg.UI.GroupByDir = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := settings(); want != "" && string(got["send_default"]) != value {
			t.Fatalf("unrelated write changed unknown send default %s: %v", value, got)
		}
	}
}

// Hidden models load sorted, without duplicates or invalid IDs and within
// the caps, and round-trip with keys a newer uam wrote.
func TestWebHiddenModelsLoadClean(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	many := make([]string, MaxHiddenModels+5)
	for i := range many {
		many[i] = strconv.Quote(fmt.Sprintf("m%03d", i))
	}
	long := strconv.Quote(strings.Repeat("x", MaxHiddenModelBytes+1))
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"web_settings":{"later_setting":"t","hidden_models":{` +
		`"copilot":["b","a","b","","bad\u0007",` + long + `],"empty":[""],"":["a"],"many":[` + strings.Join(many, ",") + `]}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	hidden := cfg.WebSettings.HiddenModels
	if len(hidden) != 2 || strings.Join(hidden["copilot"], ",") != "a,b" || len(hidden["many"]) != MaxHiddenModels || hidden["many"][0] != "m000" {
		t.Fatalf("loaded hidden models = %v", hidden)
	}
	if err := s.Update(func(cfg *Config) error {
		cfg.WebSettings.HiddenModels["copilot"] = []string{"c"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		WebSettings struct {
			LaterSetting string              `json:"later_setting"`
			HiddenModels map[string][]string `json:"hidden_models"`
		} `json:"web_settings"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if w := saved.WebSettings; w.LaterSetting != "t" || len(w.HiddenModels) != 2 || strings.Join(w.HiddenModels["copilot"], ",") != "c" || len(w.HiddenModels["many"]) != MaxHiddenModels {
		t.Fatalf("saved web_settings = %s", data)
	}
}

// Title models load without entries whose provider or model ID is empty,
// too long or holds a control character.
func TestWebTitleModelsLoadClean(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	long := strconv.Quote(strings.Repeat("x", maxWebModelBytes+1))
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"web_settings":{"title_model":{` +
		`"copilot":"gpt-6-luna","empty":"","":"m","bell":"a\u0007","long":` + long + `}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.WebSettings.TitleModel; len(got) != 1 || got["copilot"] != "gpt-6-luna" {
		t.Fatalf("loaded title models = %v", got)
	}
	if err := os.WriteFile(s.Path(), []byte(strings.Replace(raw, `"copilot":"gpt-6-luna",`, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := s.Load(); err != nil || cfg.WebSettings.TitleModel != nil {
		t.Fatalf("only invalid title models loaded as %v, %v", cfg.WebSettings.TitleModel, err)
	}
}

// Custom models load without invalid entries and round-trip with keys a
// newer uam wrote; no field holds a key.
func TestWebCustomModelsLoadClean(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},"web_settings":{"later_setting":"t","custom_models":[` +
		`{"name":"acme","display_name":"Acme Coder","base_url":"https://llm.example/v1","model_id":"coder","api_key_env":"UAM_BYOM_ACME"},` +
		`{"name":"acme","base_url":"https://other.example/v1","model_id":"other","api_key_env":"UAM_BYOM_ACME"},` +
		`{"name":"bad/name","base_url":"https://llm.example/v1","model_id":"m","api_key_env":"UAM_BYOM_K"},` +
		`{"name":"acme","display_name":"Dup","base_url":"https://llm.example/v1","model_id":"coder","api_key_env":"UAM_BYOM_ACME"},` +
		`{"name":"local","base_url":"http://127.0.0.1:8080/v1","model_id":"org/m","wire_api":"responses","api_key_env":"UAM_BYOM_LOCAL"}]}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.WebSettings.CustomModels
	if len(got) != 2 || got[0].DisplayName != "Acme Coder" || got[1].Name != "local" || got[1].WireAPI != "responses" || got[1].ModelID != "org/m" {
		t.Fatalf("loaded custom models = %+v", got)
	}
	if err := s.Update(func(cfg *Config) error {
		cfg.WebSettings.CustomModels = cfg.WebSettings.CustomModels[:1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		WebSettings struct {
			LaterSetting string           `json:"later_setting"`
			CustomModels []WebCustomModel `json:"custom_models"`
		} `json:"web_settings"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if w := saved.WebSettings; w.LaterSetting != "t" || len(w.CustomModels) != 1 || w.CustomModels[0] != got[0] {
		t.Fatalf("saved web_settings = %s", data)
	}
}

func TestValidCustomModels(t *testing.T) {
	ok := WebCustomModel{Name: "acme", BaseURL: "https://llm.example/v1", ModelID: "coder", APIKeyEnv: "UAM_BYOM_ACME"}
	if err := ValidCustomModels([]WebCustomModel{ok}); err != nil {
		t.Fatal(err)
	}
	with := func(f func(*WebCustomModel)) WebCustomModel { m := ok; f(&m); return m }
	for name, m := range map[string]WebCustomModel{
		"slash name": with(func(m *WebCustomModel) { m.Name = "a/b" }),
		"empty name": with(func(m *WebCustomModel) { m.Name = "" }),
		"long name":  with(func(m *WebCustomModel) { m.Name = strings.Repeat("a", MaxCustomModelNameBytes+1) }),
		"ftp url":    with(func(m *WebCustomModel) { m.BaseURL = "ftp://llm.example/v1" }),
		"no host":    with(func(m *WebCustomModel) { m.BaseURL = "https:///v1" }),
		"userinfo":   with(func(m *WebCustomModel) { m.BaseURL = "https://user:pw@llm.example/v1" }),
		"query":      with(func(m *WebCustomModel) { m.BaseURL = "https://llm.example/v1?key=x" }),
		"fragment":   with(func(m *WebCustomModel) { m.BaseURL = "https://llm.example/v1#x" }),
		"long url": with(func(m *WebCustomModel) {
			m.BaseURL = "https://llm.example/" + strings.Repeat("a", MaxCustomModelURLBytes)
		}),
		"empty model":     with(func(m *WebCustomModel) { m.ModelID = "" }),
		"spaced model":    with(func(m *WebCustomModel) { m.ModelID = "a b" }),
		"wire api":        with(func(m *WebCustomModel) { m.WireAPI = "chat" }),
		"env digit":       with(func(m *WebCustomModel) { m.APIKeyEnv = "1KEY" }),
		"env no prefix":   with(func(m *WebCustomModel) { m.APIKeyEnv = "GITHUB_TOKEN" }),
		"env bare prefix": with(func(m *WebCustomModel) { m.APIKeyEnv = "UAM_BYOM_" }),
		"env dash":        with(func(m *WebCustomModel) { m.APIKeyEnv = "UAM_BYOM_MY-KEY" }),
		"env empty":       with(func(m *WebCustomModel) { m.APIKeyEnv = "" }),
		"control in name": with(func(m *WebCustomModel) { m.DisplayName = "a\u0007" }),
	} {
		if ValidCustomModels([]WebCustomModel{m}) == nil {
			t.Errorf("%s: %+v accepted", name, m)
		}
	}
	if ValidCustomModels([]WebCustomModel{ok, ok}) == nil {
		t.Error("duplicate selection ID accepted")
	}
	if ValidCustomModels([]WebCustomModel{ok, with(func(m *WebCustomModel) { m.ModelID = "x"; m.APIKeyEnv = "UAM_BYOM_OTHER" })}) == nil {
		t.Error("one provider with two key variables accepted")
	}
	many := make([]WebCustomModel, MaxCustomModels+1)
	for i := range many {
		many[i] = with(func(m *WebCustomModel) { m.ModelID = fmt.Sprintf("m%d", i) })
	}
	if ValidCustomModels(many) == nil {
		t.Error("too many custom models accepted")
	}
}
