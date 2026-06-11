package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// F07 — v1->v2 migration must not resurrect user-stopped (dead-pane) sessions
// ---------------------------------------------------------------------------

func writeV1Config(t *testing.T, path string, sessions map[string]any) {
	t.Helper()
	old := map[string]any{"schema_version": 1, "sessions": sessions}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateV1_DeadSessionName_BecomesClosedByUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	writeV1Config(t, path, map[string]any{
		"claude:dead1234": map[string]any{
			"id":           "dead1234",
			"agent":        "claude",
			"tmux_session": "uam-claude-dead1234",
		},
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Probe reports the pane is gone -> the record was a user-stopped session.
	s.SetSessionProbe(func(string) bool { return false })

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := cfg.Sessions["claude:dead1234"]
	if !ok {
		t.Fatalf("session lost during migration: %+v", cfg.Sessions)
	}
	if rec.Status != StatusClosedByUser {
		t.Fatalf("status = %q, want %q (dead v1 pane must become closed-by-user)", rec.Status, StatusClosedByUser)
	}
}

func TestMigrateV1_LiveSessionName_StaysActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	writeV1Config(t, path, map[string]any{
		"claude:live1234": map[string]any{
			"id":           "live1234",
			"agent":        "claude",
			"tmux_session": "uam-claude-live1234",
		},
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Probe reports the pane is alive (reboot survivor) -> keep it Active.
	s.SetSessionProbe(func(name string) bool { return name == "uam-claude-live1234" })

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec := cfg.Sessions["claude:live1234"]
	if rec.Status != StatusActive {
		t.Fatalf("status = %q, want %q (live v1 pane must stay active)", rec.Status, StatusActive)
	}
}

func TestMigrateWithNilProbe_FallsBackToActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	writeV1Config(t, path, map[string]any{
		"claude:noprobe1": map[string]any{
			"id":           "noprobe1",
			"agent":        "claude",
			"tmux_session": "uam-claude-noprobe1",
		},
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// No probe set -> conservative fallback keeps the legacy Active behavior.
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Sessions["claude:noprobe1"].Status; got != StatusActive {
		t.Fatalf("status = %q, want %q (nil probe must fall back to active)", got, StatusActive)
	}
}

func TestMigrateV1_DoesNotOverrideExplicitStatus(t *testing.T) {
	// A v1 record that already carries an explicit status must be left alone;
	// reclassification only targets the Statusless records.
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	writeV1Config(t, path, map[string]any{
		"claude:explicit": map[string]any{
			"id":           "explicit",
			"agent":        "claude",
			"tmux_session": "uam-claude-explicit",
			"status":       string(StatusActive),
		},
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.SetSessionProbe(func(string) bool { return false })
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Sessions["claude:explicit"].Status; got != StatusActive {
		t.Fatalf("status = %q, want %q (explicit status must survive)", got, StatusActive)
	}
}

// ---------------------------------------------------------------------------
// F33 — newer-schema file must be preserved, not clobbered by an older binary
// ---------------------------------------------------------------------------

func TestLoadNewerSchemaIsPreservedNotClobbered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	// A file written by a FUTURE binary: higher schema + an unknown field.
	future := map[string]any{
		"schema_version": CurrentSchemaVersion + 1,
		"default_agent":  "codex",
		"sessions":       map[string]any{},
		"ui":             map[string]any{"sort": "state", "peek_width": 60},
		"future_only":    map[string]any{"some": "value"},
	}
	data, err := json.MarshalIndent(future, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load must not error on newer schema: %v", err)
	}
	if !cfg.ReadOnly {
		t.Fatal("newer-schema config must be flagged read-only")
	}
	// A write attempt against a read-only config must be refused (no clobber).
	if err := s.Save(cfg); err == nil {
		t.Fatal("Save must refuse a read-only (newer-schema) config")
	}
	if err := s.Update(func(c *Config) error { c.DefaultAgent = "claude"; return nil }); err == nil {
		t.Fatal("Update must refuse to write a read-only (newer-schema) config")
	}
	// The on-disk file must be byte-identical (unknown field intact).
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("newer-schema file mutated on disk:\nbefore=%s\nafter=%s", original, after)
	}
}

func TestEqualSchemaUnknownFieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	// Same schema version but carries an unknown top-level field.
	doc := map[string]any{
		"schema_version": CurrentSchemaVersion,
		"default_agent":  "claude",
		"sessions":       map[string]any{},
		"ui":             map[string]any{"group_by_dir": false, "sort": "state", "peek_width": 60},
		"experimental":   "keep-me",
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
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
	if cfg.ReadOnly {
		t.Fatal("equal-schema config must NOT be read-only")
	}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(after, &m); err != nil {
		t.Fatalf("re-saved file is not valid JSON: %v", err)
	}
	if _, ok := m["experimental"]; !ok {
		t.Fatalf("unknown field 'experimental' dropped on round-trip: %s", after)
	}
}

func TestNormalConfigMarshalsByteIdentical(t *testing.T) {
	// A config with NO unknown fields must marshal byte-for-byte the same as it
	// did before the overflow machinery was introduced.
	cfg := DefaultConfig()
	cfg.Sessions[Key("claude", "abcd1234")] = SessionRecord{
		ID: "abcd1234", Agent: "claude", Name: "t", Mode: ModeYolo, Status: StatusActive,
	}

	got, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	// Round-trip through unmarshal then marshal again must be stable.
	var back Config
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	again, err := json.MarshalIndent(back, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent (2): %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Fatalf("round-trip not stable:\nfirst=%s\nsecond=%s", got, again)
	}
}

// ---------------------------------------------------------------------------
// F44 — normalize() must clamp/coerce invalid enum/range values, never drop
// ---------------------------------------------------------------------------

func TestNormalizeRejectsUnknownStatus(t *testing.T) {
	cfg := Config{
		SchemaVersion: CurrentSchemaVersion,
		Sessions: map[string]SessionRecord{
			"claude:weird001": {ID: "weird001", Agent: "claude", Status: Status("bananas")},
		},
	}
	got := normalize(cfg)
	rec, ok := got.Sessions["claude:weird001"]
	if !ok {
		t.Fatalf("normalize dropped the record with unknown status: %+v", got.Sessions)
	}
	if rec.Status != StatusActive {
		t.Fatalf("status = %q, want %q (unknown status must coerce to Active, never Closed)", rec.Status, StatusActive)
	}
}

func TestNormalizeCoercesUnknownMode(t *testing.T) {
	cfg := Config{
		SchemaVersion: CurrentSchemaVersion,
		Sessions: map[string]SessionRecord{
			"claude:weirdmod": {ID: "weirdmod", Agent: "claude", Mode: Mode("turbo"), Status: StatusActive},
		},
	}
	got := normalize(cfg)
	if rec := got.Sessions["claude:weirdmod"]; rec.Mode != ModeYolo {
		t.Fatalf("mode = %q, want %q (unknown mode must coerce to yolo)", rec.Mode, ModeYolo)
	}
}

func TestNormalizeResetsUnknownSort(t *testing.T) {
	cfg := normalize(Config{UI: UISettings{Sort: "nonsense", PeekWidth: 60}})
	if cfg.UI.Sort != "state" {
		t.Fatalf("sort = %q, want %q (unknown sort must reset to state)", cfg.UI.Sort, "state")
	}
}

func TestNormalizeClampsNegativePeekWidth(t *testing.T) {
	cfg := normalize(Config{UI: UISettings{Sort: "state", PeekWidth: -10}})
	if cfg.UI.PeekWidth <= 0 {
		t.Fatalf("peek_width = %d, want a positive default", cfg.UI.PeekWidth)
	}
}

func TestNormalizeNeverDropsSessions(t *testing.T) {
	cfg := Config{
		SchemaVersion: CurrentSchemaVersion,
		Sessions: map[string]SessionRecord{
			"a:1": {ID: "1", Agent: "a", Status: Status("x"), Mode: Mode("y")},
			"b:2": {ID: "2", Agent: "b", Status: StatusClosedByUser, Mode: ModeSafe},
			"c:3": {ID: "3", Agent: "c"},
		},
	}
	got := normalize(cfg)
	if len(got.Sessions) != 3 {
		t.Fatalf("sessions = %d, want 3 (normalize must never delete records)", len(got.Sessions))
	}
	// The deliberately-closed record must NOT be flipped to Active.
	if got.Sessions["b:2"].Status != StatusClosedByUser {
		t.Fatalf("b:2 status = %q, want %q (valid closed status must survive)", got.Sessions["b:2"].Status, StatusClosedByUser)
	}
}
