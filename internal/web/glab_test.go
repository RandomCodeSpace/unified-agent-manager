package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func glabTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}

func glabTestRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	glabTestGit(t, dir, "init", "-b", "main")
	glabTestGit(t, dir, "config", "user.email", "fixture@example.com")
	glabTestGit(t, dir, "config", "user.name", "Fixture")
	glabTestGit(t, dir, "remote", "add", "origin", remote)
	return dir
}

func glabTestWrite(t *testing.T, file, text string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
}

func glabTestFake(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "glab")
	glabTestWrite(t, bin, "#!/bin/sh\n"+script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func glabWait(t *testing.T, g *glabGovernor, dir string) glabCachedGate {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		e := g.dirs[dir]
		g.mu.Unlock()
		if !e.loading && !e.at.IsZero() {
			return e
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gate refresh did not complete")
	return glabCachedGate{}
}

func TestGlabGate(t *testing.T) {
	for _, tc := range []struct {
		name, remote, script string
		enabled              bool
	}{
		{"github", "https://github.com/group/repo.git", "exit 0", false},
		{"signed-out", "https://gitlab.com/group/repo.git", "exit 1", false},
		{"https", "https://gitlab.com/group/sub/repo.git", "exit 0", true},
		{"scp", "git@gitlab.com:group/sub/repo.git", "exit 0", true},
		{"local", "/tmp/local-repo", "exit 0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			glabTestFake(t, tc.script)
			dir := glabTestRepo(t, tc.remote)
			var g glabGovernor
			if _, ok := g.gate(t.Context(), dir); ok {
				t.Fatal("cold gate enabled")
			}
			e := glabWait(t, &g, dir)
			if e.ok != tc.enabled {
				t.Fatalf("gate=%+v", e)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, ok := glabOrigin(t.Context(), t.TempDir()); ok {
			t.Fatal("missing executable enabled")
		}
	})
}

func TestGlabAuthCacheAndExpiry(t *testing.T) {
	bin := glabTestFake(t, "printf 'auth\\n' >> \"$0.calls\"\nexit 0")
	var g glabGovernor
	dir := glabTestRepo(t, "https://gitlab.com/group/repo.git")
	other := glabTestRepo(t, "https://gitlab.com/group/other.git")
	for _, d := range []string{dir, other} {
		g.gate(t.Context(), d)
		if !glabWait(t, &g, d).ok {
			t.Fatal("auth failed")
		}
	}
	if _, ok := g.gate(t.Context(), dir); !ok {
		t.Fatal("warm gate disabled")
	}
	data, err := os.ReadFile(bin + ".calls")
	if err != nil || string(data) != "auth\n" {
		t.Fatalf("auth calls=%q error=%v", data, err)
	}
	g.mu.Lock()
	e := g.dirs[dir]
	e.at = time.Now().Add(-glabTTL)
	g.dirs[dir] = e
	g.hosts["gitlab.com"].at = e.at
	g.mu.Unlock()
	if _, ok := g.gate(t.Context(), dir); ok {
		t.Fatal("expired gate enabled")
	}
	if !glabWait(t, &g, dir).ok {
		t.Fatal("refresh failed")
	}
	data, _ = os.ReadFile(bin + ".calls")
	if string(data) != "auth\nauth\n" {
		t.Fatalf("calls=%q", data)
	}
}

func TestGlabRunnerBounds(t *testing.T) {
	bin := glabTestFake(t, "printf '%s' sensitive-auth-text\nexit 0")
	gate := glabGate{Dir: t.TempDir(), Host: "gitlab.com", Path: "group/repo", executable: bin}
	if out, err := glabRun(t.Context(), gate, time.Second, "auth status"); err != nil || out != "" {
		t.Fatalf("auth output=%q err=%v", out, err)
	}
	if _, err := glabRun(t.Context(), gate, time.Second, "api"); err == nil {
		t.Fatal("unsupported verb ran")
	}
	glabTestWrite(t, bin, "#!/bin/sh\nexec sleep 2\n")
	if _, err := glabRun(t.Context(), gate, time.Millisecond, "ci lint", "./.gitlab-ci.yml"); err != context.DeadlineExceeded {
		t.Fatalf("timeout=%v", err)
	}
	glabTestWrite(t, bin, "#!/bin/sh\nhead -c 1048580 /dev/zero\n")
	if out, err := glabRun(t.Context(), gate, time.Second, "ci lint"); len(out) != glabOutputLimit || err == nil {
		t.Fatalf("bound=%d err=%v", len(out), err)
	}
}
