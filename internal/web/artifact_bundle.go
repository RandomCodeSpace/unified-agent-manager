package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	xhtml "golang.org/x/net/html"
)

// A published page runs with an opaque origin, so the browser refuses its
// module scripts and their imports, and the fonts its style sheets name:
// those loads need CORS (docs/web.md "Published pages"). After a publish
// copies its snapshot, bundlePages bundles each HTML file's module scripts
// into one classic script and inlines the fonts of its style sheets as
// data: URLs, with esbuild reading only the snapshot's own files. The
// original files stay in the snapshot; a page that cannot be bundled is
// published as it is, with a warning for the agent.

const (
	bundleNamespace = "snapshot"
	bundleSuffix    = ".uam-bundle"
	// maxBundleInput bounds the bytes one publish's bundles read, and so the
	// memory esbuild uses for them.
	maxBundleInput = 16 << 20
	// maxInlineFont is the largest font a style sheet inlines.
	maxInlineFont = 2 << 20
	bundleTimeout = 20 * time.Second
)

var bundleLoaders = map[string]api.Loader{
	".js": api.LoaderJS, ".mjs": api.LoaderJS, ".cjs": api.LoaderJS, ".jsx": api.LoaderJSX,
	".ts": api.LoaderTS, ".mts": api.LoaderTS, ".cts": api.LoaderTS, ".tsx": api.LoaderTSX,
	".json": api.LoaderJSON, ".css": api.LoaderCSS,
	".woff": api.LoaderDataURL, ".woff2": api.LoaderDataURL, ".ttf": api.LoaderDataURL, ".otf": api.LoaderDataURL,
}

// moduleExts are the extensions an import without one is tried with.
var moduleExts = []string{".js", ".ts", ".tsx", ".jsx", ".mjs"}

type bundler struct {
	ctx      context.Context
	root     *os.Root
	snap     *snapshotter
	read     int64
	bundled  []string
	warnings []string
}

// bundleBuild is one esbuild run for a page: entry is its entry module, and
// virtual holds the modules uam made up (the entry importing the page's
// module scripts, and its inline ones), by path.
type bundleBuild struct {
	dir     string
	entry   string
	virtual map[string]string
	files   int
	fonts   int
}

// bundlePages bundles the HTML files of the snapshot in dir and returns
// what it bundled and why it left pages as they were. It never fails the
// publish.
func bundlePages(ctx context.Context, dir string, snap *snapshotter) (bundled, warnings []string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = root.Close() }()
	var pages []string
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && isHTMLName(p) {
			pages = append(pages, p)
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(ctx, bundleTimeout)
	defer cancel()
	b := &bundler{ctx: ctx, root: root, snap: snap}
	for _, p := range pages {
		if ctx.Err() != nil {
			b.warnings = append(b.warnings, "bundling stopped after "+bundleTimeout.String()+"; the remaining pages are published as they are")
			break
		}
		b.page(p)
	}
	return b.bundled, b.warnings
}

func isHTMLName(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".html" || ext == ".htm"
}

type pageTag struct {
	start, end int
	attrs      []xhtml.Attribute
	text       string
}

func tagAttr(attrs []xhtml.Attribute, key string) (string, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// scanPage finds a page's module scripts and style sheet links, with their
// byte spans in doc.
func scanPage(doc string) (modules, styles []pageTag) {
	z := xhtml.NewTokenizer(strings.NewReader(doc))
	var open *pageTag
	for offset := 0; ; {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return modules, styles
		}
		start := offset
		offset += len(z.Raw())
		name, hasAttr := z.TagName()
		var attrs []xhtml.Attribute
		for hasAttr {
			var k, v []byte
			k, v, hasAttr = z.TagAttr()
			attrs = append(attrs, xhtml.Attribute{Key: string(k), Val: string(v)})
		}
		switch tag := string(name); {
		case open != nil && tt == xhtml.TextToken:
			open.text += doc[start:offset]
		case open != nil && tt == xhtml.EndTagToken && tag == "script":
			open.end = offset
			modules = append(modules, *open)
			open = nil
		case tt == xhtml.StartTagToken && tag == "script":
			if kind, _ := tagAttr(attrs, "type"); strings.EqualFold(strings.TrimSpace(kind), "module") {
				open = &pageTag{start: start, attrs: attrs}
			}
		case (tt == xhtml.StartTagToken || tt == xhtml.SelfClosingTagToken) && tag == "link":
			rel, _ := tagAttr(attrs, "rel")
			if _, ok := tagAttr(attrs, "href"); ok && slices.ContainsFunc(strings.Fields(rel), func(r string) bool { return strings.EqualFold(r, "stylesheet") }) {
				styles = append(styles, pageTag{start: start, end: offset, attrs: attrs})
			}
		}
	}
}

// snapshotRef returns the snapshot path that ref, a URL relative to the
// file from, names, and whether it is one inside the snapshot.
func snapshotRef(from, ref string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	p := path.Join(path.Dir(from), u.Path)
	return p, filepath.IsLocal(p)
}

type pageEdit struct {
	start, end int
	text       string
}

type bundleFile struct {
	name string
	data []byte
}

func (b *bundler) warn(page, text string) {
	b.warnings = append(b.warnings, page+": "+text)
}

// page bundles the HTML file p: its module scripts into one deferred classic
// script, and each linked style sheet that names fonts into a copy with them
// inlined.
func (b *bundler) page(p string) {
	data, err := b.root.ReadFile(p)
	if err != nil {
		return
	}
	doc := string(data)
	modules, styles := scanPage(doc)
	dir, stem := path.Dir(p), strings.TrimSuffix(path.Base(p), path.Ext(p))
	var edits []pageEdit
	var outs []bundleFile
	if len(modules) > 0 {
		e, o, err := b.modules(p, dir, stem, modules)
		if err != nil {
			b.warn(p, "its module scripts are not bundled: "+err.Error())
			return
		}
		edits, outs = append(edits, e...), append(outs, o...)
	}
	for i, s := range styles {
		e, o := b.style(p, dir, fmt.Sprintf("%s%s-%d.css", stem, bundleSuffix, i+1), s)
		if o != nil {
			edits, outs = append(edits, e), append(outs, *o)
		}
	}
	if len(outs) == 0 {
		return
	}
	slices.SortFunc(edits, func(a, b pageEdit) int { return a.start - b.start })
	var page strings.Builder
	at := 0
	for _, e := range edits {
		page.WriteString(doc[at:e.start])
		page.WriteString(e.text)
		at = e.end
	}
	page.WriteString(doc[at:])
	outs = append(outs, bundleFile{name: path.Join(dir, stem+".uam-original"+path.Ext(p)), data: data})
	if err := b.commit(p, []byte(page.String()), outs); err != nil {
		b.warn(p, "it is not bundled: "+err.Error())
		return
	}
	names := make([]string, 0, len(outs))
	for _, o := range outs[:len(outs)-1] {
		names = append(names, o.name)
	}
	b.bundled = append(b.bundled, p+" ("+strings.Join(names, ", ")+")")
}

// modules bundles the page's module scripts, in their order, into one
// script. A page whose only module scripts are inline ones importing
// nothing already runs, and is left alone.
func (b *bundler) modules(p, dir, stem string, modules []pageTag) ([]pageEdit, []bundleFile, error) {
	build := &bundleBuild{dir: dir, entry: p + " (module scripts)", virtual: map[string]string{}}
	var entry strings.Builder
	async := false
	for i, m := range modules {
		spec := fmt.Sprintf("%s (inline module %d)", p, i+1)
		if src, ok := tagAttr(m.attrs, "src"); ok {
			var inside bool
			if spec, inside = snapshotRef(p, src); !inside {
				return nil, nil, fmt.Errorf("%q is not a relative path to a file in the published folder", src)
			}
		} else {
			build.virtual[spec] = m.text
		}
		_, a := tagAttr(m.attrs, "async")
		async = async || a
		quoted, _ := json.Marshal(spec)
		fmt.Fprintf(&entry, "import %s;\n", quoted)
	}
	build.virtual[build.entry] = entry.String()
	res, err := b.build(build)
	if err != nil {
		return nil, nil, err
	}
	if build.files == 0 {
		return nil, nil, nil
	}
	var outs []bundleFile
	tags := ""
	for _, f := range res.OutputFiles {
		ext := path.Ext(f.Path)
		name := path.Join(dir, stem+bundleSuffix+ext)
		ref := html.EscapeString((&url.URL{Path: path.Base(name)}).String())
		switch ext {
		case ".css":
			tags = `<link rel="stylesheet" href="` + ref + `">` + tags
		case ".js":
			mode := "defer"
			if async {
				mode = "async"
			}
			tags += `<script ` + mode + ` src="` + ref + `"></script>`
		default:
			continue
		}
		outs = append(outs, bundleFile{name: name, data: f.Contents})
	}
	edits := []pageEdit{{start: modules[0].start, end: modules[0].end, text: tags}}
	for _, m := range modules[1:] {
		edits = append(edits, pageEdit{start: m.start, end: m.end})
	}
	return edits, outs, nil
}

// style inlines the fonts of the page's linked style sheet s into a copy
// named name; it returns no file when the sheet names no fonts.
func (b *bundler) style(p, dir, name string, s pageTag) (pageEdit, *bundleFile) {
	href, _ := tagAttr(s.attrs, "href")
	sheet, inside := snapshotRef(p, href)
	if !inside || strings.ToLower(path.Ext(sheet)) != ".css" {
		return pageEdit{}, nil
	}
	build := &bundleBuild{dir: dir, entry: sheet, virtual: map[string]string{}}
	res, err := b.build(build)
	if err != nil {
		b.warn(p, fmt.Sprintf("the fonts of %s are not inlined: %v", href, err))
		return pageEdit{}, nil
	}
	if build.fonts == 0 || len(res.OutputFiles) != 1 {
		return pageEdit{}, nil
	}
	var tag strings.Builder
	tag.WriteString("<link")
	for _, a := range s.attrs {
		if a.Key == "href" {
			a.Val = (&url.URL{Path: path.Base(name)}).String()
		}
		fmt.Fprintf(&tag, ` %s="%s"`, a.Key, html.EscapeString(a.Val))
	}
	tag.WriteString(">")
	return pageEdit{start: s.start, end: s.end, text: tag.String()}, &bundleFile{name: path.Join(dir, name), data: res.OutputFiles[0].Contents}
}

// build runs esbuild for one entry. Its plugin resolves and loads every
// path, so esbuild reads no file itself: only the snapshot's, through root.
func (b *bundler) build(build *bundleBuild) (api.BuildResult, error) {
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{build.entry},
		Bundle:      true,
		Write:       false,
		Format:      api.FormatIIFE,
		Platform:    api.PlatformBrowser,
		Outfile:     "/" + bundleSuffix + ".js",
		LogLevel:    api.LogLevelSilent,
		Plugins: []api.Plugin{{Name: "uam-snapshot", Setup: func(pb api.PluginBuild) {
			pb.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				return b.resolve(build, args)
			})
			pb.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: bundleNamespace}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				return b.load(build, args)
			})
		}}},
	})
	if len(res.Errors) > 0 {
		e := res.Errors[0]
		if e.Location != nil {
			return res, fmt.Errorf("%s: %s", strings.TrimPrefix(e.Location.File, bundleNamespace+":"), e.Text)
		}
		return res, errors.New(e.Text)
	}
	return res, nil
}

func (b *bundler) resolve(build *bundleBuild, args api.OnResolveArgs) (api.OnResolveResult, error) {
	if b.ctx.Err() != nil {
		return api.OnResolveResult{}, errors.New("bundling took too long")
	}
	if args.Kind == api.ResolveEntryPoint || args.Importer == build.entry {
		return api.OnResolveResult{Path: args.Path, Namespace: bundleNamespace}, nil
	}
	ref := args.Path
	css := args.Kind == api.ResolveCSSImportRule || args.Kind == api.ResolveCSSURLToken
	if !css && !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
		return api.OnResolveResult{}, fmt.Errorf("%q is not a relative import; packages are not bundled, so copy the library into the published folder and import it by a relative path", ref)
	}
	p, inside := snapshotRef(args.Importer, ref)
	switch {
	case !inside && css:
		// Other hosts, data: URLs, fragments and root paths stay as written.
		return api.OnResolveResult{Path: ref, External: true}, nil
	case !inside:
		return api.OnResolveResult{}, fmt.Errorf("%q is outside the published folder", ref)
	case !css:
		p = b.probe(p)
	}
	if args.Kind == api.ResolveCSSURLToken && bundleLoaders[strings.ToLower(path.Ext(p))] != api.LoaderDataURL {
		// The output sits beside the page, so keep the file it names.
		rel, _ := filepath.Rel(build.dir, p)
		suffix := ""
		if i := strings.IndexAny(ref, "?#"); i >= 0 {
			suffix = ref[i:]
		}
		return api.OnResolveResult{Path: filepath.ToSlash(rel) + suffix, External: true}, nil
	}
	return api.OnResolveResult{Path: p, Namespace: bundleNamespace}, nil
}

// probe returns the file an import without an extension names, as a
// bundler resolves it: p, p with a module extension, or p's index module.
func (b *bundler) probe(p string) string {
	for _, c := range append([]string{""}, moduleExts...) {
		if info, err := b.root.Stat(p + c); err == nil && info.Mode().IsRegular() {
			return p + c
		}
	}
	for _, c := range moduleExts {
		if info, err := b.root.Stat(p + "/index" + c); err == nil && info.Mode().IsRegular() {
			return p + "/index" + c
		}
	}
	return p
}

func (b *bundler) load(build *bundleBuild, args api.OnLoadArgs) (api.OnLoadResult, error) {
	if text, ok := build.virtual[args.Path]; ok {
		return api.OnLoadResult{Contents: &text, Loader: api.LoaderJS}, nil
	}
	loader, ok := bundleLoaders[strings.ToLower(path.Ext(args.Path))]
	if !ok {
		return api.OnLoadResult{}, fmt.Errorf("%s is not bundled: uam bundles JavaScript, TypeScript, JSX, JSON, CSS and fonts", args.Path)
	}
	info, err := b.root.Stat(args.Path)
	if err != nil || !info.Mode().IsRegular() {
		return api.OnLoadResult{}, fmt.Errorf("%s is not in the published folder", args.Path)
	}
	if loader == api.LoaderDataURL && info.Size() > maxInlineFont {
		return api.OnLoadResult{}, fmt.Errorf("%s is larger than %d MiB, too large to inline", args.Path, maxInlineFont>>20)
	}
	if b.read += info.Size(); b.read > maxBundleInput {
		return api.OnLoadResult{}, fmt.Errorf("the page's files pass %d MiB, too much to bundle", maxBundleInput>>20)
	}
	data, err := b.root.ReadFile(args.Path)
	if err != nil {
		return api.OnLoadResult{}, fmt.Errorf("%s cannot be read", args.Path)
	}
	if loader == api.LoaderDataURL {
		build.fonts++
	} else {
		build.files++
	}
	text := string(data)
	return api.OnLoadResult{Contents: &text, Loader: loader}, nil
}

// commit writes a page's new files and then the rewritten page, within the
// snapshot's caps. It writes nothing over an existing file.
func (b *bundler) commit(p string, page []byte, outs []bundleFile) error {
	size := b.snap.size + int64(len(page))
	for _, o := range outs {
		size += int64(len(o.data))
	}
	if info, err := b.root.Stat(p); err == nil {
		size -= info.Size()
	}
	if b.snap.entries+len(outs) > maxArtifactFiles || size > maxArtifactBytes {
		return errors.New("its bundle would pass the snapshot's limits")
	}
	var written []string
	undo := func() {
		for _, w := range written {
			_ = b.root.Remove(w)
		}
	}
	for _, o := range outs {
		f, err := b.root.OpenFile(o.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			undo()
			return fmt.Errorf("%s already exists", o.name)
		}
		written = append(written, o.name)
		_, err = f.Write(o.data)
		if cerr := f.Close(); err != nil || cerr != nil {
			undo()
			return errors.New("uam could not write its bundle")
		}
	}
	if err := b.root.WriteFile(p, page, 0o600); err != nil {
		undo()
		return errors.New("uam could not write its bundle")
	}
	b.snap.entries += len(outs)
	b.snap.files += len(outs)
	b.snap.size = size
	return nil
}
