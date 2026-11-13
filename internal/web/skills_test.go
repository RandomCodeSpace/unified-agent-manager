package web

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBuiltinSkillsHaveFrontmatter(t *testing.T) {
	names, err := fs.Glob(builtinSkills, "skills/*/SKILL.md")
	if err != nil || len(names) == 0 {
		t.Fatalf("built-in skills = %v, %v", names, err)
	}
	for _, name := range names {
		data, err := builtinSkills.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		head, _, ok := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---\n")
		if !ok || !strings.HasPrefix(string(data), "---\n") {
			t.Fatalf("%s has no frontmatter", name)
		}
		fields := map[string]string{}
		for line := range strings.Lines(head) {
			k, v, _ := strings.Cut(strings.TrimSpace(line), ": ")
			fields[k] = v
		}
		if want := path.Base(path.Dir(name)); fields["name"] != want || fields["description"] == "" || len(fields["description"]) > 1024 {
			t.Fatalf("%s frontmatter = %q, want name %q and a description", name, fields, want)
		}
	}
}

// uamSkill is the whole uam skill: SKILL.md and its reference files.
func uamSkill(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	err := fs.WalkDir(builtinSkills, "skills/uam", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := builtinSkills.ReadFile(name)
		b.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// SKILL.md names each reference file once, and every file it names exists.
func TestUamSkillReferencesResolve(t *testing.T) {
	data, err := builtinSkills.ReadFile("skills/uam/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile("`(references/[a-z-]+\\.md)`").FindAllStringSubmatch(string(data), -1) {
		if named[m[1]] {
			t.Errorf("SKILL.md names %s twice", m[1])
		}
		named[m[1]] = true
	}
	files, err := fs.Glob(builtinSkills, "skills/uam/references/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("reference files = %v, %v", files, err)
	}
	for _, f := range files {
		rel := strings.TrimPrefix(f, "skills/uam/")
		if !named[rel] {
			t.Errorf("SKILL.md does not name %s", rel)
		}
		delete(named, rel)
	}
	for rel := range named {
		t.Errorf("SKILL.md names %s, which does not exist", rel)
	}
}

// The uam skill documents every tool a Task gets from uam, and points agents
// at those tools rather than uam's web API.
func TestUamSkillCoversHostTools(t *testing.T) {
	skill := uamSkill(t)
	tools, _ := (&Manager{}).taskToolsLocked("task", false)
	names := []string{"uam_show_file"}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	for _, name := range names {
		if !strings.Contains(skill, "`"+name+"`") {
			t.Errorf("the uam skill does not document %s", name)
		}
	}
	if !strings.Contains(skill, "Do not call uam's web API or read its credentials") {
		t.Error("the uam skill lacks the rule against calling uam's web API")
	}
	if strings.Contains(skill, "/api/") {
		t.Error("the uam skill names a web API route")
	}
}

// The uam tool section of the skill documents exactly the ops the tool offers.
func TestUamSkillCoversUamOps(t *testing.T) {
	data, err := builtinSkills.ReadFile("skills/uam/references/uam-tool.md")
	if err != nil {
		t.Fatal(err)
	}
	section := string(data)
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`([a-z_]+) \\{").FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}
	for op := range uamShapes {
		if !documented[op] {
			t.Errorf("references/uam-tool.md does not document uam op %s", op)
		}
	}
	for op := range documented {
		if _, ok := uamShapes[op]; !ok {
			t.Errorf("references/uam-tool.md documents uam op %s, which the tool does not offer", op)
		}
	}
}

func TestInstallSkillsWritesAndRefreshes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	if err := installSkills(dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "uam", "SKILL.md")
	want, err := builtinSkills.ReadFile("skills/uam/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != string(want) {
		t.Fatalf("installed skill = %q, %v", got, err)
	}
	// Reference files land beside it, where the agent reads them by path.
	if _, err := os.Stat(filepath.Join(dir, "uam", "references", "uam-tool.md")); err != nil {
		t.Fatalf("reference file not installed: %v", err)
	}
	// An unchanged file is left alone.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	if err := installSkills(dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target); err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("unchanged skill rewritten: %v, %v", info.ModTime(), err)
	}
	// A file from another version is replaced.
	if err := os.WriteFile(target, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installSkills(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != string(want) {
		t.Fatalf("stale skill = %q, %v", got, err)
	}
}

func TestTaskConversationsLoadBuiltinSkills(t *testing.T) {
	m, prov, st := newTestManager(t)
	createSession(t, m, prov)
	dir := filepath.Join(filepath.Dir(st.Path()), "skills")
	opens := prov.Opens()
	if len(opens) != 1 || !reflect.DeepEqual(opens[0].SkillDirectories, []string{dir}) {
		t.Fatalf("opens = %+v, want skill directories [%s]", opens, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "uam", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

// The cards reference lists exactly the icon names uam_surface accepts, so a
// model picks one instead of guessing and being refused.
func TestSurfacesSkillListsIconNames(t *testing.T) {
	data, err := builtinSkills.ReadFile("skills/uam/references/surfaces.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\x60Icon\x60 name \(only\s+these: ([^)]+)\)`).FindSubmatch(data)
	if m == nil {
		t.Fatal("references/surfaces.md does not list the Icon names")
	}
	if got := strings.Fields(string(m[1])); !reflect.DeepEqual(got, surfaceEnums["Icon.name"]) {
		t.Errorf("references/surfaces.md lists Icon names %v, want %v", got, surfaceEnums["Icon.name"])
	}
}
