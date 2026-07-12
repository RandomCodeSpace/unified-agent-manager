package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if cfg.DefaultAgent != "opencode" {
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
		ID:           "12345678-1234-4234-9234-123456789abc",
		Agent:        "claude",
		CommandAlias: "ghcp",
		Name:         "fix tests",
		Mode:         ModeYolo,
		Workdir:      "/tmp/repo",
		SessionName:  "uam-claude-12345678",
		CreatedAt:    now,
		LastSeenAt:   now,
		Pinned:       true,
		Group:        "repo",
		SortIndex:    7,
		PR:           &PRRecord{URL: "https://github.com/o/r/pull/1", Number: 1, LastStatus: "open", LastChecked: now},
	}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.Sessions[Key("claude", "12345678")]
	if got.Name != "fix tests" || got.CommandAlias != "ghcp" || !got.Pinned || got.PR == nil || got.PR.Number != 1 {
		t.Fatalf("loaded record mismatch: %+v", got)
	}
}

func TestTryMarkSessionClosedReportsWhetherRecordMatched(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(cfg *Config) error {
		cfg.Sessions["fake:deadbeef"] = SessionRecord{ID: "deadbeef", Agent: "fake", SessionName: "uam-fake-deadbeef", Status: StatusActive}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	matched, err := s.TryMarkSessionClosed("uam-fake-missing", 7)
	if err != nil {
		t.Fatalf("missing record: %v", err)
	}
	if matched {
		t.Fatal("missing record unexpectedly matched")
	}

	matched, err = s.TryMarkSessionClosed("uam-fake-deadbeef", 7)
	if err != nil {
		t.Fatalf("matching record: %v", err)
	}
	if !matched {
		t.Fatal("existing record was not reported as matched")
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions["fake:deadbeef"]
	if rec.Status != StatusClosedByUser || rec.LastExitCode == nil || *rec.LastExitCode != 7 {
		t.Fatalf("record not closed: %+v", rec)
	}
}

func TestSyncDirAcceptsStoreDirectoryAndRejectsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := syncDir(dir); err != nil {
		t.Fatalf("syncDir(temp): %v", err)
	}
	if err := syncDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("syncDir(missing) succeeded")
	}
}

func TestSessionRecordCommandAliasJSONOmitEmpty(t *testing.T) {
	withoutAlias, err := json.Marshal(SessionRecord{ID: "id", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutAlias), "command_alias") {
		t.Fatalf("empty command alias should be omitted: %s", withoutAlias)
	}
	withAlias, err := json.Marshal(SessionRecord{ID: "id", Agent: "fake", CommandAlias: "ghcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withAlias), `"command_alias":"ghcp"`) {
		t.Fatalf("command alias should be serialized: %s", withAlias)
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

func TestMigrateV1BackfillsStatusActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	// Pre-v2 records had no `status` field. The migration must default them
	// to Active so they remain recoverable on attach.
	old := map[string]any{
		"schema_version": 1,
		"sessions": map[string]any{
			"claude:abcd1234": map[string]any{
				"id":           "abcd1234",
				"agent":        "claude",
				"tmux_session": "uam-claude-abcd1234",
			},
		},
	}
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
		t.Fatalf("schema = %d, want %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	rec, ok := cfg.Sessions["claude:abcd1234"]
	if !ok {
		t.Fatalf("session lost during migration: %+v", cfg.Sessions)
	}
	if rec.Status != StatusActive {
		t.Fatalf("status = %q, want %q", rec.Status, StatusActive)
	}
}

func TestMigrateV2ToV3PreservesCommandAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	old := map[string]any{
		"schema_version": 2,
		"sessions": map[string]any{
			"copilot:abcd1234": map[string]any{
				"id":            "abcd1234",
				"agent":         "copilot",
				"command_alias": "ghcp",
				"tmux_session":  "uam-copilot-abcd1234",
				"workdir":       "/tmp/repo",
				"status":        "active",
			},
		},
	}
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
		t.Fatalf("schema = %d, want %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	rec := cfg.Sessions["copilot:abcd1234"]
	if rec.CommandAlias != "ghcp" {
		t.Fatalf("command alias = %q, want ghcp", rec.CommandAlias)
	}
}

func TestStoreUpdateIsAtomicUnderConcurrency(t *testing.T) {
	// Characterizes the existing flock + read-modify-write + atomic-rename
	// behavior of Store.Update: concurrent writers each inserting a distinct
	// session key must not lose each other's writes. This is the safety net
	// that protects the later Update/LoadSessions refactor (F01).
	const writers = 20

	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			key := Key("claude", fmt.Sprintf("sess%04d", i))
			err := s.Update(func(cfg *Config) error {
				cfg.Sessions[key] = SessionRecord{ID: key, Agent: "claude"}
				return nil
			})
			if err != nil {
				t.Errorf("Update(%s): %v", key, err)
			}
		}(i)
	}
	wg.Wait()

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Sessions) != writers {
		t.Fatalf("sessions = %d, want %d (lost writes)", len(cfg.Sessions), writers)
	}
	for i := 0; i < writers; i++ {
		key := Key("claude", fmt.Sprintf("sess%04d", i))
		if _, ok := cfg.Sessions[key]; !ok {
			t.Errorf("missing key %q after concurrent updates", key)
		}
	}
}

func TestUpdateSerializesConcurrentWriters_Race(t *testing.T) {
	// Stronger than the distinct-key case: concurrent writers performing a
	// read-modify-write on the SAME key must serialize, so every increment
	// lands. This is the atomic-RMW guarantee LoadSessions (F01) relies on —
	// it re-reads inside the flock instead of saving a stale whole-config
	// snapshot.
	const writers = 25

	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	key := Key("claude", "shared00")
	if err := s.Update(func(cfg *Config) error {
		cfg.Sessions[key] = SessionRecord{ID: "shared00", Agent: "claude"}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			err := s.Update(func(cfg *Config) error {
				rec := cfg.Sessions[key]
				rec.SortIndex++
				cfg.Sessions[key] = rec
				return nil
			})
			if err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
	}
	wg.Wait()

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Sessions[key].SortIndex; got != writers {
		t.Fatalf("SortIndex = %d, want %d (lost read-modify-write updates)", got, writers)
	}
}

func TestNormalizeBackfillsStatusForExistingSessions(t *testing.T) {
	// Even at the current schema version, a record with empty Status should
	// be normalized to Active on every load so downstream consumers never
	// see an unset value.
	cfg := Config{
		SchemaVersion: CurrentSchemaVersion,
		Sessions: map[string]SessionRecord{
			"claude:deadbeef": {ID: "deadbeef", Agent: "claude"},
		},
	}
	got := normalize(cfg)
	if got.Sessions["claude:deadbeef"].Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Sessions["claude:deadbeef"].Status, StatusActive)
	}
}
