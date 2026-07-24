package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaV3BaselineLoadsCurrentRecordsAndTopLevelUnknown(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "sessions.json")
	fixture, err := os.ReadFile(filepath.Join("testdata", "schema-v3-full.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Then
	record, ok := cfg.Sessions["claude:12345678"]
	if !ok {
		t.Fatal("schema-v3 session was dropped")
	}
	if record.ID != "12345678-1234-4234-9234-123456789abc" || record.ProviderSessionID != "provider_session_12345678" {
		t.Fatalf("schema-v3 identity changed: %+v", record)
	}
	if record.LastExitCode == nil || *record.LastExitCode != 23 || record.PR == nil || record.PR.Number != 42 {
		t.Fatalf("schema-v3 exit or PR fields changed: %+v", record)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(after, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["top_level_extension"]; !ok {
		t.Fatalf("top-level unknown field was dropped: %s", after)
	}
}

func TestSchemaV3BaselineCreatesBackupBeforeOlderMigration(t *testing.T) {
	// Given
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	original := []byte(`{"schema_version":2,"sessions":{}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}

	// Then
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backups=%v err=%v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup differs from original: got=%s want=%s", backup, original)
	}
}

func TestSchemaV3BaselineQuarantinesMalformedJSON(t *testing.T) {
	// Given
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	original := []byte("{malformed")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if len(cfg.Sessions) != 0 {
		t.Fatalf("quarantine returned sessions: %+v", cfg.Sessions)
	}
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups=%v err=%v", backups, err)
	}
	quarantined, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(quarantined, original) {
		t.Fatalf("quarantine changed malformed bytes: got=%q want=%q", quarantined, original)
	}
}
