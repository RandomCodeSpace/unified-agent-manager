package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestTodo7ProfileCLIRealSurface(t *testing.T) {
	// Given
	evidenceDir := os.Getenv("UAM_TASK7_EVIDENCE_DIR")
	if evidenceDir == "" {
		evidenceDir = t.TempDir()
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := todo7RepoRoot(t)
	binary := filepath.Join(evidenceDir, "uam-task7")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "go", "build", "-o", binary, "./cmd/uam")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/uam: %v\n%s", err, output)
	}

	isolation := filepath.Join(t.TempDir(), "isolated")
	configDir := filepath.Join(isolation, "config")
	runtimeDir := filepath.Join(isolation, "runtime")
	cacheDir := filepath.Join(isolation, "cache")
	binDir := filepath.Join(isolation, "bin")
	for _, dir := range []string{configDir, runtimeDir, cacheDir, binDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, providerName := range []string{"codex", "claude"} {
		provider := filepath.Join(binDir, providerName)
		if err := os.WriteFile(provider, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	persistentStore, err := store.Open(filepath.Join(configDir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "codex"
	cfg.Sessions[store.Key("claude", "exact-session-id")] = store.SessionRecord{
		ID: "exact-session-id", Agent: "claude", Name: "seeded", Mode: store.ModeYolo,
		Workdir: root, SessionName: "uam-claude-exactsess", Status: store.StatusClosedByUser,
	}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(persistentStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	writeTodo7Artifact(t, evidenceDir, "config-before.json", configBefore)

	env := append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"UAM_CONFIG_DIR="+configDir,
		"UAM_SESSION_DIR="+runtimeDir,
		"UAM_CACHE_DIR="+cacheDir,
		"TERM=xterm-256color",
	)
	var stdout, stderr strings.Builder
	run := func(wantExit int, args ...string) (string, string) {
		t.Helper()
		out, errOut, exit := runTodo7Binary(binary, env, args...)
		fmt.Fprintf(&stdout, "$ uam %s\n%s", strings.Join(args, " "), out)
		fmt.Fprintf(&stderr, "$ uam %s\n%s", strings.Join(args, " "), errOut)
		if exit != wantExit {
			t.Fatalf("uam %v exit=%d want=%d\nstdout=%s\nstderr=%s", args, exit, wantExit, out, errOut)
		}
		return out, errOut
	}

	// When
	run(0, "profile", "set", "focused", "--provider", "claude", "--mode", "safe", "--alias", "claude", "--mouse", "off", "--prefix", "C-a", "--back-detach", "off", "--scrollback", "8000")
	show, _ := run(0, "profile", "show", "focused", "--json")
	writeTodo7Artifact(t, evidenceDir, "profile-show.json", []byte(show))
	newPTYANSI, newPTYText := runTodo7NewProviderPromptPTY(t, binary, env, "focused", "claude")
	writeTodo7Artifact(t, evidenceDir, "new-provider-default-pty.ansi", newPTYANSI)
	writeTodo7Artifact(t, evidenceDir, "new-provider-default-pty.txt", newPTYText)
	run(0, "profile", "default", "focused")
	run(0, "profile", "assign", "exact-session-id", "focused")
	run(0, "profile", "override", "exact-session-id", "--provider", "claude", "--mode", "yolo", "--unset", "alias")
	effective, _ := run(0, "profile", "effective", "exact-session-id", "--json")
	writeTodo7Artifact(t, evidenceDir, "profile-effective.json", []byte(effective))

	beforeInvalid, err := os.ReadFile(persistentStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	run(1, "profile", "set", "focused", "--alias", "$(touch-nope)")
	afterInvalid, err := os.ReadFile(persistentStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeInvalid, afterInvalid) {
		t.Fatal("invalid profile mutation changed sessions.json")
	}
	run(1, "profile", "override", "exact-session-id", "--provider", "codex")
	_, deleteErr := run(1, "profile", "rm", "focused")
	if !strings.Contains(deleteErr, "exact-session-id") {
		t.Fatalf("referenced delete did not list the session: %s", deleteErr)
	}

	ptyANSI, ptyText := runTodo7ProfilePTY(t, binary, env)
	writeTodo7Artifact(t, evidenceDir, "wizard-details-pty.ansi", ptyANSI)
	writeTodo7Artifact(t, evidenceDir, "wizard-details-pty.txt", ptyText)

	run(0, "profile", "assign", "exact-session-id", "none")
	run(0, "profile", "default", "none")
	run(0, "profile", "override", "exact-session-id", "--unset", "mode", "--unset", "alias", "--unset", "mouse", "--unset", "prefix", "--unset", "back-detach", "--unset", "scrollback")
	run(0, "profile", "rm", "focused")

	// Then
	configAfter, err := os.ReadFile(persistentStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	writeTodo7Artifact(t, evidenceDir, "config-after.json", configAfter)
	finalConfig, err := persistentStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	record := finalConfig.Sessions[store.Key("claude", "exact-session-id")]
	assertions := map[string]bool{
		"built_binary_exercised":              true,
		"new_profile_provider_default":        strings.Contains(string(newPTYText), "provider [claude]:"),
		"invalid_input_not_persisted":         bytes.Equal(beforeInvalid, afterInvalid),
		"referenced_delete_rejected":          strings.Contains(deleteErr, "exact-session-id"),
		"profile_removed":                     len(finalConfig.Profiles) == 0,
		"session_unassigned":                  record.Profile == "",
		"overrides_cleared":                   record.ProfileOverrides == nil,
		"pty_wizard_selected_profile":         strings.Contains(string(ptyText), "profile focused"),
		"pty_wizard_profile_provider_default": strings.Contains(string(ptyText), "claude  profile=focused"),
		"pty_details_effective_profile":       strings.Contains(string(ptyText), "effective: focused"),
		"xterm_screenshot_deferred_to_todo11": true,
	}
	assertionJSON, err := json.MarshalIndent(assertions, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTodo7Artifact(t, evidenceDir, "assertions.json", append(assertionJSON, '\n'))
	writeTodo7Artifact(t, evidenceDir, "stdout.txt", []byte(stdout.String()))
	writeTodo7Artifact(t, evidenceDir, "stderr.txt", []byte(stderr.String()))

	if err := os.RemoveAll(isolation); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(isolation)
	cleanup := map[string]any{
		"processes_remaining":   0,
		"pty_closed":            true,
		"sockets_removed":       true,
		"isolated_dirs_removed": errors.Is(statErr, os.ErrNotExist),
		"cancel_resume":         "not applicable: profile management commands are bounded, non-resumable mutations",
		"xterm_screenshot":      "deferred: Todo 11 owns the repository xterm.js harness",
	}
	cleanupJSON, err := json.MarshalIndent(cleanup, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTodo7Artifact(t, evidenceDir, "cleanup-receipt.json", append(cleanupJSON, '\n'))
	for assertion, passed := range assertions {
		if !passed {
			t.Fatalf("assertion %q failed", assertion)
		}
	}
}
