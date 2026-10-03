package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func stageTestSkill(t *testing.T, stage, name string) {
	t.Helper()
	dir := filepath.Join(stage, ".agents", "skills", name)
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Test skill\n---\nUse the script.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsSetupRoutesRequireAuthenticationAndTerminal(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	for _, route := range []string{"list", "install"} {
		path := "/api/configuration/skills/" + route
		body := `{"source":"vercel-labs/agent-skills","skills":["web-design-guidelines"]}`
		if response := ts.do(http.MethodPost, path, body); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s without authentication: %d", route, response.Code)
		}
		if response := ts.do(http.MethodPost, path, body, withCookie(ts)); response.Code != http.StatusForbidden {
			t.Fatalf("%s without Terminal: %d", route, response.Code)
		}
	}
}

func TestSkillsSetupInput(t *testing.T) {
	for _, source := range []string{"owner/repo", "owner/.github", "https://github.com/owner/repo/tree/main/skills/test"} {
		if err := validateSkillSetup(skillSetupInput{Source: source, Skills: []string{"test-skill"}}, true); err != nil {
			t.Errorf("valid source %q: %v", source, err)
		}
	}
	for _, source := range []string{"../skills", "./skills", "owner/..", "/tmp/skills", "--all", "file:///tmp/skills", "https://user:secret@example.com/repo", "https://example.com/repo\n--all"} {
		if err := validateSkillSetup(skillSetupInput{Source: source, Skills: []string{"test-skill"}}, true); statusOf(err) != http.StatusBadRequest {
			t.Errorf("invalid source %q: %v", source, err)
		}
	}
	for _, names := range [][]string{nil, {"--all"}, {"../escape"}, {"*"}, {"test", "test"}} {
		if err := validateSkillSetup(skillSetupInput{Source: "owner/repo", Skills: names}, true); statusOf(err) != http.StatusBadRequest {
			t.Errorf("invalid skills %v: %v", names, err)
		}
	}
}

func TestSkillsSetupStagesAndPublishes(t *testing.T) {
	m, _, _ := newTestManager(t)
	project, err := m.AddProject(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
	var stage string
	result, err := m.setupSkills(context.Background(), project.ID, skillSetupInput{Source: "owner/repo", Skills: []string{"probe"}}, true, func(_ context.Context, dir string, args []string) (string, error) {
		stage = dir
		want := []string{"--yes", "skills@1.7.0", "add", "owner/repo", "--agent", "github-copilot", "--yes", "--copy", "--json", "--skill", "probe"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("argv = %#v", args)
		}
		stageTestSkill(t, dir, "probe")
		return `[{"name":"probe","status":"installed"}]`, nil
	})
	if err != nil || !reflect.DeepEqual(result.Installed, []string{"probe"}) {
		t.Fatalf("setup = %+v, %v", result, err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stage not removed: %v", err)
	}
	script := filepath.Join(project.Dir, ".github", "skills", "probe", "scripts", "run.sh")
	if info, err := os.Stat(script); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("script = %v, %v", info, err)
	}
}

func TestSkillsSetupGlobalAndTerminalGate(t *testing.T) {
	global := t.TempDir()
	t.Setenv("COPILOT_HOME", global)
	m, _, _ := newTestManager(t)
	run := func(_ context.Context, dir string, _ []string) (string, error) {
		stageTestSkill(t, dir, "probe")
		return `[{"name":"probe","status":"installed"}]`, nil
	}
	if _, err := m.setupSkills(context.Background(), "", skillSetupInput{Source: "owner/repo", Skills: []string{"probe"}}, true, run); statusOf(err) != http.StatusForbidden {
		t.Fatalf("terminal off = %v", err)
	}
	if _, err := os.Stat(filepath.Join(global, "skills")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("terminal off wrote files: %v", err)
	}
	on := true
	if _, err := m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.setupSkills(context.Background(), "", skillSetupInput{Source: "owner/repo", Skills: []string{"probe"}}, true, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(global, "skills", "probe", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsSetupTerminalDisabledDuringInstall(t *testing.T) {
	global := t.TempDir()
	t.Setenv("COPILOT_HOME", global)
	m, _, _ := newTestManager(t)
	on := true
	if _, err := m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
	_, err := m.setupSkills(context.Background(), "", skillSetupInput{Source: "owner/repo", Skills: []string{"probe"}}, true, func(_ context.Context, dir string, _ []string) (string, error) {
		stageTestSkill(t, dir, "probe")
		off := false
		if _, err := m.UpdateSettings(SettingsPatch{Terminal: &off}); err != nil {
			t.Fatal(err)
		}
		return `[{"name":"probe","status":"installed"}]`, nil
	})
	if statusOf(err) != http.StatusForbidden {
		t.Fatalf("disabled terminal = %v", err)
	}
	if _, err := os.Stat(filepath.Join(global, "skills")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("published while disabled: %v", err)
	}
}

func TestSkillsSetupListingHasNoPublication(t *testing.T) {
	global := t.TempDir()
	t.Setenv("COPILOT_HOME", global)
	m, _, _ := newTestManager(t)
	on := true
	if _, err := m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
	result, err := m.setupSkills(context.Background(), "", skillSetupInput{Source: "owner/repo"}, false, func(_ context.Context, dir string, args []string) (string, error) {
		if args[len(args)-1] != "--list" {
			t.Fatalf("list argv %v", args)
		}
		return "Available skills: probe", nil
	})
	if err != nil || result.Output != "Available skills: probe" {
		t.Fatalf("list = %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(global, "skills")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("list published: %v", err)
	}
}

func TestSkillsSetupRefusesIncompleteResults(t *testing.T) {
	for _, output := range []string{"not JSON", `[]`, `[{"name":"probe","status":"failed"}]`, `[{"name":"other","status":"installed"}]`} {
		if err := validateSkillSetupResult(output, []string{"probe"}); err == nil {
			t.Errorf("accepted %s", output)
		}
	}
}

func TestSkillsSetupPreservesExistingSkills(t *testing.T) {
	dir, stage := t.TempDir(), t.TempDir()
	for _, name := range []string{"new", "existing"} {
		stageTestSkill(t, stage, name)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll("skills/existing", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("skills/existing/SKILL.md", []byte("user content"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = publishStagedSkills(root, "skills", stage, []string{"new", "existing"})
	if statusOf(err) != http.StatusConflict {
		t.Fatalf("collision = %v", err)
	}
	if _, err := root.Stat("skills/new"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partially published: %v", err)
	}
	if data, err := root.ReadFile("skills/existing/SKILL.md"); err != nil || string(data) != "user content" {
		t.Fatalf("existing content = %s %v", data, err)
	}
}

func TestSkillsSetupRefusesSymlinks(t *testing.T) {
	t.Run("source parent outside staging", func(t *testing.T) {
		stage, outside := t.TempDir(), t.TempDir()
		stageTestSkill(t, outside, "probe")
		if err := os.Symlink(filepath.Join(outside, ".agents"), filepath.Join(stage, ".agents")); err != nil {
			t.Fatal(err)
		}
		if err := validateStagedSkills(stage, []string{"probe"}); err == nil {
			t.Fatal("validated a skill outside the staging root")
		}
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		if err := publishStagedSkills(root, "skills", stage, []string{"probe"}); err == nil {
			t.Fatal("published a skill outside the staging root")
		}
		if _, err := root.Lstat("skills/probe"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("failed publication left a partial skill: %v", err)
		}
	})
	t.Run("source", func(t *testing.T) {
		stage := t.TempDir()
		stageTestSkill(t, stage, "probe")
		if err := os.Symlink("SKILL.md", filepath.Join(stage, ".agents", "skills", "probe", "link")); err != nil {
			t.Fatal(err)
		}
		if err := validateStagedSkills(stage, []string{"probe"}); err == nil {
			t.Fatal("accepted source symlink")
		}
	})
	for _, target := range []string{"other", t.TempDir()} {
		t.Run(target, func(t *testing.T) {
			dir, stage := t.TempDir(), t.TempDir()
			stageTestSkill(t, stage, "probe")
			if err := os.Mkdir(filepath.Join(dir, "other"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, "skills")); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			if err := publishStagedSkills(root, "skills", stage, []string{"probe"}); err == nil {
				t.Fatal("accepted destination symlink")
			}
		})
	}
}

func TestSkillsSetupRunnerBoundsAndIsolation(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"success", "printf ok", ""},
		{"failure", "printf 'failed install' >&2; exit 1", "failed install"},
		{"too much output", "head -c 140000 /dev/zero", "exceeded"},
		{"timeout", "sleep 10", "deadline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\n[ \"$XDG_STATE_HOME\" = \"$PWD/state\" ] || exit 3\n[ \"$npm_config_cache\" = \"$PWD/npm-cache\" ] || exit 4\n[ \"$npm_config_ignore_scripts\" = true ] || exit 5\n" + tc.script + "\n"
			if err := os.WriteFile(filepath.Join(bin, "npx"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx := context.Background()
			cancel := func() {}
			if tc.name == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
			}
			defer cancel()
			output, err := runSkillsSetup(ctx, t.TempDir(), []string{"--yes", skillsPackage})
			if tc.want == "" {
				if err != nil || output != "ok" {
					t.Fatalf("run = %q, %v", output, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}
