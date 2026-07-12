package log

import (
	"os"
	"path/filepath"
	"strings"
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

func TestInitEnforcesPrivateLogPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uam.log")
	if err := os.WriteFile(path, []byte("old log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("uam.log mode = %o, want 600", got)
	}
}

func TestInitRotatesOversizedLogAndRetainsThreePrivateBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uam.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxLogSize); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"backup-one", "backup-two", "backup-three"} {
		if err := os.WriteFile(path+"."+string(rune('1'+i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	wantBodies := map[string]string{
		path + ".2": "backup-one",
		path + ".3": "backup-two",
	}
	for _, candidate := range []string{path, path + ".1", path + ".2", path + ".3"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", candidate, got)
		}
		if want, ok := wantBodies[candidate]; ok {
			got, err := os.ReadFile(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("%s = %q, want %q", candidate, got, want)
			}
		}
	}
	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Size() != maxLogSize {
		t.Fatalf("rotated size = %d, want %d", rotated.Size(), maxLogSize)
	}
}

func TestLoggerRotatesWhenActiveLogCrossesSizeLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	Info("fill", "data", strings.Repeat("x", int(maxLogSize-1024)))
	Info("after rotation", "marker", "new-log", "padding", strings.Repeat("y", 2048))

	rotated, err := os.ReadFile(filepath.Join(dir, "uam.log.1"))
	if err != nil {
		t.Fatalf("read rotated log: %v", err)
	}
	if !strings.Contains(string(rotated), "fill") {
		t.Fatal("rotated log does not contain the pre-limit entry")
	}
	current, err := os.ReadFile(filepath.Join(dir, "uam.log"))
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	if !strings.Contains(string(current), "new-log") {
		t.Fatal("current log does not contain the post-rotation entry")
	}
}

func TestCacheDirFallbacks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", dir)
	if got := cacheDir(); got != filepath.Join(dir, "uam") {
		t.Fatalf("%s", got)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", dir)
	if got := cacheDir(); got != filepath.Join(dir, ".cache", "uam") {
		t.Fatalf("home cache dir = %s", got)
	}
	UseStderr(nil)
	Debug("stderr fallback debug path")
}
