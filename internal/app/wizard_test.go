package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// C2-8 — the workdir step must warn when the chosen directory is not inside a git
// repository: dispatching there means no checkpoint to recover the agent's work.
// isGitRepo walks up the tree the way git does.
func TestIsGitRepoWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if !isGitRepo(nested) {
		t.Fatal("a nested dir under a .git root should be detected as a git repo")
	}

	// A standalone temp dir with no .git anywhere up to its root is not a repo.
	// Use a path guaranteed to have no .git: a fresh tempdir whose parents are
	// system temp (not under this repo's checkout).
	bare := t.TempDir()
	// Defensive: only assert non-repo if no ancestor has .git (CI temp dirs do
	// not, but be explicit so the test is deterministic).
	if isGitRepo(bare) && !ancestorHasGit(bare) {
		t.Fatal("isGitRepo disagreed with the ancestor walk")
	}
}

func TestIsGitRepoIgnoresIncompleteGitMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if validGitMarker(filepath.Join(root, ".git")) {
		t.Fatal("an empty .git directory is not a valid repository marker")
	}
}

func TestIsGitRepoAcceptsWorktreeGitdirFile(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "worktrees", "child")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isGitRepo(root) {
		t.Fatal("a worktree gitdir file pointing at a directory with HEAD should be accepted")
	}
}

// helper for the test's own sanity check (independent walk).
func ancestorHasGit(dir string) bool {
	d, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if fi, err := os.Stat(filepath.Join(d, ".git")); err == nil && fi.IsDir() {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// C2-8 — Tab in the workdir step completes the typed path against the filesystem
// via filepath.Glob.
func TestGlobCompleteCompletesUniquePrefix(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	got := globComplete(filepath.Join(dir, "mypr"))
	if got != target {
		t.Fatalf("globComplete = %q, want %q", got, target)
	}
}

// C2-8 — when nothing matches, globComplete returns the input unchanged (no-op).
func TestGlobCompleteNoMatchKeepsInput(t *testing.T) {
	in := filepath.Join(t.TempDir(), "nonexistent-xyz")
	if got := globComplete(in); got != in {
		t.Fatalf("globComplete with no match = %q, want %q unchanged", got, in)
	}
}

// C2-8 — the workdir step shows a yellow "no checkpoint" warning when the chosen
// directory is not a git repo.
func TestLaunchDirectoryShowsNoGitWarning(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.openLaunchPad()
	m.focusLaunchField(launchFieldDir)
	m.input = t.TempDir() // not a git repo
	_, _, _, warning := m.launchFieldParts(launchFieldDir)
	if !strings.Contains(strings.ToLower(warning), "not a git") {
		t.Fatalf("directory field should warn about the missing git checkpoint: %q", warning)
	}
}

// C2-8 — Tab in the workdir step completes m.input via globComplete.
func TestLaunchDirectoryTabCompletes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "completed")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(nil, nil)
	m.openLaunchPad()
	m.focusLaunchField(launchFieldDir)
	m.input = filepath.Join(dir, "compl")
	model, _ := m.handleLaunchKey(keyMsg("tab"))
	m = model.(Model)
	if m.input != target {
		t.Fatalf("Tab should complete the path to %q, got %q", target, m.input)
	}
}

// The alias step is gone; the prompt's @agent:alias shorthand carries it.
func TestLaunchPromptAliasShorthandReachesDispatch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.defaultAgent = "fake"
	m.openLaunchPad()
	m.launchDir = "/tmp"
	m.focusLaunchField(launchFieldPrompt)
	m.input = "@fake:review look at this"
	model, cmd := m.handleLaunchKey(keyMsg("enter"))
	m = model.(Model)
	if m.launchOpen || cmd == nil {
		t.Fatal("launch pad did not dispatch")
	}
	msg, ok := cmd().(dispatchedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("dispatch = %#v", msg)
	}
	if msg.session.CommandAlias != "review" || msg.session.Prompt != "look at this" {
		t.Fatalf("session = %+v", msg.session)
	}
}

// C2-8 — Ctrl+G in the prompt step opens $EDITOR via the injected exec runner
// (tea.ExecProcess in production) and the round-trip loads the edited file back
// into the prompt buffer.
func TestLaunchCtrlGOpensEditorAndLoadsResult(t *testing.T) {
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
	m.openLaunchPad()

	// Capture the command tea.ExecProcess would have run; simulate the editor by
	// writing into the temp file it was pointed at, then invoke the callback.
	var ranArgs []string
	m.execProcess = func(c *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		ranArgs = append([]string(nil), c.Args...)
		// The last arg is the temp file path the wizard created.
		file := c.Args[len(c.Args)-1]
		if err := os.WriteFile(file, []byte("edited prompt body\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return func() tea.Msg { return cb(nil) }
	}

	model, cmd := m.handleLaunchKey(keyMsg("ctrl+g"))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+G should return an exec command")
	}
	if len(ranArgs) == 0 {
		t.Fatal("Ctrl+G should have launched an editor process")
	}
	msg := cmd()
	edited, ok := msg.(promptEditedMsg)
	if !ok {
		t.Fatalf("expected promptEditedMsg, got %T", msg)
	}
	if edited.err != nil {
		t.Fatalf("editor round-trip error: %v", edited.err)
	}
	// Feed the message back through Update; the prompt buffer should hold the
	// editor's content (trailing newline trimmed).
	model, _ = m.Update(edited)
	m = model.(Model)
	if m.input != "edited prompt body" {
		t.Fatalf("prompt buffer after editor = %q, want %q", m.input, "edited prompt body")
	}
}
