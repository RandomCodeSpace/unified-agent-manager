package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultPathBranches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got := DefaultPath(); got != filepath.Join(dir, "uam", "sessions.json") {
		t.Fatalf("xdg path=%s", got)
	}
}

func TestPathsUpdatePruneAndHelpers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CONFIG_DIR", dir)
	if got := DefaultPath(); got != filepath.Join(dir, "sessions.json") {
		t.Fatalf("path=%s", got)
	}
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if s.Path() != filepath.Join(dir, "sessions.json") {
		t.Fatal(s.Path())
	}
	if ShortID("123456789") != "12345678" || Key("Claude", "abcdefghi") != "claude:abcdefgh" {
		t.Fatal("helpers")
	}
	if err := s.Update(func(cfg *Config) error {
		cfg.Sessions["a:old"] = SessionRecord{ID: "old", Agent: "a", SessionName: "missing", LastSeenAt: time.Now().Add(-10 * 24 * time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	PruneOld(&cfg, 7*24*time.Hour, func(string) bool { return false })
	if len(cfg.Sessions) != 0 {
		t.Fatalf("not pruned %+v", cfg.Sessions)
	}
}

func TestOpenAndSaveErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "sessions.json")); err == nil {
		t.Fatal("expected open mkdir error")
	}
	s := &Store{path: filepath.Join(file, "sessions.json")}
	if err := s.Save(DefaultConfig()); err == nil {
		t.Fatal("expected save mkdir error")
	}
}

func TestNormalizeMigrateBackupMoveAside(t *testing.T) {
	dir := t.TempDir()
	s := &Store{path: filepath.Join(dir, "sessions.json")}
	cfg := normalize(Config{})
	if cfg.SchemaVersion != CurrentSchemaVersion || cfg.DefaultAgent != "opencode" || cfg.UI.PeekWidth != 60 || cfg.Sessions == nil {
		t.Fatalf("bad normalize %+v", cfg)
	}
	old := Config{SchemaVersion: 0}
	if migrated := migrate(old); migrated.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("bad migrate %+v", migrated)
	}
	if err := os.WriteFile(s.path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.copyBackup(); err != nil {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "sessions.json.bak.*")); len(matches) == 0 {
		t.Fatal("no backup")
	}
	if err := s.moveAside(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatalf("file still exists/stat err %v", err)
	}
}
