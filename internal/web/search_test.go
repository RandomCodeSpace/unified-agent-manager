package web

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
)

// sessionStore writes a Copilot session store under a fresh COPILOT_HOME,
// with the CLI's search_index layout, holding rows of (session, text).
func sessionStore(t *testing.T, rows ...[2]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	db, err := sql.Open("sqlite", filepath.Join(home, sessionStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE search_index USING fts5(content, session_id UNINDEXED, source_type UNINDEXED, source_id UNINDEXED)`); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if _, err := db.Exec(`INSERT INTO search_index VALUES (?, ?, 'turn', ?)`, r[1], r[0], i); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchThreads(t *testing.T) {
	sessionStore(t,
		[2]string{"conv-a", "We fixed the flaky redraw test by replaying focus events."},
		[2]string{"conv-a", "The redraw also needed a second pass."},
		[2]string{"conv-b", "Unrelated chat about lunch."},
		[2]string{"outside", "A redraw in a session uam does not know."},
	)
	m, prov, _ := newTestManager(t)
	a, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: a.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	setSession(m, a.ID, func(s *webSession) { s.convID = "conv-a" })
	setSession(m, b.ID, func(s *webSession) { s.convID = "conv-b" })

	got, err := m.SearchThreads(t.Context(), "flaky redr")
	if err != nil || !got.Available || len(got.Hits) != 1 || got.Hits[0].ThreadID != a.ID {
		t.Fatalf("search = %+v, %v; want one hit in %s", got, err, a.ID)
	}
	var matched []string
	for _, p := range got.Hits[0].Snippet {
		if p.Match {
			matched = append(matched, p.Text)
		}
	}
	if len(matched) != 2 || matched[0] != "flaky" || matched[1] != "redraw" {
		t.Fatalf("snippet = %+v", got.Hits[0].Snippet)
	}
	// One hit per Thread; sessions uam does not know are left out.
	if got, err := m.SearchThreads(t.Context(), "redraw"); err != nil || len(got.Hits) != 1 {
		t.Fatalf("redraw = %+v, %v", got, err)
	}
	// Syntax never reaches FTS5 as such.
	for _, q := range []string{`"unclosed`, `AND OR NOT`, `lunch)*`, `-- ;`, `NEAR(`} {
		if _, err := m.SearchThreads(t.Context(), q); err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
	}
	if got, _ := m.SearchThreads(t.Context(), `lunch)*`); len(got.Hits) != 1 || got.Hits[0].ThreadID != b.ID {
		t.Fatalf("lunch = %+v", got)
	}
}

func TestSearchThreadsWithoutAStore(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	m, _, _ := newTestManager(t)
	got, err := m.SearchThreads(t.Context(), "anything")
	if err != nil || got.Available || got.Reason == "" || got.Hits == nil {
		t.Fatalf("search without a store = %+v, %v", got, err)
	}
}

func TestSearchRoute(t *testing.T) {
	sessionStore(t)
	ts := newTestServer(t, ServerConfig{})
	if w := ts.do(http.MethodGet, "/api/search?q=x", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated search = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/search?q=x", "", withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("search = %d %s", w.Code, w.Body)
	}
}
