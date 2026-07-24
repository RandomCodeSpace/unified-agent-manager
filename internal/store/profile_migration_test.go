package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func copyV3Fixture(t *testing.T) (string, []byte) {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", "schema-v3-full.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, fixture
}

func TestMigrateV3ProfilesPreservesSessions(t *testing.T) {
	// Given
	path, original := copyV3Fixture(t)
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
	if cfg.SchemaVersion != 4 || cfg.DefaultProfile != "" || len(cfg.Profiles) != 0 {
		t.Fatalf("migrated profile config = %+v", cfg)
	}
	record, ok := cfg.Sessions["claude:12345678"]
	if !ok {
		t.Fatal("schema-v3 session was dropped")
	}
	if record.Profile != "" || record.ProfileOverrides != nil {
		t.Fatalf("v3 session selected unexpected profile: %+v", record)
	}
	if record.ID != "12345678-1234-4234-9234-123456789abc" || record.Agent != "claude" || record.ProviderSessionID != "provider_session_12345678" {
		t.Fatalf("identity changed: %+v", record)
	}
	if record.CommandAlias != "claude-local" || record.Name != "preserve every field" || record.Prompt != "characterize schema v3" || record.Mode != ModeSafe || record.Workdir != "/tmp/schema-v3-worktree" || record.SessionName != "uam-claude-12345678" || record.Status != StatusActive {
		t.Fatalf("session metadata changed: %+v", record)
	}
	if record.CreatedAt.Format("2006-01-02T15:04:05Z") != "2025-01-02T03:04:05Z" || record.LastSeenAt.Format("2006-01-02T15:04:05Z") != "2025-02-03T04:05:06Z" {
		t.Fatalf("timestamps changed: %+v", record)
	}
	if record.LastExitCode == nil || *record.LastExitCode != 23 || !record.Pinned || record.Group != "migration" || record.SortIndex != 17 || record.PR == nil || record.PR.Number != 42 {
		t.Fatalf("exit/order/UI/PR fields changed: %+v", record)
	}
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backups=%v err=%v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup differs from v3 input: got=%s want=%s", backup, original)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJSONPath(t, after, "top_level_extension") || !hasJSONPath(t, after, "sessions", "claude:12345678", "session_extension") {
		t.Fatalf("migration dropped unknown fields: %s", after)
	}
}

func TestMigrateV3ClearsUnversionedProfileSelection(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "sessions.json")
	doc := []byte(`{"schema_version":3,"default_profile":"preview","profiles":{"preview":{"mouse":"off"}},"sessions":{"claude:abc12345":{"id":"abc12345","agent":"claude","tmux_session":"uam-claude-abc12345","profile":"preview","profile_overrides":{"back_detach":false},"extension":"keep"}}}`)
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	cfg, err := store.Load()

	// Then
	if err != nil {
		t.Fatal(err)
	}
	record, ok := cfg.Sessions["claude:abc12345"]
	if !ok {
		t.Fatal("v3 session was dropped")
	}
	if cfg.DefaultProfile != "" || record.Profile != "" || record.ProfileOverrides != nil {
		t.Fatalf("v3 profile state became active: default=%q record=%+v", cfg.DefaultProfile, record)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJSONPath(t, after, "profiles", "preview") || !hasJSONPath(t, after, "sessions", "claude:abc12345", "extension") {
		t.Fatalf("migration dropped preserved data: %s", after)
	}
}

func TestMissingProfileFallsBackToLegacyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		profiles string
		profile  string
	}{
		{name: "missing profile", profiles: `{}`, profile: "deleted"},
		{name: "missing stable profile", profiles: `{}`, profile: "stable"},
		{name: "missing claude profile", profiles: `{}`, profile: "claude"},
		{name: "invalid profile", profiles: `{"broken":{"mode":"turbo"}}`, profile: "broken"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			path := filepath.Join(t.TempDir(), "sessions.json")
			doc := `{"schema_version":4,"default_profile":"` + test.profile + `","profiles":` + test.profiles + `,"sessions":{"claude:abc12345":{"id":"abc12345","agent":"claude","mode":"safe","tmux_session":"uam-claude-abc12345","profile":"` + test.profile + `"}}}`
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			previous := uamlog.SetLogger(slog.New(slog.NewTextHandler(&diagnostics, nil)))
			t.Cleanup(func() { uamlog.SetLogger(previous) })
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
			record, ok := cfg.Sessions["claude:abc12345"]
			if !ok {
				t.Fatal("session with unusable profile was dropped")
			}
			if cfg.DefaultProfile != "" || record.Profile != "" || record.Mode != ModeSafe {
				t.Fatalf("legacy fallback changed record: %+v", record)
			}
			if !strings.Contains(diagnostics.String(), "profile.resolution") ||
				!strings.Contains(diagnostics.String(), "profile_fallback") ||
				!strings.Contains(diagnostics.String(), test.profile) {
				t.Fatalf("missing fallback diagnostic: %s", diagnostics.String())
			}
		})
	}
}

func TestProfileFallbackDiagnosticRedactsPersistedIdentifiers(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "sessions.json")
	const (
		defaultProfile = "secret-input-7f3a"
		sessionKey     = "TOKEN=private-value"
		sessionProfile = "provider-output-91bc"
	)
	doc := `{"schema_version":4,"default_profile":"` + defaultProfile + `","profiles":{},"sessions":{"` + sessionKey + `":{"id":"abc12345","agent":"claude","mode":"safe","tmux_session":"uam-claude-abc12345","profile":"` + sessionProfile + `"}}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewJSONHandler(&diagnostics, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}

	// Then
	output := diagnostics.String()
	for _, sentinel := range []string{defaultProfile, sessionKey, sessionProfile} {
		if strings.Contains(output, sentinel) {
			t.Fatalf("fallback diagnostic leaked persisted identifier %q: %s", sentinel, output)
		}
	}
	if count := strings.Count(output, `"event":"profile.resolution"`); count != 2 {
		t.Fatalf("fallback diagnostic count=%d, want 2: %s", count, output)
	}
	if count := strings.Count(output, `"profile":"redacted"`); count != 2 {
		t.Fatalf("redacted profile count=%d, want 2: %s", count, output)
	}
	if !strings.Contains(output, `"policy":"default"`) || !strings.Contains(output, `"policy":"session"`) {
		t.Fatalf("fallback diagnostic lost scope: %s", output)
	}
}

func TestProfileMigrationFailurePreservesOriginal(t *testing.T) {
	// Given
	path, original := copyV3Fixture(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected migration write failure")
	store.migrationWrite = func(Config) error {
		backups, globErr := filepath.Glob(path + ".bak.*")
		if globErr != nil || len(backups) != 1 {
			t.Fatalf("backup must exist before write: backups=%v err=%v", backups, globErr)
		}
		return injected
	}

	// When
	_, loadErr := store.Load()

	// Then
	if !errors.Is(loadErr, injected) {
		t.Fatalf("Load error=%v, want injected failure", loadErr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("failed migration changed original: got=%s want=%s", after, original)
	}
}

func TestProfileMigrationBackupFailurePreservesOriginal(t *testing.T) {
	// Given
	path, original := copyV3Fixture(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected migration backup failure")
	store.migrationBackup = func() error { return injected }
	store.migrationWrite = func(Config) error {
		t.Fatal("migration write ran after backup failure")
		return nil
	}

	// When
	_, loadErr := store.Load()

	// Then
	if !errors.Is(loadErr, injected) {
		t.Fatalf("Load error=%v, want injected backup failure", loadErr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("failed backup changed original: got=%s want=%s", after, original)
	}
}

func TestProfileMigrationRetryIgnoresInterruptedTempState(t *testing.T) {
	// Given
	path, original := copyV3Fixture(t)
	staleTemp := path + ".tmp.interrupted"
	if err := os.WriteFile(staleTemp, []byte("partial migration"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first.migrationWrite = func(Config) error { return errors.New("interrupted") }
	if _, err := first.Load(); err == nil {
		t.Fatal("injected interruption unexpectedly succeeded")
	}
	failedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(failedBytes, original) {
		t.Fatal("interrupted attempt changed the original")
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	cfg, err := second.Load()

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion || len(cfg.Sessions) != 1 {
		t.Fatalf("retry did not complete migration: %+v", cfg)
	}
	stale, err := os.ReadFile(staleTemp)
	if err != nil || string(stale) != "partial migration" {
		t.Fatalf("stale temp was reused: data=%q err=%v", stale, err)
	}
}

func TestV3BinaryContractTreatsV4ReadOnly(t *testing.T) {
	// Given: this local contract models only the v3 schema guard, not an old binary.
	fixture := []byte(`{"schema_version":4,"profiles":{"future":{"mouse":"off"}},"sessions":{}}`)
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}

	// When
	err := json.Unmarshal(fixture, &header)
	readOnly := header.SchemaVersion > 3

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !readOnly {
		t.Fatalf("v3 contract accepted schema %d as writable", header.SchemaVersion)
	}
	if !bytes.Contains(fixture, []byte(`"profiles"`)) {
		t.Fatal("v4 fixture does not exercise a field unknown to schema v3")
	}
}
