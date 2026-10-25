package web

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"

	_ "modernc.org/sqlite" // The "sqlite" driver, pure Go.
)

// Search across the Threads' conversations reads the Copilot CLI's own
// full-text index (session-store.db, an FTS5 table it keeps up to date),
// read-only: uam builds and keeps no index, and nothing runs between
// searches. One search runs at a time, within searchTimeout.
const (
	searchTimeout     = 2 * time.Second
	maxSearchQuery    = 200
	maxSearchTerms    = 8
	maxSearchRows     = 200
	maxSearchHits     = 30
	maxSnippetRunes   = 300
	sessionStoreFile  = "session-store.db"
	searchUnavailable = "Search in conversations is unavailable: "
)

// SearchResults are the Threads whose conversation matches a query, best
// first, one hit each. Available is false, with Reason, when the Copilot
// session store cannot be searched.
type SearchResults struct {
	Available bool        `json:"available"`
	Reason    string      `json:"reason,omitempty"`
	Hits      []SearchHit `json:"hits"`
}

// SearchHit is a Thread and the matching passage of its conversation.
type SearchHit struct {
	ThreadID string        `json:"thread_id"`
	Snippet  []SnippetPart `json:"snippet"`
}

// SnippetPart is a run of a passage; Match marks the query's words.
type SnippetPart struct {
	Text  string `json:"text"`
	Match bool   `json:"match,omitempty"`
}

// SearchThreads finds q in the conversations of uam's Threads.
func (m *Manager) SearchThreads(ctx context.Context, q string) (SearchResults, error) {
	out := SearchResults{Hits: []SearchHit{}}
	match := ftsQuery(q)
	if match == "" {
		out.Available = true
		return out, nil
	}
	scope, err := m.configurationScope("")
	if err != nil {
		return out, err
	}
	path := filepath.Join(scope.base, sessionStoreFile)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		out.Reason = searchUnavailable + "Copilot has not indexed any conversation on this server yet."
		return out, nil
	}
	threads := map[string]string{}
	m.mu.Lock()
	if m.searchSlot == nil {
		m.searchSlot = make(chan struct{}, 1)
	}
	slot := m.searchSlot
	for _, s := range m.sessions {
		if !s.removed && s.convID != "" {
			threads[s.convID] = s.id
		}
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		return out, newError(http.StatusServiceUnavailable, "another search is still running; try again")
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return out, newError(http.StatusBadGateway, "could not open Copilot's session store: %s", shortError(err))
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	rows, err := db.QueryContext(ctx, `SELECT session_id, snippet(search_index, 0, char(2), char(3), '…', 24)
		FROM search_index WHERE search_index MATCH ? ORDER BY rank LIMIT ?`, match, maxSearchRows)
	if err != nil {
		if ctx.Err() != nil {
			return out, newError(http.StatusGatewayTimeout, "the search took too long; try more specific words")
		}
		// The store is Copilot's own: a layout it changed reads as unavailable, not as a failure.
		log.Warn("search in Copilot's session store failed", "err", err)
		out.Reason = searchUnavailable + "Copilot's session store could not be read."
		return out, nil
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() && len(out.Hits) < maxSearchHits {
		var conv, snippet string
		if err := rows.Scan(&conv, &snippet); err != nil {
			return out, newError(http.StatusBadGateway, "could not read a search result: %s", shortError(err))
		}
		id, ok := threads[conv]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out.Hits = append(out.Hits, SearchHit{ThreadID: id, Snippet: snippetParts(snippet)})
	}
	if err := rows.Err(); err != nil && ctx.Err() != nil {
		return out, newError(http.StatusGatewayTimeout, "the search took too long; try more specific words")
	}
	out.Available = true
	return out, nil
}

// ftsQuery is q as an FTS5 query: each word a quoted string, all required,
// the last one also a prefix, so typing never makes a syntax error. Words
// without a letter or digit are dropped; "" means nothing to search.
func ftsQuery(q string) string {
	if len(q) > maxSearchQuery {
		q = q[:maxSearchQuery]
	}
	var terms []string
	for _, w := range strings.Fields(q) {
		if len(terms) == maxSearchTerms {
			break
		}
		if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	if len(terms) == 0 {
		return ""
	}
	terms[len(terms)-1] += "*"
	return strings.Join(terms, " ")
}

// snippetParts splits a snippet marked with \x02…\x03 into its runs, made
// safe to show, on one line and at most maxSnippetRunes long.
func snippetParts(snippet string) []SnippetPart {
	snippet = strings.Join(strings.Fields(snippet), " ")
	var parts []SnippetPart
	left := maxSnippetRunes
	add := func(text string, match bool) {
		text = displaytext.SanitizeText(text)
		if text == "" || left <= 0 {
			return
		}
		text = clipRunes(text, left)
		left -= utf8.RuneCountInString(text)
		parts = append(parts, SnippetPart{Text: text, Match: match})
	}
	for i, run := range strings.Split(snippet, "\x02") {
		matched, rest, ok := strings.Cut(run, "\x03")
		if i == 0 || !ok {
			add(run, false)
			continue
		}
		add(matched, true)
		add(rest, false)
	}
	return parts
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	results, err := s.m.SearchThreads(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}
