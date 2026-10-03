package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testSkillDefinition = "---\nname: example\ndescription: An example skill\nmetadata:\n  future-option: keep-me\n---\nFollow these instructions.\n"

func configurationManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	return &Manager{projects: map[string]*Project{"project": {ID: "project", Dir: t.TempDir()}}, settings: Settings{Terminal: true}}
}

func configurationStatus(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func TestConfigurationNativeFilesAndRevisions(t *testing.T) {
	m := configurationManager(t)
	for _, project := range []string{"", "project"} {
		for _, kind := range []string{"agents", "skills", "hooks", "instructions"} {
			name, content := "example", testSkillDefinition
			if kind == "hooks" {
				content = `{"version":1,"hooks":{"preToolUse":[{"type":"command","bash":"printf done","future-option":true}]}}`
			}
			if kind == "instructions" {
				name = "copilot-instructions"
				content = "Keep every change focused.\n"
			}
			file, err := m.SaveConfiguration(project, kind, name, configurationInput{Content: content}, false)
			if err != nil {
				t.Fatal(project, kind, err)
			}
			data, err := os.ReadFile(file.Path)
			if err != nil || string(data) != content || file.Content != content || file.Revision == "" {
				t.Fatalf("native file was not preserved for %s/%s", project, kind)
			}
			if _, err = m.SaveConfiguration(project, kind, name, configurationInput{Content: content}, false); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("duplicate create = %v", err)
			}
			changed := content + "\n"
			updated, err := m.SaveConfiguration(project, kind, name, configurationInput{Content: changed, Revision: file.Revision}, false)
			if err != nil || updated.Revision == file.Revision {
				t.Fatalf("update = %v", err)
			}
			if _, err = m.SaveConfiguration(project, kind, name, configurationInput{Revision: file.Revision}, true); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("stale delete = %v", err)
			}
			if _, err = m.SaveConfiguration(project, kind, name, configurationInput{Revision: updated.Revision}, true); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(file.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("removed file stat = %v", err)
			}
			if kind == "skills" {
				if _, err = os.Stat(filepath.Dir(file.Path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("empty skill directory remains")
				}
				if _, err = m.SaveConfiguration(project, kind, name, configurationInput{Content: content}, false); err != nil {
					t.Fatalf("recreate skill: %v", err)
				}
			}
		}
	}
}

func TestConfigurationSkillRemovalPreservesSupportFiles(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "example", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	support := filepath.Join(filepath.Dir(file.Path), "helper.sh")
	if err = os.WriteFile(support, []byte("keep this"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SaveConfiguration("project", "skills", "example", configurationInput{Revision: file.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(support); err != nil || string(data) != "keep this" {
		t.Fatal("support file changed")
	}
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Skills) != 0 {
		t.Fatalf("deactivated skill still listed: %v", err)
	}
}

func TestConfigurationTerminalGateAndScope(t *testing.T) {
	m := configurationManager(t)
	m.settings.Terminal = false
	for _, kind := range []string{"agents", "hooks", "skills"} {
		if _, err := m.SaveConfiguration("project", kind, "example", configurationInput{Content: testSkillDefinition}, false); configurationStatus(err) != http.StatusForbidden {
			t.Fatalf("%s without Terminal = %v", kind, err)
		}
	}
	if _, err := m.SaveConfiguration("project", "instructions", "copilot-instructions", configurationInput{Content: "Use clear names."}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveConfiguration("missing", "skills", "example", configurationInput{Content: testSkillDefinition}, false); configurationStatus(err) != http.StatusNotFound {
		t.Fatalf("unknown project = %v", err)
	}
	for _, name := range []string{"../escape", "/absolute", "a/b", "..", "bad\\name"} {
		if _, err := m.SaveConfiguration("project", "skills", name, configurationInput{Content: testSkillDefinition}, false); configurationStatus(err) != http.StatusBadRequest {
			t.Fatalf("unsafe name %q = %v", name, err)
		}
	}
}

func TestConfigurationRefusesSymlinksAndNonRegularFiles(t *testing.T) {
	for _, target := range []string{"parent", "file"} {
		t.Run(target, func(t *testing.T) {
			m := configurationManager(t)
			scope, _ := m.configurationScope("project")
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "copilot-instructions.md"), []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			if target == "parent" {
				if err := os.Symlink(outside, filepath.Join(scope.base, ".github")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(scope.base, ".github"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "copilot-instructions.md"), filepath.Join(scope.base, ".github", "copilot-instructions.md")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.SaveConfiguration("project", "instructions", "copilot-instructions", configurationInput{Content: "changed"}, false); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("symlink write = %v", err)
			}
			file := configurationFile(scope, "instructions", "copilot-instructions", true)
			if file.Content != "" || file.Editable || file.Error == "" {
				t.Fatal("symlink content was exposed")
			}
			if data, err := os.ReadFile(filepath.Join(outside, "copilot-instructions.md")); err != nil || string(data) != "private" {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestConfigurationPublishDoesNotOverwriteConcurrentChange(t *testing.T) {
	m := configurationManager(t)
	scope, _ := m.configurationScope("project")
	root, err := openConfigurationRoot(scope, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, name := range []string{"staged", "destination"} {
		if err := os.WriteFile(filepath.Join(scope.base, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := publishConfiguration(root, "staged", "destination", ""); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("concurrent creation = %v", err)
	}
	_, revision, err := readConfiguration(root, "destination")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scope.base, "destination"), []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = publishConfiguration(root, "staged", "destination", revision); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("concurrent edit = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(scope.base, "destination")); err != nil || string(data) != "external edit" {
		t.Fatal("external edit lost")
	}
}

func TestConfigurationCatalogSharedSkillsEditableAndBuiltinsReadOnly(t *testing.T) {
	m := configurationManager(t)
	scope, _ := m.configurationScope("project")
	shared := filepath.Join(scope.base, ".agents", "skills", "shared")
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "SKILL.md"), []byte(testSkillDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Skills) != 1 || !cfg.Skills[0].Editable || cfg.Skills[0].Content != testSkillDefinition {
		t.Fatalf("shared skill catalog = %v", err)
	}
	if cfg.Scope != "project" || cfg.ProjectID != "project" || !cfg.Instructions.Editable || cfg.Instructions.Revision != "" {
		t.Fatal("scope or missing instructions incorrect")
	}
	m.skillDirs = []string{filepath.Join(t.TempDir(), "skills")}
	if err := installSkills(m.skillDirs[0]); err != nil {
		t.Fatal(err)
	}
	cfg, err = m.Configuration("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range cfg.Skills {
		if f.Name == "uam" && strings.HasPrefix(f.Path, m.skillDirs[0]) {
			found = true
			if f.Editable {
				t.Fatal("builtin editable")
			}
		}
	}
	if !found {
		t.Fatal("builtin missing")
	}
}

func TestConfigurationValidationAndHTTP(t *testing.T) {
	for _, tc := range []struct{ kind, content string }{{"skills", "no frontmatter"}, {"hooks", `{"version":2,"hooks":{}}`}, {"hooks", `{"version":1,"hooks":{"sessionStart":null}}`}, {"hooks", `{"version":1,"hooks":{"sessionStart":[null]}}`}, {"instructions", strings.Repeat("a", maxConfigurationBytes+1)}} {
		if err := validateConfiguration(tc.kind, tc.content); err == nil {
			t.Fatalf("invalid %s accepted", tc.kind)
		}
	}
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	if w := ts.do(http.MethodGet, "/api/configuration", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", w.Code)
	}
	body, _ := json.Marshal(configurationInput{Content: "Global instructions"})
	url := "/api/configuration/instructions/copilot-instructions"
	w := ts.do(http.MethodPut, url, string(body), withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPut, url, `{"revision":""}`, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("missing content = %d", w.Code)
	}
	if w := ts.do(http.MethodDelete, url, `{}`, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("missing revision = %d", w.Code)
	}
}

func TestConfigurationProjectAgentInstructions(t *testing.T) {
	m := configurationManager(t)
	m.settings.Terminal = false
	project := m.projects["project"].Dir
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.InstructionFiles) != 2 || cfg.InstructionFiles[0] != cfg.Instructions {
		t.Fatalf("project instruction files = %+v, %v", cfg, err)
	}
	file := cfg.InstructionFiles[1]
	if file.Name != "agents" || file.Path != filepath.Join(project, "AGENTS.md") || !file.Editable || file.Revision != "" || file.Error != "" {
		t.Fatalf("missing AGENTS.md placeholder = %+v", file)
	}
	copilot, err := m.SaveConfiguration("project", "instructions", "copilot-instructions", configurationInput{Content: "Existing Copilot instructions.\n"}, false)
	if err != nil {
		t.Fatal(err)
	}
	content := "# Agent instructions\nKeep changes focused.\n"
	created, err := m.SaveConfiguration("project", "instructions", "agents", configurationInput{Content: content}, false)
	if err != nil || created.Path != file.Path || created.Content != content || created.Revision == "" {
		t.Fatalf("create AGENTS.md = %+v, %v", created, err)
	}
	if data, err := os.ReadFile(filepath.Join(project, "AGENTS.md")); err != nil || string(data) != content {
		t.Fatalf("root AGENTS.md = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".github", "AGENTS.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("AGENTS.md must not be placed in .github: %v", err)
	}
	if _, err := m.SaveConfiguration("project", "instructions", "agents", configurationInput{Content: content}, false); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("duplicate AGENTS.md create = %v", err)
	}
	updated, err := m.SaveConfiguration("project", "instructions", "agents", configurationInput{Content: content + "Use scoped checks.\n", Revision: created.Revision}, false)
	if err != nil || updated.Revision == created.Revision {
		t.Fatalf("update AGENTS.md = %+v, %v", updated, err)
	}
	cfg, err = m.Configuration("project")
	if err != nil || cfg.Instructions != copilot || len(cfg.InstructionFiles) != 2 || cfg.InstructionFiles[1] != updated {
		t.Fatalf("instructions compatibility and listing = %+v, %v", cfg, err)
	}
	for _, remove := range []bool{false, true} {
		if _, err := m.SaveConfiguration("project", "instructions", "agents", configurationInput{Content: "stale", Revision: created.Revision}, remove); configurationStatus(err) != http.StatusConflict {
			t.Fatalf("stale AGENTS.md mutation, remove %v = %v", remove, err)
		}
	}
	if _, err := m.SaveConfiguration("project", "instructions", "agents", configurationInput{Revision: updated.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted AGENTS.md still present: %v", err)
	}
	if data, err := os.ReadFile(copilot.Path); err != nil || string(data) != copilot.Content {
		t.Fatal("AGENTS.md editing changed existing Copilot instructions")
	}
}

func TestConfigurationAgentInstructionsGlobalAndSymlinkGuards(t *testing.T) {
	m := configurationManager(t)
	cfg, err := m.Configuration("")
	if err != nil || len(cfg.InstructionFiles) != 1 || cfg.InstructionFiles[0] != cfg.Instructions {
		t.Fatalf("global instructions = %+v, %v", cfg, err)
	}
	for _, remove := range []bool{false, true} {
		if _, err := m.SaveConfiguration("", "instructions", "agents", configurationInput{Content: "No inert global file."}, remove); configurationStatus(err) != http.StatusBadRequest {
			t.Fatalf("global AGENTS.md mutation = %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("COPILOT_HOME"), "AGENTS.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported global AGENTS.md created: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(outside, []byte("Leave these instructions alone."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(m.projects["project"].Dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	cfg, err = m.Configuration("project")
	if err != nil || len(cfg.InstructionFiles) != 2 || cfg.InstructionFiles[1].Editable || cfg.InstructionFiles[1].Content != "" || cfg.InstructionFiles[1].Error == "" {
		t.Fatalf("symlinked AGENTS.md exposed = %+v, %v", cfg, err)
	}
	if _, err := m.SaveConfiguration("project", "instructions", "agents", configurationInput{Content: "Overwrite"}, false); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("symlinked AGENTS.md write = %v", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "Leave these instructions alone." {
		t.Fatal("symlink target changed")
	}
}

func TestConfigurationSkillSymlinksResolveAndDeduplicate(t *testing.T) {
	for _, project := range []string{"", "project"} {
		t.Run(project, func(t *testing.T) {
			m := configurationManager(t)
			scope, _ := m.configurationScope(project)
			direct, err := m.SaveConfiguration(project, "skills", "z-managed", configurationInput{Content: testSkillDefinition}, false)
			if err != nil {
				t.Fatal(err)
			}
			root := scope.root("skills")
			if err := os.Symlink(filepath.Dir(direct.Path), filepath.Join(root, "a-directory-alias")); err != nil {
				t.Fatal(err)
			}
			fileAlias := filepath.Join(root, "b-file-alias", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(fileAlias), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(direct.Path, fileAlias); err != nil {
				t.Fatal(err)
			}
			sharedRoot := filepath.Join(scope.base, ".agents", "skills")
			if scope.global {
				sharedRoot = filepath.Join(t.TempDir(), "builtin-pack")
				m.skillDirs = []string{sharedRoot}
			}
			if err := os.MkdirAll(sharedRoot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Dir(direct.Path), filepath.Join(sharedRoot, "shared")); err != nil {
				t.Fatal(err)
			}
			rootAlias := filepath.Join(scope.base, ".claude", "skills")
			if err := os.MkdirAll(filepath.Dir(rootAlias), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(sharedRoot, rootAlias); err != nil {
				t.Fatal(err)
			}
			if scope.global {
				m.skillDirs = append(m.skillDirs, rootAlias)
			}
			cfg, err := m.Configuration(project)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, file := range cfg.Skills {
				if file.Path == direct.Path {
					count++
					if file.Name != "z-managed" || !file.Editable || file.Content != testSkillDefinition || file.Error != "" {
						t.Fatalf("managed direct file must win: %+v", file)
					}
				}
			}
			if count != 1 {
				t.Fatalf("direct/alias skill count = %d", count)
			}
			for _, alias := range []string{"a-directory-alias", "b-file-alias"} {
				for _, remove := range []bool{false, true} {
					if _, err := m.SaveConfiguration(project, "skills", alias, configurationInput{Content: testSkillDefinition, Revision: direct.Revision}, remove); configurationStatus(err) != http.StatusConflict {
						t.Fatalf("symlink mutation %s, remove %v = %v", alias, remove, err)
					}
				}
			}
			if data, err := os.ReadFile(direct.Path); err != nil || string(data) != testSkillDefinition {
				t.Fatal("alias mutation changed the target")
			}
		})
	}
}

func TestConfigurationExternalSkillAliasesAreReadableAndReadOnly(t *testing.T) {
	m := configurationManager(t)
	scope, _ := m.configurationScope("project")
	target := filepath.Join(t.TempDir(), "external", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(testSkillDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{".github", ".agents", ".claude"} {
		root := filepath.Join(scope.base, kind, "skills")
		if err := os.MkdirAll(filepath.Join(root, "file-alias"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "file-alias", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Dir(target), filepath.Join(root, "directory-alias")); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Skills) != 1 {
		t.Fatalf("external alias listing = %+v, %v", cfg.Skills, err)
	}
	file := cfg.Skills[0]
	if file.Path != target || file.Editable || file.Error != "" || file.Content != testSkillDefinition || file.Revision == "" {
		t.Fatalf("external skill = %+v", file)
	}
}

func TestConfigurationBrokenSkillLinksAreNotUsable(t *testing.T) {
	m := configurationManager(t)
	scope, _ := m.configurationScope("project")
	good, err := m.SaveConfiguration("project", "skills", "valid", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := scope.root("skills")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(root, "broken-directory")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "broken-file"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing.md"), filepath.Join(root, "broken-file", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(scope.base, ".agents", "skills")
	if err := os.MkdirAll(filepath.Dir(shared), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing-skills"), shared); err != nil {
		t.Fatal(err)
	}
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Skills) != 1 {
		t.Fatalf("broken links appear as usable skills: %+v, %v", cfg.Skills, err)
	}
	if cfg.Skills[0].Path != good.Path || cfg.Skills[0].Content != testSkillDefinition || !cfg.Skills[0].Editable {
		t.Fatalf("valid skill was lost: %+v", cfg.Skills)
	}
	want := []string{filepath.Join(root, "broken-directory", "SKILL.md"), filepath.Join(root, "broken-file", "SKILL.md"), shared}
	if len(cfg.ConflictDetails) != len(want) || len(cfg.ConflictWarnings) != len(want) {
		t.Fatalf("missing file diagnostics: %+v", cfg.ConflictDetails)
	}
	for _, detail := range cfg.ConflictDetails {
		if detail.Kind != "skills" || !slices.Contains(want, detail.Path) || !strings.Contains(detail.Message, "no such file") {
			t.Fatalf("missing-skill diagnostic = %+v", detail)
		}
	}
}

func TestConfigurationLinkedSkillReadLimits(t *testing.T) {
	for _, kind := range []string{"oversize", "directory", "invalid-text"} {
		t.Run(kind, func(t *testing.T) {
			m := configurationManager(t)
			target := filepath.Join(t.TempDir(), "SKILL.md")
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				data := []byte{0xff}
				if kind == "oversize" {
					data = []byte(strings.Repeat("a", maxConfigurationBytes+1))
				}
				if err := os.WriteFile(target, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(m.projects["project"].Dir, ".agents", "skills", "linked")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			cfg, err := m.Configuration("project")
			if err != nil || len(cfg.Skills) != 0 || len(cfg.ConflictDetails) != 1 || cfg.ConflictDetails[0].Kind != "skills" || cfg.ConflictDetails[0].Path != target {
				t.Fatalf("unsafe linked content exposed or its diagnostic lost: %+v, %v", cfg, err)
			}
		})
	}
}

func TestConfigurationDuplicatesAcrossProjectAndGlobal(t *testing.T) {
	m := configurationManager(t)
	name := "duplicate-review-fixture"
	paths := map[string][]string{}
	for _, project := range []string{"", "project"} {
		for _, kind := range []string{"agents", "skills", "hooks", "instructions"} {
			resource, content := name, testSkillDefinition
			if kind == "hooks" {
				content = `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":"printf ready"}]}}`
			}
			if kind == "instructions" {
				resource, content = "copilot-instructions", "Instructions for "+project
			}
			file, err := m.SaveConfiguration(project, kind, resource, configurationInput{Content: content}, false)
			if err != nil {
				t.Fatal(err)
			}
			paths[kind] = append(paths[kind], file.Path)
		}
	}
	cfg, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"agents", "skills"} {
		i := slices.IndexFunc(cfg.Conflicts, func(conflict ConfigurationConflict) bool { return conflict.Kind == kind && conflict.Name == name })
		if i < 0 {
			t.Fatalf("missing %s duplicate group", kind)
		}
		slices.Sort(paths[kind])
		if !slices.Equal(cfg.Conflicts[i].Paths, paths[kind]) {
			t.Fatalf("%s duplicate paths = %v", kind, cfg.Conflicts[i].Paths)
		}
	}
	for _, conflict := range cfg.Conflicts {
		if conflict.Kind != "agents" && conflict.Kind != "skills" {
			t.Fatalf("composable configuration marked as conflict: %+v", conflict)
		}
	}
	if len(cfg.Agents) != 1 || len(cfg.Skills) != 1 || !cfg.Agents[0].Editable || !cfg.Skills[0].Editable {
		t.Fatal("cross-scope checks changed the selected scope's editable listing")
	}
	for _, kind := range []string{"agents", "skills"} {
		for _, path := range paths[kind] {
			if data, err := os.ReadFile(path); err != nil || string(data) != testSkillDefinition {
				t.Fatalf("duplicate detection modified %s", path)
			}
		}
	}
}

func TestConfigurationDuplicateSkillsExcludeAliasesAndInvalidEntries(t *testing.T) {
	m := configurationManager(t)
	name := "duplicate-alias-fixture"
	global, err := m.SaveConfiguration("", "skills", name, configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(m.projects["project"].Dir, ".agents", "skills")
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(global.Path), filepath.Join(shared, name)); err != nil {
		t.Fatal(err)
	}
	cfg, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(cfg.Conflicts, func(conflict ConfigurationConflict) bool { return conflict.Name == name }) {
		t.Fatal("a project/global canonical alias was marked as a duplicate definition")
	}
	other := filepath.Join(m.projects["project"].Dir, ".claude", "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(testSkillDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(cfg.Conflicts, func(conflict ConfigurationConflict) bool { return conflict.Name == name })
	if i < 0 || len(cfg.Conflicts[i].Paths) != 2 || !slices.Contains(cfg.Conflicts[i].Paths, other) || !slices.Contains(cfg.Conflicts[i].Paths, global.Path) {
		t.Fatal("distinct same-name files were not grouped after canonical alias deduplication")
	}
	invalid := []ConfigurationFile{
		global,
		{Name: name, Path: other, Error: "unreadable", Revision: "present"},
		{Name: name, Path: other},
		{Name: name, Path: filepath.Join(t.TempDir(), "missing"), Revision: "stale"},
	}
	if conflicts := configurationDuplicateDefinitions("skills", invalid); len(conflicts) != 0 {
		t.Fatalf("missing or errored definitions formed a group: %+v", conflicts)
	}
}

func TestConfigurationGlobalDuplicateSkills(t *testing.T) {
	m := configurationManager(t)
	name := "duplicate-builtin-fixture"
	native, err := m.SaveConfiguration("", "skills", name, configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "builtin-pack")
	builtin := filepath.Join(root, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(builtin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(builtin, []byte(testSkillDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	m.skillDirs = []string{root}
	cfg, err := m.Configuration("")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(cfg.Conflicts, func(conflict ConfigurationConflict) bool { return conflict.Kind == "skills" && conflict.Name == name })
	if i < 0 || len(cfg.Conflicts[i].Paths) != 2 || !slices.Contains(cfg.Conflicts[i].Paths, native.Path) || !slices.Contains(cfg.Conflicts[i].Paths, builtin) {
		t.Fatal("global native/builtin duplicate not reported")
	}
}

func TestConfigurationAliasNameDoesNotHideOtherDuplicateIdentity(t *testing.T) {
	m := configurationManager(t)
	name := "duplicate-identity-fixture"
	global, err := m.SaveConfiguration("", "skills", name, configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(m.projects["project"].Dir, ".agents", "skills")
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(global.Path), filepath.Join(shared, "different-alias-name")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "builtin-pack")
	other := filepath.Join(root, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(testSkillDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	m.skillDirs = []string{root}
	cfg, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(cfg.Conflicts, func(conflict ConfigurationConflict) bool { return conflict.Kind == "skills" && conflict.Name == name })
	if i < 0 || len(cfg.Conflicts[i].Paths) != 2 || !slices.Contains(cfg.Conflicts[i].Paths, global.Path) || !slices.Contains(cfg.Conflicts[i].Paths, other) {
		t.Fatal("a differently named project alias hid the global same-name duplicate")
	}
	if slices.ContainsFunc(cfg.Conflicts, func(conflict ConfigurationConflict) bool { return conflict.Name == "different-alias-name" }) {
		t.Fatal("alias-only identity was marked as a duplicate")
	}
}

func TestConfigurationConflictWarningsKeepProjectListingAvailable(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "local", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	badHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badHome, []byte("unreadable global configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COPILOT_HOME", badHome)
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Skills) != 1 || cfg.Skills[0] != file {
		t.Fatalf("global conflict check prevented project listing: %v", err)
	}
	if len(cfg.ConflictWarnings) == 0 || !slices.ContainsFunc(cfg.ConflictWarnings, func(warning string) bool { return strings.Contains(warning, "Global agents") }) {
		t.Fatal("incomplete global conflict check was silently omitted")
	}
}

func TestConfigurationSharedSkillExactPathMutations(t *testing.T) {
	for _, project := range []string{"", "project"} {
		t.Run(project, func(t *testing.T) {
			m := configurationManager(t)
			base := m.projects["project"].Dir
			if project == "" {
				base = os.Getenv("HOME")
			}
			native, err := m.SaveConfiguration(project, "skills", "example", configurationInput{Content: testSkillDefinition}, false)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{}
			for _, dir := range []string{".agents", ".claude"} {
				path := filepath.Join(base, dir, "skills", "example", "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(testSkillDefinition), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, path)
			}
			cfg, err := m.Configuration(project)
			if err != nil || len(cfg.Skills) != 3 {
				t.Fatalf("shared listing = %+v, %v", cfg.Skills, err)
			}
			for _, file := range cfg.Skills {
				if !file.Editable || file.ReadOnlyReason != "" {
					t.Fatalf("regular skill not editable: %+v", file)
				}
			}
			updated, err := m.SaveConfiguration(project, "skills", "example", configurationInput{Content: testSkillDefinition + "Updated.\n", Revision: native.Revision, Path: paths[0]}, false)
			if err != nil || updated.Path != paths[0] || updated.Revision == native.Revision {
				t.Fatalf("shared save = %+v, %v", updated, err)
			}
			info, err := os.Stat(paths[0])
			if err != nil || info.Mode().Perm() != 0644 {
				t.Fatalf("existing shared file mode changed: %v, %v", info, err)
			}
			for _, path := range []string{native.Path, paths[1]} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != testSkillDefinition {
					t.Fatalf("same-name neighbor changed: %s", path)
				}
			}
			for _, remove := range []bool{false, true} {
				if _, err := m.SaveConfiguration(project, "skills", "example", configurationInput{Content: testSkillDefinition, Revision: native.Revision, Path: paths[0]}, remove); configurationStatus(err) != http.StatusConflict {
					t.Fatalf("stale explicit mutation = %v", err)
				}
			}
			support := filepath.Join(filepath.Dir(paths[0]), "helper.sh")
			if err := os.WriteFile(support, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.SaveConfiguration(project, "skills", "example", configurationInput{Revision: updated.Revision, Path: paths[0]}, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(paths[0]); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("selected skill still exists: %v", err)
			}
			if data, err := os.ReadFile(support); err != nil || string(data) != "keep" {
				t.Fatal("shared skill support file removed")
			}
		})
	}
}

func TestConfigurationExplicitPathGuards(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "example", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.SaveConfiguration("project", "skills", "other", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte(testSkillDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, revision string }{
		{"example", outside, file.Revision},
		{"example", other.Path, file.Revision},
		{"example", file.Path, ""},
		{"new", filepath.Join(filepath.Dir(file.Path), "..", "new", "SKILL.md"), "pretend-existing"},
		{"example", strings.TrimPrefix(file.Path, "/"), file.Revision},
	} {
		for _, remove := range []bool{false, true} {
			if _, err := m.SaveConfiguration("project", "skills", tc.name, configurationInput{Content: testSkillDefinition + "changed", Revision: tc.revision, Path: tc.path}, remove); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("explicit guard (%+v, remove %v) = %v", tc, remove, err)
			}
		}
	}
	m.settings.Terminal = false
	for _, remove := range []bool{false, true} {
		if _, err := m.SaveConfiguration("project", "skills", file.Name, configurationInput{Content: testSkillDefinition, Revision: file.Revision, Path: file.Path}, remove); configurationStatus(err) != http.StatusForbidden {
			t.Fatalf("explicit Terminal gate = %v", err)
		}
	}
	for _, path := range []string{file.Path, other.Path, outside} {
		if data, err := os.ReadFile(path); err != nil || string(data) != testSkillDefinition {
			t.Fatalf("guarded target changed: %s", path)
		}
	}
}

func TestConfigurationExplicitPathProtectsBuiltinExternalAndChangedLinks(t *testing.T) {
	for _, scenario := range []string{"builtin", "external", "replaced-file", "replaced-directory"} {
		t.Run(scenario, func(t *testing.T) {
			m := configurationManager(t)
			project := m.projects["project"].Dir
			base := filepath.Join(project, ".agents", "skills")
			path := filepath.Join(base, "example", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(testSkillDefinition), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := m.Configuration("project")
			if err != nil || len(cfg.Skills) != 1 {
				t.Fatal("fixture missing")
			}
			file := cfg.Skills[0]
			outside := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.WriteFile(outside, []byte(testSkillDefinition), 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "builtin":
				m.skillDirs = []string{base}
			case "external":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
				file.Path = outside
			case "replaced-file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "replaced-directory":
				if err := os.Rename(filepath.Dir(path), filepath.Dir(path)+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Dir(outside), filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err = m.Configuration("project")
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range cfg.Skills {
				if item.Path == outside || scenario == "builtin" && item.Path == path {
					if item.Editable || item.ReadOnlyReason == "" {
						t.Fatalf("protected target lacks readonly reason: %+v", item)
					}
				}
			}
			for _, remove := range []bool{false, true} {
				if _, err := m.SaveConfiguration("project", "skills", file.Name, configurationInput{Content: testSkillDefinition + "changed", Revision: file.Revision, Path: file.Path}, remove); configurationStatus(err) != http.StatusConflict {
					t.Fatalf("protected mutation = %v", err)
				}
			}
			disabled := true
			if _, err := m.SaveConfiguration("project", "skills", file.Name, configurationInput{Revision: file.Revision, Path: file.Path, Disabled: &disabled}, false); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("protected disable = %v", err)
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != testSkillDefinition {
				t.Fatal("outside target changed")
			}
		})
	}
}

func TestConfigurationGlobalSkillErrorDetailsStayKindSpecific(t *testing.T) {
	m := configurationManager(t)
	broken := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "broken")
	if err := os.MkdirAll(filepath.Dir(broken), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), broken); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"", "project"} {
		cfg, err := m.Configuration(project)
		if err != nil || len(cfg.Skills) != 0 || len(cfg.ConflictDetails) != 1 {
			t.Fatalf("missing skill diagnostic for %q: %+v, %v", project, cfg, err)
		}
		detail := cfg.ConflictDetails[0]
		if detail.Kind != "skills" || detail.Path != filepath.Join(broken, "SKILL.md") || !strings.Contains(detail.Message, "no such file") {
			t.Fatalf("wrong missing-skill diagnostic: %+v", detail)
		}
	}
}

func TestConfigurationExplicitPathHTTP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	url := "/api/configuration/instructions/copilot-instructions"
	w := ts.do(http.MethodPut, url, `{"content":"Initial","revision":""}`, withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var file ConfigurationFile
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(configurationInput{Content: "Updated", Revision: file.Revision, Path: file.Path})
	w = ts.do(http.MethodPut, url, string(body), withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("explicit PUT = %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(configurationInput{Revision: file.Revision, Path: file.Path})
	w = ts.do(http.MethodDelete, url, string(body), withCookie(ts))
	if w.Code != http.StatusNoContent {
		t.Fatalf("explicit DELETE = %d %s", w.Code, w.Body)
	}
}

func TestConfigurationSharedSkillDiscoveryLimitIsDiagnosed(t *testing.T) {
	m := configurationManager(t)
	root := filepath.Join(m.projects["project"].Dir, ".agents", "skills")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxConfigurationFiles; i++ {
		if _, err := os.MkdirTemp(root, "entry-"); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Skills) != 0 || len(cfg.ConflictDetails) != 1 {
		t.Fatalf("limit diagnostic = %+v, %v", cfg, err)
	}
	detail := cfg.ConflictDetails[0]
	if detail.Kind != "skills" || detail.Path != root || !strings.Contains(detail.Message, "128 entries") {
		t.Fatalf("limit diagnostic = %+v", detail)
	}
}

func TestConfigurationSkillRootRemainsAnchoredAfterParentReplacement(t *testing.T) {
	m := configurationManager(t)
	scope, _ := m.configurationScope("")
	base := os.Getenv("HOME")
	path := filepath.Join(base, ".agents", "skills", "example", "SKILL.md")
	other := filepath.Join(base, "other", "skills", "example", "SKILL.md")
	for _, file := range []string{path, other} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(testSkillDefinition), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, relative, err := openConfigurationSkillRoot(scope, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	_, revision, err := readConfiguration(root, relative)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(base, ".agents"), filepath.Join(base, ".agents-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "other"), filepath.Join(base, ".agents")); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(filepath.Dir(relative), "staged")
	if err := root.WriteFile(staged, []byte("Changed through anchored root"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishConfiguration(root, staged, relative, revision); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != testSkillDefinition {
		t.Fatal("parent replacement redirected write outside the known skills root")
	}
	if data, err := os.ReadFile(filepath.Join(base, ".agents-old", "skills", "example", "SKILL.md")); err != nil || string(data) != "Changed through anchored root" {
		t.Fatal("opened skills root was not retained")
	}
	if replacement, _, err := openConfigurationSkillRoot(scope, path); err == nil {
		if err := replacement.Close(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("linked known root was accepted")
	}
}

func TestConfigurationDisableRestoreAndRemove(t *testing.T) {
	for _, project := range []string{"", "project"} {
		for _, kind := range []string{"agents", "skills", "hooks"} {
			t.Run(project+"/"+kind, func(t *testing.T) {
				m := configurationManager(t)
				content := testSkillDefinition
				if kind == "hooks" {
					content = `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":"printf hello"}]}}`
				}
				file, err := m.SaveConfiguration(project, kind, "example", configurationInput{Content: content}, false)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(file.Path, 0640); err != nil {
					t.Fatal(err)
				}
				if project != "" {
					if _, err := m.SaveConfiguration("", kind, "example", configurationInput{Content: content}, false); err != nil {
						t.Fatal(err)
					}
				}
				cfg, err := m.Configuration(project)
				if err != nil {
					t.Fatal(err)
				}
				wantConflict := project != "" && kind != "hooks"
				if (len(cfg.Conflicts) != 0) != wantConflict {
					t.Fatalf("initial conflicts = %+v", cfg.Conflicts)
				}
				disabled := true
				stored, err := m.SaveConfiguration(project, kind, file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false)
				if err != nil || !stored.Disabled || stored.Path != file.Path+disabledConfigurationSuffix || stored.Content != content || stored.Revision != file.Revision {
					t.Fatalf("disable = %+v, %v", stored, err)
				}
				if _, err := os.Lstat(file.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("active definition remains: %v", err)
				}
				info, err := os.Stat(stored.Path)
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("mode changed: %v, %v", info, err)
				}
				cfg, err = m.Configuration(project)
				if err != nil || len(cfg.Conflicts) != 0 || len(cfg.ConflictDetails) != 0 {
					t.Fatalf("disabled conflicts = %+v, %v", cfg, err)
				}
				files := map[string][]ConfigurationFile{"agents": cfg.Agents, "skills": cfg.Skills, "hooks": cfg.Hooks}[kind]
				if len(files) != 1 || files[0] != stored {
					t.Fatalf("disabled listing = %+v", files)
				}
				if _, err := m.SaveConfiguration(project, kind, stored.Name, configurationInput{Path: stored.Path, Revision: stored.Revision, Content: content}, false); configurationStatus(err) != http.StatusConflict {
					t.Fatalf("disabled edit = %v", err)
				}
				disabled = false
				restored, err := m.SaveConfiguration(project, kind, stored.Name, configurationInput{Path: stored.Path, Revision: stored.Revision, Disabled: &disabled}, false)
				if err != nil || restored != file {
					t.Fatalf("restore = %+v, %v", restored, err)
				}
				cfg, err = m.Configuration(project)
				if err != nil || (len(cfg.Conflicts) != 0) != wantConflict {
					t.Fatalf("restored conflicts = %+v, %v", cfg.Conflicts, err)
				}
				disabled = true
				stored, err = m.SaveConfiguration(project, kind, file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.SaveConfiguration(project, kind, stored.Name, configurationInput{Path: stored.Path, Revision: stored.Revision}, true); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(stored.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("disabled file remains after remove: %v", err)
				}
			})
		}
	}
}

func TestConfigurationDisableCollisionAndStaleRevision(t *testing.T) {
	for _, kind := range []string{"agents", "skills", "hooks"} {
		t.Run(kind, func(t *testing.T) {
			m := configurationManager(t)
			content := testSkillDefinition
			if kind == "hooks" {
				content = `{"version":1,"hooks":{}}`
			}
			file, err := m.SaveConfiguration("project", kind, "example", configurationInput{Content: content}, false)
			if err != nil {
				t.Fatal(err)
			}
			disabled := true
			if err := os.WriteFile(file.Path+disabledConfigurationSuffix, []byte(content+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.SaveConfiguration("project", kind, file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("disable collision = %v", err)
			}
			if data, err := os.ReadFile(file.Path + disabledConfigurationSuffix); err != nil || string(data) != content+"\n" {
				t.Fatal("disabled destination overwritten")
			}
			cfg, err := m.Configuration("project")
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]ConfigurationFile{"agents": cfg.Agents, "skills": cfg.Skills, "hooks": cfg.Hooks}[kind]
			if len(files) != 2 {
				t.Fatalf("active and disabled definitions not both listed: %+v", files)
			}
			stored := files[slices.IndexFunc(files, func(f ConfigurationFile) bool { return f.Disabled })]
			disabled = false
			if _, err := m.SaveConfiguration("project", kind, stored.Name, configurationInput{Path: stored.Path, Revision: stored.Revision, Disabled: &disabled}, false); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("restore collision = %v", err)
			}
			if data, err := os.ReadFile(file.Path); err != nil || string(data) != content {
				t.Fatal("active destination overwritten")
			}
			if err := os.Remove(stored.Path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file.Path, []byte(content+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			disabled = true
			if _, err := m.SaveConfiguration("project", kind, file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false); configurationStatus(err) != http.StatusConflict {
				t.Fatalf("stale disable = %v", err)
			}
			if data, err := os.ReadFile(file.Path); err != nil || string(data) != content+"\n" {
				t.Fatal("stale mutation changed file")
			}
		})
	}
}

func TestConfigurationDisabledSharedSkillAliasesRestore(t *testing.T) {
	for _, project := range []string{"", "project"} {
		t.Run(project, func(t *testing.T) {
			m := configurationManager(t)
			base := m.projects["project"].Dir
			if project == "" {
				base = os.Getenv("HOME")
			}
			path := filepath.Join(base, ".agents", "skills", "example", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(testSkillDefinition), 0640); err != nil {
				t.Fatal(err)
			}
			support := filepath.Join(filepath.Dir(path), "helper.sh")
			if err := os.WriteFile(support, []byte("keep"), 0700); err != nil {
				t.Fatal(err)
			}
			scope, _ := m.configurationScope(project)
			root := scope.root("skills")
			for _, name := range []string{"file-alias", "chained-alias"} {
				if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			alias := filepath.Join(root, "file-alias", "SKILL.md")
			target, err := filepath.Rel(filepath.Dir(alias), path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../file-alias/SKILL.md", filepath.Join(root, "chained-alias", "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Dir(path), filepath.Join(root, "directory-alias")); err != nil {
				t.Fatal(err)
			}
			cfg, err := m.Configuration(project)
			if err != nil || len(cfg.Skills) != 1 || !cfg.Skills[0].Editable {
				t.Fatalf("initial aliases = %+v, %v", cfg, err)
			}
			file := cfg.Skills[0]
			disabled := true
			stored, err := m.SaveConfiguration(project, "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err = m.Configuration(project)
			if err != nil || len(cfg.Skills) != 1 || cfg.Skills[0] != stored || len(cfg.Conflicts) != 0 || len(cfg.ConflictDetails) != 0 {
				t.Fatalf("disabled aliases = %+v, %v", cfg, err)
			}
			if data, err := os.ReadFile(support); err != nil || string(data) != "keep" {
				t.Fatal("support file changed")
			}
			if info, err := os.Stat(stored.Path); err != nil || info.Mode().Perm() != 0640 {
				t.Fatalf("skill mode changed: %v %v", info, err)
			}
			disabled = false
			if _, err := m.SaveConfiguration(project, "skills", stored.Name, configurationInput{Path: stored.Path, Revision: stored.Revision, Disabled: &disabled}, false); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"file-alias", "chained-alias", "directory-alias"} {
				if data, err := os.ReadFile(filepath.Join(root, name, "SKILL.md")); err != nil || string(data) != testSkillDefinition {
					t.Fatalf("alias %s not restored: %v", name, err)
				}
			}
		})
	}
}

func TestConfigurationDisableGuards(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "example", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	for _, tc := range []struct {
		project, kind, name string
		input               configurationInput
		remove              bool
		status              int
	}{
		{"project", "skills", file.Name, configurationInput{Revision: file.Revision, Disabled: &disabled}, false, http.StatusBadRequest},
		{"project", "skills", file.Name, configurationInput{Path: file.Path, Disabled: &disabled}, false, http.StatusBadRequest},
		{"project", "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Content: testSkillDefinition, Disabled: &disabled}, false, http.StatusBadRequest},
		{"project", "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, true, http.StatusBadRequest},
		{"project", "instructions", "agents", configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false, http.StatusBadRequest},
		{"", "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false, http.StatusConflict},
		{"project", "skills", "different", configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false, http.StatusConflict},
	} {
		if _, err := m.SaveConfiguration(tc.project, tc.kind, tc.name, tc.input, tc.remove); configurationStatus(err) != tc.status {
			t.Fatalf("guard %+v = %v", tc, err)
		}
	}
	m.skillDirs = []string{filepath.Dir(filepath.Dir(file.Path))}
	if _, err := m.SaveConfiguration("project", "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("builtin disable = %v", err)
	}
	m.skillDirs = nil
	m.settings.Terminal = false
	if _, err := m.SaveConfiguration("project", "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false); configurationStatus(err) != http.StatusForbidden {
		t.Fatalf("Terminal gate = %v", err)
	}
	if data, err := os.ReadFile(file.Path); err != nil || string(data) != testSkillDefinition {
		t.Fatal("guarded file changed")
	}
}

func TestConfigurationDisableHTTP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	ts.m.settings.Terminal = true
	file, err := ts.m.SaveConfiguration("", "skills", "example", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	url := "/api/configuration/skills/example"
	for _, tc := range []struct {
		method string
		fields map[string]any
		status int
	}{
		{http.MethodPut, map[string]any{"path": file.Path, "revision": file.Revision, "disabled": true, "content": ""}, http.StatusBadRequest},
		{http.MethodDelete, map[string]any{"path": file.Path, "revision": file.Revision, "disabled": true}, http.StatusBadRequest},
		{http.MethodPut, map[string]any{"path": file.Path, "disabled": true}, http.StatusBadRequest},
		{http.MethodPut, map[string]any{"revision": file.Revision, "disabled": true}, http.StatusBadRequest},
	} {
		body, _ := json.Marshal(tc.fields)
		if w := ts.do(tc.method, url, string(body), withCookie(ts)); w.Code != tc.status {
			t.Fatalf("invalid toggle = %d %s", w.Code, w.Body)
		}
	}
	for _, disabled := range []bool{true, false} {
		body, _ := json.Marshal(map[string]any{"path": file.Path, "revision": file.Revision, "disabled": disabled})
		w := ts.do(http.MethodPut, url, string(body), withCookie(ts))
		if w.Code != http.StatusOK {
			t.Fatalf("toggle = %d %s", w.Code, w.Body)
		}
		var next ConfigurationFile
		if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil || next.Disabled != disabled {
			t.Fatalf("toggle response = %+v, %v", next, err)
		}
		file = next
	}
}

func TestConfigurationDisableRollsBackLinkAfterRevisionChange(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "example", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := m.configurationScope("project")
	root, path, err := openConfigurationSkillRoot(scope, file.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := os.WriteFile(file.Path, []byte(testSkillDefinition+"Changed."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := moveConfiguration(root, path, path+disabledConfigurationSuffix, file.Revision); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("changed during move = %v", err)
	}
	if _, err := os.Lstat(file.Path + disabledConfigurationSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary destination remains: %v", err)
	}
	if data, err := os.ReadFile(file.Path); err != nil || string(data) != testSkillDefinition+"Changed." {
		t.Fatal("changed source was lost")
	}
}
