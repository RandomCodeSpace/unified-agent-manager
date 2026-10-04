package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

type connectedFileFixture struct {
	proxy  *connectedProxyFixture
	remote *testServer
	task   SessionSummary
	temp   string
}

func newConnectedFileFixture(t *testing.T) connectedFileFixture {
	t.Helper()
	tempRoot := t.TempDir()
	runtime := filepath.Join(tempRoot, "runtime")
	if err := os.Mkdir(runtime, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tempRoot)
	t.Setenv("UAM_SESSION_DIR", runtime)
	remote := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	task, _ := createSession(t, remote.m, remote.prov)
	if err := os.Mkdir(filepath.Join(task.Workdir, "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"out/index.html": `<link rel="stylesheet" href="style.css"><script src="app.js"></script><img src="../image.svg">`,
		"out/style.css":  "body { color: red; }", "out/app.js": "document.body.dataset.ready = 'yes';",
		"image.svg": `<svg xmlns="http://www.w3.org/2000/svg"></svg>`, "bytes.txt": "0123456789abcdefghijklmnopqrstuvwxyz",
	} {
		if err := os.WriteFile(filepath.Join(task.Workdir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	temp := filepath.Join(tempRoot, "temporary.html")
	if err := os.WriteFile(temp, []byte("<p>temporary report</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/files/view/{path...}", remote.srv.handleRemoteViewFile)
	mux.HandleFunc("POST /api/sessions/{id}/file-grants", remote.srv.handleCreateGrant)
	mux.HandleFunc("DELETE /api/sessions/{id}/file-grants/{grant_id}", remote.srv.handleDeleteGrant)
	mux.HandleFunc(grantKeyRoute, remote.srv.handleRemoteGrantFile)
	mux.HandleFunc("POST /api/sessions/{id}/attachments", remote.srv.handleUpload)
	mux.HandleFunc("GET /api/sessions/{id}/attachments/{attachment_id}", remote.srv.handleAttachment)
	proxy := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		local := r.Clone(r.Context())
		local.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/federation/workload")
		local.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/api/federation/workload")
		mux.ServeHTTP(w, local)
	})
	return connectedFileFixture{proxy: proxy, remote: remote, task: task, temp: temp}
}

func (f connectedFileFixture) readGrant(t *testing.T, method, target string, cookies []*http.Cookie, headers http.Header) *http.Response {
	t.Helper()
	r, err := http.NewRequest(method, f.proxy.server.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	response, err := f.proxy.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestConnectedFilesPreviewUsesHomeGrantAndCompanionCookie(t *testing.T) {
	f := newConnectedFileFixture(t)
	initial := f.proxy.request(t, "GET", "/api/sessions/"+f.task.ID+"/files/view/out/index.html", "", nil)
	if initial.StatusCode != http.StatusFound {
		t.Fatalf("preview bootstrap=%d %s", initial.StatusCode, connectedBody(t, initial))
	}
	location, cookies := initial.Header.Get("Location"), initial.Cookies()
	if !strings.HasPrefix(location, "/api/connected/"+f.proxy.target.ID+"/files/"+f.task.ID+"/") || len(cookies) != 1 ||
		!cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteNoneMode {
		t.Fatalf("grant bootstrap location=%q cookies=%+v", location, cookies)
	}
	if response := f.readGrant(t, "GET", location, nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("HTTPS preview without companion=%d", response.StatusCode)
	}
	response := f.readGrant(t, "GET", location, cookies, nil)
	body := connectedBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `href="style.css"`) || response.Header.Get("Content-Security-Policy") != viewSecurity {
		t.Fatalf("sandboxed preview status=%d policy=%q body=%q", response.StatusCode, response.Header.Get("Content-Security-Policy"), body)
	}
	base, _ := url.Parse(location)
	for _, relative := range []string{"style.css", "app.js", "../image.svg"} {
		u, _ := url.Parse(relative)
		target := base.ResolveReference(u).String()
		nested := f.readGrant(t, "GET", target, cookies, nil)
		if nested.StatusCode != http.StatusOK || nested.Header.Get("Content-Security-Policy") != viewSecurity {
			t.Errorf("nested %s status=%d policy=%q", relative, nested.StatusCode, nested.Header.Get("Content-Security-Policy"))
		}
	}
	// The same signed URL cannot select a different task, instance or host.
	wrongTask := strings.Replace(location, f.task.ID, "another-task", 1)
	if response := f.readGrant(t, "GET", wrongTask, cookies, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("grant crossed task boundary: %d", response.StatusCode)
	}
	next := f.proxy.target
	next.Label = "changed connection generation"
	if _, err := f.proxy.home.srv.connections.put(next, f.proxy.target.Generation); err != nil {
		t.Fatal(err)
	}
	if response := f.readGrant(t, "GET", location, cookies, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("old generation grant remained valid: %d", response.StatusCode)
	}
}

func TestConnectedFilesRangesHeadDownloadAndConfinement(t *testing.T) {
	f := newConnectedFileFixture(t)
	initial := f.proxy.request(t, "GET", "/api/sessions/"+f.task.ID+"/files/view/bytes.txt?download=1", "", nil)
	location, cookies := initial.Header.Get("Location"), initial.Cookies()
	if !strings.HasSuffix(location, "?download=1") {
		t.Fatalf("download request lost: %q", location)
	}
	response := f.readGrant(t, "GET", location, cookies, http.Header{"Range": {"bytes=2-5"}})
	if response.StatusCode != http.StatusPartialContent || connectedBody(t, response) != "2345" ||
		response.Header.Get("Content-Range") != "bytes 2-5/36" || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("range contract status=%d headers=%v", response.StatusCode, response.Header)
	}
	head := f.readGrant(t, "HEAD", location, cookies, nil)
	if head.StatusCode != http.StatusOK || head.ContentLength != 36 || connectedBody(t, head) != "" {
		t.Fatalf("HEAD status=%d length=%d", head.StatusCode, head.ContentLength)
	}
	conditional := f.readGrant(t, "GET", location, cookies, http.Header{"If-None-Match": {head.Header.Get("ETag")}})
	if conditional.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional read=%d etag=%q", conditional.StatusCode, head.Header.Get("ETag"))
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("must stay outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.task.Workdir, "outside.txt")); err != nil {
		t.Fatal(err)
	}
	for _, filePath := range []string{"outside.txt", "%2e%2e/secret.txt"} {
		bootstrap := f.proxy.request(t, "GET", "/api/sessions/"+f.task.ID+"/files/view/"+filePath, "", nil)
		if bootstrap.StatusCode == http.StatusFound {
			bootstrap = f.readGrant(t, "GET", bootstrap.Header.Get("Location"), bootstrap.Cookies(), nil)
		}
		if bootstrap.StatusCode == http.StatusOK || strings.Contains(connectedBody(t, bootstrap), "must stay outside") {
			t.Errorf("confined file escaped through %q", filePath)
		}
	}
}

func TestConnectedFilesTemporaryGrantRevocationAndURLRewrite(t *testing.T) {
	f := newConnectedFileFixture(t)
	body, _ := json.Marshal(map[string]string{"path": f.temp})
	response := f.proxy.request(t, "POST", "/api/sessions/"+f.task.ID+"/file-grants", string(body), nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("grant creation=%d %s", response.StatusCode, connectedBody(t, response))
	}
	var grant fileGrant
	if err := json.Unmarshal([]byte(connectedBody(t, response)), &grant); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(grant.URL, "/api/connected/"+f.proxy.target.ID+"/grants/"+f.task.ID+"/") || strings.Contains(grant.URL, f.proxy.upstream.URL) {
		t.Fatalf("remote URL escaped into browser: %q", grant.URL)
	}
	if result := f.readGrant(t, "GET", grant.URL, nil, nil); result.StatusCode != http.StatusForbidden {
		t.Fatalf("temporary file without companion=%d", result.StatusCode)
	}
	result := f.readGrant(t, "GET", grant.URL, response.Cookies(), nil)
	if result.StatusCode != http.StatusOK || connectedBody(t, result) != "<p>temporary report</p>" || result.Header.Get("Content-Security-Policy") != viewSecurity {
		t.Fatalf("temporary file status=%d policy=%q", result.StatusCode, result.Header.Get("Content-Security-Policy"))
	}
	revoke := f.proxy.request(t, "DELETE", "/api/sessions/"+f.task.ID+"/file-grants/"+grant.ID, "", nil)
	if revoke.StatusCode != http.StatusNoContent {
		t.Fatalf("temporary revoke=%d", revoke.StatusCode)
	}
	if after := f.readGrant(t, "GET", grant.URL, response.Cookies(), nil); after.StatusCode == http.StatusOK {
		t.Fatal("target-revoked temporary grant still reads")
	}
}

func TestConnectedFilesUploadAndImageDownload(t *testing.T) {
	f := newConnectedFileFixture(t)
	// A GIF signature is sufficient for the existing attachment sniff contract.
	data := "GIF89a" + strings.Repeat("\x00", 20)
	upload := f.proxy.request(t, "POST", "/api/sessions/"+f.task.ID+"/attachments?name=image.gif", data, http.Header{"Content-Type": {"application/octet-stream"}})
	if upload.StatusCode != http.StatusCreated {
		t.Fatalf("upload=%d %s", upload.StatusCode, connectedBody(t, upload))
	}
	var attachment struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(connectedBody(t, upload)), &attachment); err != nil || attachment.ID == "" {
		t.Fatalf("invalid attachment: %v %+v", err, attachment)
	}
	image := f.proxy.request(t, "GET", "/api/sessions/"+f.task.ID+"/attachments/"+attachment.ID, "", nil)
	if image.StatusCode != 200 || image.Header.Get("Content-Type") != "image/gif" || connectedBody(t, image) != data {
		t.Fatalf("image contract=%d %v", image.StatusCode, image.Header)
	}
}

func TestConnectedFileKeysRedactedInURLs(t *testing.T) {
	secret := "123456.very-secret-capability"
	for _, value := range []string{
		"/api/connected/connection/files/task/" + secret + "/index.html",
		"https://home.example/api/connected/connection/grants/task/grant/" + secret,
		"https://home.example/%61pi/%63onnected/connection/files/task/" + secret + "/index.html",
		"http://[invalid/api/connected/connection/files/task/" + secret,
		"https://home.example/%61pi/%63onnected/connection/files/task/" + secret + "/%",
		"https://home.example/%61pi%2F%63onnected%2Fconnection%2Ffiles%2Ftask%2F" + secret + "%",
	} {
		redacted, recognized := redactConnectedKeyURL(value)
		if !recognized || strings.Contains(redacted, secret) || !strings.Contains(redacted, "redacted") {
			t.Errorf("capability leak: recognized=%v value=%q", recognized, redacted)
		}
	}
	if _, recognized := redactConnectedKeyURL("/api/connected/connection/api/sessions?uam_generation=" + strconv.Itoa(1)); recognized {
		t.Fatal("ordinary connected URL mistaken for a file capability")
	}
	for _, value := range []string{"ordinary%header", "v\r\n{\"msg\":\"web request\"}\n", "connected but not a URL%"} {
		if redacted, recognized := redactConnectedKeyURL(value); recognized {
			t.Errorf("ordinary malformed header changed: %q => %q", value, redacted)
		}
	}
}

func TestConnectedFileKeysNeverEnterRequestOrHeaderLogs(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	var output bytes.Buffer
	previous := uamlog.L()
	uamlog.SetLogger(slog.New(slog.NewJSONHandler(&output, nil)))
	defer uamlog.SetLogger(previous)
	ts.srv.headerLog = true
	secret := "123456.connected-capability-secret"
	for _, target := range []string{
		"/api/connected/connection/files/task/" + secret + "/index.html",
		"/api/connected/connection/grants/task/grant/" + secret,
	} {
		for _, suffix := range []string{"", "%"} {
			ts.do("GET", target, "", withHeader("Referer", "https://home.example"+target+suffix), withHeader("X-Preview", "https://home.example"+target+suffix))
			encoded := strings.ReplaceAll(target, "/connected/", "/%63onnected/")
			ts.do("GET", target, "", withHeader("X-Preview", "https://home.example"+encoded+suffix))
		}
	}
	if raw := output.String(); strings.Contains(raw, secret) || !strings.Contains(raw, "web request") {
		t.Fatal("connected file authorization leaked into request or URL-valued header logs")
	}
}
