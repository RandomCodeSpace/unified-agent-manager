package web

import (
	"cmp"
	"hash/fnv"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// badgeColors are the palette keys a Project badge's colour is drawn from.
// The browser maps each key to a tone of its one theme.
var badgeColors = []string{"red", "orange", "amber", "lime", "green", "teal", "cyan", "blue", "violet", "pink"}

// badgeChars are the characters of a badge's text; the first 26 are A–Z.
const badgeChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randomPick is the Manager's default pick: a random int in [0, n).
func randomPick(n int) int {
	return rand.IntN(n) // #nosec G404 -- badge colours and letters are cosmetic, not security-sensitive values.
}

func validBadge(b store.WebBadge) bool {
	return len(b.Text) == 2 && strings.IndexByte(badgeChars, b.Text[0]) >= 0 && strings.IndexByte(badgeChars, b.Text[1]) >= 0 &&
		slices.Contains(badgeColors, b.Color)
}

// newBadge picks the badge of a Project named name, given every Project's
// badge. The text is the name's first and last ASCII letter or digit (P and
// a random free A–Z when it has none), uppercased, so "config" is CG. When
// that pair is taken or the name has one such character, the second is a
// random one from the rest of the name that no Project has with the first,
// then a random free A–Z, and then the text is any free pair; past 1,296
// Projects it may repeat. The colour is one no Project uses, or any once
// every one is used. pick returns a random int in [0, n).
func newBadge(name string, projects map[string]store.WebProject, pick func(int) int) store.WebBadge {
	texts, colors := map[string]bool{}, map[string]bool{}
	for _, p := range projects {
		if p.Badge != (store.WebBadge{}) {
			texts[p.Badge.Text], colors[p.Badge.Color] = true, true
		}
	}
	// Bytes, not runes: only ASCII qualifies, and folding a non-ASCII
	// letter's case can yield an ASCII one.
	var chars []byte
	for i := range len(name) {
		c := name[i]
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		if strings.IndexByte(badgeChars, c) >= 0 {
			chars = append(chars, c)
		}
	}
	first := "P"
	if len(chars) > 0 {
		first, chars = string(chars[:1]), chars[1:]
	}
	free := func(firsts, seconds string) []string {
		var out []string
		seen := map[string]bool{}
		for i := range len(firsts) {
			for j := range len(seconds) {
				text := firsts[i:i+1] + seconds[j:j+1]
				if !texts[text] && !seen[text] {
					seen[text] = true
					out = append(out, text)
				}
			}
		}
		return out
	}
	var options []string
	if len(chars) > 0 {
		options = free(first, string(chars[len(chars)-1:]))
	}
	if len(options) == 0 {
		options = free(first, string(chars))
	}
	if len(options) == 0 {
		options = free(first, badgeChars[:26])
	}
	if len(options) == 0 {
		options = free(badgeChars, badgeChars)
	}
	if len(options) == 0 {
		n := pick(26)
		options = []string{first + badgeChars[n:n+1]}
	}
	tones := slices.DeleteFunc(slices.Clone(badgeColors), func(c string) bool { return colors[c] })
	if len(tones) == 0 {
		tones = badgeColors
	}
	return store.WebBadge{Text: options[pick(len(options))], Color: tones[pick(len(tones))]}
}

// badgeless lists the Projects, oldest first, that need a new badge: theirs
// is invalid, or an older Project's badge has its text while a free text is
// left. Once every text is taken a new badge would repeat one too, so a
// repeat is kept rather than rewritten on every start.
func badgeless(projects map[string]store.WebProject) []string {
	ids := slices.SortedFunc(maps.Keys(projects), func(a, b string) int {
		return cmp.Or(projects[a].CreatedAt.Compare(projects[b].CreatedAt), strings.Compare(a, b))
	})
	texts := map[string]bool{}
	for _, p := range projects {
		if validBadge(p.Badge) {
			texts[p.Badge.Text] = true
		}
	}
	full := len(texts) == len(badgeChars)*len(badgeChars)
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if b := projects[id].Badge; validBadge(b) && (!seen[b.Text] || full) {
			seen[b.Text] = true
			continue
		}
		out = append(out, id)
	}
	return out
}

// assignBadges gives every Project badgeless lists a new badge, oldest
// first. Other Projects keep theirs. A nil pick draws each Project's choices
// from a generator seeded with its id, so a badge that cannot be stored is
// the same on every start.
func assignBadges(cfg *store.Config, pick func(int) int) {
	ids := badgeless(cfg.WebProjects)
	for _, id := range ids {
		p := cfg.WebProjects[id]
		p.Badge = store.WebBadge{}
		cfg.WebProjects[id] = p
	}
	for _, id := range ids {
		p := cfg.WebProjects[id]
		pk := pick
		if pk == nil {
			h := fnv.New64a()
			_, _ = h.Write([]byte(id)) // hash.Hash.Write never returns an error.
			seed := h.Sum64()
			pk = rand.New(rand.NewPCG(seed, seed)).IntN // #nosec G404 -- a deterministic cosmetic badge must survive a read-only store restart.
		}
		p.Badge = newBadge(loadedName(p.Name, p.Dir), cfg.WebProjects, pk)
		cfg.WebProjects[id] = p
	}
}
