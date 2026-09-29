package web

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// staleRepo is a repository whose first commit is the returned pin, with
// two commits since: one changing internal/web/a.go, one docs/readme.md.
func staleRepo(t *testing.T) (repo, pin, head string) {
	t.Helper()
	repo = branchRepo(t)
	pin = gitOutput(t, repo, "rev-parse", "HEAD")
	writeRepoFile(t, repo, "internal/web/a.go", "package web\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "code")
	writeRepoFile(t, repo, "docs/readme.md", "# docs\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "docs")
	return repo, pin, gitOutput(t, repo, "rev-parse", "HEAD")
}

func TestStaleBehindAndPaths(t *testing.T) {
	ctx := context.Background()
	repo, pin, head := staleRepo(t)
	var c staleCache
	for _, tc := range []struct {
		paths []string
		files []string
	}{
		{nil, []string{}},
		{[]string{"internal/**/*.go"}, []string{"internal/web/a.go"}},
		{[]string{"docs"}, []string{"docs/readme.md"}},
		{[]string{"*.go"}, []string{}},
		{[]string{"**"}, []string{"docs/readme.md", "internal/web/a.go"}},
		{[]string{"cmd/**", "[bad"}, []string{}},
	} {
		s, err := c.staleFor(ctx, repo, pin, head, tc.paths)
		if err != nil {
			t.Fatal(err)
		}
		if s.Behind != 2 || s.Diverged || !slices.Equal(s.Files, tc.files) {
			t.Fatalf("paths %q: stale = %+v, want behind 2, files %q", tc.paths, s, tc.files)
		}
	}
	if s, err := c.staleFor(ctx, repo, head, head, []string{"**"}); err != nil || s.Behind != 0 || s.Diverged || len(s.Files) != 0 {
		t.Fatalf("a pin at HEAD = %+v, %v", s, err)
	}
	if _, err := c.staleFor(ctx, repo, "--all", head, nil); err == nil {
		t.Fatal("an option as the pin was accepted")
	}
}

func TestStaleDivergedAfterHistoryRewrite(t *testing.T) {
	ctx := context.Background()
	repo, _, head := staleRepo(t)
	gitIn(t, repo, "reset", "-q", "--hard", "HEAD~1")
	writeRepoFile(t, repo, "docs/readme.md", "# rewritten\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "docs, rewritten")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "more")
	now := gitOutput(t, repo, "rev-parse", "HEAD")
	var c staleCache
	s, err := c.staleFor(ctx, repo, head, now, []string{"docs/*"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Diverged || s.Behind != 2 || !slices.Equal(s.Files, []string{"docs/readme.md"}) {
		t.Fatalf("stale = %+v, want diverged, behind 2, docs/readme.md", s)
	}
	gone := "0123456789abcdef0123456789abcdef01234567"
	if s, err := c.staleFor(ctx, repo, gone, now, []string{"**"}); err != nil || !s.Diverged || s.Behind != 0 || len(s.Files) != 0 {
		t.Fatalf("a pruned pin = %+v, %v", s, err)
	}
}

func TestStaleCacheHits(t *testing.T) {
	ctx := context.Background()
	repo, pin, head := staleRepo(t)
	var c staleCache
	first, err := c.staleFor(ctx, repo, pin, head, []string{"docs/**"})
	if err != nil {
		t.Fatal(err)
	}
	first.Files[0] = "changed by the caller"
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	again, err := c.staleFor(ctx, repo, pin, head, []string{"docs/**"})
	if err != nil || again.Behind != 2 || !slices.Equal(again.Files, []string{"docs/readme.md"}) {
		t.Fatalf("a cached result = %+v, %v", again, err)
	}
	if _, err := c.staleFor(ctx, repo, pin, head, []string{"internal/**"}); err == nil {
		t.Fatal("other paths hit the cache")
	}
}

func TestStaleCacheIsBounded(t *testing.T) {
	var c staleCache
	key := func(i int) staleKey { return newStaleKey("dir", "pin"+strconv.Itoa(i), "head", nil) }
	for i := range staleCacheSize + 1 {
		c.put(key(i), Stale{Behind: i})
	}
	if _, ok := c.get(key(0)); ok {
		t.Fatal("the least recently used result was kept")
	}
	if s, ok := c.get(key(staleCacheSize)); !ok || s.Behind != staleCacheSize || c.entries.Len() != staleCacheSize {
		t.Fatalf("latest = %+v, %v; %d entries", s, ok, c.entries.Len())
	}
	c.put(key(1), Stale{Behind: -1})
	if s, _ := c.get(key(1)); s.Behind != -1 || c.entries.Len() != staleCacheSize {
		t.Fatalf("a replaced result = %+v", s)
	}
}

// Test plan 19: staleness is never computed for held subtasks.
func TestStaleBatchSkipsHeldAndIneligibleCards(t *testing.T) {
	ctx := context.Background()
	repo, pin, head := staleRepo(t)
	card := func(id string, edit func(*board.Card)) board.Card {
		c := board.Card{ID: id, Kind: board.KindSubtask, Status: board.StatusTodo, PinnedSHA: pin}
		if edit != nil {
			edit(&c)
		}
		return c
	}
	expires := time.Now()
	cards := []board.Card{
		card("code", func(c *board.Card) { c.Paths = []string{"internal/**"} }),
		card("docs", func(c *board.Card) { c.Paths = []string{"docs/*.md"} }),
		card("current", func(c *board.Card) { c.PinnedSHA = head }),
		card("held", func(c *board.Card) { c.HeldBy = "task-1"; c.Status = board.StatusDoing }),
		card("unconfirmed", func(c *board.Card) { c.ExpiresAt = &expires }),
		card("done", func(c *board.Card) { c.Status = board.StatusDone }),
		card("cancelled", func(c *board.Card) { c.Status = board.StatusCancelled }),
		card("story", func(c *board.Card) { c.Kind = board.KindStory }),
		card("unpinned", func(c *board.Card) { c.PinnedSHA = "" }),
	}
	var c staleCache
	got, err := c.staleBatch(ctx, repo, cards)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Stale{
		"code":    {Behind: 2, Files: []string{"internal/web/a.go"}},
		"docs":    {Behind: 2, Files: []string{"docs/readme.md"}},
		"current": {Files: []string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("batch = %+v, want %+v", got, want)
	}
	for id, w := range want {
		if g, ok := got[id]; !ok || g.Behind != w.Behind || g.Diverged || !slices.Equal(g.Files, w.Files) {
			t.Fatalf("%s = %+v, want %+v", id, g, w)
		}
	}
	if again, err := c.staleBatch(ctx, repo, cards[:1]); err != nil || !slices.Equal(again["code"].Files, want["code"].Files) {
		t.Fatalf("a cached batch = %+v, %v", again, err)
	}
	empty := t.TempDir()
	gitIn(t, empty, "init", "-q")
	if got, err := c.staleBatch(ctx, empty, cards); err != nil || len(got) != 0 {
		t.Fatalf("a batch before the first commit = %+v, %v", got, err)
	}
	if _, err := c.staleBatch(ctx, t.TempDir(), cards); err == nil {
		t.Fatal("a batch outside git succeeded")
	}
}

// A pin git cannot read leaves only its own card without staleness.
func TestStaleBatchSkipsAnUnreadablePin(t *testing.T) {
	ctx := context.Background()
	repo, pin, _ := staleRepo(t)
	gitIn(t, repo, "switch", "-q", "-c", "side")
	writeRepoFile(t, repo, "side.txt", "only on the side branch\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "side")
	broken := gitOutput(t, repo, "rev-parse", "HEAD")
	tree := gitOutput(t, repo, "rev-parse", "HEAD^{tree}")
	gitIn(t, repo, "switch", "-q", "main")
	// Without its tree the side commit still reads as a commit, but its files
	// cannot be listed.
	if err := os.Remove(filepath.Join(repo, ".git", "objects", tree[:2], tree[2:])); err != nil {
		t.Fatal(err)
	}
	cards := []board.Card{
		{ID: "broken", Kind: board.KindSubtask, Status: board.StatusTodo, PinnedSHA: broken, Paths: []string{"**"}},
		{ID: "fine", Kind: board.KindSubtask, Status: board.StatusTodo, PinnedSHA: pin, Paths: []string{"docs/**"}},
	}
	var c staleCache
	got, err := c.staleBatch(ctx, repo, cards)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["broken"]; ok || len(got) != 1 || got["fine"].Behind != 2 || !slices.Equal(got["fine"].Files, []string{"docs/readme.md"}) {
		t.Fatalf("batch = %+v", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.staleBatch(cancelled, repo, cards); err == nil {
		t.Fatal("a cancelled batch succeeded")
	}
}

func TestMatchPath(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"a.go", "a.go", true},
		{"*.go", "a.go", true},
		{"*.go", "pkg/a.go", false},
		{"**/*.go", "a.go", true},
		{"**/*.go", "x/y/a.go", true},
		{"internal/**/web/*.go", "internal/web/a.go", true},
		{"internal/**/web/*.go", "internal/x/y/web/a.go", true},
		{"internal/**/web/*.go", "internal/x/y/web/sub/a.go", false},
		{"internal/**/web/*.go", "internal/x/y/api/a.go", false},
		{"internal", "internal/web/a.go", true},
		{"/internal/", "internal/web/a.go", true},
		{"./docs/*.md", "docs/readme.md", true},
		{"./*.go", "a.go", true},
		{"./*.go", "pkg/a.go", false},
		{"internal/*", "internal/web/a.go", true},
		{"internal/web/a.go", "internal", false},
		{"inter", "internal/a.go", false},
		{"[bad", "[bad", false},
		{"", "a.go", false},
		{"**", "a.go", true},
	} {
		if got := matchPath(tc.pattern, tc.name); got != tc.want {
			t.Fatalf("matchPath(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
