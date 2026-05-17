package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingReturnsDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema = %d, want %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	if cfg.DefaultAgent != "claude" {
		t.Fatalf("default agent = %q", cfg.DefaultAgent)
	}
	if cfg.Sessions == nil {
		t.Fatal("sessions map is nil")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	cfg := DefaultConfig()
	cfg.Sessions[Key("claude", "12345678")] = SessionRecord{
		ID:          "12345678-1234-4234-9234-123456789abc",
		Agent:       "claude",
		Name:        "fix tests",
		Mode:        ModeYolo,
		Workdir:     "/tmp/repo",
		TmuxSession: "uam-claude-12345678",
		CreatedAt:   now,
		LastSeenAt:  now,
		Pinned:      true,
		Group:       "repo",
		SortIndex:   7,
		PR:          &PRRecord{URL: "https://github.com/o/r/pull/1", Number: 1, LastStatus: "open", LastChecked: now},
	}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.Sessions[Key("claude", "12345678")]
	if got.Name != "fix tests" || !got.Pinned || got.PR == nil || got.PR.Number != 1 {
		t.Fatalf("loaded record mismatch: %+v", got)
	}
}

func TestLoadCorruptJSONBacksUpAndStartsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Sessions) != 0 {
		t.Fatalf("sessions = %d, want 0", len(cfg.Sessions))
	}
	matches, err := filepath.Glob(filepath.Join(dir, "sessions.json.bak.*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("backup matches=%v err=%v", matches, err)
	}
}

func TestMigrateCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	old := map[string]any{"schema_version": 0, "sessions": map[string]any{}}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema = %d", cfg.SchemaVersion)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "sessions.json.bak.*"))
	if len(matches) != 1 {
		t.Fatalf("backups = %v", matches)
	}
}
