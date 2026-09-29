// Ported from github.com/RandomCodeSpace/kb internal/store/search.go (MIT).

package board

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SimilarHit is a card whose title resembles a query. The similarity check
// is information only; the duplicate-title rule is the only refusal.
type SimilarHit struct {
	ID     string
	Seq    int64
	Title  string
	Status Status
	Score  float64
}

// SimilarityFloor rejects candidates sharing too little title vocabulary.
const SimilarityFloor = 0.34

const (
	maxSearchBytes  = 500
	maxSearchTokens = 12
)

func searchTokenBoundary(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

// quotedTokens splits raw into at most maxSearchTokens literal FTS phrases.
func quotedTokens(raw string) []string {
	tokens := strings.FieldsFunc(raw, searchTokenBoundary)
	if len(tokens) > maxSearchTokens {
		tokens = tokens[:maxSearchTokens]
	}
	for i, token := range tokens {
		tokens[i] = `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
	}
	return tokens
}

// ftsAnyQuery converts untrusted text to an OR of literal FTS phrases.
func ftsAnyQuery(raw string) string { return strings.Join(quotedTokens(raw), " OR ") }

// ftsSearchQuery converts free text to an AND of literal FTS phrases with
// the last one a prefix, so "auth log" matches cards containing "auth" and a
// word starting with "log". Empty text means no filter.
func ftsSearchQuery(raw string) string {
	tokens := quotedTokens(raw)
	if len(tokens) == 0 {
		return ""
	}
	tokens[len(tokens)-1] += "*"
	return strings.Join(tokens, " AND ")
}

// Similarity is the Sørensen–Dice coefficient over normalized title token
// sets.
func Similarity(a, b string) float64 {
	aSet, bSet := tokenSet(a), tokenSet(b)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}
	overlap := 0
	for token := range aSet {
		if _, ok := bSet[token]; ok {
			overlap++
		}
	}
	return 2 * float64(overlap) / float64(len(aSet)+len(bSet))
}

func tokenSet(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, token := range normalizedTokens(raw) {
		set[token] = struct{}{}
	}
	return set
}

func normalizedTokens(raw string) []string {
	var out []string
	for _, token := range strings.FieldsFunc(strings.ToLower(raw), searchTokenBoundary) {
		token = strings.TrimFunc(token, unicode.IsPunct)
		if utf8.RuneCountInString(token) >= 3 {
			out = append(out, token)
		}
	}
	return out
}

// Similar returns up to limit cards in projectID whose titles resemble
// title, best first, excluding excludeID.
func (s *Store) Similar(ctx context.Context, projectID, title, excludeID string, limit int) ([]SimilarHit, error) {
	match := ftsAnyQuery(title)
	if match == "" || limit <= 0 {
		return nil, nil
	}
	if len(title) > maxSearchBytes {
		return nil, invalid("search query too long")
	}
	normalized := strings.Join(normalizedTokens(title), " ")
	var hits []SimilarHit
	err := s.read(ctx, func(t *txn) error {
		o, err := t.outline(projectID)
		if err != nil {
			return err
		}
		ids, err := t.ids(`SELECT id FROM cards_fts WHERE cards_fts MATCH ? AND project = ? AND id <> ?
			ORDER BY bm25(cards_fts, 5.0, 1.0, 3.0) LIMIT ?`, match, projectID, excludeID, max(limit*8, 24))
		if err != nil {
			return err
		}
		for _, id := range ids {
			n := o.byID[id]
			score := Similarity(title, n.Title)
			exact := normalized != "" && strings.Contains(strings.Join(normalizedTokens(n.Title), " "), normalized)
			if score < SimilarityFloor && !exact {
				continue
			}
			hits = append(hits, SimilarHit{ID: n.ID, Seq: n.Seq, Title: n.Title, Status: n.Status, Score: score})
		}
		return nil
	})
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, err
}
