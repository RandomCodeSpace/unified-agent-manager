package web

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestDeclarationValidatorUsesExistingWorkdirAndTempRulesWithoutGrant(t *testing.T) {
	m, prov, _ := newTestManager(t)
	workdir := t.TempDir()
	project := addProject(t, m, workdir)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project, Name: "declaration"})
	if err != nil {
		t.Fatal(err)
	}
	workFile := filepath.Join(workdir, "report.txt")
	if err := os.WriteFile(workFile, []byte("private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	validate := m.declarationValidator(sum.ID, workdir)
	if got, err := validate(context.Background(), "report.txt"); err != nil || got != workFile {
		t.Fatalf("workdir = %q, %v", got, err)
	}
	tempDir, runtimeDir := t.TempDir(), t.TempDir()
	roots, err := openGrantRoots(tempDir, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &tempGrants{manager: m, ctx: ctx, cancel: cancel, roots: roots, entries: make(map[string]*tempGrant), ops: make(chan struct{}, maxGrantOps)}
	m.mu.Lock()
	if m.fileGrants == nil {
		m.fileGrants = make(map[*tempGrants]struct{})
	}
	m.fileGrants[g] = struct{}{}
	m.mu.Unlock()
	t.Cleanup(func() { g.close() })
	tempFile := filepath.Join(tempDir, "owned.txt")
	if err := os.WriteFile(tempFile, []byte("private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := validate(context.Background(), tempFile); err != nil || got != tempFile {
		t.Fatalf("temporary = %q, %v", got, err)
	}
	if relative, err := filepath.Rel(workdir, tempFile); err == nil {
		if got, err := validate(context.Background(), relative); err == nil {
			t.Fatalf("relative temporary escape = %q", got)
		}
	}
	if len(g.entries) != 0 {
		t.Fatal("declaration minted a temporary grant")
	}
	for _, candidate := range []string{tempDir, runtimeDir, filepath.Join(tempDir, "missing"), filepath.Join(t.TempDir(), "outside")} {
		if got, err := validate(context.Background(), candidate); err == nil {
			t.Fatalf("accepted %q as %q", candidate, got)
		}
	}
	for _, candidate := range []string{tempDir + "/./owned.txt", tempDir + "/x/../owned.txt"} {
		if got, err := validate(context.Background(), candidate); err == nil {
			t.Fatalf("accepted dot-segment temporary path %q as %q", candidate, got)
		}
	}
	if err := os.Symlink(runtimeDir, filepath.Join(tempDir, "alias")); err != nil {
		t.Fatal(err)
	}
	if got, err := validate(context.Background(), filepath.Join(tempDir, "alias", "anything")); err == nil {
		t.Fatalf("accepted runtime alias %q", got)
	}
}

func TestDeclarationReadableForeignOwnedWorkdirFileMatchesView(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses discretionary file permissions")
	}
	m, prov, _ := newTestManager(t)
	workdir := t.TempDir()
	project := addProject(t, m, workdir)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project, Name: "readable workdir file"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workdir, "other-readable.txt")
	if err := os.WriteFile(path, []byte("visible through other permissions"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0004); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 0, 0); err != nil {
		if err := exec.Command("sudo", "-n", "chown", "0:0", path).Run(); err != nil {
			t.Skipf("cannot prepare a foreign-owned permission fixture: %v", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid == uint32(os.Geteuid()) || info.Mode().Perm()&0400 != 0 || info.Mode().Perm()&0004 == 0 {
		t.Fatalf("fixture does not require other-read permission: mode=%v stat=%v", info.Mode(), info.Sys())
	}
	served, err := m.ViewFile(sum.ID, "other-readable.txt")
	if err != nil {
		t.Fatalf("existing workdir opener rejected readable file: %v", err)
	}
	_ = served.File.Close()
	if got, err := m.declarationValidator(sum.ID, workdir)(context.Background(), path); err != nil || got != path {
		t.Fatalf("declaration differs from workdir opener: path=%q error=%v", got, err)
	}
}

func TestDeclarationMetadataSurvivesCompactAndNewestCardCap(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	makeItem := func(i int) agentapi.Item {
		return agentapi.Item{ID: fmt.Sprintf("tool-%03d", i), Kind: agentapi.ItemTool, Time: time.Unix(int64(i+1), 0), Tool: &agentapi.ToolCall{Name: "uam_show_file", Status: agentapi.ToolCompleted, Input: `{"path":"private"}`, Output: `{"artifact_id":"id"}`, Declaration: &agentapi.FileDeclaration{ArtifactID: fmt.Sprintf("artifact-%03d", i), Path: fmt.Sprintf("/tmp/report-%03d.txt", i), Title: "Report"}}}
	}
	first := projectItem(clampItem(makeItem(0), time.Now()))
	if first.Tool == nil || first.Tool.Declaration == nil || first.Tool.Declaration.Path != "/tmp/report-000.txt" || first.Tool.Input != "" || first.Tool.Output != "" || len(first.Tool.FilePaths) != 1 || first.Tool.FilePaths[0] != first.Tool.Declaration.Path {
		t.Fatalf("compact declaration = %+v", first.Tool)
	}
	bad := makeItem(0)
	bad.Tool.Declaration.Path = strings.Repeat("x", maxGrantPathBytes+1)
	if clampItem(bad, time.Now()).Tool.Declaration != nil {
		t.Fatal("oversized declaration survived clamp")
	}
	items := make([]agentapi.Item, 130)
	for i := range items {
		items[i] = makeItem(i)
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.applyHistoryLocked(s, agentapi.History{Items: items}, false)
	count := 0
	for i, item := range s.items {
		if item.Tool.Declaration != nil {
			count++
		} else if i >= 2 {
			t.Fatalf("new card %d was dropped", i)
		}
	}
	if count != maxDeclarationCards {
		t.Fatalf("history retained %d cards", count)
	}
	m.upsertItemLocked(s, makeItem(130), false)
	count = 0
	for _, item := range s.items {
		if item.Tool.Declaration != nil {
			count++
		}
	}
	m.mu.Unlock()
	if count != maxDeclarationCards {
		t.Fatalf("live retained %d cards", count)
	}
}
