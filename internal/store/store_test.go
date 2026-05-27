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

// TestMigrateV1ToV2PreservesRecords seeds a sessions.json at schema v1
// containing a populated SessionRecord, loads it, and asserts:
//   - the schema is rewritten to v2 (CurrentSchemaVersion at this release)
//   - every record field survives the migration unchanged (no field
//     rename happens in v0.2.0; tmux_session is retained verbatim)
//   - the loader writes exactly one .bak.<unix-nano> snapshot before
//     touching the live file.
func TestMigrateV1ToV2PreservesRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	now := time.Now().UTC().Truncate(time.Second)
	v1 := map[string]any{
		"schema_version": 1,
		"default_agent":  "claude",
		"sessions": map[string]any{
			"claude:abc12345": map[string]any{
				"id":           "abc12345-1111-4222-9333-444455556666",
				"agent":        "claude",
				"name":         "carry-forward",
				"mode":         "yolo",
				"workdir":      "/tmp/repo",
				"tmux_session": "uam-claude-abc12345",
				"created_at":   now.Format(time.RFC3339Nano),
				"last_seen_at": now.Format(time.RFC3339Nano),
				"pinned":       true,
				"group":        "repo",
				"sort_index":   3,
			},
		},
		"ui": map[string]any{"sort": "state", "peek_width": 60},
	}
	data, err := json.Marshal(v1)
	if err != nil {
		t.Fatalf("marshal v1: %v", err)
	}
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
	if cfg.SchemaVersion != 2 {
		t.Fatalf("post-migration schema = %d, want 2", cfg.SchemaVersion)
	}
	rec, ok := cfg.Sessions["claude:abc12345"]
	if !ok {
		t.Fatalf("session record lost during migration: %#v", cfg.Sessions)
	}
	if rec.ID != "abc12345-1111-4222-9333-444455556666" ||
		rec.Agent != "claude" ||
		rec.Name != "carry-forward" ||
		rec.Mode != ModeYolo ||
		rec.Workdir != "/tmp/repo" ||
		rec.TmuxSession != "uam-claude-abc12345" ||
		!rec.Pinned ||
		rec.Group != "repo" ||
		rec.SortIndex != 3 {
		t.Fatalf("record mutated by migration: %+v", rec)
	}
	if !rec.CreatedAt.Equal(now) || !rec.LastSeenAt.Equal(now) {
		t.Fatalf("timestamps mutated: created=%v lastSeen=%v want %v", rec.CreatedAt, rec.LastSeenAt, now)
	}

	// Verify the .bak.<unix-nano> snapshot exists and contains the
	// pre-migration v1 bytes verbatim.
	matches, _ := filepath.Glob(filepath.Join(dir, "sessions.json.bak.*"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one backup snapshot, got %v", matches)
	}
	bakBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	var bak map[string]any
	if err := json.Unmarshal(bakBytes, &bak); err != nil {
		t.Fatalf("backup is not valid JSON: %v", err)
	}
	if sv, _ := bak["schema_version"].(float64); int(sv) != 1 {
		t.Fatalf("backup snapshot schema = %v, want pre-migration value 1", bak["schema_version"])
	}
}

// TestMigrateV2ToV2IsNoOp asserts that loading a sessions.json that is
// already at CurrentSchemaVersion does not rewrite the file and does
// not produce any .bak snapshot. Migration must be safely re-runnable.
func TestMigrateV2ToV2IsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	cfg := DefaultConfig()
	cfg.SchemaVersion = CurrentSchemaVersion
	cfg.Sessions[Key("claude", "deadbeef")] = SessionRecord{
		ID:          "deadbeef-0000-4000-8000-000000000000",
		Agent:       "claude",
		Name:        "noop",
		Mode:        ModeYolo,
		Workdir:     "/tmp/noop",
		TmuxSession: "uam-claude-deadbeef",
		CreatedAt:   time.Unix(1_700_000_000, 0).UTC(),
		LastSeenAt:  time.Unix(1_700_000_000, 0).UTC(),
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal v2: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pre-load: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema mutated: %d", loaded.SchemaVersion)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read post-load: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("v2->v2 load rewrote the file:\nbefore=%q\nafter =%q", before, after)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "sessions.json.bak.*"))
	if len(matches) != 0 {
		t.Fatalf("v2->v2 load created backup snapshots: %v", matches)
	}
}
