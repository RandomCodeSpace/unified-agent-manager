package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestSettingsDefaultStoredAndStreamed(t *testing.T) {
	st := openTestStore(t)
	m := startManager(t, st)
	if got := m.Settings(); got.SendDefault != "steer" {
		t.Fatalf("default settings = %+v", got)
	}
	sub, snap, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	if f := parseFrame(t, snap); string(f.data["settings"]) != `{"send_default":"steer","terminal":false}` {
		t.Fatalf("snapshot settings = %s", f.data["settings"])
	}

	if _, err := m.UpdateSettings(SettingsPatch{SendDefault: setting("interrupt")}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown value = %v, want 400", err)
	}
	if _, err := os.Stat(st.Path()); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote the store: %v", err)
	}
	got, err := m.UpdateSettings(SettingsPatch{SendDefault: setting("queue")})
	if err != nil || got.SendDefault != "queue" || !reflect.DeepEqual(m.Settings(), got) {
		t.Fatalf("set queue = %+v, %v", got, err)
	}
	f := frameOf(t, sub, "settings")
	if string(f.data["settings"]) != `{"send_default":"queue","terminal":false}` || f.seq == 0 {
		t.Fatalf("settings frame = %+v", f.data)
	}
	cfg, err := st.Load()
	if err != nil || cfg.WebSettings.SendDefault != store.WebSendQueue {
		t.Fatalf("stored settings = %+v, %v", cfg.WebSettings, err)
	}

	// No change writes nothing and sends nothing.
	before, err := os.Stat(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, same := range []*string{setting("queue"), nil} {
		if got, err := m.UpdateSettings(SettingsPatch{SendDefault: same}); err != nil || got.SendDefault != "queue" {
			t.Fatalf("no change = %+v, %v", got, err)
		}
	}
	if after, err := os.Stat(st.Path()); err != nil || !os.SameFile(before, after) {
		t.Fatalf("an unchanged setting rewrote the store: %v", err)
	}
	if _, err := m.AddProject(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if f := nextFrame(t, sub); f.event != "project" {
		t.Fatalf("frame after an unchanged setting = %s, want the next change", f.event)
	}

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := startManager(t, st).Settings(); got.SendDefault != "queue" {
		t.Fatalf("settings after restart = %+v", got)
	}
}

func TestSettingsLoadInvalidAsSteer(t *testing.T) {
	st := openTestStore(t)
	if err := os.WriteFile(st.Path(), []byte(`{"schema_version":4,"default_agent":"opencode","profiles":{},"sessions":{},"ui":{"sort":"state","peek_width":60},"web_settings":{"send_default":"bogus"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := startManager(t, st).Settings(); got.SendDefault != "steer" {
		t.Fatalf("invalid stored send default loaded as %+v", got)
	}
}

func TestSettingsRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	queue, queued := `{"send_default":"queue"}`, `{"send_default":"queue","terminal":false}`
	if w := ts.do(http.MethodGet, "/api/settings", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET without sign-in = %d", w.Code)
	}
	for name, opts := range map[string][]reqOpt{
		"no sign-in":   nil,
		"foreign host": {auth, withHost("evil.example")},
		"cross-site":   {auth, withHeader("Sec-Fetch-Site", "cross-site")},
		"foreign":      {auth, withHeader("Origin", "https://evil.example")},
		"text/plain":   {auth, withHeader("Content-Type", "text/plain")},
	} {
		if w := ts.do(http.MethodPatch, "/api/settings", queue, opts...); w.Code/100 != 4 {
			t.Fatalf("PATCH with %s = %d %s", name, w.Code, w.Body)
		}
	}
	get := func() string {
		t.Helper()
		w := ts.do(http.MethodGet, "/api/settings", "", auth)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/settings = %d %s", w.Code, w.Body)
		}
		return strings.TrimSpace(w.Body.String())
	}
	if got := get(); got != `{"send_default":"steer","terminal":false}` {
		t.Fatalf("GET default = %s", got)
	}
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH /api/settings %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	if got := patch(queue, http.StatusOK); got != queued {
		t.Fatalf("PATCH queue = %s", got)
	}
	for _, body := range []string{
		`{"send_default":"bogus"}`, `{"send_default":""}`, `{"send_default":null}`, `{"send_default":1}`,
		`{"sendDefault":"steer"}`, `{"send_default":"steer","title_model":"x"}`, `[]`, `{`,
	} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, `"error"`) {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	if got := get(); got != queued {
		t.Fatalf("refused PATCHes changed the settings: %s", got)
	}
	if got := patch(`{}`, http.StatusOK); got != queued {
		t.Fatalf("empty PATCH = %s", got)
	}
	if got := patch(`{"send_default":"steer"}`, http.StatusOK); got != `{"send_default":"steer","terminal":false}` || get() != got {
		t.Fatalf("PATCH steer = %s", got)
	}
}

func TestHiddenModelsStoredStreamedAndNotEnforced(t *testing.T) {
	st := openTestStore(t)
	fake := agenttest.NewProvider("fake", allCaps)
	fake.SetModels(selectionModels(), nil)
	other := agenttest.NewProvider("other", allCaps)
	m := startManager(t, st, fake, other)
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	hide := func(byProvider map[string][]string) (Settings, error) {
		return m.UpdateSettings(SettingsPatch{HiddenModels: byProvider})
	}

	// Sorted and without duplicates; an ID the catalog does not list is kept.
	got, err := hide(map[string][]string{"fake": {"c", "gone", "b", "c"}, "other": {"x"}})
	want := map[string][]string{"fake": {"b", "c", "gone"}, "other": {"x"}}
	if err != nil || !reflect.DeepEqual(got.HiddenModels, want) || got.SendDefault != "steer" {
		t.Fatalf("hide = %+v, %v", got, err)
	}
	f := frameOf(t, sub, "settings")
	if string(f.data["settings"]) != `{"send_default":"steer","terminal":false,"hidden_models":{"fake":["b","c","gone"],"other":["x"]}}` {
		t.Fatalf("settings frame = %s", f.data["settings"])
	}

	// A patch replaces only the providers it names; an empty list shows all.
	if got, err := hide(map[string][]string{"other": {}}); err != nil || !reflect.DeepEqual(got.HiddenModels, map[string][]string{"fake": {"b", "c", "gone"}}) {
		t.Fatalf("clear other = %+v, %v", got, err)
	}
	for name, change := range map[string]map[string][]string{
		"unknown provider":  {"nobody": {"a"}},
		"empty ID":          {"fake": {""}},
		"control character": {"fake": {"a\x07"}},
		"long ID":           {"fake": {strings.Repeat("m", store.MaxHiddenModelBytes+1)}},
		"too many":          {"fake": manyIDs(store.MaxHiddenModels + 1)},
		"every listed":      {"fake": {"a", "b", "c", "auto"}},
	} {
		if _, err := hide(change); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s = %v, want 400", name, err)
		}
	}
	if _, err := hide(map[string][]string{"fake": append(manyIDs(store.MaxHiddenModels), "a", "a")}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("201 distinct IDs = %v, want 400", err)
	}
	if got, err := hide(map[string][]string{"fake": append(manyIDs(store.MaxHiddenModels-1), "a", "a")}); err != nil || len(got.HiddenModels["fake"]) != store.MaxHiddenModels {
		t.Fatalf("200 distinct IDs = %d, %v", len(got.HiddenModels["fake"]), err)
	}
	if _, err := hide(map[string][]string{"fake": {"a", "b", strings.Repeat("m", store.MaxHiddenModelBytes)}}); err != nil {
		t.Fatal(err)
	}
	// Hidden models are a display preference: requests may still use them.
	if _, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &TaskDefaults{Provider: "fake", Model: "a", Mode: "safe"}}); err != nil {
		t.Fatalf("task default on a hidden model: %v", err)
	}
	project, err := m.AddProject(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project.ID, Model: "b"})
	if err != nil {
		t.Fatalf("task on a hidden model: %v", err)
	}
	if _, err := m.SetModel(sum.ID, setting("a"), nil, nil); err != nil {
		t.Fatalf("switch to a hidden model: %v", err)
	}

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantStored := map[string][]string{"fake": {"a", "b", strings.Repeat("m", store.MaxHiddenModelBytes)}}
	if cfg, err := st.Load(); err != nil || !reflect.DeepEqual(cfg.WebSettings.HiddenModels, wantStored) {
		t.Fatalf("stored hidden models = %v, %v", cfg.WebSettings.HiddenModels, err)
	}
	if got := startManager(t, st, fake, other).Settings(); !reflect.DeepEqual(got.HiddenModels, wantStored) {
		t.Fatalf("hidden models after restart = %v", got.HiddenModels)
	}
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("model-%03d", i)
	}
	return out
}

func TestHiddenModelsRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	ts.prov.SetModels(selectionModels(), nil)
	setNow(ts.m, time.Now().Add(time.Hour))
	ts.m.RefreshModels()
	auth := withCookie(ts)
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH /api/settings %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	hidden := `{"send_default":"steer","terminal":false,"hidden_models":{"fake":["a","b"]}}`
	if got := patch(`{"hidden_models":{"fake":["b","a"]}}`, http.StatusOK); got != hidden {
		t.Fatalf("PATCH hidden_models = %s", got)
	}
	for _, body := range []string{
		`{"hidden_models":null}`, `{"hidden_models":["a"]}`, `{"hidden_models":{"fake":null}}`, `{"hidden_models":{"fake":"a"}}`,
		`{"hidden_models":{"fake":[1]}}`, `{"hidden_models":{"nobody":["a"]}}`, `{"hidden_models":{"fake":["a","b","c","auto"]}}`,
		`{"send_default":"queue","hidden_models":{"fake":[""]}}`,
	} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, `"error"`) {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	if w := ts.do(http.MethodGet, "/api/settings", "", auth); strings.TrimSpace(w.Body.String()) != hidden {
		t.Fatalf("GET after refused PATCHes = %s", w.Body)
	}
	if got := patch(`{"hidden_models":{}}`, http.StatusOK); got != hidden {
		t.Fatalf("empty hidden_models = %s", got)
	}
	if got := patch(`{"hidden_models":{"fake":[]}}`, http.StatusOK); got != `{"send_default":"steer","terminal":false}` {
		t.Fatalf("clear hidden_models = %s", got)
	}
}

func TestHiddenModelsConcurrentRefresh(t *testing.T) {
	fake := agenttest.NewProvider("fake", allCaps)
	fake.SetModels(selectionModels(), nil)
	m := startManager(t, openTestStore(t), fake)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3000; i++ {
			m.mu.Lock()
			m.modelsAt["fake"] = time.Time{}
			m.mu.Unlock()
			m.RefreshModels()
		}
	}()
	for i := 0; i < 3000; i++ {
		_, err := m.UpdateSettings(SettingsPatch{HiddenModels: map[string][]string{"fake": {"a", "b", "c", "auto"}}})
		if statusOf(err) != http.StatusBadRequest {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	<-done
}

// The compaction threshold is checked, stored only away from the default,
// and handed to every conversation UAM creates or reopens.
func TestCompactionThresholdReachesNewAndReopenedTasks(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	for _, body := range []string{`{"compact_threshold":49}`, `{"compact_threshold":91}`, `{"compact_threshold":"60"}`} {
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, w.Code)
		}
	}
	sum, _ := createSession(t, ts.m, ts.prov)
	if got := ts.prov.Last().Request().CompactionThreshold; got != 0 {
		t.Fatalf("default threshold = %v", got)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"compact_threshold":60}`, withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"compact_threshold":60`) {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	if _, err := ts.m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.m.Submit(sum.ID, PromptRequest{Text: "reopen", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if got := ts.prov.Last().Request().CompactionThreshold; got != 0.6 {
		t.Fatalf("reopened threshold = %v", got)
	}
	createSession(t, ts.m, ts.prov)
	if got := ts.prov.Last().Request().CompactionThreshold; got != 0.6 {
		t.Fatalf("new task threshold = %v", got)
	}
	// The default itself is not stored.
	if w := ts.do(http.MethodPatch, "/api/settings", `{"compact_threshold":80}`, withCookie(ts)); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "compact_threshold") {
		t.Fatalf("default = %d %s", w.Code, w.Body)
	}
	if got := ts.m.Settings().CompactionThreshold; got != nil {
		t.Fatalf("stored default = %v", *got)
	}
}

// An open Task reports the threshold its conversation opened with: a
// change in Settings reaches it only when it reopens.
func TestOpenTaskReportsItsCompactionThreshold(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	threshold := func(id string) int {
		ts.m.mu.Lock()
		defer ts.m.mu.Unlock()
		return ts.m.summaryLocked(ts.m.sessions[id]).CompactThreshold
	}
	sum, _ := createSession(t, ts.m, ts.prov)
	if got := threshold(sum.ID); got != 80 {
		t.Fatalf("open with the default: %d", got)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"compact_threshold":60}`, withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	if got := threshold(sum.ID); got != 80 {
		t.Fatalf("still open after the change: %d, want 80", got)
	}
	if _, err := ts.m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if got := threshold(sum.ID); got != 0 {
		t.Fatalf("closed: %d, want none", got)
	}
	if _, err := ts.m.Submit(sum.ID, PromptRequest{Text: "reopen", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the reopened threshold", func() bool { return threshold(sum.ID) == 60 })
}

// githubMCPProvider records each GitHub MCP switch it is given, as the
// Copilot adapter takes them.
type githubMCPProvider struct {
	agentapi.Provider
	mu       sync.Mutex
	switches []bool
}

func (p *githubMCPProvider) SetGitHubMCP(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.switches = append(p.switches, on)
}

func (p *githubMCPProvider) given() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.switches)
}

// The built-in GitHub MCP server is off until Settings turns it on, also for
// a store saved before the setting existed; the setting is checked, stored,
// streamed, given to the provider and kept across a restart.
func TestGitHubMCPSetting(t *testing.T) {
	st := openTestStore(t)
	if err := st.Update(func(cfg *store.Config) error { cfg.WebSettings.Terminal = true; return nil }); err != nil {
		t.Fatal(err)
	}
	prov := &githubMCPProvider{Provider: agenttest.NewProvider("fake", allCaps)}
	m := startManager(t, st, prov)
	if m.Settings().GitHubMCP || !slices.Equal(prov.given(), []bool{false}) {
		t.Fatalf("default = %v, provider given %v", m.Settings().GitHubMCP, prov.given())
	}
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m}
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts))
		if w.Code != want {
			t.Fatalf("PATCH %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	for _, body := range []string{`{"github_mcp":null}`, `{"github_mcp":"true"}`, `{"github_mcp":1}`} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, "github_mcp must be true or false") {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	on := `{"send_default":"steer","terminal":true,"github_mcp":true}`
	if got := patch(`{"github_mcp":true}`, http.StatusOK); got != on {
		t.Fatalf("PATCH github_mcp = %s", got)
	}
	if f := frameOf(t, sub, "settings"); string(f.data["settings"]) != on {
		t.Fatalf("settings frame = %s", f.data["settings"])
	}
	if cfg, err := st.Load(); err != nil || !cfg.WebSettings.GitHubMCP {
		t.Fatalf("stored settings = %+v, %v", cfg.WebSettings, err)
	}
	// No change gives the provider nothing.
	patch(`{"github_mcp":true}`, http.StatusOK)
	if !slices.Equal(prov.given(), []bool{false, true}) {
		t.Fatalf("provider given %v", prov.given())
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := &githubMCPProvider{Provider: agenttest.NewProvider("fake", allCaps)}
	if !startManager(t, st, again).Settings().GitHubMCP || !slices.Equal(again.given(), []bool{true}) {
		t.Fatalf("after a restart the provider was given %v", again.given())
	}
}

// The output style library is checked and stored. A new Thread pins its
// own choice, else its Project's, else the default; later changes to the
// Project or default leave it as it is, while edits to its style's text
// reach its next open.
func TestOutputStyles(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	patch := func(path, body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, path, body, withCookie(ts))
		if w.Code != want {
			t.Fatalf("PATCH %s %s = %d %s, want %d", path, body, w.Code, w.Body, want)
		}
		return w.Body.String()
	}
	for _, body := range []string{`{"output_styles":null}`, `{"output_styles":[{"name":"","text":"x"}]}`, `{"output_styles":[{"name":"copilot","text":"x"}]}`,
		`{"output_styles":[{"name":"A","text":"x"},{"name":"a","text":"y"}]}`, `{"output_styles":[{"name":"A","text":"` + strings.Repeat("é", maxOutputStyleRunes+1) + `"}]}`,
		`{"output_style":"Missing"}`} {
		patch("/api/settings", body, http.StatusBadRequest)
	}
	if got := patch("/api/settings", `{"output_styles":[{"name":" Terse ","text":" Be terse. "},{"name":"Warm","text":"Be warm."}],"output_style":"Terse"}`, http.StatusOK); !strings.Contains(got, `"output_styles":[{"name":"Terse","text":"Be terse."},{"name":"Warm","text":"Be warm."}],"output_style":"Terse"`) {
		t.Fatalf("set = %s", got)
	}
	if cfg, err := ts.m.store.Load(); err != nil || cfg.WebSettings.OutputStyle != "Terse" || len(cfg.WebSettings.OutputStyles) != 2 {
		t.Fatalf("stored=%+v err=%v", cfg.WebSettings, err)
	}
	pid := addProject(t, ts.m, t.TempDir())
	create := func(style string) SessionSummary {
		t.Helper()
		sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: pid, Name: "task", OutputStyle: style})
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	opened := func() string {
		t.Helper()
		opens := ts.prov.Opens()
		return opens[len(opens)-1].OutputStyle
	}
	if sum := create(""); sum.OutputStyle != "Terse" || opened() != "Be terse." {
		t.Fatalf("default: pinned %q, opened with %q", sum.OutputStyle, opened())
	}
	patch("/api/projects/"+pid, `{"output_style":"Missing"}`, http.StatusBadRequest)
	patch("/api/projects/"+pid, `{"output_style":"Warm"}`, http.StatusOK)
	warm := create("")
	if warm.OutputStyle != "Warm" || opened() != "Be warm." {
		t.Fatalf("project: pinned %q, opened with %q", warm.OutputStyle, opened())
	}
	if _, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: pid, Name: "task", OutputStyle: "Missing"}); err == nil {
		t.Fatal("created with a style not in the library")
	}
	if sum := create("Copilot"); sum.OutputStyle != "Copilot" || opened() != "" {
		t.Fatalf("own: pinned %q, opened with %q", sum.OutputStyle, opened())
	}
	// The Project's later change leaves the Thread pinned; the Thread is no
	// longer patchable.
	patch("/api/projects/"+pid, `{"output_style":"Copilot"}`, http.StatusOK)
	s, err := ts.m.lookup(warm.ID)
	if err != nil {
		t.Fatal(err)
	}
	style := func() string {
		ts.m.mu.Lock()
		defer ts.m.mu.Unlock()
		return ts.m.outputStyleLocked(s)
	}
	if style() != "Be warm." {
		t.Fatalf("after a Project change = %q", style())
	}
	patch("/api/sessions/"+warm.ID, `{"output_style":"Terse"}`, http.StatusBadRequest)
	// Dropping the default from the library clears it; a Thread whose style
	// left the library gets the provider's own.
	if got := patch("/api/settings", `{"output_styles":[{"name":"Warm","text":"Warmer."}]}`, http.StatusOK); strings.Contains(got, `"output_style":`) || style() != "Warmer." {
		t.Fatalf("default kept = %s, style %q", got, style())
	}
	patch("/api/settings", `{"output_styles":[{"name":"Calm","text":"Be calm."}]}`, http.StatusOK)
	if style() != "" {
		t.Fatalf("after removal = %q", style())
	}
}

// The built-in style is offered in meta, usable as the default, a Project's
// or a Thread's style without being in the library, and its name is reserved.
func TestBuiltinOutputStyle(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	name := builtinOutputStyles[0].Name
	if w := ts.do(http.MethodGet, "/api/meta", "", withCookie(ts)); !strings.Contains(w.Body.String(), `"output_styles":[{"name":"`+name+`"`) {
		t.Fatalf("meta = %.300s", w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"output_styles":[{"name":"simplified technical english","text":"x"}]}`, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("shadowing name = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"output_style":"`+name+`"}`, withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("default = %d %s", w.Code, w.Body)
	}
	// Saving an empty library keeps a built-in default.
	if w := ts.do(http.MethodPatch, "/api/settings", `{"output_styles":[]}`, withCookie(ts)); !strings.Contains(w.Body.String(), `"output_style":"`+name+`"`) {
		t.Fatalf("default after an empty library = %s", w.Body)
	}
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: addProject(t, ts.m, t.TempDir()), Name: "task"})
	if err != nil {
		t.Fatal(err)
	}
	opens := ts.prov.Opens()
	if sum.OutputStyle != name || opens[len(opens)-1].OutputStyle != builtinOutputStyles[0].Text {
		t.Fatalf("pinned %q, opened with %.40q", sum.OutputStyle, opens[len(opens)-1].OutputStyle)
	}
}
