package web

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
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

// The uam skill documents every tool a Task gets from uam, and points agents
// at those tools rather than uam's web API.
func TestUamSkillCoversHostTools(t *testing.T) {
	data, err := builtinSkills.ReadFile("skills/uam/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(data)
	tools, _ := (&Manager{}).taskToolsLocked("task", false)
	names := []string{"uam_show_file"}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	for _, name := range names {
		if !strings.Contains(skill, "`"+name+"`") {
			t.Errorf("SKILL.md does not document %s", name)
		}
	}
	if !strings.Contains(skill, "Do not call uam's web API or read its credentials") {
		t.Error("SKILL.md lacks the rule against calling uam's web API")
	}
	if strings.Contains(skill, "/api/") {
		t.Error("SKILL.md names a web API route")
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
