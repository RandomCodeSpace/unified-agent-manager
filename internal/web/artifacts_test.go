package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func publishCall(t *testing.T, conv *agenttest.Conversation, callID, args string) agentapi.HostToolResult {
	t.Helper()
	res, err := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: publishToolName, CallID: callID, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var artifactIDText = regexp.MustCompile(`artifact id is "([0-9a-f]{16})"`)

// A snapshot copies the site's regular files and leaves out repository data,
// packages, environment files and links; the page reaches only its snapshot.
func TestPublishSnapshotsTheSiteAndServesOnlyIt(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret.txt": "outside secret"})
	writeTree(t, dir, map[string]string{
		"secret.txt":              "workdir secret",
		"site/index.html":         `<link rel="stylesheet" href="style.css"><script src="app.js"></script>`,
		"site/style.css":          "h1 { color: red }",
		"site/app.js":             "document.title = 'ran'",
		"site/about/index.html":   "about page",
		"site/.env":               "TOKEN=x",
		"site/.env.local":         "TOKEN=y",
		"site/.git/config":        "[core]",
		"site/node_modules/a.js":  "module",
		"site/sub/node_modules/b": "module",
	})
	for link, target := range map[string]string{"site/out.txt": filepath.Join(outside, "secret.txt"), "site/in.txt": filepath.Join(dir, "secret.txt")} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	res := publishCall(t, conv, "call-1", `{"path":"site","title":"Demo site"}`)
	match := artifactIDText.FindStringSubmatch(res.Text)
	if res.Failed || match == nil || !strings.Contains(res.Text, "version 1 (4 files") {
		t.Fatalf("publish = %+v", res)
	}
	id := match[1]
	// Later edits leave the snapshot alone.
	writeTree(t, dir, map[string]string{"site/style.css": "h1 { color: blue }"})

	list, err := ts.m.Artifacts(sum.ID)
	if err != nil || len(list) != 1 || list[0].Title != "Demo site" || list[0].Source != "site" || len(list[0].Versions) != 1 || list[0].Versions[0].Entry != "index.html" || list[0].Versions[0].CallID != "call-1" {
		t.Fatalf("artifacts = %+v, %v", list, err)
	}
	if got, _ := ts.m.Summary(sum.ID); got.Artifacts != 1 {
		t.Fatalf("summary artifacts = %d", got.Artifacts)
	}

	view := authenticatedFileView(t, ts)
	base := "/api/sessions/" + sum.ID + "/artifacts/" + id
	w := view(http.MethodGet, base+"/")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "style.css") || w.Header().Get("Content-Security-Policy") != artifactSecurity || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("latest = %d %v", w.Code, w.Header())
	}
	auth := ts.login(t, "127.0.0.1:8260")
	redirect := ts.do(http.MethodGet, base+"/v/1/style.css", "", auth)
	key := redirect.Header().Get("Location")
	if redirect.Code != http.StatusFound || !strings.Contains(key, "/artifact-files/"+id+"/1/") {
		t.Fatalf("version redirect = %d %q", redirect.Code, key)
	}
	if w := ts.do(http.MethodGet, key, ""); w.Code != http.StatusOK || w.Body.String() != "h1 { color: red }" || w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("snapshot style = %d %q", w.Code, w.Body)
	}
	keyRoot := strings.TrimSuffix(key, "style.css")
	if w := ts.do(http.MethodGet, keyRoot+"about", ""); w.Code != http.StatusFound || !strings.HasSuffix(w.Header().Get("Location"), "/about/") {
		t.Fatalf("folder = %d %q", w.Code, w.Header().Get("Location"))
	}
	for _, p := range []string{".env", ".env.local", ".git/config", "node_modules/a.js", "sub/node_modules/b", "out.txt", "in.txt",
		"..%2Fsecret.txt", "..%2F..%2F..%2Fsecret.txt", "%2Fetc%2Fpasswd"} {
		if w := ts.do(http.MethodGet, keyRoot+p, ""); w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("GET %s = %d %q", p, w.Code, w.Body)
		}
	}
	// The key opens only its own version, only with its own Thread and artifact.
	for _, other := range []string{
		strings.Replace(keyRoot, "/1/", "/2/", 1),
		strings.Replace(keyRoot, sum.ID, "00000000-0000-4000-8000-000000000000", 1),
		strings.Replace(keyRoot, id, "0123456789abcdef", 1),
	} {
		if w := ts.do(http.MethodGet, other+"index.html", ""); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d", other, w.Code)
		}
	}
	if w := ts.do(http.MethodGet, base+"/", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("latest without a cookie = %d", w.Code)
	}
}

func TestPublishRefusesPathsOutsideTheThreadAndOverTheCaps(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"index.html": "outside"})
	writeTree(t, dir, map[string]string{"index.html": "top", "noindex/a.css": "x", ".env": "TOKEN=x"})
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	many := map[string]string{"many/index.html": "x"}
	for i := range maxArtifactFiles {
		many[fmt.Sprintf("many/f%d.txt", i)] = "x"
	}
	writeTree(t, dir, many)
	if err := os.MkdirAll(filepath.Join(dir, "big"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"big/index.html": "x"})
	big, err := os.Create(filepath.Join(dir, "big", "blob.bin"))
	if err != nil || big.Truncate(maxArtifactBytes) != nil || big.Close() != nil {
		t.Fatal(err)
	}
	for args, want := range map[string]string{
		`{"path":"..","title":"x"}`:                                 "inside the thread's directory",
		`{"path":".","title":"x"}`:                                  "inside the thread's directory",
		`{"path":"linked","title":"x"}`:                             "symbolic link",
		`{"path":"` + outside + `","title":"x"}`:                    "inside the thread's directory",
		`{"path":".env","title":"x"}`:                               ".env",
		`{"path":"noindex","title":"x"}`:                            "has no index.html",
		`{"path":"many","title":"x"}`:                               "more than 500 files and folders",
		`{"path":"big","title":"x"}`:                                "more than 500 files and folders or 25 MiB",
		`{"path":"index.html","title":""}`:                          "title must be",
		`{"path":"index.html","title":"x","id":"no"}`:               "id must be",
		`{"path":"index.html","title":"x","id":"0123456789abcdef"}`: "no artifact",
	} {
		if res := publishCall(t, conv, "c", args); !res.Failed || !strings.Contains(res.Text, want) {
			t.Errorf("%s = %+v, want %q", args, res, want)
		}
	}
	entries, _ := os.ReadDir(ts.m.artifactRoot(sum.ID))
	if len(entries) != 0 {
		t.Fatalf("refused publishes left %d entries", len(entries))
	}
}

// Publishing the same title or id adds a version; the latest link follows
// it; an artifact keeps its last versions; deleting removes a version, then
// the artifact.
func TestPublishVersionsLatestAndDelete(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{"report.html": "v1"})
	id := artifactIDText.FindStringSubmatch(publishCall(t, conv, "c1", `{"path":"report.html","title":"Report"}`).Text)[1]
	writeTree(t, dir, map[string]string{"report.html": "v2"})
	if res := publishCall(t, conv, "c2", `{"path":"report.html","title":"Report"}`); !strings.Contains(res.Text, "version 2") {
		t.Fatalf("same title = %+v", res)
	}
	writeTree(t, dir, map[string]string{"report.html": "v3"})
	if res := publishCall(t, conv, "c3", `{"path":"report.html","title":"Renamed report","id":"`+id+`"}`); !strings.Contains(res.Text, "version 3") || !strings.Contains(res.Text, id) {
		t.Fatalf("by id = %+v", res)
	}
	view := authenticatedFileView(t, ts)
	base := "/api/sessions/" + sum.ID + "/artifacts/" + id
	for path, want := range map[string]string{base + "/": "v3", base + "/v/1/": "v1", base + "/v/2/report.html": "v2"} {
		if w := view(http.MethodGet, path); w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("GET %s = %d %q, want %q", path, w.Code, w.Body, want)
		}
	}
	for i := 4; i <= maxArtifactVersions+2; i++ {
		publishCall(t, conv, fmt.Sprintf("c%d", i), `{"path":"report.html","title":"Renamed report"}`)
	}
	list, _ := ts.m.Artifacts(sum.ID)
	if len(list) != 1 || list[0].Title != "Renamed report" || len(list[0].Versions) != maxArtifactVersions || list[0].Versions[0].Version != 3 {
		t.Fatalf("pruned = %+v", list)
	}
	if w := view(http.MethodGet, base+"/v/1/"); w.Code != http.StatusNotFound {
		t.Fatalf("pruned version = %d", w.Code)
	}

	auth := ts.login(t, "127.0.0.1:8260")
	if w := ts.do(http.MethodDelete, base+"/versions/3", "", auth); w.Code != http.StatusNoContent {
		t.Fatalf("delete version = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodDelete, base+"/versions/3", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("delete version again = %d", w.Code)
	}
	if _, err := os.Stat(artifactVersionDir(ts.m.artifactRoot(sum.ID), id, 3)); !os.IsNotExist(err) {
		t.Fatalf("version 3 left on disk: %v", err)
	}
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/artifacts", "", auth)
	var got struct{ Artifacts []Artifact }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Artifacts) != 1 || len(got.Artifacts[0].Versions) != maxArtifactVersions-1 {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodDelete, base, "", auth); w.Code != http.StatusNoContent {
		t.Fatalf("delete artifact = %d %s", w.Code, w.Body)
	}
	if list, _ := ts.m.Artifacts(sum.ID); len(list) != 0 || summaryArtifacts(ts.m, sum.ID) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

func summaryArtifacts(m *Manager, id string) int {
	got, _ := m.Summary(id)
	return got.Artifacts
}

// A selected path is checked name by name: an excluded folder or a link on
// the way refuses it, whatever lies below.
func TestPublishRefusesExcludedOrLinkedSelections(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{
		".git/config":                 "[core]",
		".git/site/index.html":        "repo",
		"node_modules/pkg/index.html": "pkg",
		"site/.env.local":             "TOKEN=x",
		"site/.env.d/index.html":      "env",
		"site/index.html":             "site",
	})
	if err := os.Symlink(filepath.Join(dir, "site"), filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".git/config", ".git/site", "node_modules/pkg/index.html", "site/.env.local", "site/.env.d", "site/.env.d/index.html", dir + "/.git/site", "alias", "alias/index.html"} {
		if res := publishCall(t, conv, "c", `{"path":"`+p+`","title":"x"}`); !res.Failed {
			t.Errorf("publish %s = %+v", p, res)
		}
	}
	if list, _ := ts.m.Artifacts(sum.ID); len(list) != 0 {
		t.Fatalf("artifacts = %+v", list)
	}
}

// A folder swapped for a link after Lstat saw it is not opened through it.
func TestSnapshotDirRefusesASwappedFolder(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"site/index.html": "site", ".git/index.html": "repo"})
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat("site")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "site"), filepath.Join(dir, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(dir, "site")); err != nil {
		t.Fatal(err)
	}
	if _, err := openSnapshotDir(root, "site", info); err == nil {
		t.Fatal("opened the link put in the folder's place")
	}
}

// The copy reads through the directory it opened: replacing the Thread's
// directory by name afterwards does not change what it copies.
func TestSnapshotCopiesThroughTheOpenedRoot(t *testing.T) {
	parent := t.TempDir()
	workdir := filepath.Join(parent, "work")
	writeTree(t, workdir, map[string]string{"index.html": "original"})
	root, err := os.OpenRoot(workdir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := os.Rename(workdir, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, workdir, map[string]string{"index.html": "replacement"})
	dst := t.TempDir()
	s := snapshotter{ctx: context.Background()}
	if err := s.copyDir(root, dst, "", 1); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "index.html")); string(data) != "original" {
		t.Fatalf("copied %q", data)
	}
}

// Hard-linked files and special files are left out and named in the result;
// a selected hard-linked file is refused; a FIFO is never opened.
func TestPublishLeavesOutHardLinksAndSpecialFiles(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{"site/index.html": "page", ".env": "TOKEN=secret"})
	if err := os.Link(filepath.Join(dir, ".env"), filepath.Join(dir, "site", "env.txt")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "site", "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := publishCall(t, conv, "c1", `{"path":"site","title":"Site"}`)
	if res.Failed || !strings.Contains(res.Text, "1 files") || !strings.Contains(res.Text, "env.txt (has other hard links)") || !strings.Contains(res.Text, "pipe (a link or special file)") {
		t.Fatalf("publish = %+v", res)
	}
	list, _ := ts.m.Artifacts(sum.ID)
	if _, err := ts.m.ArtifactFile(sum.ID, list[0].ID, 1, "env.txt"); err == nil {
		t.Fatal("the hard link was published")
	}
	if res := publishCall(t, conv, "c2", `{"path":"site/env.txt","title":"Env"}`); !res.Failed || !strings.Contains(res.Text, "hard links") {
		t.Fatalf("publish the link = %+v", res)
	}
}

// Folders count toward the entry cap and nesting is bounded.
func TestPublishBoundsFoldersAndDepth(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	_, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{"wide/index.html": "x", "deep/index.html": "x"})
	for i := range maxArtifactFiles {
		if err := os.Mkdir(filepath.Join(dir, "wide", fmt.Sprintf("d%d", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "deep", strings.Repeat("d/", maxArtifactDepth+1)), 0o700); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"wide": "more than 500 files and folders", "deep": "more than 32 deep"} {
		if res := publishCall(t, conv, "c", `{"path":"`+p+`","title":"x"}`); !res.Failed || !strings.Contains(res.Text, want) {
			t.Errorf("publish %s = %+v", p, res)
		}
	}
}

// Concurrent publishes of new artifacts never pass the Thread's cap.
func TestConcurrentPublishesKeepTheArtifactCap(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{"page.html": "x"})
	for i := range maxThreadArtifacts - 1 {
		if res := publishCall(t, conv, fmt.Sprintf("c%d", i), fmt.Sprintf(`{"path":"page.html","title":"t%d"}`, i)); res.Failed {
			t.Fatal(res.Text)
		}
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			publishCall(t, conv, fmt.Sprintf("r%d", i), fmt.Sprintf(`{"path":"page.html","title":"race %d"}`, i))
		})
	}
	wg.Wait()
	if list, _ := ts.m.Artifacts(sum.ID); len(list) != maxThreadArtifacts {
		t.Fatalf("artifacts = %d", len(list))
	}
	if entries, _ := os.ReadDir(ts.m.artifactRoot(sum.ID)); len(entries) != maxThreadArtifacts {
		t.Fatalf("entries = %d", len(entries))
	}
}

// Deleting the newest version never lets the next publish reuse its number,
// so a key for the deleted version keeps failing; every change bumps the
// summary's revision, even one that keeps the count.
func TestPublishNeverReusesAVersionNumber(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{"report.html": "v1"})
	id := artifactIDText.FindStringSubmatch(publishCall(t, conv, "c1", `{"path":"report.html","title":"Report"}`).Text)[1]
	writeTree(t, dir, map[string]string{"report.html": "v2 secret draft"})
	publishCall(t, conv, "c2", `{"path":"report.html","title":"Report"}`)
	auth := ts.login(t, "127.0.0.1:8260")
	base := "/api/sessions/" + sum.ID + "/artifacts/" + id
	key := ts.do(http.MethodGet, base+"/v/2/", "", auth).Header().Get("Location")
	if w := ts.do(http.MethodGet, key, ""); w.Code != http.StatusOK || w.Body.String() != "v2 secret draft" {
		t.Fatalf("v2 = %d %q", w.Code, w.Body)
	}
	before, _ := ts.m.Summary(sum.ID)
	if w := ts.do(http.MethodDelete, base+"/versions/2", "", auth); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	after, _ := ts.m.Summary(sum.ID)
	if after.Artifacts != before.Artifacts || after.ArtifactsRevision == before.ArtifactsRevision {
		t.Fatalf("summary before %+v after %+v", before, after)
	}
	writeTree(t, dir, map[string]string{"report.html": "v3"})
	if res := publishCall(t, conv, "c3", `{"path":"report.html","title":"Report"}`); !strings.Contains(res.Text, "version 3") {
		t.Fatalf("next publish = %+v", res)
	}
	if w := ts.do(http.MethodGet, key, ""); w.Code != http.StatusNotFound {
		t.Fatalf("deleted version's key = %d %q", w.Code, w.Body)
	}
}

// What an interrupted publish left is never served and goes on the next
// publish: staging folders, unlisted versions, an artifact with no metadata.
func TestPublishReconcilesWhatAnInterruptedPublishLeft(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	writeTree(t, dir, map[string]string{"report.html": "v1"})
	id := artifactIDText.FindStringSubmatch(publishCall(t, conv, "c1", `{"path":"report.html","title":"Report"}`).Text)[1]
	root := ts.m.artifactRoot(sum.ID)
	leftovers := []string{filepath.Join(root, stagingPrefix+"123"), artifactVersionDir(root, id, 2), artifactVersionDir(root, "0123456789abcdef", 1)}
	for _, p := range leftovers {
		writeTree(t, p, map[string]string{"report.html": "stray"})
	}
	if _, err := ts.m.ArtifactFile(sum.ID, id, 2, "report.html"); err == nil {
		t.Fatal("served a version its metadata does not list")
	}
	if res := publishCall(t, conv, "c2", `{"path":"report.html","title":"Report"}`); res.Failed || !strings.Contains(res.Text, "version 2") {
		t.Fatalf("publish = %+v", res)
	}
	for _, p := range []string{leftovers[0], filepath.Dir(leftovers[2])} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left: %v", p, err)
		}
	}
	if f, err := ts.m.ArtifactFile(sum.ID, id, 2, "report.html"); err != nil {
		t.Fatal(err)
	} else if data, _ := io.ReadAll(f.File); string(data) != "v1" {
		t.Fatalf("version 2 = %q", data)
	}
}
