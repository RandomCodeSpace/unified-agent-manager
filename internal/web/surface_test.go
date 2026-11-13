// PROTOTYPE: uam_surface (A2UI v1.0). Throwaway.

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func surfaceArgs(msgs ...string) json.RawMessage {
	return json.RawMessage(`{"messages":[` + strings.Join(msgs, ",") + `]}`)
}

// Adapted from a2ui catalogs/basic/v1/examples/00_interactive-button.json.
const surfaceButtonExample = `{"version":"1.0","createSurface":{"surfaceId":"gallery-interactive-button","catalogId":"urn:uam:catalog:v1"}},
{"version":"1.0","updateComponents":{"surfaceId":"gallery-interactive-button","components":[
 {"id":"root","component":"Column","children":["title","action_button"],"justify":"center","align":"center"},
 {"id":"title","component":"Text","text":"Click the button below","variant":"body"},
 {"id":"action_button","component":"Button","child":"button_label","variant":"primary","action":{"event":{"name":"button_clicked","context":{}}}},
 {"id":"button_label","component":"Text","text":"Click Me"}]}}`

// Adapted from 07_task-card.json: no sendDataModel; DateTimeInput and Icon,
// which the catalog lacks, become Text.
const surfaceTaskExample = `{"version":"1.0","createSurface":{"surfaceId":"gallery-task-card","catalogId":"urn:uam:catalog:v1"}},
{"version":"1.0","updateComponents":{"surfaceId":"gallery-task-card","components":[
 {"id":"root","component":"Card","child":"main_row"},
 {"id":"main_row","component":"Row","children":["status_checkbox","content","priority"],"align":"start"},
 {"id":"status_checkbox","component":"CheckBox","label":"","value":{"@path":"/completed"}},
 {"id":"content","component":"Column","children":["title","description","meta_row"]},
 {"id":"title","component":"Text","text":{"@path":"/title"}},
 {"id":"description","component":"Text","text":{"@path":"/description"},"variant":"body"},
 {"id":"meta_row","component":"Row","children":["due_date","project"],"align":"center"},
 {"id":"due_date","component":"Text","text":{"@path":"/dueDate"},"variant":"caption"},
 {"id":"project","component":"Text","text":{"@path":"/project"},"variant":"caption"},
 {"id":"priority","component":"Status","label":"Priority","tone":{"@path":"/tone"}}]}},
{"version":"1.0","updateDataModel":{"surfaceId":"gallery-task-card","value":{"completed":false,"title":"Review pull request","description":"Review the auth changes.","dueDate":"2025-12-15","project":"Backend","tone":"warn"}}}`

func TestSurfaceAccepts(t *testing.T) {
	doc, err := builtinSkills.ReadFile("skills/uam/references/surfaces.md")
	if err != nil {
		t.Fatal(err)
	}
	docExample := regexp.MustCompile("(?s)```json\n(.*?)```").FindSubmatch(doc)
	if docExample == nil {
		t.Fatal("surfaces.md has no json example")
	}
	for name, args := range map[string]json.RawMessage{
		"button":  surfaceArgs(surfaceButtonExample),
		"task":    surfaceArgs(surfaceTaskExample),
		"doc":     docExample[1],
		"release": releaseCard(t),
		// A media path may also be absolute under the working folder.
		"absolute": surfaceArgs(`{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"urn:uam:catalog:v1","components":[
 {"id":"root","component":"Column","children":["v","t"]},
 {"id":"v","component":"Video","url":"/w/out/clip.webm","posterUrl":"out/./clip.PNG"},
 {"id":"t","component":"DateTimeInput","value":"2026-11-13T14:30:05Z","enableDate":true,"enableTime":true,"min":"2026-11-13T09:00"}]}}`),
	} {
		got, changed, lines, err := applySurfaceCall(nil, "call-1", "/w", args)
		if err != nil || len(changed) != 1 || len(lines) == 0 {
			t.Fatalf("%s: refused: %v", name, err)
		}
		for _, s := range got {
			if s.callID != "call-1" || s.version != len(lines) {
				t.Errorf("%s: call %q version %d, want call-1 version %d", name, s.callID, s.version, len(lines))
			}
		}
	}

	// The doc's follow-up update, then an append and a delete.
	state, _, _, _ := applySurfaceCall(nil, "c1", "/w", docExample[1])
	state, _, _, err = applySurfaceCall(state, "c2", "/w", surfaceArgs(
		`{"version":"1.0","updateDataModel":{"surfaceId":"ci","path":"/jobs/0/tone","value":"ok"}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"ci","path":"/jobs/-","value":{"name":"e2e","tone":"neutral"}}}`,
		`{"version":"1.0","updateDataModel":{"surfaceId":"ci","path":"/jobs/1","value":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(state["ci"])
	if want := `"data_model":{"jobs":[{"name":"lint","tone":"ok"},{"name":"e2e","tone":"neutral"}]}`; !strings.Contains(string(b), want) || !strings.Contains(string(b), `"call_id":"c1","catalog_id":"urn:uam:catalog:v1","version":4`) {
		t.Errorf("surface JSON = %s", b)
	}
}

func TestSurfaceRefuses(t *testing.T) {
	create := func(components string) string {
		return `{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"urn:uam:catalog:v1","components":[` + components + `]}}`
	}
	root := `{"id":"root","component":"Text","text":"hi"}`
	for name, tc := range map[string]struct {
		args json.RawMessage
		want string
	}{
		"version":       {surfaceArgs(`{"version":"0.9","deleteSurface":{"surfaceId":"s"}}`), `"version" must be "1.0"`},
		"two kinds":     {surfaceArgs(`{"version":"1.0","deleteSurface":{"surfaceId":"s"},"updateDataModel":{"surfaceId":"s","value":1}}`), "exactly one of"},
		"unsupported":   {surfaceArgs(`{"version":"1.0","callRendererFunction":{}}`), "unknown field"},
		"catalog":       {surfaceArgs(`{"version":"1.0","createSurface":{"surfaceId":"s","catalogId":"x"}}`), `"catalogId" must be`},
		"no root":       {surfaceArgs(create(`{"id":"a","component":"Text","text":"hi"}`)), `no component with id "root"`},
		"unknown":       {surfaceArgs(create(`{"id":"root","component":"Map","url":"x"}`)), `component "root": "Map" is not in the catalog`},
		"prop":          {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","color":"red"}`)), `component "root" (Text) "color" is not a property`},
		"enum":          {surfaceArgs(create(`{"id":"root","component":"Text","text":"a","variant":"h1"}`)), `"variant" must be one of caption, body`},
		"required":      {surfaceArgs(create(`{"id":"root","component":"Card"}`)), `component "root" (Card) "child" is required`},
		"dangling":      {surfaceArgs(create(`{"id":"root","component":"Card","child":"x"}`)), `component "root" refers to "x"`},
		"duplicate":     {surfaceArgs(create(root + `,` + root)), `component id "root" appears twice`},
		"cycle":         {surfaceArgs(create(`{"id":"root","component":"Card","child":"a"},{"id":"a","component":"Column","children":["root"]}`)), "contains itself"},
		"pointer":       {surfaceArgs(create(`{"id":"root","component":"Text","text":{"@path":"/a~2"}}`)), "valid JSON Pointer"},
		"call":          {surfaceArgs(create(`{"id":"root","component":"Text","text":{"@call":"now"}}`)), "@call is not supported"},
		"function":      {surfaceArgs(create(`{"id":"root","component":"Button","child":"root","action":{"functionCall":{"call":"x"}}}`)), "functionCall is not supported"},
		"missing":       {surfaceArgs(`{"version":"1.0","updateDataModel":{"surfaceId":"nope","value":{}}}`), `there is no surface "nope"`},
		"path":          {surfaceArgs(create(root), `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"a/b","value":1}}`), "is not a JSON Pointer"},
		"media https":   {surfaceArgs(create(`{"id":"root","component":"Image","url":"https://example.com/a.png"}`)), `(Image) "url" must be the literal path of a file in the thread's working folder; save the file`},
		"media http":    {surfaceArgs(create(`{"id":"root","component":"Video","url":"http://example.com/a.mp4"}`)), `(Video) "url" must be the literal path`},
		"media data":    {surfaceArgs(create(`{"id":"root","component":"Image","url":"data:image/png;base64,AAAA"}`)), `(Image) "url" must be the literal path`},
		"media file":    {surfaceArgs(create(`{"id":"root","component":"AudioPlayer","url":"file:///w/a.mp3"}`)), `(AudioPlayer) "url" must be the literal path`},
		"media host":    {surfaceArgs(create(`{"id":"root","component":"Image","url":"//example.com/a.png"}`)), `(Image) "url" must be the literal path`},
		"media escape":  {surfaceArgs(create(`{"id":"root","component":"Image","url":"shots/../../a.png"}`)), `(Image) "url" must be the literal path`},
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
	} {
		next, _, _, err := applySurfaceCall(nil, "c", "/w", tc.args)
		if err == nil || next != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}

	// The data model limit holds across calls.
	big := func(k string) string {
		return `{"version":"1.0","updateDataModel":{"surfaceId":"s","path":"/` + k + `","value":"` + strings.Repeat("x", 15000) + `"}}`
	}
	state, _, _, err := applySurfaceCall(nil, "c", "/w", surfaceArgs(create(root), big("a")))
	for _, k := range []string{"b", "c", "d", "e"} {
		if err != nil {
			break
		}
		state, _, _, err = applySurfaceCall(state, "c", "/w", surfaceArgs(big(k)))
	}
	if err == nil || !strings.Contains(err.Error(), "data model is") {
		t.Errorf("data limit: err = %v", err)
	}

	// The surface limit counts every surface the Thread has.
	state = map[string]*surface{}
	for i := range maxSurfaces {
		state[string(rune('a'+i))] = &surface{}
	}
	if _, _, _, err := applySurfaceCall(state, "c", "/w", surfaceArgs(create(root))); err == nil || !strings.Contains(err.Error(), "the limit") {
		t.Errorf("ninth surface: err = %v", err)
	}
}

func TestSurfaceActionRefuses(t *testing.T) {
	state, _, _, err := applySurfaceCall(nil, "c", "/w", surfaceArgs(surfaceButtonExample))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{surfaces: map[string]map[string]*surface{"t": state}}
	for name, a := range map[string]SurfaceAction{
		"no surface":   {SurfaceID: "x", Name: "button_clicked", SourceComponentID: "action_button"},
		"not a button": {SurfaceID: "gallery-interactive-button", Name: "button_clicked", SourceComponentID: "title"},
		"wrong name":   {SurfaceID: "gallery-interactive-button", Name: "other", SourceComponentID: "action_button"},
		"big context":  {SurfaceID: "gallery-interactive-button", Name: "button_clicked", SourceComponentID: "action_button", Context: json.RawMessage(`{"a":"` + strings.Repeat("x", maxSurfaceContext) + `"}`)},
	} {
		var e *Error
		if _, err := m.surfaceAction("t", a); err == nil || !errors.As(err, &e) || e.Status == http.StatusAccepted {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// releaseCard is the mock's release notes card, which uses the six media,
// icon, modal and date components, so the validator and the React mock agree.
func releaseCard(t *testing.T) json.RawMessage {
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
	for _, p := range []string{"../clip.mp4", "fake.png", "clip.mov"} {
		if _, err := openImage(dir, p); err == nil {
			t.Errorf("%s was served", p)
		}
	}
}
