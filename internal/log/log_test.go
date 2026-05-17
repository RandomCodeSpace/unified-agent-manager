package log

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitAndLogging(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	Debug("debug")
	Info("info")
	Warn("warn")
	Error("error")
	if L() == nil {
		t.Fatal("nil logger")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uam.log")); err != nil {
		t.Fatal(err)
	}
}

func TestInitError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_CACHE_DIR", filepath.Join(file, "child"))
	if _, err := Init(); err == nil {
		t.Fatal("expected init error")
	}
}

func TestCacheDirFallbacks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", dir)
	if got := cacheDir(); got != filepath.Join(dir, "uam") {
		t.Fatalf("%s", got)
	}
}
