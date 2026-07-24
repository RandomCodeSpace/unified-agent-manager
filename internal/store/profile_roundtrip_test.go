package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func boolPointer(value bool) *bool { return &value }

func runtimeOnlyConfigFields() []string {
	return []string{
		"client_id",
		"client_ids",
		"client_role",
		"controller",
		"controller_id",
		"controller_client_id",
		"controller_role",
		"role",
		"requested_role",
		"assigned_role",
		"terminal_width",
		"terminal_height",
		"terminal_columns",
		"terminal_rows",
		"terminal_dimensions",
		"terminal_size",
		"capability",
		"capabilities",
		"capability_response",
		"capability_responses",
		"protocol_version",
		"negotiated_protocol_version",
	}
}

func TestProfileExplicitFalseRoundTrip(t *testing.T) {
	// Given
	store, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	mode := ModeSafe
	mouse := MousePolicyOff
	provider := "claude"
	alias := "claude-local"
	prefix := "C-a"
	scrollback := 8000
	cfg := DefaultConfig()
	cfg.DefaultProfile = "focused"
	cfg.Profiles["focused"] = Profile{
		Provider:        &provider,
		Mode:            &mode,
		CommandAlias:    &alias,
		Mouse:           &mouse,
		ControlPrefix:   &prefix,
		BackDetach:      boolPointer(false),
		ScrollbackLines: &scrollback,
	}
	cfg.Sessions["claude:abc12345"] = SessionRecord{
		ID: "abc12345", Agent: "claude", SessionName: "uam-claude-abc12345", Profile: "focused",
		ProfileOverrides: &SessionProfileOverrides{BackDetach: boolPointer(false)},
	}

	// When
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Then
	profile := loaded.Profiles["focused"]
	if profile.BackDetach == nil || *profile.BackDetach || profile.Provider == nil || *profile.Provider != "claude" || profile.Mode == nil || *profile.Mode != ModeSafe || profile.CommandAlias == nil || *profile.CommandAlias != "claude-local" {
		t.Fatalf("profile optional values changed: %+v", profile)
	}
	overrides := loaded.Sessions["claude:abc12345"].ProfileOverrides
	if overrides == nil || overrides.BackDetach == nil || *overrides.BackDetach {
		t.Fatalf("session explicit false changed: %+v", overrides)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || !containsJSONFalse(t, raw, "profiles", "focused", "back_detach") {
		t.Fatalf("explicit false missing from JSON: %s", raw)
	}
}

func containsJSONFalse(t *testing.T, data []byte, keys ...string) bool {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	current := value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[key]
		if !ok {
			return false
		}
	}
	boolean, ok := current.(bool)
	return ok && !boolean
}

func TestNestedUnknownFieldsRoundTrip(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "sessions.json")
	doc := []byte(`{"schema_version":4,"profiles":{"future":{"mouse":"off","profile_extension":{"value":7}}},"sessions":{"claude:abc12345":{"id":"abc12345","agent":"claude","tmux_session":"uam-claude-abc12345","profile":"future","session_extension":{"value":9}}}}`)
	if err := os.WriteFile(path, doc, 0o600); err != nil {
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
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJSONPath(t, after, "profiles", "future", "profile_extension") {
		t.Fatalf("profile unknown field dropped: %s", after)
	}
	if !hasJSONPath(t, after, "sessions", "claude:abc12345", "session_extension") {
		t.Fatalf("session unknown field dropped: %s", after)
	}
}

func hasJSONPath(t *testing.T, data []byte, keys ...string) bool {
	t.Helper()
	var current any
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[key]
		if !ok {
			return false
		}
	}
	return true
}

func TestRuntimeClientStateIsNotPersisted(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "sessions.json")
	doc := []byte(`{"schema_version":4,"profiles":{},"sessions":{"claude:abc12345":{"id":"abc12345","agent":"claude","tmux_session":"uam-claude-abc12345","session_extension":"keep","client_id":"client-1","controller_id":"client-1","requested_role":"controller","assigned_role":"controller","terminal_width":120,"terminal_height":40,"capabilities":["framed_output"],"negotiated_protocol_version":2}}}`)
	if err := os.WriteFile(path, doc, 0o600); err != nil {
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
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"client_id", "controller_id", "requested_role", "assigned_role", "terminal_width", "terminal_height", "capabilities", "negotiated_protocol_version"} {
		if hasJSONPath(t, after, "sessions", "claude:abc12345", field) {
			t.Fatalf("runtime field %q persisted: %s", field, after)
		}
	}
	if !hasJSONPath(t, after, "sessions", "claude:abc12345", "session_extension") {
		t.Fatalf("legitimate unknown field was dropped with runtime state: %s", after)
	}
}

func TestRuntimeClientStateAtConfigRootIsNotPersisted(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "sessions.json")
	runtimeFields := runtimeOnlyConfigFields()
	doc := map[string]any{
		"schema_version": 4,
		"profiles":       map[string]any{},
		"sessions":       map[string]any{},
		"root_extension": map[string]any{"preserve": true},
	}
	for _, field := range runtimeFields {
		doc[field] = "forbidden-runtime-state"
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
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
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range runtimeFields {
		if hasJSONPath(t, after, field) {
			t.Errorf("root runtime field %q persisted: %s", field, after)
		}
	}
	if !hasJSONPath(t, after, "root_extension") {
		t.Fatalf("legitimate root unknown field was dropped: %s", after)
	}
}
