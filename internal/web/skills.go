package web

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// builtinSkills are the skills uam gives every Task, each skills/<name>/SKILL.md.
//
//go:embed skills
var builtinSkills embed.FS

// installSkills writes the built-in skills under dir, rewriting a file only
// when its content differs, so the provider can load them from disk.
func installSkills(dir string) error {
	return fs.WalkDir(builtinSkills, "skills", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := builtinSkills.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("skills", filepath.FromSlash(name))
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, data) { // #nosec G304 -- uam's own state directory and an embedded skill name.
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("install skill %s: %w", path.Dir(name), err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("install skill %s: %w", path.Dir(name), err)
		}
		return nil
	})
}
