package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"golang.org/x/sys/unix"
)

type grantFixture struct {
	ts                 *testServer
	task               SessionSummary
	dir, runtime, file string
	auth               reqOpt
}

func newGrantFixture(t *testing.T, configure ...func(string, string) (string, string)) grantFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(dir, "runtime")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(file, []byte(strings.Repeat("0123456789abcdef", 8192)), 0600); err != nil {
		t.Fatal(err)
	}
	tempPath, runtimePath := dir, runtime
	if len(configure) != 0 {
		tempPath, runtimePath = configure[0](dir, runtime)
	}
	t.Setenv("TMPDIR", tempPath)
	t.Setenv("UAM_SESSION_DIR", runtimePath)
	ts := newTestServer(t, ServerConfig{})
	task, _ := createSession(t, ts.m, ts.prov)
	return grantFixture{ts: ts, task: task, dir: dir, runtime: runtime, file: file, auth: ts.login(t, "127.0.0.1:8260")}
}

func (f grantFixture) post(t *testing.T, candidate string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"path": candidate})
	if err != nil {
		t.Fatal(err)
	}
	return f.ts.do(http.MethodPost, "/api/sessions/"+f.task.ID+"/file-grants", string(body), f.auth)
}

func (f grantFixture) create(t *testing.T, candidate string) fileGrant {
	t.Helper()
	w := f.post(t, candidate)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var grant fileGrant
	if err := json.Unmarshal(w.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f grantFixture) entry(id string) *tempGrant {
	g := f.ts.srv.grants
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.entries[id]
}

func TestTempGrantHTTPContract(t *testing.T) {
	f := newGrantFixture(t)
	base := "/api/sessions/" + f.task.ID + "/file-grants"
	body, _ := json.Marshal(map[string]string{"path": f.file})
	for _, tc := range []struct {
		name, method, target, body string
		opts                       []reqOpt
		status                     int
	}{
		{"mint needs cookie", "POST", base, string(body), nil, 401},
		{"foreign origin", "POST", base, string(body), []reqOpt{f.auth, withHeader("Origin", "https://foreign.example")}, 403},
		{"JSON required", "POST", base, string(body), []reqOpt{f.auth, withHeader("Content-Type", "text/plain")}, 415},
		{"body limit", "POST", base, strings.Repeat(" ", maxGrantBody+1), []reqOpt{f.auth}, 413},
		{"extra object", "POST", base, string(body) + `{}`, []reqOpt{f.auth}, 400},
		{"unknown field", "POST", base, `{"path":"x","title":"x"}`, []reqOpt{f.auth}, 400},
		{"invalid UTF8", "POST", base, "{\"path\":\"\xff\"}", []reqOpt{f.auth}, 400},
		{"unknown task", "POST", "/api/sessions/missing/file-grants", string(body), []reqOpt{f.auth}, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.ts.do(tc.method, tc.target, tc.body, tc.opts...)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status/header = %d %v", w.Code, w.Header())
			}
		})
	}
	grant := f.create(t, f.file)
	if grant.Name != "report.txt" || grant.Size != 131072 || grant.MIME != "text/plain; charset=utf-8" ||
		strings.Contains(grant.URL, f.dir) || time.Until(grant.ExpiresAt) > grantTTL || time.Until(grant.ExpiresAt) < grantTTL-2*time.Second {
		t.Fatalf("metadata = %+v", grant)
	}
	meta := f.ts.do("GET", "/api/meta", "", f.auth)
	var metadata Meta
	if err := json.Unmarshal(meta.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.TempRoot != f.dir || len(metadata.TempRootAliases) != 0 {
		t.Fatalf("meta = %+v", metadata)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := f.ts.do(method, grant.URL, "", withHeader("Range", "bytes=16-31"), withHeader("Accept-Encoding", "gzip"))
		if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 16-31/131072" || w.Header().Get("Content-Encoding") != "" ||
			w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") != viewSecurity || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Fatalf("%s range = %d %v", method, w.Code, w.Header())
		}
		if method == "GET" && w.Body.String() != "0123456789abcdef" || method == "HEAD" && w.Body.Len() != 0 {
			t.Fatalf("%s body = %q", method, w.Body.String())
		}
		w = f.ts.do(method, grant.URL+"?download=1", "", withHeader("Accept-Encoding", "gzip"))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Encoding") != "" {
			t.Fatalf("download = %d %v", w.Code, w.Header())
		}
	}
	if w := f.ts.do("GET", grant.URL, "", withHeader("Range", "bytes=999999-")); w.Code != 416 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid range = %d %v", w.Code, w.Header())
	}
	parts := strings.Split(grant.URL, "/")
	key := parts[len(parts)-1]
	for _, tc := range []struct {
		name, method, target string
		opts                 []reqOpt
		status               int
	}{
		{"wrong host", "GET", grant.URL, []reqOpt{withHost("other.example")}, 401},
		{"wrong task", "GET", strings.Replace(grant.URL, f.task.ID, "other-task", 1), nil, 401},
		{"wrong grant", "GET", strings.Replace(grant.URL, grant.ID, "other-grant", 1), nil, 401},
		{"workdir key", "GET", strings.TrimSuffix(grant.URL, key) + fileKey(testToken, "127.0.0.1:8260", f.task.ID, time.Now().Add(time.Hour).Unix()), nil, 401},
		{"expired key", "GET", strings.TrimSuffix(grant.URL, key) + grantKey(testToken, "127.0.0.1:8260", f.task.ID, grant.ID, time.Now().Add(-time.Second).Unix()), nil, 401},
		{"no siblings", "GET", grant.URL + "/sibling.txt", nil, 401},
		{"no cookie revoke", "DELETE", base + "/" + grant.ID, nil, 401},
		{"key cannot revoke", "DELETE", grant.URL, nil, 401},
		{"key cannot read app", "GET", "/api/sessions/" + f.task.ID + "?key=" + key, nil, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.ts.do(tc.method, tc.target, "", tc.opts...)
			if w.Code != tc.status {
				t.Fatalf("status = %d %s", w.Code, w.Body.String())
			}
		})
	}
	entry := f.entry(grant.ID)
	if err := f.ts.m.Delete(f.task.ID); statusOf(err) != http.StatusConflict || f.entry(grant.ID) == nil {
		t.Fatalf("failed Task deletion affected its grant: %v", err)
	}
	for range 2 {
		w := f.ts.do("DELETE", base+"/"+grant.ID, "", f.auth)
		if w.Code != 204 {
			t.Fatalf("delete = %d", w.Code)
		}
	}
	if _, err := entry.file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("revoked FD = %v", err)
	}
	if w := f.ts.do("GET", grant.URL, ""); w.Code != 404 {
		t.Fatalf("revoked URL = %d", w.Code)
	}
}

func TestTempGrantPathPolicy(t *testing.T) {
	f := newGrantFixture(t)
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(name, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.runtime, "secret.txt"))
	for name, target := range map[string]string{"file-link": f.file, "runtime-link": f.runtime, "dir-link": f.dir} {
		if err := os.Symlink(target, filepath.Join(f.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	hard := filepath.Join(f.dir, "hard.txt")
	if err := os.Link(f.file, hard); err != nil {
		t.Fatal(err)
	}
	if w := f.post(t, f.file); w.Code != 404 {
		t.Fatalf("hardlink = %d", w.Code)
	}
	if err := os.Remove(hard); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(f.dir, "unreadable.txt")
	write(unreadable)
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(f.dir, "large.txt")
	write(large)
	if err := os.Truncate(large, maxGrantFileBytes+1); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(f.dir, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for candidate, status := range map[string]int{
		"relative.txt": 400, f.dir + "/../report.txt": 400, f.dir + "/./report.txt": 400, f.dir + "/x\x00": 400,
		"/" + strings.Repeat("x", maxGrantPathBytes): 400, f.dir + "/" + strings.Repeat("a/", maxGrantDepth) + "x": 400,
		f.dir: 404, filepath.Dir(f.dir) + "/outside.txt": 404, f.runtime + "/secret.txt": 404,
		f.dir + "/file-link": 404, f.dir + "/runtime-link/secret.txt": 404, f.dir + "/dir-link/report.txt": 404,
		unreadable: 404, large: 413, fifo: 404, f.dir + "/missing": 404,
	} {
		if w := f.post(t, candidate); w.Code != status {
			t.Errorf("%q = %d want %d: %s", candidate, w.Code, status, w.Body.String())
		}
	}
	if os.Getuid() == 0 {
		foreign := filepath.Join(f.dir, "foreign.txt")
		write(foreign)
		if err := os.Chown(foreign, 1, -1); err != nil {
			t.Fatal(err)
		}
		if w := f.post(t, foreign); w.Code != 404 {
			t.Fatalf("foreign UID = %d", w.Code)
		}
	}
	// The configured root alias is allowed; arbitrary aliases below it are not.
	alias := filepath.Join(t.TempDir(), "temp-alias")
	if err := os.Symlink(f.dir, alias); err != nil {
		t.Fatal(err)
	}
	roots, err := openGrantRoots(alias, f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer roots.close()
	if roots.canonical != f.dir || len(roots.aliases) != 1 || roots.aliases[0] != alias {
		t.Fatalf("roots = %+v", roots)
	}
	for _, candidate := range []string{f.file, filepath.Join(alias, "report.txt")} {
		rel, err := roots.relative(candidate)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := roots.checkedOpen(context.Background(), rel, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = opened.Close()
	}
	// Identity rejects a renamed runtime root; its original configured name
	// stays forbidden even if a different directory replaces it.
	_ = f.create(t, f.file)
	renamed := filepath.Join(f.dir, "moved-runtime")
	if err := os.Rename(f.runtime, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.runtime, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(f.runtime, "secret.txt"))
	for _, candidate := range []string{renamed + "/secret.txt", f.runtime + "/secret.txt"} {
		if w := f.post(t, candidate); w.Code != 404 {
			t.Fatalf("runtime substitution = %d", w.Code)
		}
	}
}

func TestTempGrantRejectsParentAndFileSwaps(t *testing.T) {
	for _, phase := range []string{"before-leaf", "before-rewalk"} {
		for _, replacement := range []string{"directory", "symlink"} {
			t.Run(phase+"/"+replacement, func(t *testing.T) {
				f := newGrantFixture(t)
				parent := filepath.Join(f.dir, "parent")
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				candidate := filepath.Join(parent, "secret.txt")
				if err := os.WriteFile(candidate, []byte("forbidden-original"), 0600); err != nil {
					t.Fatal(err)
				}
				hook := func() {
					moved := filepath.Join(f.runtime, "moved")
					if err := os.Rename(parent, moved); err != nil {
						t.Fatal(err)
					}
					if replacement == "symlink" {
						if err := os.Symlink(moved, parent); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := os.Mkdir(parent, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(candidate, []byte("replacement"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				g := f.ts.srv.grants
				if phase == "before-leaf" {
					g.beforeLeaf = hook
				} else {
					g.beforeRewalk = hook
				}
				w := f.post(t, candidate)
				if w.Code != 404 || strings.Contains(w.Body.String(), "forbidden-original") {
					t.Fatalf("racing parent = %d %s", w.Code, w.Body.String())
				}
				if len(g.entries) != 0 || len(g.ops) != 0 {
					t.Fatal("failed creation retained state")
				}
			})
		}
	}
	for _, change := range []string{"replace", "hardlink", "chmod", "grow", "symlink"} {
		t.Run(change, func(t *testing.T) {
			f := newGrantFixture(t)
			grant := f.create(t, f.file)
			entry := f.entry(grant.ID)
			switch change {
			case "replace":
				if err := os.Remove(f.file); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.file, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(f.file, f.file+"-link"); err != nil {
					t.Fatal(err)
				}
			case "chmod":
				if err := os.Chmod(f.file, 0); err != nil {
					t.Fatal(err)
				}
			case "grow":
				if err := os.Truncate(f.file, maxGrantFileBytes+1); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(f.file, f.file+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(f.file+"-moved", f.file); err != nil {
					t.Fatal(err)
				}
			}
			w := f.ts.do("GET", grant.URL, "")
			if w.Code != 404 && w.Code != 413 {
				t.Fatalf("changed object = %d", w.Code)
			}
			if f.entry(grant.ID) != nil {
				t.Fatal("invalid grant retained")
			}
			if _, err := entry.file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("invalidated FD = %v", err)
			}
		})
	}
}

func TestTempGrantConcurrentRangesAndLiveWrites(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.create(t, f.file)
	// In-place edits preserve the authorized object, unlike replacement.
	file, err := os.OpenFile(f.file, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt([]byte("live"), 0); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if w := f.ts.do("GET", grant.URL, "", withHeader("Range", "bytes=0-3")); w.Code != 206 || w.Body.String() != "live" {
		t.Fatalf("live write = %d %q", w.Code, w.Body.String())
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start := int64(16 + i*64)
			w := f.ts.do("GET", grant.URL, "", withHeader("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(start+31, 10)))
			if w.Code != 206 || w.Body.String() != strings.Repeat("0123456789abcdef", 2) {
				t.Errorf("reader %d = %d %q", i, w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
}

func TestTempGrantPublicationRacesAndCapacity(t *testing.T) {
	for _, action := range []string{"delete", "close", "cancel"} {
		t.Run(action, func(t *testing.T) {
			f := newGrantFixture(t)
			g := f.ts.srv.grants
			if action == "delete" {
				if _, err := f.ts.m.Archive(f.task.ID); err != nil {
					t.Fatal(err)
				}
			}
			paused := make(chan *os.File, 1)
			resume := make(chan struct{})
			done := make(chan error, 1)
			g.beforePublish = func(file *os.File) { paused <- file; <-resume }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _, err := g.create(ctx, "127.0.0.1:8260", f.task.ID, f.file); done <- err }()
			file := <-paused
			switch action {
			case "delete":
				if err := f.ts.m.Delete(f.task.ID); err != nil {
					t.Fatal(err)
				}
			case "close":
				f.ts.srv.Close()
			case "cancel":
				cancel()
			}
			close(resume)
			if err := <-done; err == nil {
				t.Fatal("late creation published")
			}
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("late descriptor = %v", err)
			}
			if len(g.entries) != 0 || len(g.ops) != 0 {
				t.Fatal("late creation retained state")
			}
			if action == "close" && len(f.ts.m.fileGrants) != 0 {
				t.Fatal("closed registry retained by Manager")
			}
		})
	}
	f := newGrantFixture(t)
	g := f.ts.srv.grants
	for range maxTaskGrants - 1 {
		f.create(t, f.file)
	}
	paused := make(chan struct{}, maxGrantOps)
	resume := make(chan struct{})
	done := make(chan error, maxGrantOps)
	g.beforePublish = func(*os.File) { paused <- struct{}{}; <-resume }
	for range maxGrantOps {
		go func() { _, err := g.create(context.Background(), "127.0.0.1:8260", f.task.ID, f.file); done <- err }()
	}
	for range maxGrantOps {
		<-paused
	}
	if _, err := g.create(context.Background(), "127.0.0.1:8260", f.task.ID, f.file); statusOf(err) != 429 {
		t.Fatalf("operation cap = %v", err)
	}
	close(resume)
	success := 0
	for range maxGrantOps {
		if err := <-done; err == nil {
			success++
		} else if statusOf(err) != 429 {
			t.Errorf("capacity race = %v", err)
		}
	}
	if success != 1 || len(g.entries) != maxTaskGrants || len(g.ops) != 0 {
		t.Fatalf("capacity race = %d successes, %d grants, %d ops", success, len(g.entries), len(g.ops))
	}
	g.beforePublish = nil
	for range 3 {
		task, _ := createSession(t, f.ts.m, f.ts.prov)
		other := f
		other.task = task
		for range maxTaskGrants {
			other.create(t, other.file)
		}
	}
	task, _ := createSession(t, f.ts.m, f.ts.prov)
	if _, err := g.create(context.Background(), "127.0.0.1:8260", task.ID, f.file); statusOf(err) != 429 || len(g.entries) != maxGrants {
		t.Fatalf("global cap = %v; %d entries", err, len(g.entries))
	}
}

func TestTempGrantReaderCancellationAndCleanup(t *testing.T) {
	for _, action := range []string{"revoke", "expire", "delete", "close", "request"} {
		t.Run(action, func(t *testing.T) {
			f := newGrantFixture(t)
			grant := f.create(t, f.file)
			entry := f.entry(grant.ID)
			g := f.ts.srv.grants
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opened, readCtx, release, err := g.open(ctx, "127.0.0.1:8260", f.task.ID, grant.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if entry.comparisons != 0 {
				t.Fatal("comparison lease did not end")
			}
			reader := grantReader{ctx: readCtx, reader: io.NewSectionReader(opened.File, 0, opened.Info.Size())}
			if n, err := reader.Read(make([]byte, 4)); n != 4 || err != nil {
				t.Fatalf("initial read = %d %v", n, err)
			}
			switch action {
			case "revoke":
				g.revoke("127.0.0.1:8260", f.task.ID, grant.ID)
			case "expire":
				g.mu.Lock()
				entry.timer.Reset(time.Millisecond)
				g.mu.Unlock()
				<-entry.ctx.Done()
			case "delete":
				if _, err := f.ts.m.Archive(f.task.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.ts.m.Delete(f.task.ID); err != nil {
					t.Fatal(err)
				}
			case "close":
				f.ts.srv.Close()
			case "request":
				cancel()
				<-readCtx.Done()
			}
			if n, err := reader.Read(make([]byte, 4)); n != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("read after cancellation = %d %v", n, err)
			}
			if _, err := reader.Seek(0, 0); !errors.Is(err, context.Canceled) {
				t.Fatalf("seek after cancellation = %v", err)
			}
			waitUntil(t, "reader descriptor closed before release", func() bool { _, err := opened.File.Stat(); return errors.Is(err, os.ErrClosed) })
			if action != "request" {
				if _, err := entry.file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("retained descriptor = %v", err)
				}
			}
		})
	}
	for _, action := range []string{"revoke", "expire"} {
		t.Run("before-first-read/"+action, func(t *testing.T) {
			f := newGrantFixture(t)
			grant := f.create(t, f.file)
			g := f.ts.srv.grants
			entry := f.entry(grant.ID)
			g.beforeRead = func() {
				if action == "revoke" {
					g.revoke("127.0.0.1:8260", f.task.ID, grant.ID)
				} else {
					g.mu.Lock()
					entry.timer.Reset(time.Millisecond)
					g.mu.Unlock()
					<-entry.ctx.Done()
				}
			}
			w := f.ts.do("GET", grant.URL, "")
			if w.Code != 404 || strings.Contains(w.Body.String(), "0123456789") {
				t.Fatalf("cancel before first read = %d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestTempGrantUnavailableRootsAndRedaction(t *testing.T) {
	f := newGrantFixture(t, func(temp, runtime string) (string, string) { return temp, runtime + "-absent" })
	if w := f.post(t, f.file); w.Code != 503 {
		t.Fatalf("missing protection = %d", w.Code)
	}
	w := f.ts.do("GET", "/api/meta", "", f.auth)
	if bytes.Contains(w.Body.Bytes(), []byte(`"temp_root"`)) {
		t.Fatalf("unavailable meta = %s", w.Body.String())
	}
	f.ts.srv.Close()
	if len(f.ts.m.fileGrants) != 0 {
		t.Fatal("registry leaked after protection failure")
	}
	secret := "1234567.secret-mac"
	for _, input := range []string{"/api/sessions/task/file-grants/grant/" + secret, "https://uam.example/api/sessions/task/file-grants/grant/" + secret + "?download=1"} {
		if got := redactGrantURL(input); strings.Contains(got, secret) || !strings.Contains(got, "redacted") {
			t.Fatalf("redaction = %q", got)
		}
		if got := redactHeaderValue("Referer", input); strings.Contains(got, secret) {
			t.Fatalf("Referer = %q", got)
		}
	}
	for range 3 {
		s, err := NewServer(ServerConfig{Manager: f.ts.m, Token: testToken})
		if err != nil {
			t.Fatal(err)
		}
		if len(f.ts.m.fileGrants) != 1 {
			t.Fatal("unexpected registry count")
		}
		s.Close()
		if len(f.ts.m.fileGrants) != 0 {
			t.Fatal("registry retained")
		}
	}
}

func TestTempGrantRefusesTempRootInsideRuntime(t *testing.T) {
	f := newGrantFixture(t, func(temp, runtime string) (string, string) {
		inside := filepath.Join(runtime, "temp")
		if err := os.Mkdir(inside, 0700); err != nil {
			t.Fatal(err)
		}
		return inside, runtime
	})
	inside := filepath.Join(f.runtime, "temp")
	file := filepath.Join(inside, "private.txt")
	if err := os.WriteFile(file, []byte("runtime-private"), 0600); err != nil {
		t.Fatal(err)
	}
	w := f.post(t, file)
	if w.Code != 503 || strings.Contains(w.Body.String(), "runtime-private") {
		t.Fatalf("nested configured temp root = %d %s", w.Code, w.Body.String())
	}
	meta := f.ts.do("GET", "/api/meta", "", f.auth)
	if strings.Contains(meta.Body.String(), `"temp_root"`) {
		t.Fatal("unsafe temp root advertised")
	}
}

func TestTempGrantPinsRuntimeBeforeFirstRequest(t *testing.T) {
	f := newGrantFixture(t)
	secret := filepath.Join(f.runtime, "private.txt")
	if err := os.WriteFile(secret, []byte("runtime-private"), 0600); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(f.dir, "renamed-runtime")
	if err := os.Rename(f.runtime, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.runtime, 0700); err != nil {
		t.Fatal(err)
	}
	w := f.post(t, filepath.Join(moved, "private.txt"))
	if w.Code != 404 {
		t.Fatalf("runtime renamed before first grant = %d", w.Code)
	}
}

func TestTempGrantRefusesMovedConfiguredTempRoot(t *testing.T) {
	for _, phase := range []string{"before-open", "before-rewalk", "symlink-replacement"} {
		t.Run(phase, func(t *testing.T) {
			f := newGrantFixture(t, func(temp, runtime string) (string, string) {
				child := filepath.Join(temp, "actual-temp")
				if err := os.Mkdir(child, 0700); err != nil {
					t.Fatal(err)
				}
				return child, runtime
			})
			temp := filepath.Join(f.dir, "actual-temp")
			file := filepath.Join(temp, "report.txt")
			if err := os.WriteFile(file, []byte("moved-under-runtime"), 0600); err != nil {
				t.Fatal(err)
			}
			grant := f.create(t, file)
			moved := filepath.Join(f.runtime, "moved-temp")
			move := func() {
				if err := os.Rename(temp, moved); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "before-rewalk" {
				f.ts.srv.grants.beforeRewalk = move
			} else {
				move()
			}
			if phase == "symlink-replacement" {
				if err := os.Symlink(moved, temp); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				if phase == "symlink-replacement" {
					_ = os.Remove(temp)
				}
				_ = os.Rename(moved, temp)
			}()
			w := f.ts.do("GET", grant.URL, "")
			if w.Code != 404 || strings.Contains(w.Body.String(), "moved-under-runtime") {
				t.Fatalf("moved configured temp root = %d", w.Code)
			}
			if f.entry(grant.ID) != nil {
				t.Fatal("moved-root grant retained")
			}
		})
	}
}

func TestTempGrantBoundsResponseToCheckedSize(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.create(t, f.file)
	f.ts.srv.grants.beforeRead = func() {
		if err := os.Truncate(f.file, maxGrantFileBytes+1); err != nil {
			t.Fatal(err)
		}
	}
	w := f.ts.do("GET", grant.URL, "")
	if w.Code != 200 || w.Body.Len() != int(grant.Size) || w.Header().Get("Content-Length") != strconv.FormatInt(grant.Size, 10) {
		t.Fatalf("post-validation growth = %d %d bytes %v", w.Code, w.Body.Len(), w.Header())
	}
	// The next read revalidates the now-oversized object and revokes it.
	f.ts.srv.grants.beforeRead = nil
	w = f.ts.do("GET", grant.URL, "")
	if w.Code != 413 || f.entry(grant.ID) != nil {
		t.Fatalf("grown object = %d", w.Code)
	}
}

type grantBlockedWriter struct {
	*httptest.ResponseRecorder
	started, resume chan struct{}
	once            sync.Once
}

func (w *grantBlockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.resume })
	return w.ResponseRecorder.Write(p)
}

func TestTempGrantClosesDescriptorsDuringBlockedWrite(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.create(t, f.file)
	g := f.ts.srv.grants
	entry := f.entry(grant.ID)
	file, ctx, release, err := g.open(context.Background(), "127.0.0.1:8260", f.task.ID, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := &grantBlockedWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer release()
		http.ServeContent(w, httptest.NewRequest("GET", grant.URL, nil), file.Info.Name(), file.Info.ModTime(), grantReader{ctx: ctx, reader: io.NewSectionReader(file.File, 0, file.Info.Size())})
	}()
	<-w.started
	g.revoke("127.0.0.1:8260", f.task.ID, grant.ID)
	waitUntil(t, "blocked reader descriptor closes", func() bool { _, err := file.File.Stat(); return errors.Is(err, os.ErrClosed) })
	if _, err := entry.file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("retained file during blocked write = %v", err)
	}
	close(w.resume)
	<-done
	if w.Body.Len() >= int(file.Info.Size()) {
		t.Fatalf("revoked response kept reading: %d bytes", w.Body.Len())
	}
	if len(g.ops) != 0 {
		t.Fatal("serving operation leaked")
	}
}

func TestTempGrantCancellationDoesNotRevokeOtherReaders(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.create(t, f.file)
	g := f.ts.srv.grants
	ctx, cancel := context.WithCancel(context.Background())
	g.beforeLeaf = cancel
	if _, _, release, err := g.open(ctx, "127.0.0.1:8260", f.task.ID, grant.ID); err == nil {
		release()
		t.Fatal("canceled validation succeeded")
	}
	g.beforeLeaf = nil
	if f.entry(grant.ID) == nil {
		t.Fatal("request cancellation revoked the grant")
	}
	if w := f.ts.do("GET", grant.URL, "", withHeader("Range", "bytes=0-3")); w.Code != 206 || w.Body.String() != "0123" {
		t.Fatalf("other reader = %d %s", w.Code, w.Body.String())
	}
	var releases []func()
	for range maxGrantOps {
		_, _, release, err := g.open(context.Background(), "127.0.0.1:8260", f.task.ID, grant.ID)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := g.create(context.Background(), "127.0.0.1:8260", f.task.ID, f.file); statusOf(err) != 429 {
		t.Fatalf("readers do not count toward operation cap: %v", err)
	}
	for _, release := range releases {
		release()
	}
	if len(g.ops) != 0 {
		t.Fatal("reader operations retained")
	}
}

func TestTempGrantIdleExpiryAndRootCleanup(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.create(t, f.file)
	entry := f.entry(grant.ID)
	g := f.ts.srv.grants
	g.mu.Lock()
	entry.timer.Reset(time.Millisecond)
	g.mu.Unlock()
	<-entry.ctx.Done()
	waitUntil(t, "idle grant descriptor closed", func() bool { _, err := entry.file.Stat(); return errors.Is(err, os.ErrClosed) })
	if f.entry(grant.ID) != nil {
		t.Fatal("idle expired entry retained")
	}
	roots, err := g.rootsForUse()
	if err != nil {
		t.Fatal(err)
	}
	f.ts.srv.Close()
	for _, root := range []*os.Root{roots.temp, roots.runtime} {
		if file, err := root.Open("."); err == nil {
			_ = file.Close()
			t.Fatal("root descriptor survived Close")
		}
	}
}

func TestTempGrantOpenFailureDescriptorBound(t *testing.T) {
	// Linux exposes a stable descriptor count without opening a listener or
	// requiring platform-specific test helpers. Darwin exercises the same
	// descriptor ownership through the portable close assertions above.
	count := func() (int, error) { entries, err := os.ReadDir("/proc/self/fd"); return len(entries), err }
	if _, err := count(); err != nil {
		t.Skip("descriptor inventory unavailable")
	}
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	roots, err := openGrantRoots(dir, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer roots.close()
	deep := strings.Repeat("d/", maxGrantDepth-1)
	if err := os.MkdirAll(filepath.Join(dir, deep), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, deep, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// Warm the runtime poller before measuring descriptors.
	file, err := roots.checkedOpen(context.Background(), deep+"file", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	before, err := count()
	if err != nil {
		t.Fatal(err)
	}
	peak := before
	hook := func() {
		n, err := count()
		if err != nil {
			t.Fatal(err)
		}
		peak = max(peak, n)
	}
	file, err = roots.checkedOpen(context.Background(), deep+"file", hook, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	for range 16 {
		ctx, cancel := context.WithCancel(context.Background())
		if file, err := roots.checkedOpen(ctx, deep+"file", cancel, nil); err == nil {
			_ = file.Close()
			t.Fatal("canceled traversal returned a file")
		}
		cancel()
		if file, err := roots.checkedOpen(context.Background(), deep+"missing", nil, nil); err == nil {
			_ = file.Close()
			t.Fatal("missing file opened")
		}
	}
	after, err := count()
	if err != nil {
		t.Fatal(err)
	}
	if after != before || peak > before+3 {
		t.Fatalf("descriptor count before=%d peak=%d after=%d", before, peak, after)
	}
}

func TestTempGrantLogsNeverContainKeys(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.create(t, f.file)
	key := grant.URL[strings.LastIndex(grant.URL, "/")+1:]
	var buf bytes.Buffer
	previous := uamlog.L()
	uamlog.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer uamlog.SetLogger(previous)
	f.ts.srv.headerLog = true
	for _, suffix := range []string{"", "%"} {
		f.ts.do("HEAD", grant.URL, "", withHeader("Referer", "https://uam.example"+grant.URL+suffix))
	}
	f.ts.do("GET", grant.URL+"invalid", "", withHeader("Referer", "https://uam.example"+grant.URL))
	if raw := buf.String(); strings.Contains(raw, key) || !strings.Contains(raw, "web request") {
		t.Fatalf("grant credential was not redacted from request/header logs")
	}
}
