package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publishedTree(t *testing.T, ts *testServer, taskID, text string, version int) string {
	t.Helper()
	match := artifactIDText.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("no artifact id in %q", text)
	}
	return artifactVersionDir(ts.m.artifactRoot(taskID), match[1], version)
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A page's module scripts, with their relative JS, TS, JSON and CSS imports,
// become one classic script beside it; fonts become data: URLs; the
// original files stay.
func TestPublishBundlesModulePages(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	page := `<!doctype html><link rel="stylesheet" href="style.css" media="screen"><link rel="stylesheet" href="plain.css">` +
		`<script type="module" src="./main.js?v=2"></script><p id="out"></p>` +
		`<script type="module">import { label } from './lib/util'; document.title = label</script>`
	writeTree(t, dir, map[string]string{
		"site/index.html":       page,
		"site/main.js":          "import { label } from './lib/util.ts'; import data from './data.json'; import './theme.css'; document.getElementById('out').textContent = label + data.word",
		"site/lib/util.ts":      "export const label: string = 'from-ts-'",
		"site/data.json":        `{"word":"from-json"}`,
		"site/theme.css":        "@font-face { font-family: T; src: url(fonts/t.woff2) } body { background: url(img/bg.png) }",
		"site/style.css":        "@import './more.css'; h1 { color: red }",
		"site/more.css":         "@font-face { font-family: M; src: url('fonts/m.ttf?v=1#x') } p { background: url(img/bg.png#a) }",
		"site/plain.css":        "p { color: blue }",
		"site/fonts/t.woff2":    "\x00woff2",
		"site/fonts/m.ttf":      "\x00ttf",
		"site/img/bg.png":       "png",
		"site/about/index.html": `<script type="module" src="../main.js"></script>`,
	})
	res := publishCall(t, conv, "c1", `{"path":"site","title":"Modules"}`)
	if res.Failed || !strings.Contains(res.Text, "Bundled module scripts") || strings.Contains(res.Text, "Not bundled") {
		t.Fatalf("publish = %+v", res)
	}
	v := publishedTree(t, ts, sum.ID, res.Text, 1)
	got := readFileT(t, filepath.Join(v, "index.html"))
	if strings.Contains(got, `type="module"`) || !strings.Contains(got, `<link rel="stylesheet" href="index.uam-bundle.css"><script defer src="index.uam-bundle.js"></script><p id="out"></p>`) ||
		!strings.Contains(got, `<link rel="stylesheet" href="index.uam-bundle-1.css" media="screen"><link rel="stylesheet" href="plain.css">`) {
		t.Fatalf("index.html = %s", got)
	}
	js := readFileT(t, filepath.Join(v, "index.uam-bundle.js"))
	if strings.Contains(js, "import ") || !strings.Contains(js, "from-json") || !strings.Contains(js, "from-ts-") || !strings.Contains(js, "document.title = label") {
		t.Fatalf("bundle = %s", js)
	}
	css := readFileT(t, filepath.Join(v, "index.uam-bundle.css"))
	if !strings.Contains(css, "url(data:font/woff2") || !strings.Contains(css, "img/bg.png") {
		t.Fatalf("bundle css = %s", css)
	}
	sheet := readFileT(t, filepath.Join(v, "index.uam-bundle-1.css"))
	if !strings.Contains(sheet, "url(data:font/ttf") || !strings.Contains(sheet, "img/bg.png#a") || !strings.Contains(sheet, "color: red") {
		t.Fatalf("sheet = %s", sheet)
	}
	if readFileT(t, filepath.Join(v, "index.uam-original.html")) != page || readFileT(t, filepath.Join(v, "style.css")) != "@import './more.css'; h1 { color: red }" {
		t.Fatal("the original files changed")
	}
	if about := readFileT(t, filepath.Join(v, "about", "index.html")); about != `<link rel="stylesheet" href="index.uam-bundle.css"><script defer src="index.uam-bundle.js"></script>` {
		t.Fatalf("about = %s", about)
	}
	if css := readFileT(t, filepath.Join(v, "about", "index.uam-bundle.css")); !strings.Contains(css, "url(../img/bg.png)") {
		t.Fatalf("about css = %s", css)
	}
	list, _ := ts.m.Artifacts(sum.ID)
	// 12 files, then 3 bundles and the original beside index.html, 2 and the original beside about/index.html.
	if files := list[0].Versions[0].Files; files != 19 {
		t.Fatalf("files = %d", files)
	}
}

// A page that cannot be bundled is published as it is, with a warning, and
// bundling never reads outside the snapshot.
func TestPublishLeavesUnbundlablePagesWithAWarning(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	pages := map[string]string{
		"site/bare.html":           `<script type="module">import React from 'react'</script>`,
		"site/up.html":             `<script type="module" src="../secret.js"></script>`,
		"site/sub/deep.html":       `<script type="module">import s from '../../secret.js'</script>`,
		"site/root.html":           `<script type="module" src="/main.js"></script>`,
		"site/broken.html":         `<script type="module" src="broken.js"></script>`,
		"site/image.html":          `<script type="module">import logo from './logo.png'</script>`,
		"site/plain.html":          `<script type="module">document.title = 'inline'</script><link rel="stylesheet" href="../secret.css">`,
		"site/index.html":          `<script src="classic.js"></script>`,
		"site/index.uam-bundle.js": "taken",
		"site/clash.html":          `<script type="module" src="main.js"></script>`,
		"site/clash.uam-bundle.js": "taken",
	}
	writeTree(t, dir, pages)
	writeTree(t, dir, map[string]string{"secret.js": "export default 'outside secret'", "secret.css": "@font-face{src:url(s.woff2)}", "s.woff2": "outside font",
		"site/broken.js": "export const = ;", "site/main.js": "document.title = 'main'", "site/logo.png": "png", "site/classic.js": ""})
	res := publishCall(t, conv, "c1", `{"path":"site","title":"Broken"}`)
	if res.Failed || strings.Contains(res.Text, "Bundled") {
		t.Fatalf("publish = %+v", res)
	}
	for _, want := range []string{
		`bare.html: its module scripts are not bundled: bare.html (inline module 1): "react" is not a relative import`,
		`up.html: its module scripts are not bundled: "../secret.js" is not a relative path to a file in the published folder`,
		`sub/deep.html: its module scripts are not bundled: sub/deep.html (inline module 1): "../../secret.js" is outside the published folder`,
		`root.html: its module scripts are not bundled: "/main.js" is not a relative path`,
		`broken.html: its module scripts are not bundled: broken.js:`,
		`image.html: its module scripts are not bundled: image.html (inline module 1): logo.png is not bundled`,
		`clash.html: it is not bundled: clash.uam-bundle.js already exists`,
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("result lacks %q: %s", want, res.Text)
		}
	}
	v := publishedTree(t, ts, sum.ID, res.Text, 1)
	for name, text := range pages {
		if got := readFileT(t, filepath.Join(v, strings.TrimPrefix(name, "site/"))); got != text {
			t.Errorf("%s = %q", name, got)
		}
	}
	entries, _ := os.ReadDir(v)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".uam-original") || strings.Contains(e.Name(), "-1.css") {
			t.Errorf("left %s", e.Name())
		}
	}
}

// Bundles count toward the snapshot's caps; a page whose bundle would pass
// them is published as it is.
func TestPublishBundlesWithinTheSnapshotCaps(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "")
	files := map[string]string{"site/index.html": `<script type="module" src="main.js"></script>`, "site/main.js": "document.title = 'x'"}
	for i := range maxArtifactFiles - 2 {
		files[fmt.Sprintf("site/f%d.txt", i)] = ""
	}
	writeTree(t, dir, files)
	res := publishCall(t, conv, "c1", `{"path":"site","title":"Full"}`)
	if res.Failed || !strings.Contains(res.Text, "index.html: it is not bundled: its bundle would pass the snapshot's limits") {
		t.Fatalf("publish = %+v", res)
	}
	v := publishedTree(t, ts, sum.ID, res.Text, 1)
	if _, err := os.Stat(filepath.Join(v, "index.uam-bundle.js")); !os.IsNotExist(err) {
		t.Fatalf("bundle written: %v", err)
	}
}
