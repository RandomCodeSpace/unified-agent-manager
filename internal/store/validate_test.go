package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeConfig marshals an arbitrary config map to the store path. Using a
// generic map (not Config) lets these tests inject values that the typed
// constructors would never produce — exactly the untrusted-on-disk shape that
// load-time validation must defend against.
func writeConfig(t *testing.T, sessions map[string]any) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	cfg := map[string]any{
		"schema_version": CurrentSchemaVersion,
		"default_agent":  "claude",
		"sessions":       sessions,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// goodRecord is the canonical full-UUID-style record produced by Dispatch.
func goodRecord() map[string]any {
	return map[string]any{
		"id":           "12345678-1234-4234-9234-123456789abc",
		"agent":        "claude",
		"name":         "fix tests",
		"mode":         "yolo",
		"workdir":      "/tmp/repo",
		"tmux_session": "uam-claude-12345678",
		"status":       "active",
	}
}

func TestLoadDropsRecordWithMetacharSessionName(t *testing.T) {
	s := writeConfig(t, map[string]any{
		"claude:12345678": goodRecord(),
		"claude:evil1234": map[string]any{
			"id":           "evil1234",
			"agent":        "claude",
			"tmux_session": "evil'; touch x #",
		},
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:evil1234"]; ok {
		t.Fatal("record with shell-metachar tmux_session was NOT dropped")
	}
	if _, ok := cfg.Sessions["claude:12345678"]; !ok {
		t.Fatal("valid sibling record was wrongly dropped")
	}
}

func TestLoadDropsRecordWithMetacharID(t *testing.T) {
	s := writeConfig(t, map[string]any{
		"claude:12345678": goodRecord(),
		"claude:bad": map[string]any{
			"id":           "evil'; rm -rf /",
			"agent":        "claude",
			"tmux_session": "uam-claude-evilrmrf",
		},
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:bad"]; ok {
		t.Fatal("record with shell-metachar id was NOT dropped")
	}
	if _, ok := cfg.Sessions["claude:12345678"]; !ok {
		t.Fatal("valid sibling record was wrongly dropped")
	}
}

func TestLoadDropsRecordWithBadPRURL(t *testing.T) {
	s := writeConfig(t, map[string]any{
		"claude:12345678": goodRecord(),
		"claude:badpr111": map[string]any{
			"id":           "badpr111",
			"agent":        "claude",
			"tmux_session": "uam-claude-badpr111",
			"pr": map[string]any{
				"url":    "https://evil.example.com/not/a/pull/req",
				"number": 1,
			},
		},
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:badpr111"]; ok {
		t.Fatal("record with non-github PR url was NOT dropped")
	}
	if _, ok := cfg.Sessions["claude:12345678"]; !ok {
		t.Fatal("valid sibling record was wrongly dropped")
	}
}

func TestLoadDropsRecordWithRelativeWorkdir(t *testing.T) {
	s := writeConfig(t, map[string]any{
		"claude:12345678": goodRecord(),
		"claude:relwd111": map[string]any{
			"id":           "relwd111",
			"agent":        "claude",
			"tmux_session": "uam-claude-relwd111",
			"workdir":      "relative/path",
		},
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:relwd111"]; ok {
		t.Fatal("record with non-absolute workdir was NOT dropped")
	}
	if _, ok := cfg.Sessions["claude:12345678"]; !ok {
		t.Fatal("valid sibling record was wrongly dropped")
	}
}

func TestLoadDropsRecordWithControlCharWorkdir(t *testing.T) {
	s := writeConfig(t, map[string]any{
		"claude:12345678": goodRecord(),
		"claude:ctlwd111": map[string]any{
			"id":           "ctlwd111",
			"agent":        "claude",
			"tmux_session": "uam-claude-ctlwd111",
			"workdir":      "/tmp/re\npo",
		},
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:ctlwd111"]; ok {
		t.Fatal("record with control-char workdir was NOT dropped")
	}
	if _, ok := cfg.Sessions["claude:12345678"]; !ok {
		t.Fatal("valid sibling record was wrongly dropped")
	}
}

func TestLoadDropsRecordWithUnsafeCommandAlias(t *testing.T) {
	rec := goodRecord()
	rec["command_alias"] = "ghcp;rm"
	s := writeConfig(t, map[string]any{
		"claude:12345678": goodRecord(),
		"claude:badalias": rec,
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:badalias"]; ok {
		t.Fatal("record with unsafe command_alias was NOT dropped")
	}
	if _, ok := cfg.Sessions["claude:12345678"]; !ok {
		t.Fatal("valid sibling record was wrongly dropped")
	}
}

func TestLoadKeepsSafeCommandAlias(t *testing.T) {
	rec := goodRecord()
	rec["command_alias"] = "ghcp.local"
	s := writeConfig(t, map[string]any{"claude:12345678": rec})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Sessions["claude:12345678"]
	if got.CommandAlias != "ghcp.local" {
		t.Fatalf("command alias = %q, want ghcp.local", got.CommandAlias)
	}
}

// --- GREEN accept-tests: legitimate records must survive load unchanged. ---

func TestLoadKeepsCanonicalUUIDRecord(t *testing.T) {
	s := writeConfig(t, map[string]any{"claude:12345678": goodRecord()})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := cfg.Sessions["claude:12345678"]
	if !ok {
		t.Fatal("canonical UUID-style record was wrongly dropped")
	}
	if rec.ID != "12345678-1234-4234-9234-123456789abc" || rec.SessionName != "uam-claude-12345678" {
		t.Fatalf("record mutated: %+v", rec)
	}
}

func TestLoadKeepsListDiscoveredShape(t *testing.T) {
	// The List path persists records whose ID is the 8-hex remainder after the
	// "uam-<agent>-" prefix is trimmed. These must survive validation.
	s := writeConfig(t, map[string]any{
		"claude:abcd1234": map[string]any{
			"id":           "abcd1234",
			"agent":        "claude",
			"tmux_session": "uam-claude-abcd1234",
			"status":       "active",
		},
	})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions["claude:abcd1234"]; !ok {
		t.Fatal("8-hex List-discovered record was wrongly dropped")
	}
}

func TestLoadKeepsNilPRRecord(t *testing.T) {
	rec := goodRecord()
	delete(rec, "pr")
	s := writeConfig(t, map[string]any{"claude:12345678": rec})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := cfg.Sessions["claude:12345678"]
	if !ok {
		t.Fatal("nil-PR record was wrongly dropped")
	}
	if got.PR != nil {
		t.Fatalf("PR unexpectedly populated: %+v", got.PR)
	}
}

func TestLoadKeepsValidPRRecord(t *testing.T) {
	rec := goodRecord()
	rec["pr"] = map[string]any{
		"url":    "https://github.com/owner/repo/pull/42",
		"number": 42,
	}
	s := writeConfig(t, map[string]any{"claude:12345678": rec})
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := cfg.Sessions["claude:12345678"]
	if !ok {
		t.Fatal("record with valid github PR url was wrongly dropped")
	}
	if got.PR == nil || got.PR.Number != 42 {
		t.Fatalf("PR not preserved: %+v", got.PR)
	}
}

func TestLoadKeepsMigratedRecord(t *testing.T) {
	// A pre-v2 record (no status field) must survive both migration and
	// validation. This mirrors TestMigrateV1BackfillsStatusActive but proves
	// validation does not regress the migration path.
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
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
	if err := os.WriteFile(path, data, 0o600); err != nil {
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
	rec, ok := cfg.Sessions["claude:abcd1234"]
	if !ok {
		t.Fatalf("migrated record dropped by validation: %+v", cfg.Sessions)
	}
	if rec.Status != StatusActive {
		t.Fatalf("status = %q, want %q", rec.Status, StatusActive)
	}
}

// The provider session id is passed as a resume argv value; anything outside
// the UUID alphabet must drop the record on load.
func TestValidateRejectsUnsafeProviderSessionID(t *testing.T) {
	rec := SessionRecord{ID: "abc12345", Agent: "claude", SessionName: "uam-claude-abc12345", Workdir: "/tmp"}
	for _, good := range []string{"abc12345-dead-beef-cafe-0123456789ab", "ses_2132323b6ffeuRlYHhPcU8DaZ6"} {
		rec.ProviderSessionID = good
		if reason := validateRecord(rec); reason != "" {
			t.Fatalf("provider session id %q should pass, got %q", good, reason)
		}
	}
	for _, bad := range []string{"--continue", "-leadingdash", "x; rm -rf /", "id with space", "$(boom)"} {
		rec.ProviderSessionID = bad
		if reason := validateRecord(rec); reason == "" {
			t.Fatalf("provider session id %q must be rejected", bad)
		}
	}
}
