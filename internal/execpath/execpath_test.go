package execpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveInDirsIgnoresPATHAndUsesExecutableInFixedDirs(t *testing.T) {
	pathDir := t.TempDir()
	fixedDir := t.TempDir()
	writeExecutable(t, filepath.Join(pathDir, "tool"))
	want := filepath.Join(fixedDir, "tool")
	writeExecutable(t, want)
	t.Setenv("PATH", pathDir)

	got, err := ResolveInDirs("tool", []string{fixedDir})
	if err != nil {
		t.Fatalf("ResolveInDirs: %v", err)
	}
	if got != want {
		t.Fatalf("resolved %q, want %q", got, want)
	}
}

func TestResolveInDirsRejectsRelativeOrNonExecutableCandidates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"./tool", "../tool", "nested/tool"} {
		if got, err := ResolveInDirs(name, []string{dir}); err == nil || got != "" {
			t.Fatalf("ResolveInDirs(%q) = %q, %v; want rejection", name, got, err)
		}
	}
	if got, err := ResolveInDirs("tool", []string{dir}); err == nil || got != "" {
		t.Fatalf("ResolveInDirs(non-executable) = %q, %v; want rejection", got, err)
	}
}

func TestValidateAbsoluteExecutableChecksPathAndMode(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	writeExecutable(t, exe)
	if err := ValidateAbsoluteExecutable(exe); err != nil {
		t.Fatalf("valid executable rejected: %v", err)
	}
	if err := ValidateAbsoluteExecutable("tool"); err == nil {
		t.Fatal("relative path should be rejected")
	}
	nonExecutable := filepath.Join(dir, "plain")
	if err := os.WriteFile(nonExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAbsoluteExecutable(nonExecutable); err == nil {
		t.Fatal("non-executable file should be rejected")
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
