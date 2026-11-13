package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func surfaceArgs(msgs ...string) json.RawMessage {
	return json.RawMessage(`{"messages":[` + strings.Join(msgs, ",") + `]}`)
}

func surfaceCreate(id, components string) string {
	return `{"version":"1.0","createSurface":{"surfaceId":"` + id + `","catalogId":"urn:uam:catalog:v1","components":[` + components + `]}}`
}

// applySurfaceArgs applies a call's raw arguments to t as a uam_surface call does.
func applySurfaceArgs(t *threadSurfaces, callID string, raw json.RawMessage) ([]*surface, []string, error) {
	msgs, err := surfaceMessages(raw)
	if err != nil {
		return nil, nil, err
	}
	return applySurfaceCall(t, callID, "/w", msgs)
}

// Adapted from a2ui catalogs/basic/v1/examples/00_interactive-button.json.
const surfaceButtonExample = `{"version":"1.0","createSurface":{"surfaceId":"gallery-interactive-button","catalogId":"urn:uam:catalog:v1"}},
{"version":"1.0","updateComponents":{"surfaceId":"gallery-interactive-button","components":[
 {"id":"root","component":"Column","children":["title","action_button"],"justify":"center","align":"center"},
 {"id":"title","component":"Text","text":"Click the button below","variant":"title"},
 {"id":"action_button","component":"Button","child":"button_label","variant":"primary","action":{"event":{"name":"button_clicked","context":{}}}},
 {"id":"button_label","component":"Text","text":"Click Me"}]}}`

// Adapted from 07_task-card.json, without sendDataModel.
const surfaceTaskExample = `{"version":"1.0","createSurface":{"surfaceId":"gallery-task-card","catalogId":"urn:uam:catalog:v1"}},
{"version":"1.0","updateComponents":{"surfaceId":"gallery-task-card","components":[
 {"id":"root","component":"Card","child":"main_row"},
 {"id":"main_row","component":"Row","children":["status_checkbox","content","priority"],"align":"start"},
 {"id":"status_checkbox","component":"CheckBox","label":"","value":{"@path":"/completed"}},
 {"id":"content","component":"Column","children":["title","description","meta_row"],"weight":1},
 {"id":"title","component":"Text","text":{"@path":"/title"},"variant":"title"},
 {"id":"description","component":"Text","text":{"@path":"/description"},"variant":"body"},
 {"id":"meta_row","component":"Row","children":["due_date","project"],"align":"center"},
 {"id":"due_date","component":"Text","text":{"@path":"/dueDate"},"variant":"caption","weight":0.5},
 {"id":"project","component":"Text","text":{"@path":"/project"},"variant":"caption","weight":12},
 {"id":"priority","component":"Status","label":"Priority","tone":{"@path":"/tone"}}]}},
{"version":"1.0","updateDataModel":{"surfaceId":"gallery-task-card","value":{"completed":false,"title":"Review pull request","description":"Review the auth changes.","dueDate":"2025-12-15","project":"Backend","tone":"warn"}}}`

// A Modal's trigger Button needs no action: pressing it opens the dialog.
const surfaceModalExample = `{"version":"1.0","createSurface":{"surfaceId":"m","catalogId":"urn:uam:catalog:v1","components":[
 {"id":"root","component":"Modal","trigger":"open","content":"body"},
 {"id":"open","component":"Button","child":"ol","variant":"borderless"},
 {"id":"ol","component":"Text","text":"Details"},
 {"id":"body","component":"Text","text":"Long notes"}]}}`

func surfaceDoc(t *testing.T) json.RawMessage {
	t.Helper()
	doc, err := builtinSkills.ReadFile("skills/uam/references/surfaces.md")
	if err != nil {
		t.Fatal(err)
	}
	example := regexp.MustCompile("(?s)```json\n(.*?)```").FindSubmatch(doc)
	if example == nil {
		t.Fatal("surfaces.md has no json example")
	}
	return example[1]
}

func TestSurfaceAccepts(t *testing.T) {
	for name, args := range map[string]json.RawMessage{
		"button":  surfaceArgs(surfaceButtonExample),
		"task":    surfaceArgs(surfaceTaskExample),
		"modal":   surfaceArgs(surfaceModalExample),
		"doc":     surfaceDoc(t),
		"release": releaseCard(t),
		// A media path may also be absolute under the working folder.
		"absolute": surfaceArgs(surfaceCreate("s", `{"id":"root","component":"Column","children":["v","t"]},
 {"id":"v","component":"Video","url":"/w/out/clip.webm","posterUrl":"out/./clip.PNG"},
 {"id":"t","component":"DateTimeInput","value":"2026-11-13T14:30:05Z","enableDate":true,"enableTime":true,"min":"2026-11-13T09:00"}`)),
	} {
		state := &threadSurfaces{}
		changed, lines, err := applySurfaceArgs(state, "call-1", args)
		if err != nil || len(changed) != 1 || len(lines) == 0 {
			t.Fatalf("%s: refused: %v", name, err)
		}
		s := state.Surfaces[0]
		if s.CallID != "call-1" || s.Version != len(lines) || lines[0] != "created surface "+s.SurfaceID {
			t.Errorf("%s: call %q version %d lines %q", name, s.CallID, s.Version, lines)
		}
	}

	// The doc's follow-up update, then an append and a delete.
	state := &threadSurfaces{}
	if _, _, err := applySurfaceArgs(state, "c1", surfaceDoc(t)); err != nil {
		t.Fatal(err)
	}
	_, lines, err := applySurfaceArgs(state, "c2", surfaceArgs(
		`{"version":"1.0","updateDataModel":{"surfaceId":"ci","path":"/jobs/0/tone","value":"ok"}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"ci","path":"/jobs/-","value":{"name":"e2e","tone":"neutral"}}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"ci","path":"/jobs/1","value":null}}`))
	if err != nil || strings.HasPrefix(lines[0], surfaceCreatedLine) {
		t.Fatalf("update: %q, %v", lines, err)
	}
	b, _ := json.Marshal(state.byID("ci"))
	if want := `"data_model":{"jobs":[{"name":"lint","pct":40,"tone":"ok"},{"name":"e2e","tone":"neutral"}]}`; !strings.Contains(string(b), want) || !strings.Contains(string(b), `"call_id":"c1","catalog_id":"urn:uam:catalog:v1","version":4`) {
		t.Errorf("surface JSON = %s", b)
	}
}

func TestSurfaceRefuses(t *testing.T) {
	create := func(components string) string { return surfaceCreate("s", components) }
	root := `{"id":"root","component":"Text","text":"hi"}`
	for name, tc := range map[string]struct {
		args json.RawMessage
		want string
	}{
		"version":       {surfaceArgs(`{"version":"0.9","deleteSurface":{"surfaceId":"s"}}`), `"version" must be "1.0"`},
		"two kinds":     {surfaceArgs(`{"version":"1.0","deleteSurface":{"surfaceId":"s"},"updateDataModel":{"surfaceId":"s","value":1}}`), "exactly one of"},
		"unsupported":   {surfaceArgs(`{"version":"1.0","callRendererFunction":{}}`), "unknown field"},
		"catalog":       {surfaceArgs(`{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"x"}}`), `"catalogId" must be`},
		"two creates":   {surfaceArgs(create(root), surfaceCreate("t", root)), `this call already created surface "s"`},
		"data model":    {surfaceArgs(`{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"urn:uam:catalog:v1","components":[` + root + `],"dataModel":[1]}}`), `"dataModel" must be an object`},
		"no root":       {surfaceArgs(create(`{"id":"a","component":"Text","text":"hi"}`)), `no component with id "root"`},
		"unknown":       {surfaceArgs(create(`{"id":"root","component":"Map","url":"x"}`)), `component "root": "Map" is not in the catalog`},
		"prop":          {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","color":"red"}`)), `component "root" (Text) "color" is not a property`},
		"enum":          {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","variant":"h1"}`)), `"variant" must be one of title, body, caption`},
		"weight zero":   {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","weight":0}`)), `(Text) "weight" must be a number above 0 and at most 12`},
		"weight big":    {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","weight":13}`)), `"weight" must be a number`},
		"weight text":   {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","weight":"1"}`)), `"weight" must be a number`},
		"required":      {surfaceArgs(create(`{"id":"root","component":"Card"}`)), `component "root" (Card) "child" is required`},
		"no action":     {surfaceArgs(create(`{"id":"root","component":"Button","child":"l"},{"id":"l","component":"Text","text":"Go"}`)), `component "root" (Button) "action" is required unless the Button is a Modal's trigger`},
		"modal content": {surfaceArgs(create(`{"id":"root","component":"Modal","trigger":"t","content":"b"},{"id":"t","component":"Text","text":"x"},{"id":"b","component":"Button","child":"t"}`)), `component "b" (Button) "action" is required`},
		"dangling":      {surfaceArgs(create(`{"id":"root","component":"Card","child":"x"}`)), `component "root" refers to "x"`},
		"duplicate":     {surfaceArgs(create(root + `,` + root)), `component id "root" appears twice`},
		"cycle":         {surfaceArgs(create(`{"id":"root","component":"Card","child":"a"},{"id":"a","component":"Column","children":["root"]}`)), "contains itself"},
		"pointer":       {surfaceArgs(create(`{"id":"root","component":"Text","text":{"@path":"/a~2"}}`)), "valid JSON Pointer"},
		"call":          {surfaceArgs(create(`{"id":"root","component":"Text","text":{"@call":"now"}}`)), "@call is not supported"},
		"function":      {surfaceArgs(create(`{"id":"root","component":"Button","child":"root","action":{"functionCall":{"call":"x"}}}`)), "functionCall is not supported"},
		"missing":       {surfaceArgs(`{"version":"1.0","updateDataModel":{"surfaceId":"nope","value":{}}}`), `there is no surface "nope"`},
		"path":          {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"a/b","value":1}}`), "is not a JSON Pointer"},
		"index sign":    {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l","value":[1]}}`, `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/+0","value":2}}`), `"+0" is not an index of the list at that path, which has 1 items`},
		"index zero":    {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l","value":[1]}}`, `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/00","value":2}}`), `"00" is not an index`},
		"index range":   {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l","value":[1]}}`, `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/2","value":2}}`), `"2" is not an index`},
		"through":       {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/n","value":1}}`, `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/n/x","value":2}}`), "goes through a float64"},
		"whole model":   {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","value":[1]}}`), `"value" must be an object (the whole model)`},
		"media https":   {surfaceArgs(create(`{"id":"root","component":"Image","url":"https://example.com/a.png"}`)), `(Image) "url" must be the literal path of a file in the thread's working folder; save the file`},
		"media http":    {surfaceArgs(create(`{"id":"root","component":"Video","url":"http://example.com/a.mp4"}`)), `(Video) "url" must be the literal path`},
		"media data":    {surfaceArgs(create(`{"id":"root","component":"Image","url":"data:image/png;base64,AAAA"}`)), `(Image) "url" must be the literal path`},
		"media file":    {surfaceArgs(create(`{"id":"root","component":"AudioPlayer","url":"file:///w/a.mp3"}`)), `(AudioPlayer) "url" must be the literal path`},
		"media host":    {surfaceArgs(create(`{"id":"root","component":"Image","url":"//example.com/a.png"}`)), `(Image) "url" must be the literal path`},
		"media escape":  {surfaceArgs(create(`{"id":"root","component":"Image","url":"shots/../../a.png"}`)), `(Image) "url" must be the literal path`},
		"media encoded": {surfaceArgs(create(`{"id":"root","component":"Image","url":"%2e%2e/a.png"}`)), `(Image) "url" must be the literal path`},
		"media windows": {surfaceArgs(create(`{"id":"root","component":"Image","url":"shots\\..\\..\\a.png"}`)), `(Image) "url" must be the literal path`},
		"media drive":   {surfaceArgs(create(`{"id":"root","component":"Image","url":"C:/w/a.png"}`)), `(Image) "url" must be the literal path`},
		"media outside": {surfaceArgs(create(`{"id":"root","component":"Video","url":"/w2/a.mp4"}`)), `(Video) "url" must be the literal path`},
		"media bound":   {surfaceArgs(create(`{"id":"root","component":"AudioPlayer","url":{"@path":"/u"}}`)), `(AudioPlayer) "url" must be the literal path`},
		"media poster":  {surfaceArgs(create(`{"id":"root","component":"Video","url":"a.mp4","posterUrl":"https://x/p.png"}`)), `(Video) "posterUrl" must be the literal path`},
		"media type":    {surfaceArgs(create(`{"id":"root","component":"Video","url":"clip.mov"}`)), `(Video) "url" must name a .mp4, .webm file`},
		"audio type":    {surfaceArgs(create(`{"id":"root","component":"AudioPlayer","url":"a.png"}`)), `must name a .mp3, .ogg, .wav, .m4a file`},
		"icon name":     {surfaceArgs(create(`{"id":"root","component":"Icon","name":"rocket"}`)), `(Icon) "name": "rocket" is not one of accountCircle`},
		"svg chars":     {surfaceArgs(create(`{"id":"root","component":"Icon","name":{"svgPath":"M0 0\"/><script>"}}`)), `{"svgPath": ...} must be a literal SVG path`},
		"svg long":      {surfaceArgs(create(`{"id":"root","component":"Icon","name":{"svgPath":"` + strings.Repeat("M1 1", 600) + `"}}`)), `at most 2048 characters`},
		"svg bound":     {surfaceArgs(create(`{"id":"root","component":"Icon","name":{"svgPath":{"@path":"/p"}}}`)), `must be a literal SVG path`},
		"date shape":    {surfaceArgs(create(`{"id":"root","component":"DateTimeInput","value":"2026-13-01","enableDate":true}`)), `"value" "2026-13-01" is not ISO 8601 a date such as 2026-11-13`},
		"time shape":    {surfaceArgs(create(`{"id":"root","component":"DateTimeInput","value":"","enableTime":true,"max":"2026-11-13"}`)), `"max" "2026-11-13" is not ISO 8601 a time`},
		"stamp shape":   {surfaceArgs(create(`{"id":"root","component":"DateTimeInput","value":"2026-11-13","enableDate":true,"enableTime":true}`)), `is not ISO 8601 a date and time`},
		"no shape":      {surfaceArgs(create(`{"id":"root","component":"DateTimeInput","value":""}`)), `"enableDate" and "enableTime" are both off`},
		"modal cycle":   {surfaceArgs(create(`{"id":"root","component":"Modal","trigger":"b","content":"root"},{"id":"b","component":"Text","text":"x"}`)), "contains itself"},
		"modal dangle":  {surfaceArgs(create(`{"id":"root","component":"Modal","trigger":"b","content":"x"},{"id":"b","component":"Text","text":"x"}`)), `component "root" refers to "x"`},
		"too big":       {json.RawMessage(`{"messages":["` + strings.Repeat("x", maxSurfaceCall) + `"]}`), "the limit is 32 KiB"},
		"deep pointer":  {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"`+strings.Repeat("/", 10001)+`","value":1}}`), "at most 32 levels deep"},
		"deep binding":  {surfaceArgs(create(`{"id":"root","component":"Text","text":{"@path":"` + strings.Repeat("/a", 33) + `"}}`)), "of at most 32 levels"},
		"deep model":    {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/a","value":`+strings.Repeat("[", 32)+strings.Repeat("]", 32)+`}}`), "nests more than 32 levels"},
		"overflow":      {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/n","value":1e400}}`), `"value": json: cannot unmarshal number 1e400`},
		"overflow data": {surfaceArgs(`{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"urn:uam:catalog:v1","components":[` + root + `],"dataModel":{"n":-1e400}}}`), `"dataModel" must be an object of finite numbers`},
		"overflow prop": {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","weight":1e400}`)), "cannot unmarshal number 1e400"},
		"dash delete":   {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l","value":[1]}}`, `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/-","value":null}}`), `"-" only appends a value to a list`},
		"dash through":  {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l","value":[1]}}`, `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/-/x","value":2}}`), `"-" only appends a value to a list`},
		"root list":     {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/","value":[1]}}`), `without a path, or at "/", "value" must be an object`},
		// 13 levels of a component referring twice to the next show 2^14-1.
		"shared": {surfaceArgs(create(sharedChain(13))), `would show at least 2001 components`},
		// 50 items, each a row of 50: 2,551 components.
		"template": {surfaceArgs(`{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"urn:uam:catalog:v1","components":[
 {"id":"root","component":"Column","children":{"componentId":"row","path":"/rows"}},
 {"id":"row","component":"Row","children":{"componentId":"cell","path":"/cells"}},
 {"id":"cell","component":"Text","text":"x"}],
 "dataModel":{"rows":[` + strings.Repeat("{},", 49) + `{}],"cells":[` + strings.Repeat("1,", 49) + `1]}}}`), `would show at least 2001 components`},
	} {
		state := &threadSurfaces{}
		if _, _, err := applySurfaceArgs(state, "c", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}

	// The data model and component limits hold across calls.
	big := func(k string) string {
		return `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/` + k + `","value":"` + strings.Repeat("x", 15000) + `"}}`
	}
	text := func(k string) string {
		return `{"version":"1.0","updateComponents":{"surfaceId":"s","components":[{"id":"` + k + `","component":"Text","text":"` + strings.Repeat("x", 15000) + `"}]}}`
	}
	for _, step := range []struct {
		next func(string) string
		want string
	}{{big, "data model is"}, {text, "its components are"}} {
		state := &threadSurfaces{}
		_, _, err := applySurfaceArgs(state, "c", surfaceArgs(create(root)))
		for _, k := range []string{"a", "b", "c", "d", "e"} {
			if err != nil {
				break
			}
			_, _, err = applySurfaceArgs(state, "c", surfaceArgs(step.next(k)))
		}
		if err == nil || !strings.Contains(err.Error(), step.want) {
			t.Errorf("limit %q: err = %v", step.want, err)
		}
	}

	// The surface limit counts deleted surfaces too; a deleted id is reused.
	state := &threadSurfaces{}
	for i := range maxSurfaces {
		state.Surfaces = append(state.Surfaces, &surface{SurfaceID: string(rune('a' + i)), Deleted: i == 0})
	}
	if _, _, err := applySurfaceArgs(state, "c", surfaceArgs(create(root))); err == nil || !strings.Contains(err.Error(), "the limit") {
		t.Errorf("ninth surface: err = %v", err)
	}
	if _, _, err := applySurfaceArgs(state, "c", surfaceArgs(surfaceCreate("a", root))); err != nil || len(state.Retired) != 1 {
		t.Errorf("reusing a deleted id: %v, retired %d", err, len(state.Retired))
	}
}

// JSON Pointer tokens decode ~1 then ~0, allow empty keys, and a deletion
// of what is not there changes nothing.
func TestSurfaceSetPointer(t *testing.T) {
	doc := map[string]any{"l": []any{"a", "b"}}
	steps := []struct {
		path  string
		value any
	}{
		{"/x~1y", 1.0}, {"/x~0y", 2.0}, {"/~01", 3.0}, {"/e//k", 5.0},
		{"/l/-", "c"}, {"/l/0", nil}, {"/l/2", "d"}, {"/gone/deep", nil}, {"/l/3/x", nil},
	}
	var got any = doc
	for _, s := range steps {
		var err error
		if got, err = setPointer(got, s.path, s.value); err != nil {
			t.Fatalf("%s: %v", s.path, err)
		}
	}
	b, _ := json.Marshal(got)
	if want := `{"e":{"":{"k":5}},"l":["b","c","d"],"x/y":1,"x~y":2,"~1":3}`; string(b) != want {
		t.Errorf("doc = %s, want %s", b, want)
	}
	// "/" is the whole model (A2UI v1.0), as no path is.
	if got, err := setPointer(doc, "/", map[string]any{"a": 1.0}); err != nil || len(got.(map[string]any)) != 1 {
		t.Errorf(`"/" = %v, %v`, got, err)
	}
}

// sharedChain is a root and n levels of components, each referring to
// the next twice.
func sharedChain(n int) string {
	parts := []string{`{"id":"root","component":"Row","children":["c1","c1"]}`}
	for i := 1; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":"c%d","component":"Row","children":["c%d","c%d"]}`, i, i+1, i+1))
	}
	return strings.Join(append(parts, fmt.Sprintf(`{"id":"c%d","component":"Text","text":"x"}`, n)), ",")
}

// The rendered count stops past the limit, however far a shared chain
// would go, and counts a template's items where the list is now.
func TestSurfaceRenderedCount(t *testing.T) {
	state := &threadSurfaces{}
	if _, _, err := applySurfaceArgs(state, "c", surfaceArgs(surfaceCreate("s", sharedChain(9)))); err != nil {
		t.Fatalf("1,023 shown: %v", err)
	}
	parts := map[string]map[string]any{}
	var comps []map[string]any
	if err := json.Unmarshal([]byte("["+sharedChain(60)+"]"), &comps); err != nil {
		t.Fatal(err)
	}
	for _, c := range comps {
		parts[c["id"].(string)] = c
	}
	if n := renderedCount(parts, nil); n != maxSurfaceRender+1 {
		t.Errorf("chain of 60 counted %d", n)
	}
	// A template under the root's base reads its list relative to the item.
	parts = map[string]map[string]any{
		"root": {"id": "root", "component": "Column", "children": map[string]any{"componentId": "g", "path": "/groups"}},
		"g":    {"id": "g", "component": "Column", "children": map[string]any{"componentId": "t", "path": "items"}},
		"t":    {"id": "t", "component": "Text", "text": map[string]any{"@path": "name"}},
	}
	data := map[string]any{"groups": map[string]any{"a/b": map[string]any{"items": []any{1.0, 2.0}}, "c": map[string]any{"items": []any{1.0}}}}
	if n := renderedCount(parts, data); n != 6 {
		t.Errorf("groups counted %d, want 6", n)
	}
}

// Whole-model writes at "/" or no path, appends at the end of a list.
func TestSurfaceDataModelWrites(t *testing.T) {
	state := &threadSurfaces{}
	_, _, err := applySurfaceArgs(state, "c", surfaceArgs(surfaceCreate("s", `{"id":"root","component":"Text","text":"hi"}`),
		`{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/old","value":1}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/","value":{"l":[]}}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/-","value":"a"}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/1","value":"b"}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/l/0","value":"A"}}`))
	if b, _ := json.Marshal(state.byID("s").DataModel); err != nil || string(b) != `{"l":["A","b"]}` {
		t.Errorf("data model = %s, %v", b, err)
	}
	_, _, err = applySurfaceArgs(state, "c2", surfaceArgs(`{"version":"1.0","updateDataModel":{"surfaceId":"s","value":{}}}`))
	if b, _ := json.Marshal(state.byID("s").DataModel); err != nil || string(b) != `{}` {
		t.Errorf("no path = %s, %v", b, err)
	}
}

// callSurface calls uam_surface in conv's Task with args.
func callSurface(t *testing.T, conv *agenttest.Conversation, callID, args string) agentapi.HostToolResult {
	t.Helper()
	res, err := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: surfaceToolName, CallID: callID, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A Thread's surfaces are kept in its surfaces file: a new manager reads
// them, a deleted surface still answers for its calls, and deleting the
// Thread removes the file.
func TestSurfacesPersistPerThread(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	root := `{"id":"root","component":"Text","text":{"@path":"/t"}}`
	steps := []struct{ call, args string }{
		{"c1", string(surfaceArgs(surfaceCreate("a", root)))},
		{"c2", string(surfaceArgs(`{"version":"1.0","updateDataModel":{"surfaceId":"a","path":"/t","value":"hi"}}`))},
		{"c3", string(surfaceArgs(surfaceCreate("b", root), `{"version":"1.0","deleteSurface":{"surfaceId":"b"}}`))},
		{"c4", string(surfaceArgs(`{"version":"1.0","deleteSurface":{"surfaceId":"a"}}`))},
		{"c5", string(surfaceArgs(surfaceCreate("a", root)))},
	}
	for _, s := range steps {
		if res := callSurface(t, conv, s.call, s.args); res.Failed {
			t.Fatalf("%s: %s", s.call, res.Text)
		}
	}
	path := filepath.Join(m.taskUploadDir(sum.ID), surfacesFile)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("surfaces file: %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(sum.ConversationID, nil)
	m = startManager(t, st, prov2)
	for call, want := range map[string]string{
		"c1": `"surface_id":"a","call_id":"c1","catalog_id":"urn:uam:catalog:v1","version":3,"deleted":true,"components":[],"data_model":{}`,
		"c3": `"surface_id":"b","call_id":"c3","catalog_id":"urn:uam:catalog:v1","version":2,"deleted":true`,
		"c5": `"surface_id":"a","call_id":"c5","catalog_id":"urn:uam:catalog:v1","version":4,"deleted":false,"components":[{"component":"Text","id":"root"`,
	} {
		s, err := m.TaskSurface(sum.ID, call)
		b, _ := json.Marshal(s)
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s = %s, %v", call, b, err)
		}
	}
	for _, call := range []string{"c2", "c4", "nope", ""} {
		if _, err := m.TaskSurface(sum.ID, call); statusOf(err) != http.StatusNotFound {
			t.Errorf("%s: %v, want 404", call, err)
		}
	}
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("surfaces file after delete: %v", err)
	}
}

// The compact transcript marks the call that created a surface, from its
// own result, so the mark outlives the surface and a restart.
func TestCompactToolMarksCreatedSurface(t *testing.T) {
	for name, tc := range map[string]struct {
		tool agentapi.ToolCall
		want string
	}{
		"created": {agentapi.ToolCall{Name: surfaceToolName, Status: agentapi.ToolCompleted, Output: "created surface build-board\nupdated data model of build-board at \"/a\""}, "build-board"},
		"update":  {agentapi.ToolCall{Name: surfaceToolName, Status: agentapi.ToolCompleted, Output: "updated data model of build-board at \"\""}, ""},
		"failed":  {agentapi.ToolCall{Name: surfaceToolName, Status: agentapi.ToolFailed, Output: "created surface x"}, ""},
		"other":   {agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Output: "created surface x"}, ""},
		"bad id":  {agentapi.ToolCall{Name: surfaceToolName, Status: agentapi.ToolCompleted, Output: "created surface a b"}, ""},
	} {
		tool := tc.tool
		it := projectItem(agentapi.Item{ID: "i", Kind: agentapi.ItemTool, Tool: &tool})
		b, _ := json.Marshal(it)
		if it.Tool.SurfaceID != tc.want || tc.want == "" && strings.Contains(string(b), "surface_id") {
			t.Errorf("%s: surface_id %q in %s", name, it.Tool.SurfaceID, b)
		}
	}
}

// Over HTTP: the surface reads by its call, and a Button press queues the
// one-line action prompt; presses that do not match the card are refused,
// and a Thread never reaches another Thread's surfaces.
func TestSurfaceActionOverHTTP(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := busySession(t, ts.m, ts.prov)
	other, _ := createSession(t, ts.m, ts.prov)
	if res := callSurface(t, conv, "c1", string(surfaceDoc(t))); res.Failed {
		t.Fatal(res.Text)
	}
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/surface?call=c1", "", withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"surface_id":"ci","call_id":"c1"`) {
		t.Fatalf("GET surface = %d %s", w.Code, w.Body.String())
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+other.ID+"/surface?call=c1", "", withCookie(ts)); w.Code != http.StatusNotFound {
		t.Fatalf("another thread's surface = %d", w.Code)
	}
	post := func(id, body string) *httptest.ResponseRecorder {
		return ts.do(http.MethodPost, "/api/sessions/"+id+"/surface/action", body, withCookie(ts))
	}
	press := `{"surface_id":"ci","name":"retry","source_component_id":"retry","context":{"job":"test"},"base_path":"/jobs/1","request_id":"0f8fd0b2-6a8e-4f53-9d3c-3b1c1f1e2a10"}`
	for name, tc := range map[string]struct {
		id, body string
		code     int
	}{
		"other thread":  {other.ID, press, http.StatusNotFound},
		"no surface":    {sum.ID, `{"surface_id":"x","name":"retry","source_component_id":"retry"}`, http.StatusNotFound},
		"not a button":  {sum.ID, `{"surface_id":"ci","name":"retry","source_component_id":"rl"}`, http.StatusBadRequest},
		"wrong name":    {sum.ID, `{"surface_id":"ci","name":"other","source_component_id":"retry"}`, http.StatusBadRequest},
		"undeclared":    {sum.ID, `{"surface_id":"ci","name":"retry","source_component_id":"retry","context":{"cmd":"rm"}}`, http.StatusBadRequest},
		"array context": {sum.ID, `{"surface_id":"ci","name":"retry","source_component_id":"retry","context":[1]}`, http.StatusBadRequest},
		"bad id":        {sum.ID, `{"surface_id":"../x","name":"retry","source_component_id":"retry"}`, http.StatusBadRequest},
		"big context":   {sum.ID, `{"surface_id":"ci","name":"retry","source_component_id":"retry","context":{"job":"` + strings.Repeat("x", maxSurfaceContext) + `"}}`, http.StatusRequestEntityTooLarge},
		"request id":    {sum.ID, `{"surface_id":"ci","name":"retry","source_component_id":"retry","request_id":"press-1"}`, http.StatusBadRequest},
		"base path":     {sum.ID, `{"surface_id":"ci","name":"retry","source_component_id":"retry","base_path":"jobs/1"}`, http.StatusBadRequest},
	} {
		if w := post(tc.id, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body.String(), tc.code)
		}
	}
	// A retried press with the same request ID queues nothing new.
	for range 2 {
		if w := post(sum.ID, press); w.Code != http.StatusAccepted {
			t.Fatalf("press = %d %s", w.Code, w.Body.String())
		}
	}
	queue := detail(t, ts.m, sum.ID).Queue
	line := regexp.MustCompile(`^\[uam_surface action\] \{"version":"1\.0","action":\{"name":"retry","surfaceId":"ci","sourceComponentId":"retry","timestamp":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ","context":\{"job":"test"\},"label":"Retry","title":"CI on main"\}\}$`)
	if len(queue) != 1 || !line.MatchString(queue[0].Text) {
		t.Fatalf("queued = %+v", queue)
	}
	if res := callSurface(t, conv, "c2", string(surfaceArgs(`{"version":"1.0","deleteSurface":{"surfaceId":"ci"}}`))); res.Failed {
		t.Fatal(res.Text)
	}
	if w := post(sum.ID, strings.Replace(press, "0f8fd0b2", "1f8fd0b2", 1)); w.Code != http.StatusNotFound {
		t.Fatalf("press on a deleted card = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/surface?call=c1", "", withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatalf("GET deleted surface = %d %s", w.Code, w.Body.String())
	}
}

// releaseCard is the mock's release notes card, which uses the six media,
// icon, modal and date components, so the validator and the React mock agree.
func releaseCard(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../../web/packages/ui/src/mock/surfaces/release.json")
	if err != nil {
		t.Fatal(err)
	}
	var card struct{ Create json.RawMessage }
	if err := json.Unmarshal(b, &card); err != nil {
		t.Fatal(err)
	}
	return card.Create
}

// The raw file route serves surface audio and video by extension, with
// Range, from the working folder only; images are still sniffed.
func TestSurfaceMediaServed(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"clip.mp4": "0123456789", "fake.png": "not a png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.mp4")); err != nil {
		t.Fatal(err)
	}
	f, err := openImage(dir, "clip.mp4")
	if err != nil || f.MIME != "video/mp4" {
		t.Fatalf("clip.mp4: %v", err)
	}
	defer func() { _ = f.File.Close() }()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Range", "bytes=2-4")
	w := httptest.NewRecorder()
	http.ServeContent(w, r, "", f.Info.ModTime(), f.File)
	if w.Code != http.StatusPartialContent || w.Body.String() != "234" {
		t.Errorf("range: %d %q", w.Code, w.Body.String())
	}
	for _, p := range []string{"../clip.mp4", "fake.png", "clip.mov", "link.mp4", outside} {
		if _, err := openImage(dir, p); err == nil {
			t.Errorf("%s was served", p)
		}
	}
}

// A connected instance's surface media seek: Range goes to the instance and
// its partial answer comes back whole.
func TestConnectedSurfaceMediaRange(t *testing.T) {
	f := newConnectedProxyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/federation/workload/api/sessions/task/files/raw" || r.Header.Get("Range") != "bytes=2-4" {
			http.Error(w, "unexpected", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 2-4/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "234")
	})
	r := f.request(t, "GET", "/api/sessions/task/files/raw?path=clip.mp4", "", http.Header{"Range": {"bytes=2-4"}})
	if body := connectedBody(t, r); r.StatusCode != http.StatusPartialContent || body != "234" || r.Header.Get("Content-Range") != "bytes 2-4/10" || r.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("ranged media = %d %q %v", r.StatusCode, body, r.Header)
	}
}

// An action line names the press: the Button's text in the pressed
// template item, and the card's title Text, else its first Text.
func TestSurfaceActionNames(t *testing.T) {
	var comps []map[string]any
	if err := json.Unmarshal([]byte(`[
 {"id":"root","component":"Card","child":"col"},
 {"id":"col","component":"Column","children":["h","t","rows"]},
 {"id":"h","component":"Text","text":"  "},
 {"id":"t","component":"Text","text":{"@path":"/title"},"variant":"title"},
 {"id":"rows","component":"List","children":{"componentId":"b","path":"/jobs"}},
 {"id":"b","component":"Button","child":"bl","action":{"event":{"name":"go"}}},
 {"id":"bl","component":"Row","children":["i","l"]},
 {"id":"i","component":"Icon","name":"check"},
 {"id":"l","component":"Text","text":{"@path":"name"},"variant":"caption"}]`), &comps); err != nil {
		t.Fatal(err)
	}
	parts := map[string]map[string]any{}
	for _, c := range comps {
		parts[c["id"].(string)] = c
	}
	data := map[string]any{"title": "Deploys", "jobs": []any{map[string]any{"name": "alpha"}, map[string]any{"name": 2.5}}}
	for _, tc := range []struct{ id, base, variant, want string }{
		{"bl", "/jobs/1", "", "2.5"}, {"bl", "/jobs/0", "", "alpha"}, {"bl", "/jobs/9", "", ""},
		{"root", "", "title", "Deploys"}, {"col", "", "caption", ""},
	} {
		if got := surfaceText(parts, data, tc.id, tc.base, tc.variant); got != tc.want {
			t.Errorf("%s at %q (%s) = %q, want %q", tc.id, tc.base, tc.variant, got, tc.want)
		}
	}
}

// A surfaces file uam cannot read, or one over the size limit, is refused
// and kept, never replaced by an empty set.
func TestSurfacesUnreadableFileKept(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	root := `{"id":"root","component":"Text","text":"hi"}`
	if res := callSurface(t, conv, "c1", string(surfaceArgs(surfaceCreate("a", root)))); res.Failed {
		t.Fatal(res.Text)
	}
	path := m.surfacesPath(sum.ID)
	for name, body := range map[string][]byte{
		"corrupt":   []byte(`{"surfaces":[`),
		"oversized": append([]byte(`{"surfaces":[]}`), bytes.Repeat([]byte(" "), maxSurfacesFile)...),
	} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		res := callSurface(t, conv, "c2", string(surfaceArgs(surfaceCreate("b", root))))
		if got, err := os.ReadFile(path); !res.Failed || err != nil || !bytes.Equal(got, body) {
			t.Errorf("%s: call failed %v (%s); file kept %v", name, res.Failed, res.Text, bytes.Equal(got, body))
		}
		if _, err := m.TaskSurface(sum.ID, "c1"); err == nil || statusOf(err) == http.StatusNotFound {
			t.Errorf("%s: read = %v, want an error", name, err)
		}
	}
}
