package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A wrongly typed field anywhere in the file quarantines the whole file, as
// malformed JSON does, instead of loading half of it.
func TestLoadQuarantinesWronglyTypedNestedField(t *testing.T) {
	const record = `"id":"0f0e0d0c-1111-4222-8333-444455556666","agent":"copilot","mode":"safe","status":"active"`
	cases := map[string]string{
		"top level":         `{"schema_version":"four"}`,
		"session record":    `{"schema_version":4,"sessions":{"copilot:0f0e0d0c":{"id":1}}}`,
		"web state":         `{"schema_version":4,"sessions":{"copilot:0f0e0d0c":{` + record + `,"surface":"web","web":{"turn":1}}}}`,
		"profile overrides": `{"schema_version":4,"sessions":{"copilot:0f0e0d0c":{` + record + `,"profile_overrides":{"scrollback_lines":"many"}}}}`,
		"profile":           `{"schema_version":4,"profiles":{"work":{"mode":1}}}`,
		"web project":       `{"schema_version":4,"web_projects":{"p":{"id":"p","dir":1}}}`,
		"web settings":      `{"schema_version":4,"web_settings":{"terminal":"yes"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(filepath.Join(dir, "sessions.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := s.Load()
			if err != nil || len(cfg.Sessions) != 0 || len(cfg.Profiles) != 0 || len(cfg.WebProjects) != 0 || cfg.WebSettings.Terminal {
				t.Fatalf("Load = %+v, %v; want a fresh default config", cfg, err)
			}
			if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
				t.Fatalf("corrupt file left in place: %v", err)
			}
			backups, _ := filepath.Glob(filepath.Join(dir, "sessions.json.bak.*"))
			if len(backups) != 1 {
				t.Fatalf("backups = %v, want one", backups)
			}
			if kept, err := os.ReadFile(backups[0]); err != nil || string(kept) != raw {
				t.Fatalf("backup = %q, %v; want the original bytes", kept, err)
			}
		})
	}
}

// Save refuses a config that cannot be encoded and keeps the file it had.
func TestSaveRefusesUnencodableConfig(t *testing.T) {
	outOfRange := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	const id = "0f0e0d0c-1111-4222-8333-444455556666"
	cases := map[string]func(*Config){
		"session timestamp": func(cfg *Config) {
			cfg.Sessions["copilot:0f0e0d0c"] = SessionRecord{ID: id, Agent: "copilot", CreatedAt: outOfRange}
		},
		"web command result": func(cfg *Config) {
			cfg.Sessions["copilot:0f0e0d0c"] = SessionRecord{ID: id, Agent: "copilot", Surface: SurfaceWeb, Web: &WebState{CommandResult: json.RawMessage("{")}}
		},
		"project timestamp": func(cfg *Config) {
			cfg.WebProjects = map[string]WebProject{"p": {ID: "p", Dir: "/tmp/p", CreatedAt: outOfRange}}
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(filepath.Join(dir, "sessions.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Save(DefaultConfig()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(s.Path())
			if err != nil {
				t.Fatal(err)
			}
			cfg := DefaultConfig()
			spoil(&cfg)
			if err := s.Save(cfg); err == nil {
				t.Fatal("Save encoded an unencodable config")
			}
			if after, err := os.ReadFile(s.Path()); err != nil || string(after) != string(before) {
				t.Fatalf("file changed by a failed save: %q, %v", after, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.Contains(e.Name(), ".tmp.") {
					t.Fatalf("failed save left %s", e.Name())
				}
			}
		})
	}
}

// The unknown-field helpers report malformed JSON instead of dropping it.
func TestUnknownFieldHelpersRejectMalformedJSON(t *testing.T) {
	if _, err := mergeUnknownJSON([]byte("not json"), map[string]json.RawMessage{"future": json.RawMessage("1")}, knownConfigFields); err == nil {
		t.Fatal("mergeUnknownJSON accepted a malformed base")
	}
	if _, err := decodeUnknownJSON([]byte("[1]"), knownConfigFields); err == nil {
		t.Fatal("decodeUnknownJSON accepted a non-object")
	}
}
