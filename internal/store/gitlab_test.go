package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWebGitLabReceiptsSurviveReopenAndTaskDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.WebSettings.GitLabAutoInvestigate = true
	const taskID = "a19f4d5c-8b12-4ec6-9d4a-c34e1a4b5f76"
	cfg.Sessions["copilot:"+taskID] = SessionRecord{
		ID: taskID, Agent: "copilot", SessionName: "uam-copilot-" + taskID,
	}
	want := WebGitLabCommit{
		TaskID: "source-task", ProjectID: "uam-project", Host: "gitlab.example.com",
		Path: "team/project", SHA: "0123456789abcdef0123456789abcdef01234567", Branch: "feature",
		CreatedAt: time.Date(2026, 10, 11, 1, 2, 3, 0, time.UTC),
		Investigations: map[string]WebGitLabInvestigation{
			"42:51": {TaskID: taskID, Registered: true},
			"42:52": {TaskID: "reserved-task"},
		},
	}
	cfg.WebGitLab = map[string]WebGitLabCommit{"commit-key": want}
	if err := st.Save(cfg); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Update(func(cfg *Config) error {
		delete(cfg.Sessions, "copilot:"+taskID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.WebSettings.GitLabAutoInvestigate {
		t.Fatal("automatic investigation setting was lost on reopen")
	}
	if got := loaded.WebGitLab["commit-key"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("commit provenance or pipeline receipt changed: got %#v, want %#v", got, want)
	}
	if _, ok := loaded.Sessions["copilot:"+taskID]; ok {
		t.Fatal("investigation Task was not deleted")
	}
}

func TestWebGitLabKnownFieldsDoNotShadowClearedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	doc := []byte(`{"schema_version":4,"profiles":{},"sessions":{},"root_extension":{"keep":true},"web_settings":{"gitlab_auto_investigate":true,"settings_extension":{"keep":true}},"web_gitlab":{"old":{"task_id":"source"}}}`)
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *Config) error {
		cfg.WebSettings.GitLabAutoInvestigate = false
		cfg.WebGitLab = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if hasJSONPath(t, after, "web_settings", "gitlab_auto_investigate") || hasJSONPath(t, after, "web_gitlab") {
		t.Fatalf("cleared modeled fields were restored by unknown-field preservation: %s", after)
	}
	if !hasJSONPath(t, after, "root_extension") || !hasJSONPath(t, after, "web_settings", "settings_extension") {
		t.Fatalf("unrelated extension was dropped: %s", after)
	}
	var empty WebSettings
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.GitLabAutoInvestigate {
		t.Fatal("automatic investigation must default to disabled")
	}
}
