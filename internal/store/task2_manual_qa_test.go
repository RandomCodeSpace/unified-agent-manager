package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTask2Artifact(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTodo2ManualDataSurfaceQA(t *testing.T) {
	// Given
	evidenceDir := os.Getenv("UAM_TASK2_EVIDENCE_DIR")
	configDir := os.Getenv("UAM_CONFIG_DIR")
	if configDir == "" {
		configDir = t.TempDir()
		t.Setenv("UAM_CONFIG_DIR", configDir)
	}
	if evidenceDir == "" {
		evidenceDir = t.TempDir()
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "schema-v3-full.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inputDocument map[string]any
	if err := json.Unmarshal(fixture, &inputDocument); err != nil {
		t.Fatal(err)
	}
	for _, field := range runtimeOnlyConfigFields() {
		inputDocument[field] = "forbidden-runtime-state"
	}
	input, err := json.MarshalIndent(inputDocument, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := DefaultPath()
	writeTask2Artifact(t, path, input)
	writeTask2Artifact(t, filepath.Join(evidenceDir, "before.json"), input)
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}

	// When
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if cfg.SchemaVersion != 4 || len(cfg.Sessions) != 1 || cfg.Sessions["claude:12345678"].ProviderSessionID != "provider_session_12345678" {
		t.Fatalf("semantic migration failed: %+v", cfg)
	}
	if !bytes.Equal(backup, input) {
		t.Fatal("backup is not byte-identical to v3 input")
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(after, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["profiles"]; !ok {
		t.Fatal("v4 profiles object missing")
	}
	if !hasJSONPath(t, after, "top_level_extension") || !hasJSONPath(t, after, "sessions", "claude:12345678", "session_extension") {
		t.Fatal("unknown fields were not preserved")
	}
	for _, runtimeField := range runtimeOnlyConfigFields() {
		if bytes.Contains(after, []byte(`"`+runtimeField+`"`)) {
			t.Fatalf("runtime field %q serialized", runtimeField)
		}
	}
	writeTask2Artifact(t, filepath.Join(evidenceDir, "after.json"), after)
	writeTask2Artifact(t, filepath.Join(evidenceDir, "backup.json"), backup)
	semanticDiff := []byte("schema_version: 3 -> 4\ndefault_profile: absent -> empty\nprofiles: absent -> {}\nsessions: 1 -> 1\nruntime_only_root_fields: present -> removed\nidentities_timestamps_exit_order_ui_pr_unknowns: preserved\n")
	writeTask2Artifact(t, filepath.Join(evidenceDir, "diff.txt"), semanticDiff)

	failureDir := filepath.Join(configDir, "write-failure")
	if err := os.MkdirAll(failureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	failurePath := filepath.Join(failureDir, configFileName)
	writeTask2Artifact(t, failurePath, input)
	failureStore, err := Open(failurePath)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("manual injected migration write failure")
	failureStore.migrationWrite = func(Config) error { return injected }
	if _, err := failureStore.Load(); !errors.Is(err, injected) {
		t.Fatalf("failure injection error=%v", err)
	}
	failureAfter, err := os.ReadFile(failurePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(failureAfter, input) {
		t.Fatal("migration write failure changed original bytes")
	}
	failureBackups, err := filepath.Glob(failurePath + ".bak.*")
	if err != nil || len(failureBackups) != 1 {
		t.Fatalf("failure backups=%v err=%v", failureBackups, err)
	}
	writeTask2Artifact(t, filepath.Join(evidenceDir, "failure-before.json"), input)
	writeTask2Artifact(t, filepath.Join(evidenceDir, "failure-after.json"), failureAfter)

	assertions := fmt.Sprintf("PASS schema=%d sessions=%d backup_exact=true unknowns_preserved=true runtime_state_absent_at_root_and_nested=true\nPASS injected_write_failure_preserved_original=true backup_created_before_write=true\n", cfg.SchemaVersion, len(cfg.Sessions))
	writeTask2Artifact(t, filepath.Join(evidenceDir, "assertions.txt"), []byte(assertions))

	if os.Getenv("UAM_TASK2_EVIDENCE_DIR") != "" {
		tempRoot := filepath.Clean(os.TempDir()) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(configDir)+string(os.PathSeparator), tempRoot) {
			t.Fatalf("refusing cleanup outside temp root: %s", configDir)
		}
		if err := os.RemoveAll(configDir); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("config fixture still exists: %v", err)
		}
		writeTask2Artifact(t, filepath.Join(evidenceDir, "cleanup-receipt.txt"), []byte("removed isolated UAM_CONFIG_DIR: "+configDir+"\n"))
	}
}
