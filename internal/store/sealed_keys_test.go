package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPlainKey = "sk-plain-key-0123456789"

func testCustomModel() WebCustomModel {
	return WebCustomModel{Name: "acme", BaseURL: "https://acme.example/v1", ModelID: "m1", APIKey: testPlainKey}
}

func readState(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSealKeyRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	sealed, err := sealKey(key, testPlainKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, sealedKeyPrefix) || strings.Contains(sealed, testPlainKey) {
		t.Fatalf("sealed value %q is not prefixed or still carries the key", sealed)
	}
	again, err := sealKey(key, testPlainKey)
	if err != nil || again == sealed {
		t.Fatalf("second seal reused the nonce or failed: %v", err)
	}
	for _, v := range []string{sealed, again} {
		got, err := openKey(key, v)
		if err != nil || got != testPlainKey {
			t.Fatalf("openKey = %q, %v", got, err)
		}
	}
	other := make([]byte, 32)
	if _, err := openKey(other, sealed); err == nil {
		t.Fatal("openKey succeeded with another key")
	}
}

func TestSaveStoresCustomKeyEncrypted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.WebSettings.CustomModels = []WebCustomModel{testCustomModel()}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if data := readState(t, path); strings.Contains(data, testPlainKey) || !strings.Contains(data, `"api_key": "`+sealedKeyPrefix) {
		t.Fatal("state file holds the key in clear or without the sealed prefix")
	}
	info, err := os.Stat(filepath.Join(dir, customKeyFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, mode %v", err, info.Mode())
	}
	// The caller's list is left in clear for use.
	if cfg.WebSettings.CustomModels[0].APIKey != testPlainKey {
		t.Fatal("Save changed the caller's key")
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if m := loaded.WebSettings.CustomModels[0]; m.APIKey != testPlainKey || m.KeyError != "" {
		t.Fatalf("loaded key = %q, error %q", m.APIKey, m.KeyError)
	}
}

func TestLoadMigratesPlaintextCustomKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	legacy := fmt.Sprintf(`{"schema_version": %d, "web_settings": {"custom_models": [{"name": "acme", "base_url": "https://acme.example/v1", "model_id": "m1", "api_key_env": "", "api_key": %q}]}}`, CurrentSchemaVersion, testPlainKey)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if m := cfg.WebSettings.CustomModels[0]; m.APIKey != testPlainKey {
		t.Fatalf("loaded key = %q", m.APIKey)
	}
	if data := readState(t, path); strings.Contains(data, testPlainKey) || !strings.Contains(data, sealedKeyPrefix) {
		t.Fatal("plaintext key was not re-saved encrypted on load")
	}
	if _, err := os.Stat(filepath.Join(dir, customKeyFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWithoutKeyFileKeepsSealedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.WebSettings.CustomModels = []WebCustomModel{testCustomModel()}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, customKeyFileName)
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := loaded.WebSettings.CustomModels[0]
	if m.APIKey != "" || m.SealedKey == "" || !strings.Contains(m.KeyError, "missing") {
		t.Fatalf("APIKey %q, SealedKey set %v, KeyError %q", m.APIKey, m.SealedKey != "", m.KeyError)
	}
	// A save without the key file keeps the stored value and never a plain key.
	if err := s.Save(loaded); err != nil {
		t.Fatal(err)
	}
	if data := readState(t, path); !strings.Contains(data, m.SealedKey) || strings.Contains(data, testPlainKey) {
		t.Fatal("save without the key file dropped or exposed the sealed key")
	}
	if _, err := os.Stat(keyFile); err == nil {
		t.Fatal("save without a plain key created a new key file")
	}
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if m := restored.WebSettings.CustomModels[0]; m.APIKey != testPlainKey || m.KeyError != "" {
		t.Fatalf("after restoring the key file: APIKey %q, KeyError %q", m.APIKey, m.KeyError)
	}
}
