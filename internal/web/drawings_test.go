package web

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// The fixtures of the prototype's diff.test.mjs, as Excalidraw elements.
func exBox(id string, x float64, label string) []map[string]any {
	return []map[string]any{
		{"id": id, "type": "rectangle", "x": x, "y": 0, "width": 100, "height": 50, "strokeColor": "#000", "backgroundColor": "transparent"},
		{"id": id + "-t", "type": "text", "containerId": id, "text": label, "originalText": label, "x": x, "y": 0},
	}
}

func exArrow(id, from, to string) map[string]any {
	return map[string]any{"id": id, "type": "arrow", "x": 0, "y": 0, "startBinding": map[string]any{"elementId": from}, "endBinding": map[string]any{"elementId": to}}
}

func exScene(t *testing.T, elements ...any) string {
	t.Helper()
	var all []any
	for _, e := range elements {
		switch e := e.(type) {
		case []map[string]any:
			for _, m := range e {
				all = append(all, m)
			}
		default:
			all = append(all, e)
		}
	}
	data, err := json.Marshal(map[string]any{"type": "excalidraw", "version": 2, "elements": all})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustDiagram(t *testing.T, format, content string) *diagram {
	t.Helper()
	d, err := parseDiagram(format, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func changeLines(changes []DrawingChange) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Kind+" "+c.Text)
	}
	return out
}

// sceneA and sceneB are the prototype's a and b.
func sceneA(t *testing.T) string {
	return exScene(t, exBox("a", 0, "API"), exBox("d", 200, "DB"), exBox("w", 400, "Worker"), exArrow("e1", "a", "d"), exArrow("e2", "a", "w"))
}

func sceneB(t *testing.T) string {
	gone := exBox("x", 0, "gone")[0]
	gone["isDeleted"] = true
	return exScene(t, exBox("a", 0, "API"), exBox("d", 260, "DB"), exBox("w", 400, "Queue worker"), exBox("c", 100, "Cache"), exArrow("e2", "a", "w"), exArrow("e3", "a", "c"), gone)
}

// The diff says what the prototype's diff.js said, line for line.
func TestDrawingDiffMatchesThePrototype(t *testing.T) {
	a, b := mustDiagram(t, "excalidraw", sceneA(t)), mustDiagram(t, "excalidraw", sceneB(t))
	changes, status := diffDiagrams(a, b)
	want := []string{
		`~ node "Worker" renamed "Queue worker"`,
		`+ node "Cache"`,
		`+ edge "API" → "Cache"`,
		`- edge "API" → "DB"`,
		`~ moved "DB"`,
	}
	if got := changeLines(changes); !slices.Equal(got, want) {
		t.Fatalf("changes = %q, want %q", got, want)
	}
	if status["c"] != "added" || status["e1"] != "removed" || status["d"] != "moved" || len(status) != 5 {
		t.Fatalf("status = %v", status)
	}
	if same, _ := diffDiagrams(a, a); len(same) != 0 {
		t.Fatalf("self diff = %v", same)
	}
	note := drawingNote("f.excalidraw", 2, 3, changes, b)
	if !regexp.MustCompile(`v2 → v3[\s\S]*Now 4 nodes, 2 edges`).MatchString(note) {
		t.Fatalf("note = %q", note)
	}
	wantNote := "The user edited f.excalidraw since your version (v2 → v3):\n" + strings.Join(want, "\n") + "\nNow 4 nodes, 2 edges. Build on the user's version; uam_diagram_read for the full diagram."
	if note != wantNote {
		t.Fatalf("note = %q", note)
	}
	if drawingNote("f.excalidraw", 1, 1, nil, b) != "" {
		t.Fatal("a note without changes")
	}
}

// An unlabelled shape is named by the closest labelled one and its side.
func TestDrawingNamesUnlabelledShapes(t *testing.T) {
	only := mustDiagram(t, "excalidraw", exScene(t, exBox("a", 0, "API")))
	lone := mustDiagram(t, "excalidraw", exScene(t, exBox("a", 0, "API"), map[string]any{"id": "n", "type": "rectangle", "x": 0, "y": 120, "width": 100, "height": 50}))
	if changes, _ := diffDiagrams(only, lone); changes[0].Text != `node unlabelled box below "API"` {
		t.Fatalf("named %q", changes[0].Text)
	}
	for _, tc := range []struct {
		x, y float64
		want string
	}{
		{0, -120, `unlabelled ellipse above "API"`},
		{300, 0, `unlabelled ellipse right of "API"`},
		{-300, 0, `unlabelled ellipse left of "API"`},
	} {
		d := mustDiagram(t, "excalidraw", exScene(t, exBox("a", 0, "API"), map[string]any{"id": "n", "type": "ellipse", "x": tc.x, "y": tc.y, "width": 100, "height": 50}))
		if got := d.name("n"); got != tc.want {
			t.Fatalf("(%v,%v) named %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
	alone := mustDiagram(t, "excalidraw", exScene(t, map[string]any{"id": "n", "type": "diamond", "x": 0, "y": 0, "width": 10, "height": 10}))
	if got := alone.describe(); got != "1 nodes, 0 edges\nnode unlabelled diamond on its own (diamond)" {
		t.Fatalf("describe = %q", got)
	}
}

// describe lists the count, every node and every edge; container-less
// text is a node, bound text is a label, deleted elements are skipped, and
// a loose edge end is "(nothing)".
func TestDrawingDescribe(t *testing.T) {
	deleted := exBox("z", 900, "Zombie")[0]
	deleted["isDeleted"] = true
	d := mustDiagram(t, "excalidraw", exScene(t, exBox("a", 0, "API"), exBox("d", 200, "DB"),
		map[string]any{"id": "t", "type": "text", "x": 0, "y": 200, "width": 10, "height": 10, "text": " Note ", "originalText": nil},
		map[string]any{"id": "e1", "type": "arrow", "startBinding": map[string]any{"elementId": "a"}},
		map[string]any{"id": "e1-t", "type": "text", "containerId": "e1", "text": "reads", "originalText": "reads"},
		exArrow("e2", "a", "d"), deleted))
	want := "3 nodes, 2 edges\nnode \"API\"\nnode \"DB\"\nnode \"Note\" (text)\nedge \"API\" → (nothing) \"reads\"\nedge \"API\" → \"DB\""
	if got := d.describe(); got != want {
		t.Fatalf("describe =\n%s\nwant\n%s", got, want)
	}
	for _, bad := range []string{"", "{}", `{"elements": null}`, "[]", "not json"} {
		if _, err := excalidrawDiagram([]byte(bad)); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}

const drawioPlain = `<mxfile host="test"><diagram id="p1" name="Page-1"><mxGraphModel><root>
<mxCell id="0"/><mxCell id="1" parent="0"/>
<mxCell id="a" value="API" style="rounded=1;strokeColor=#000000;fillColor=#ffffff;" vertex="1" parent="1"><mxGeometry x="0" y="0" width="100" height="50" as="geometry"/></mxCell>
<mxCell id="d" value="&lt;b&gt;DB&lt;/b&gt;" style="ellipse;html=1;" vertex="1" parent="1"><mxGeometry x="200" y="0" width="100" height="50" as="geometry"/></mxCell>
<object label="Worker" id="w"><mxCell style="rhombus;" vertex="1" parent="1"><mxGeometry x="400" y="0" width="100" height="50" as="geometry"/></mxCell></object>
<mxCell id="e1" style="edgeStyle=orthogonalEdgeStyle;" edge="1" parent="1" source="a" target="d"><mxGeometry relative="1" as="geometry"/></mxCell>
<mxCell id="e1-l" value="reads" style="edgeLabel;html=1;" vertex="1" connectable="0" parent="e1"><mxGeometry x="-0.2" relative="1" as="geometry"/></mxCell>
<mxCell id="e2" value="queues" edge="1" parent="1" source="a" target="w"><mxGeometry relative="1" as="geometry"/></mxCell>
</root></mxGraphModel></diagram></mxfile>`

// compressDrawio packs an mxGraphModel the way draw.io's compressed files
// do: URL-encoded, deflated raw, base64.
func compressDrawio(t *testing.T, model string) string {
	t.Helper()
	escaped := strings.ReplaceAll(url.QueryEscape(model), "+", "%20")
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(escaped)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// A draw.io file models as the Excalidraw one does: vertices are nodes
// with their value as label (HTML stripped), edges bind source and target,
// edge labels come from the edge or its label cell, object wrappers carry
// the id and label. The compressed form reads the same.
func TestDrawioDiagramPlainAndCompressed(t *testing.T) {
	want := "3 nodes, 2 edges\nnode \"API\"\nnode \"DB\" (ellipse)\nnode \"Worker\" (diamond)\nedge \"API\" → \"DB\" \"reads\"\nedge \"API\" → \"Worker\" \"queues\""
	plain := mustDiagram(t, "drawio", drawioPlain)
	if got := plain.describe(); got != want {
		t.Fatalf("plain =\n%s\nwant\n%s", got, want)
	}
	if n := plain.node("a"); n.Stroke != "#000000" || n.Fill != "#ffffff" || n.X != 0 || n.W != 100 || n.H != 50 {
		t.Fatalf("node a = %+v", n)
	}
	model := drawioPlain[strings.Index(drawioPlain, "<mxGraphModel>"):strings.Index(drawioPlain, "</diagram>")]
	compressed := `<mxfile host="test"><diagram id="p1" name="Page-1">` + compressDrawio(t, model) + `</diagram></mxfile>`
	if got := mustDiagram(t, "drawio", compressed).describe(); got != want {
		t.Fatalf("compressed =\n%s\nwant\n%s", got, want)
	}
	if got := mustDiagram(t, "drawio", model).describe(); got != want {
		t.Fatalf("bare model =\n%s", got)
	}
	for _, bad := range []string{"", "<svg/>", "<mxfile></mxfile>", `<mxfile><diagram>not base64!</diagram></mxfile>`, "{}"} {
		if _, err := drawioDiagram([]byte(bad)); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
	// Both formats diff alike.
	moved := strings.Replace(drawioPlain, `x="200" y="0"`, `x="260" y="0"`, 1)
	changes, status := diffDiagrams(plain, mustDiagram(t, "drawio", moved))
	if got := changeLines(changes); !slices.Equal(got, []string{`~ moved "DB"`}) || status["d"] != "moved" {
		t.Fatalf("drawio diff = %q %v", got, status)
	}
}

func TestDrawingPathIsConfinedToDocs(t *testing.T) {
	for _, p := range []string{"docs/flow.excalidraw", "docs/sub/flow.drawio", "docs/a b.excalidraw"} {
		if _, _, err := drawingPath(p); err != nil {
			t.Fatalf("%q refused: %v", p, err)
		}
	}
	for _, p := range []string{"", "docs", "docs/", "flow.excalidraw", "docs/../flow.excalidraw", "../docs/flow.excalidraw", "docs/flow.txt", "docs/.excalidraw",
		"/docs/flow.excalidraw", "docs//flow.excalidraw", "docs/./flow.excalidraw", "notes/docs/flow.excalidraw", "docs/flow.excalidraw/", "docs/x\x00.drawio", "docs/sub/..", "docs/flow.EXCALIDRAW"} {
		if _, _, err := drawingPath(p); err == nil {
			t.Fatalf("%q accepted", p)
		}
	}
}

func drawingTask(t *testing.T) (*testServer, SessionSummary, *agenttest.Conversation, string) {
	t.Helper()
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	sum, conv, dir := chartTask(t, ts.m, ts.prov, "safe")
	return ts, sum, conv, dir
}

func putDrawing(ts *testServer, id, p string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	data, _ := json.Marshal(body)
	w := ts.do(http.MethodPut, "/api/sessions/"+id+"/drawings/"+p, string(data), withCookie(ts))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func countOf(m *Manager, name string) int {
	m.counts.mu.Lock()
	defer m.counts.mu.Unlock()
	return m.counts.n[name]
}

// A convert creates the file (owner-only, under docs/) as the agent's
// version 1 and refuses a second; a save bumps the version, reports the
// changes since the agent's version and refuses a stale base_version; the
// list, the session snapshot and the single read show the state.
func TestDrawingRoutesConvertSaveAndRead(t *testing.T) {
	ts, sum, _, dir := drawingTask(t)
	id := sum.ID
	w, res := putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert", "item_id": "item-1", "fence_index": 2})
	if w.Code != http.StatusOK || res["version"] != 1.0 || res["agent_version"] != 1.0 || res["note"] != "" {
		t.Fatalf("convert = %d %s", w.Code, w.Body)
	}
	info, err := os.Lstat(filepath.Join(dir, "docs", "flow.excalidraw"))
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		t.Fatalf("file = %v %v", info, err)
	}
	if w, _ := putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert"}); w.Code != http.StatusConflict {
		t.Fatalf("second convert = %d %s", w.Code, w.Body)
	}
	if w, _ := putDrawing(ts, id, "docs/other.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw"}); w.Code != http.StatusNotFound {
		t.Fatalf("save without convert = %d %s", w.Code, w.Body)
	}
	if w, _ := putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneB(t), "format": "excalidraw", "base_version": 7}); w.Code != http.StatusConflict {
		t.Fatalf("stale save = %d %s", w.Code, w.Body)
	}
	w, res = putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneB(t), "format": "excalidraw", "base_version": 1})
	if w.Code != http.StatusOK || res["version"] != 2.0 || res["agent_version"] != 1.0 {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	if note, _ := res["note"].(string); !strings.HasPrefix(note, "The user edited docs/flow.excalidraw since your version (v1 → v2):\n~ node \"Worker\" renamed") {
		t.Fatalf("note = %q", note)
	}
	if countOf(ts.m, "drawing.convert") != 1 || countOf(ts.m, "drawing.save") != 1 {
		t.Fatal("counts not taken")
	}

	w = ts.do(http.MethodGet, "/api/sessions/"+id+"/drawings", "", withCookie(ts))
	var list struct{ Drawings []DrawingSummary }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	want := DrawingSummary{Path: "docs/flow.excalidraw", Format: "excalidraw", Version: 2, AgentVersion: 1, ItemID: "item-1", FenceIndex: 2, ChangesCount: 5}
	if len(list.Drawings) != 1 || list.Drawings[0] != want {
		t.Fatalf("list = %+v", list.Drawings)
	}
	if d := detail(t, ts.m, id); len(d.Drawings) != 1 || d.Drawings[0] != want {
		t.Fatalf("snapshot drawings = %+v", d.Drawings)
	}

	w = ts.do(http.MethodGet, "/api/sessions/"+id+"/drawings/docs/flow.excalidraw", "", withCookie(ts))
	var view DrawingView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != http.StatusOK {
		t.Fatalf("read = %d %s", w.Code, w.Body)
	}
	if view.Version != 2 || view.AgentVersion != 1 || view.Content != sceneB(t) || view.Baseline == nil || *view.Baseline != sceneA(t) || len(view.Changes) != 5 || view.Status["c"] != "added" || view.Note == "" {
		t.Fatalf("view = %+v", view)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+id+"/drawings/docs/none.excalidraw", "", withCookie(ts)); w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/none/drawings", "", withCookie(ts)); w.Code != http.StatusNotFound {
		t.Fatalf("no session = %d", w.Code)
	}
	// The index lives in uam's store, not the project.
	if _, err := os.Stat(filepath.Join(filepath.Dir(ts.m.store.Path()), drawingsDir, id+".json")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "docs")); len(entries) != 1 {
		t.Fatalf("docs holds %d entries", len(entries))
	}
}

// Saves refuse what the contract bounds: a path outside docs/ or with the
// wrong extension, a mismatched format, content that is not a drawing, a
// file past 2 MiB, a symbolic link in docs/.
func TestDrawingSaveRefusals(t *testing.T) {
	ts, sum, _, dir := drawingTask(t)
	id := sum.ID
	for _, tc := range []struct {
		path string
		body map[string]any
		want int
	}{
		{"notes/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert"}, http.StatusBadRequest},
		{"docs/flow.svg", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert"}, http.StatusBadRequest},
		{"docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "drawio", "origin": "convert"}, http.StatusBadRequest},
		{"docs/flow.excalidraw", map[string]any{"content": "nope", "format": "excalidraw", "origin": "convert"}, http.StatusBadRequest},
		{"docs/flow.drawio", map[string]any{"content": sceneA(t), "format": "drawio", "origin": "convert"}, http.StatusBadRequest},
		{"docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "import"}, http.StatusBadRequest},
		{"docs/flow.excalidraw", map[string]any{"content": strings.Repeat(" ", maxDrawingBytes+1) + sceneA(t), "format": "excalidraw", "origin": "convert"}, http.StatusRequestEntityTooLarge},
	} {
		if w, _ := putDrawing(ts, id, tc.path, tc.body); w.Code != tc.want {
			t.Fatalf("PUT %s %v = %d %s, want %d", tc.path, tc.body["format"], w.Code, w.Body, tc.want)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "docs")); len(entries) != 0 {
		t.Fatalf("a refused save wrote %d entries", len(entries))
	}
	// A symbolic link where the drawing would go is never followed.
	outside := filepath.Join(t.TempDir(), "outside.excalidraw")
	writeFile(t, outside, sceneA(t))
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "docs", "link.excalidraw")); err != nil {
		t.Fatal(err)
	}
	if w, _ := putDrawing(ts, id, "docs/link.excalidraw", map[string]any{"content": sceneB(t), "format": "excalidraw", "origin": "convert"}); w.Code != http.StatusConflict {
		t.Fatalf("convert over a link = %d", w.Code)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "docs", "away")); err != nil {
		t.Fatal(err)
	}
	if w, _ := putDrawing(ts, id, "docs/away/flow.excalidraw", map[string]any{"content": sceneB(t), "format": "excalidraw", "origin": "convert"}); w.Code != http.StatusInternalServerError {
		t.Fatalf("convert through a linked folder = %d %s", w.Code, w.Body)
	}
	if data, _ := os.ReadFile(outside); string(data) != sceneA(t) {
		t.Fatal("the file outside the thread changed")
	}
}

// A save takes the drawing lock and then the Manager lock to broadcast,
// while a snapshot takes them the other way round; neither may hold one
// while waiting for the other, or a PUT racing a snapshot stops the
// service.
func TestDrawingSaveAndSnapshotDoNotDeadlock(t *testing.T) {
	ts, sum, _, _ := drawingTask(t)
	id := sum.ID
	if _, err := ts.m.SaveDrawing(id, "docs/flow.excalidraw", DrawingSave{Content: sceneA(t), Format: "excalidraw", Origin: "convert"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for i := range 150 {
					scene := sceneA(t)
					if i%2 == 1 {
						scene = sceneB(t)
					}
					if _, err := ts.m.SaveDrawing(id, "docs/flow.excalidraw", DrawingSave{Content: scene, Format: "excalidraw"}); err != nil {
						t.Error(err)
						return
					}
				}
			}()
			go func() {
				defer wg.Done()
				for range 150 {
					if _, err := ts.m.Detail(id); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("saves and snapshots deadlocked")
	}
	if list, _ := ts.m.Drawings(id); list[0].Version != 301 {
		t.Fatalf("version = %d", list[0].Version)
	}
}

// A file under 2 MiB saves although its JSON body is larger, up to the
// route's 3 MiB cap; one over 2 MiB is refused.
func TestDrawingSaveTakesALargeFile(t *testing.T) {
	ts, sum, _, _ := drawingTask(t)
	// Each quote is two bytes in the file and four in the JSON body: a
	// 1.9 MiB file makes a body of about 2.8 MiB.
	label := strings.Repeat(`"`, 450<<10) + strings.Repeat("a", 1000<<10)
	scene := exScene(t, map[string]any{"id": "t", "type": "text", "x": 0, "y": 0, "width": 10, "height": 10, "text": label, "originalText": nil})
	if n := len(scene); n < 1900<<10 || n > maxDrawingBytes {
		t.Fatalf("scene is %d bytes", n)
	}
	w, res := putDrawing(ts, sum.ID, "docs/big.excalidraw", map[string]any{"content": scene, "format": "excalidraw", "origin": "convert"})
	if w.Code != http.StatusOK || res["version"] != 1.0 {
		t.Fatalf("large convert = %d %s", w.Code, clipRunes(w.Body.String(), 200))
	}
	if w := ts.do(http.MethodPut, "/api/sessions/"+sum.ID+"/drawings/docs/huge.excalidraw", `{"content":"`+strings.Repeat("a", maxDrawingBody)+`"}`, withCookie(ts)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the body cap = %d", w.Code)
	}
}

// Deleting a Thread drops its drawing index and nothing of its directory.
func TestDeletingAThreadForgetsItsDrawings(t *testing.T) {
	ts, sum, _, dir := drawingTask(t)
	putDrawing(ts, sum.ID, "docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert"})
	index := ts.m.drawingIndexPath(sum.ID)
	if _, err := os.Stat(index); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := ts.m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(index); !os.IsNotExist(err) {
		t.Fatalf("index after delete: %v", err)
	}
	ts.m.drawings.mu.Lock()
	_, cached := ts.m.drawings.by[sum.ID]
	ts.m.drawings.mu.Unlock()
	if cached {
		t.Fatal("index still cached")
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "flow.excalidraw")); err != nil {
		t.Fatalf("the owner's file went: %v", err)
	}
}

// uam_diagram_read lists the drawings, or describes one with the changes
// since the agent's version, which then becomes the current one.
func TestDiagramReadTool(t *testing.T) {
	ts, sum, conv, _ := drawingTask(t)
	id := sum.ID
	read := func(args string) agentapi.HostToolResult {
		t.Helper()
		res, err := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: diagramReadToolName, CallID: "c1", Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := read(`{}`); res.Failed || !strings.HasPrefix(res.Text, "This thread has no drawings") {
		t.Fatalf("empty list = %+v", res)
	}
	putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert"})
	putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneB(t), "format": "excalidraw"})
	if res := read(`{}`); res.Failed || res.Text != "1 drawings:\ndocs/flow.excalidraw (excalidraw) v2, your version v1, 5 unseen changes" {
		t.Fatalf("list = %+v", res)
	}
	res := read(`{"path":"docs/flow.excalidraw"}`)
	want := mustDiagram(t, "excalidraw", sceneB(t)).describe() + "\n\nChanges since your version (v1 → v2):\n~ node \"Worker\" renamed \"Queue worker\"\n+ node \"Cache\"\n+ edge \"API\" → \"Cache\"\n- edge \"API\" → \"DB\"\n~ moved \"DB\""
	if res.Failed || res.Text != want {
		t.Fatalf("read =\n%s\nwant\n%s", res.Text, want)
	}
	if list, _ := ts.m.Drawings(id); list[0].AgentVersion != 2 || list[0].ChangesCount != 0 {
		t.Fatalf("after read = %+v", list[0])
	}
	if res := read(`{"path":"docs/flow.excalidraw"}`); res.Failed || res.Text != mustDiagram(t, "excalidraw", sceneB(t)).describe() {
		t.Fatalf("second read = %+v", res)
	}
	if countOf(ts.m, "drawing.read") != 4 {
		t.Fatalf("drawing.read = %d", countOf(ts.m, "drawing.read"))
	}
	for _, args := range []string{`{"path":"docs/none.excalidraw"}`, `{"path":"../x.excalidraw"}`, `{"paths":"x"}`} {
		if res := read(args); !res.Failed {
			t.Fatalf("%s = %+v", args, res)
		}
	}
	// A note is no longer owed once the agent read the drawing.
	if note := conv.Request().Hooks.Prompt(t.Context()); note != "" {
		t.Fatalf("note after read = %q", note)
	}
}

// The owner's edits reach the agent as a note on its next prompt, once,
// beside the end notes of the Threads it created; the agent's version then
// is the current one.
func TestDrawingNotesJoinThePromptHook(t *testing.T) {
	ts, sum, conv, _ := drawingTask(t)
	id := sum.ID
	putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw", "origin": "convert"})
	putDrawing(ts, id, "docs/b.drawio", map[string]any{"content": drawioPlain, "format": "drawio", "origin": "convert"})
	if note := conv.Request().Hooks.Prompt(t.Context()); note != "" {
		t.Fatalf("note after convert = %q", note)
	}
	putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneB(t), "format": "excalidraw"})
	putDrawing(ts, id, "docs/b.drawio", map[string]any{"content": strings.Replace(drawioPlain, `value="API"`, `value="Gateway"`, 1), "format": "drawio"})
	s, _ := ts.m.lookup(id)
	ts.m.mu.Lock()
	s.childNotes = []childNote{{text: "uam thread: child ended"}}
	ts.m.mu.Unlock()
	note := conv.Request().Hooks.Prompt(t.Context())
	want := "uam thread: child ended\n\n" +
		"The user edited docs/b.drawio since your version (v1 → v2):\n~ node \"API\" renamed \"Gateway\"\nNow 3 nodes, 2 edges. Build on the user's version; uam_diagram_read for the full diagram.\n\n" +
		"The user edited docs/flow.excalidraw since your version (v1 → v2):\n~ node \"Worker\" renamed \"Queue worker\"\n+ node \"Cache\"\n+ edge \"API\" → \"Cache\"\n- edge \"API\" → \"DB\"\n~ moved \"DB\"\nNow 4 nodes, 2 edges. Build on the user's version; uam_diagram_read for the full diagram."
	if note != want {
		t.Fatalf("note =\n%s\nwant\n%s", note, want)
	}
	if again := conv.Request().Hooks.Prompt(t.Context()); again != "" {
		t.Fatalf("second note = %q", again)
	}
	list, _ := ts.m.Drawings(id)
	for _, d := range list {
		if d.AgentVersion != 2 || d.ChangesCount != 0 {
			t.Fatalf("after the note = %+v", d)
		}
	}
	if countOf(ts.m, "drawing.note") != 2 {
		t.Fatalf("drawing.note = %d", countOf(ts.m, "drawing.note"))
	}
	// The baseline moved with it: the next read shows no changes, and a
	// further edit diffs against the owner's version.
	w := ts.do(http.MethodGet, "/api/sessions/"+id+"/drawings/docs/flow.excalidraw", "", withCookie(ts))
	var view DrawingView
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if len(view.Changes) != 0 || view.Baseline != nil || view.Note != "" {
		t.Fatalf("view after note = %+v", view)
	}
	_, res := putDrawing(ts, id, "docs/flow.excalidraw", map[string]any{"content": sceneA(t), "format": "excalidraw"})
	if note, _ := res["note"].(string); !strings.Contains(note, "(v2 → v3)") || !strings.Contains(note, `~ node "Queue worker" renamed "Worker"`) {
		t.Fatalf("note = %q", note)
	}
}

// drawioServer serves a page with the given body and headers at a loopback
// http origin.
func drawioServer(t *testing.T, status int, body string, headers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "embed=1&proto=json" {
			http.Error(w, "wrong query", http.StatusBadRequest)
			return
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const drawioPage = `<!DOCTYPE html><html><head><title>Flowchart Maker</title></head><body><div id="geApp"></div><script src="js/app.min.js"></script></body></html>`

// An origin is a plain host name or IP with an optional numeric port, so
// nothing a policy would read as a delimiter can reach the page policy.
func TestDrawioOriginIsPolicySafe(t *testing.T) {
	for raw, want := range map[string]string{
		"https://drawio.example.com":      "https://drawio.example.com",
		"HTTPS://DrawIO.Example.com:8443": "https://drawio.example.com:8443",
		"http://127.0.0.1:8080/":          "http://127.0.0.1:8080",
		"http://[::1]:8080":               "http://[::1]:8080",
		" https://localhost ":             "https://localhost",
	} {
		if got, err := drawioOrigin(raw); err != nil || got != want {
			t.Fatalf("drawioOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"", "drawio.example", "ftp://drawio.example", "https://user@drawio.example", "https://drawio.example/app", "https://drawio.example?x=1", "https://drawio.example#f",
		"https://drawio.example;script-src *", "https://a,b.example", "https://a'b.example", "https://*.example", "https://a b.example", "https://a_b.example",
		"https://drawio.example:x", "https://drawio.example:0", "https://drawio.example:70000", "https://drawio.example:08a", "https://[::1", "https://%41.example", "https://drawio.example:",
	} {
		if got, err := drawioOrigin(raw); err == nil {
			t.Fatalf("drawioOrigin(%q) = %q, accepted", raw, got)
		}
	}
}

func TestCheckDrawIO(t *testing.T) {
	ctx := context.Background()
	ok := drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Access-Control-Allow-Origin": "*"})
	for _, tc := range []struct {
		name    string
		origin  string
		https   bool
		public  []string
		wantOK  bool
		wantErr string
		detail  DrawIODetail
	}{
		{"self-hosted with cors", ok.URL, false, nil, true, "", DrawIODetail{CORS: true, FramingOK: true}},
		{"trailing slash", ok.URL + "/", false, nil, true, "", DrawIODetail{CORS: true, FramingOK: true}},
		{"loopback http under an https uam", ok.URL, true, []string{"https://uam.example"}, true, "", DrawIODetail{CORS: true, FramingOK: true}},
		{"relative", "drawio.example", false, nil, false, "absolute http(s) origin", DrawIODetail{}},
		{"with a path", ok.URL + "/app", false, nil, false, "absolute http(s) origin", DrawIODetail{}},
		{"ftp", "ftp://drawio.example", false, nil, false, "absolute http(s) origin", DrawIODetail{}},
		{"mixed content", "http://drawio.example", true, []string{"https://uam.example"}, false, "mixed content", DrawIODetail{}},
		{"unreachable", "http://127.0.0.1:1", false, nil, false, "could not be reached", DrawIODetail{}},
		{"not draw.io", drawioServer(t, http.StatusOK, "<html>hello</html>", nil).URL, false, nil, false, "does not serve a draw.io page", DrawIODetail{FramingOK: true}},
		{"not found", drawioServer(t, http.StatusNotFound, drawioPage, nil).URL, false, nil, false, "HTTP 404", DrawIODetail{}},
		{"x-frame-options deny", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"X-Frame-Options": "DENY"}).URL, false, nil, false, "X-Frame-Options DENY", DrawIODetail{}},
		{"x-frame-options sameorigin", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"X-Frame-Options": "sameorigin"}).URL, false, nil, false, "X-Frame-Options SAMEORIGIN", DrawIODetail{}},
		{"frame-ancestors none", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'"}).URL, false, nil, false, "frame-ancestors", DrawIODetail{}},
		{"frame-ancestors other host", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Content-Security-Policy": "frame-ancestors https://other.example"}).URL, false, []string{"https://uam.example"}, false, "frame-ancestors", DrawIODetail{}},
		{"frame-ancestors uam", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Content-Security-Policy": "frame-ancestors https://uam.example"}).URL, true, []string{"https://uam.example"}, true, "", DrawIODetail{FramingOK: true}},
		{"frame-ancestors wildcard", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Content-Security-Policy": "frame-ancestors 'self' *"}).URL, false, nil, true, "", DrawIODetail{FramingOK: true}},
		{"frame-ancestors https:", drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Content-Security-Policy": "frame-ancestors https:"}).URL, true, []string{"https://uam.example"}, true, "", DrawIODetail{FramingOK: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDrawIO(ctx, nil, tc.origin, tc.https, tc.public)
			if got.OK != tc.wantOK || got.Detail != tc.detail {
				t.Fatalf("check = %+v, want ok %v detail %+v", got, tc.wantOK, tc.detail)
			}
			if tc.wantErr != "" && (len(got.Errors) == 0 || !strings.Contains(got.Errors[0], tc.wantErr)) {
				t.Fatalf("errors = %q, want %q", got.Errors, tc.wantErr)
			}
			if got.Errors == nil || got.Warnings == nil {
				t.Fatal("errors and warnings must be lists")
			}
			for _, e := range append(got.Errors, got.Warnings...) {
				if strings.Contains(e, "geApp") && !strings.Contains(e, "marker") || strings.Contains(e, "<html>") {
					t.Fatalf("body text in the result: %q", e)
				}
			}
		})
	}
	// A self-hosted origin without CORS runs on its own origin only.
	plain := drawioServer(t, http.StatusOK, drawioPage, nil)
	if got := checkDrawIO(ctx, nil, plain.URL, false, nil); !got.OK || got.Detail.CORS || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "allow-same-origin") {
		t.Fatalf("no cors = %+v", got)
	}
	// A redirect to another origin is refused; one within the origin is
	// followed.
	target := ok.URL
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, target+"/?embed=1&proto=json", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(away.Close)
	if got := checkDrawIO(ctx, nil, away.URL, false, nil); got.OK || !strings.Contains(got.Errors[0], "redirects elsewhere") {
		t.Fatalf("redirect = %+v", got)
	}
	// The public service gets its terms warning.
	host := drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Access-Control-Allow-Origin": "*"})
	client := host.Client()
	client.Transport = rewriteHost(host.Listener.Addr().String())
	if got := checkDrawIO(ctx, client, "https://embed.diagrams.net", true, []string{"https://uam.example"}); !got.OK || !got.Detail.Public || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "third-party") {
		t.Fatalf("public = %+v", got)
	}
}

// rewriteHost sends every request to addr over plain http, whatever its URL.
func rewriteHost(addr string) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = "http", addr
		return http.DefaultTransport.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The check route reports the probe; enabling draw.io in Settings needs a
// passing one and then widens the page policy by a frame source, which a
// disable or a restart-with-settings keeps consistent.
func TestDrawIOSettingNeedsAPassingCheck(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	bad := drawioServer(t, http.StatusOK, "<html>nope</html>", nil)
	good := drawioServer(t, http.StatusOK, drawioPage, map[string]string{"Access-Control-Allow-Origin": "*"})
	w := ts.do(http.MethodPost, "/api/settings/drawio/check", fmt.Sprintf(`{"origin":%q}`, bad.URL), withCookie(ts))
	var check DrawIOCheck
	if err := json.Unmarshal(w.Body.Bytes(), &check); err != nil || w.Code != http.StatusOK || check.OK || len(check.Errors) != 1 {
		t.Fatalf("check = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/settings/drawio/check", `{"origin":"nope"}`, withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("check of a bad origin = %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"drawio":null}`, `{"drawio":"x"}`, `{"drawio":{"origin":"nope","enabled":false}}`} {
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", body, w.Code, w.Body)
		}
	}
	if w := ts.do(http.MethodPatch, "/api/settings", fmt.Sprintf(`{"drawio":{"origin":%q,"enabled":true}}`, bad.URL), withCookie(ts)); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "draw.io origin check failed") {
		t.Fatalf("enable a failing origin = %d %s", w.Code, w.Body)
	}
	if st := ts.m.Settings(); st.DrawIO != (DrawIOSettings{}) {
		t.Fatalf("settings changed: %+v", st.DrawIO)
	}
	if w := ts.do(http.MethodGet, "/", "", withCookie(ts)); w.Header().Get("Content-Security-Policy") != contentSecurity {
		t.Fatalf("policy changed: %q", w.Header().Get("Content-Security-Policy"))
	}
	// Saving the origin unenabled needs no check.
	if w := ts.do(http.MethodPatch, "/api/settings", fmt.Sprintf(`{"drawio":{"origin":%q,"enabled":false}}`, bad.URL+"/"), withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("save disabled = %d %s", w.Code, w.Body)
	}
	if st := ts.m.Settings(); st.DrawIO != (DrawIOSettings{Origin: bad.URL}) {
		t.Fatalf("settings = %+v", st.DrawIO)
	}
	w = ts.do(http.MethodPatch, "/api/settings", fmt.Sprintf(`{"drawio":{"origin":%q,"enabled":true}}`, good.URL), withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"drawio":{"origin":"`+good.URL+`","enabled":true}`) {
		t.Fatalf("enable = %d %s", w.Code, w.Body)
	}
	want := contentSecurity + "; frame-src 'self' " + good.URL
	for _, p := range []string{"/", "/api/settings", "/sessions/abc"} {
		if w := ts.do(http.MethodGet, p, "", withCookie(ts)); w.Header().Get("Content-Security-Policy") != want {
			t.Fatalf("GET %s policy = %q, want %q", p, w.Header().Get("Content-Security-Policy"), want)
		}
	}
	cfg, err := ts.m.store.Load()
	if err != nil || cfg.WebSettings.DrawIO.Origin != good.URL || !cfg.WebSettings.DrawIO.Enabled {
		t.Fatalf("stored = %+v %v", cfg.WebSettings.DrawIO, err)
	}
	if err := ts.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, ts.m.store, ts.prov)
	if again.Settings().DrawIO != (DrawIOSettings{Origin: good.URL, Enabled: true}) || again.pagePolicy() != want {
		t.Fatalf("after restart = %+v %q", again.Settings().DrawIO, again.pagePolicy())
	}
	if _, err := again.UpdateSettings(SettingsPatch{DrawIO: &DrawIOSettings{}}); err != nil || again.pagePolicy() != contentSecurity {
		t.Fatalf("disable = %v %q", err, again.pagePolicy())
	}
	if encoded, _ := json.Marshal(again.Settings()); strings.Contains(string(encoded), "drawio") {
		t.Fatalf("unset draw.io is not omitted: %s", encoded)
	}
}
