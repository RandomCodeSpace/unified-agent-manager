package web

import (
	"container/list"
	"context"
	"crypto/sha256"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// Staleness (ADR 0005 §9): how far HEAD has moved from a subtask's pin,
// whether HEAD still descends from it, and which of the files changed since
// match the subtask's paths.
const (
	staleCacheSize = 512
	maxStaleFiles  = 100
)

// Stale is a subtask's staleness: Behind counts the commits on HEAD that
// are not on the pin, Diverged is set once the pin is not an ancestor of
// HEAD (or no longer exists), and Files lists the changed files that match
// the subtask's paths, at most maxStaleFiles.
type Stale struct {
	Behind   int      `json:"behind"`
	Diverged bool     `json:"diverged"`
	Files    []string `json:"files"`
}

type staleKey struct {
	dir, pin, head string
	paths          [sha256.Size]byte
}

type staleEntry struct {
	key   staleKey
	stale Stale
}

// staleCache computes staleness and keeps the latest staleCacheSize
// results. Commits never change, so a result stays right for its key. The
// zero value is ready to use.
type staleCache struct {
	mu      sync.Mutex
	entries list.List // *staleEntry, most recently used first
	index   map[staleKey]*list.Element
}

// pinFacts is what git says about one pin against HEAD, read once per
// batch; changed is read only when a subtask with paths needs it.
type pinFacts struct {
	behind   int
	diverged bool
	gone     bool
	changed  []string
	listed   bool
}

// staleFor is the staleness of a subtask pinned at pin, with paths, against
// head in the repository at dir.
func (c *staleCache) staleFor(ctx context.Context, dir, pin, head string, paths []string) (Stale, error) {
	if !isRev(pin) || !isRev(head) {
		return Stale{}, newError(http.StatusBadRequest, "the pin and HEAD must be commit names")
	}
	key := newStaleKey(dir, pin, head, paths)
	if s, ok := c.get(key); ok {
		return s, nil
	}
	repo, err := openEvidenceRepo(ctx, dir)
	if err != nil {
		return Stale{}, err
	}
	return c.compute(ctx, repo, key, paths, map[string]*pinFacts{})
}

// staleBatch computes staleness for a Project's candidates, as
// StaleCandidates lists them, against the repository's current HEAD, and
// returns it by card ID. Each distinct pin is read once. A card that is not
// a confirmed, live, unheld subtask with a pin gets none: staleness never
// runs for a held subtask (ADR 0005 test plan 19).
func (c *staleCache) staleBatch(ctx context.Context, dir string, cards []board.Card) (map[string]Stale, error) {
	out := map[string]Stale{}
	repo, err := openEvidenceRepo(ctx, dir)
	if err != nil {
		return nil, err
	}
	head, err := repo.head(ctx)
	if err != nil || head == "" {
		return out, err
	}
	facts := map[string]*pinFacts{}
	for _, card := range cards {
		if card.Kind != board.KindSubtask || !card.Confirmed() || card.HeldBy != "" ||
			card.Status == board.StatusDone || card.Status == board.StatusCancelled || !isRev(card.PinnedSHA) {
			continue
		}
		key := newStaleKey(dir, card.PinnedSHA, head, card.Paths)
		s, ok := c.get(key)
		if !ok {
			if s, err = c.compute(ctx, repo, key, card.Paths, facts); err != nil {
				return nil, err
			}
		}
		out[card.ID] = s
	}
	return out, nil
}

func newStaleKey(dir, pin, head string, paths []string) staleKey {
	return staleKey{dir: dir, pin: pin, head: head, paths: sha256.Sum256([]byte(strings.Join(paths, "\x00")))}
}

// compute reads key's staleness from git, through facts, and caches it.
func (c *staleCache) compute(ctx context.Context, repo *gitRepo, key staleKey, paths []string, facts map[string]*pinFacts) (Stale, error) {
	s := Stale{Files: []string{}}
	if key.pin != key.head {
		f := facts[key.pin]
		if f == nil {
			f = &pinFacts{}
			if err := f.read(ctx, repo, key.pin, key.head); err != nil {
				return Stale{}, err
			}
			facts[key.pin] = f
		}
		s.Behind, s.Diverged = f.behind, f.diverged
		if len(paths) > 0 && !f.gone {
			if err := f.list(ctx, repo, key.pin, key.head); err != nil {
				return Stale{}, err
			}
			for _, name := range f.changed {
				if len(s.Files) == maxStaleFiles {
					break
				}
				if slices.ContainsFunc(paths, func(p string) bool { return matchPath(p, name) }) {
					s.Files = append(s.Files, name)
				}
			}
		}
	}
	c.put(key, s)
	return s, nil
}

// read learns whether pin is an ancestor of head and how many commits head
// has that pin lacks. A pin that no longer exists, pruned after a history
// rewrite, is diverged.
func (f *pinFacts) read(ctx context.Context, repo *gitRepo, pin, head string) error {
	_, code, stderr, err := runGit(ctx, repo.git, repo.top, 4096, "merge-base", "--is-ancestor", pin, head)
	if err != nil {
		return err
	}
	switch code {
	case 0:
	case 1:
		f.diverged = true
	default:
		_, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "cat-file", "-e", pin+"^{commit}")
		if err != nil {
			return err
		}
		if code == 0 {
			return newError(http.StatusBadGateway, "git merge-base failed: %s", gitMessage(stderr))
		}
		f.diverged, f.gone = true, true
		return nil
	}
	out, code, stderr, err := runGit(ctx, repo.git, repo.top, 4096, "rev-list", "--count", pin+".."+head, "--")
	if err != nil {
		return err
	}
	if code != 0 {
		return newError(http.StatusBadGateway, "git rev-list failed: %s", gitMessage(stderr))
	}
	if f.behind, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
		return newError(http.StatusBadGateway, "git rev-list gave no count")
	}
	return nil
}

// list reads, once, the files that differ between pin and head.
func (f *pinFacts) list(ctx context.Context, repo *gitRepo, pin, head string) error {
	if f.listed {
		return nil
	}
	out, code, stderr, err := runGit(ctx, repo.git, repo.top, maxStatusBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--name-only", "-z", pin, head, "--")
	if err != nil {
		return err
	}
	if code != 0 {
		return newError(http.StatusBadGateway, "git diff failed: %s", gitMessage(stderr))
	}
	f.changed, f.listed = nulRecords(out), true
	return nil
}

func (c *staleCache) get(key staleKey) (Stale, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.index[key]
	if !ok {
		return Stale{}, false
	}
	c.entries.MoveToFront(e)
	s := e.Value.(*staleEntry).stale
	s.Files = slices.Clone(s.Files)
	return s, true
}

func (c *staleCache) put(key staleKey, s Stale) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s.Files = slices.Clone(s.Files)
	if e, ok := c.index[key]; ok {
		e.Value.(*staleEntry).stale = s
		c.entries.MoveToFront(e)
		return
	}
	if c.index == nil {
		c.index = map[staleKey]*list.Element{}
	}
	c.index[key] = c.entries.PushFront(&staleEntry{key: key, stale: s})
	if c.entries.Len() > staleCacheSize {
		delete(c.index, c.entries.Remove(c.entries.Back()).(*staleEntry).key)
	}
}

// matchPath reports whether name, a slash-separated work tree path, matches
// pattern segment by segment with path.Match, where a "**" segment matches
// any number of segments. A pattern that matches a directory matches every
// file under it.
func matchPath(pattern, name string) bool {
	return matchSegments(strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(name, "/"))
}

func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := range len(name) + 1 {
				if matchSegments(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pattern[0], name[0]); err != nil || !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return true
}
