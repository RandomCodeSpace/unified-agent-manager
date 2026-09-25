package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if f := parseFrame(t, snap); string(f.data["settings"]) != `{"send_default":"steer"}` {
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
	if string(f.data["settings"]) != `{"send_default":"queue"}` || f.seq == 0 {
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
	queue := `{"send_default":"queue"}`
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
	if got := get(); got != `{"send_default":"steer"}` {
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
	if got := patch(queue, http.StatusOK); got != queue {
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
	if got := get(); got != queue {
		t.Fatalf("refused PATCHes changed the settings: %s", got)
	}
	if got := patch(`{}`, http.StatusOK); got != queue {
		t.Fatalf("empty PATCH = %s", got)
	}
	if got := patch(`{"send_default":"steer"}`, http.StatusOK); got != `{"send_default":"steer"}` || get() != got {
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
	if string(f.data["settings"]) != `{"send_default":"steer","hidden_models":{"fake":["b","c","gone"],"other":["x"]}}` {
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
	hidden := `{"send_default":"steer","hidden_models":{"fake":["a","b"]}}`
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
	if got := patch(`{"hidden_models":{"fake":[]}}`, http.StatusOK); got != `{"send_default":"steer"}` {
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
