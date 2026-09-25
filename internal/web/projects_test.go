package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// nextFrame returns the next queued frame of sub.
func nextFrame(t *testing.T, sub *Subscriber) frame {
	t.Helper()
	select {
	case raw := <-sub.Frames():
		return parseFrame(t, raw)
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
		return frame{}
	}
}

// frameOf skips frames until one named event arrives.
func frameOf(t *testing.T, sub *Subscriber, event string) frame {
	t.Helper()
	for {
		if f := nextFrame(t, sub); f.event == event {
			return f
		}
	}
}

func decodeField(t *testing.T, f frame, field string, v any) {
	t.Helper()
	if err := json.Unmarshal(f.data[field], v); err != nil {
		t.Fatalf("%s frame field %s: %v (%s)", f.event, field, err, f.data[field])
	}
}

func loadRecord(t *testing.T, st *store.Store, provider, id string) (store.SessionRecord, bool) {
	t.Helper()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := cfg.Sessions[store.Key(provider, id)]
	return rec, ok
}

func TestProjectsAddRenameAndOnePerDirectory(t *testing.T) {
	m, _, st := newTestManager(t)
	sub, snapRaw, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	if snap := parseFrame(t, snapRaw); string(snap.data["projects"]) != "[]" {
		t.Fatalf("snapshot projects = %s", snap.data["projects"])
	}
	dir := t.TempDir()
	p, err := m.AddProject(dir, "")
	if err != nil || p.Name != filepath.Base(dir) || p.ID == "" || !validRequestID(p.ID) {
		t.Fatalf("AddProject = %+v, %v", p, err)
	}
	var announced Project
	decodeField(t, frameOf(t, sub, "project"), "project", &announced)
	if announced.ID != p.ID || announced.Name != p.Name || announced.Dir != p.Dir || !announced.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("project frame = %+v, want %+v", announced, p)
	}
	_, err = m.AddProject(dir+"/.", "other")
	var dup *Error
	if !errors.As(err, &dup) || dup.Status != http.StatusConflict || dup.ProjectID != p.ID {
		t.Fatalf("second project for the directory = %v", err)
	}
	renamed, err := m.UpdateProject(p.ID, "  My \x1b[1mrepo ")
	if err != nil || renamed.Name != "My repo" || renamed.Dir != dir {
		t.Fatalf("UpdateProject = %+v, %v", renamed, err)
	}
	decodeField(t, frameOf(t, sub, "project"), "project", &announced)
	if announced.Name != "My repo" {
		t.Fatalf("rename frame = %+v", announced)
	}
	if reset, err := m.UpdateProject(p.ID, ""); err != nil || reset.Name != filepath.Base(dir) {
		t.Fatalf("empty rename = %+v, %v", reset, err)
	}
	if _, err := m.UpdateProject("missing", "x"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("rename unknown = %v, want 404", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored := cfg.WebProjects[p.ID]; stored.Dir != dir || stored.Name != filepath.Base(dir) || len(cfg.WebProjects) != 1 {
		t.Fatalf("stored projects = %+v", cfg.WebProjects)
	}
}

func TestTaskDefaultsSettingValidatedStoredAndLoaded(t *testing.T) {
	caps := allCaps
	caps.ContextSize = true
	fake := agenttest.NewProvider("fake", caps)
	fake.SetModels(selectionModels(), nil)
	plain := agenttest.NewProvider("plain", allCaps)
	plain.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, fake, plain)
	if got := m.Settings(); got.TaskDefaults != (TaskDefaults{}) {
		t.Fatalf("task defaults before any = %+v", got.TaskDefaults)
	}
	for _, d := range []TaskDefaults{
		{Provider: "missing", Mode: "safe"},
		{Provider: "fake", Model: "gpt-9", Mode: "safe"},
		{Provider: "fake", Model: "b", Effort: "low", Mode: "safe"},
		{Provider: "fake", Model: "auto", Effort: "high", Mode: "safe"},
		{Provider: "plain", Model: "a", ContextSize: "long_context", Mode: "safe"},
		{Provider: "fake", Model: "b", ContextSize: "long_context", Mode: "safe"},
		{Provider: "fake", Model: "a", Mode: "bogus"},
		{Provider: "fake", Model: "a"},
	} {
		if _, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &d}); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("UpdateSettings with task defaults %+v = %v, want 400", d, err)
		}
	}
	if _, err := os.Stat(st.Path()); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote the store: %v", err)
	}
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	want := TaskDefaults{Provider: "fake", Model: "a", Effort: "high", ContextSize: "long_context", Mode: "yolo"}
	got, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &want})
	if err != nil || got.TaskDefaults != want || m.Settings().TaskDefaults != want {
		t.Fatalf("set task defaults = %+v, %v", got, err)
	}
	var streamed Settings
	decodeField(t, frameOf(t, sub, "settings"), "settings", &streamed)
	if streamed.TaskDefaults != want {
		t.Fatalf("settings frame = %+v", streamed)
	}
	// An empty context size is stored as default; the same value again writes nothing.
	bare, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &TaskDefaults{Provider: "plain", Model: "auto", Mode: "safe"}})
	if err != nil || bare.TaskDefaults != (TaskDefaults{Provider: "plain", Model: "auto", ContextSize: "default", Mode: "safe"}) {
		t.Fatalf("bare task defaults = %+v, %v", bare, err)
	}
	before, err := os.Stat(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if same, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &TaskDefaults{Provider: "plain", Model: "auto", ContextSize: "default", Mode: "safe"}}); err != nil || same.TaskDefaults != bare.TaskDefaults {
		t.Fatalf("no change = %+v, %v", same, err)
	}
	if after, err := os.Stat(st.Path()); err != nil || !os.SameFile(before, after) {
		t.Fatalf("an unchanged setting rewrote the store: %v", err)
	}
	if cfg, err := st.Load(); err != nil || cfg.WebSettings.TaskDefaults != store.WebTaskDefaults(bare.TaskDefaults) {
		t.Fatalf("stored task defaults = %+v, %v", cfg.WebSettings.TaskDefaults, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := startManager(t, st, agenttest.NewProvider("fake", caps), agenttest.NewProvider("plain", allCaps)).Settings(); got.TaskDefaults != bare.TaskDefaults {
		t.Fatalf("task defaults after restart = %+v", got)
	}
}

// Task defaults kept per Project by older versions become the setting on
// load: the newest Project's valid ones, once, and the Projects lose them.
func TestProjectDefaultsMigrateIntoSettings(t *testing.T) {
	st := openTestStore(t)
	project := func(id, created, defaults string) string {
		return `"` + id + `":{"id":"` + id + `","name":"` + id + `","dir":"/tmp/` + id + `","created_at":"` + created + `","defaults":` + defaults + `}`
	}
	raw := `{"schema_version":4,"default_agent":"opencode","profiles":{},"sessions":{},"ui":{"sort":"state","peek_width":60},"web_projects":{` + strings.Join([]string{
		project("old", "2026-09-01T00:00:00Z", `{"provider":"fake","model":"a","effort":"","context_size":"default","mode":"safe"}`),
		project("new", "2026-09-20T00:00:00Z", `{"provider":"fake","model":"b","effort":"high","context_size":"","mode":"yolo"}`),
		project("newest-invalid", "2026-09-24T00:00:00Z", `{"provider":"","model":"c","effort":"","context_size":"default","mode":"safe"}`),
	}, ",") + `}}`
	if err := os.WriteFile(st.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := agenttest.NewProvider("fake", allCaps)
	fake.SetModels(selectionModels(), nil)
	m := startManager(t, st, fake)
	want := TaskDefaults{Provider: "fake", Model: "b", Effort: "high", ContextSize: "default", Mode: "yolo"}
	if got := m.Settings(); got.TaskDefaults != want {
		t.Fatalf("migrated task defaults = %+v, want %+v", got.TaskDefaults, want)
	}
	// The next save drops the Projects' defaults and keeps the setting; a
	// later change to the setting is not undone by the old Projects.
	if _, err := m.UpdateProject("old", "renamed"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		WebProjects map[string]map[string]json.RawMessage `json:"web_projects"`
		WebSettings struct {
			TaskDefaults TaskDefaults `json:"task_defaults"`
		} `json:"web_settings"`
	}
	if err := json.Unmarshal(data, &saved); err != nil || saved.WebSettings.TaskDefaults != want {
		t.Fatalf("store after migration: %s, %v", data, err)
	}
	for id, p := range saved.WebProjects {
		if _, has := p["defaults"]; has {
			t.Fatalf("project %s kept defaults after the next save: %s", id, data)
		}
	}
	changed := TaskDefaults{Provider: "fake", Model: "a", Mode: "safe"}
	if _, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &changed}); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := startManager(t, st, agenttest.NewProvider("fake", allCaps)).Settings(); got.TaskDefaults != (TaskDefaults{Provider: "fake", Model: "a", ContextSize: "default", Mode: "safe"}) {
		t.Fatalf("task defaults after restart = %+v", got.TaskDefaults)
	}
}

func TestRemoveProjectRefusedUntilEveryTaskArchivedThenRemovesTasksOnly(t *testing.T) {
	m, prov, st := newTestManager(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := addProject(t, m, dir)
	other := addProject(t, m, t.TempDir())
	busyTask, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	busyConv := prov.Last()
	idleTask, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	idleConv := prov.Last()
	kept, err := m.Create(CreateRequest{Provider: "fake", ProjectID: other})
	if err != nil {
		t.Fatal(err)
	}
	busyConv.EmitTurn(agentapi.TurnWorking, "")
	if err := m.RemoveProject(project); statusOf(err) != http.StatusConflict {
		t.Fatalf("remove with a working task = %v, want 409", err)
	}
	busyConv.EmitInteraction(permissionRequest("p1"))
	busyConv.EmitTurn(agentapi.TurnCompleted, "")
	if err := m.RemoveProject(project); statusOf(err) != http.StatusConflict {
		t.Fatalf("remove with a task awaiting permission = %v, want 409", err)
	}
	if _, err := m.Answer(busyTask.ID, "p1", agentapi.Answer{Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Archive(idleTask.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveProject(project); statusOf(err) != http.StatusConflict {
		t.Fatalf("remove with an active task = %v, want 409", err)
	}
	if _, err := m.Settle(busyTask.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveProject(project); statusOf(err) != http.StatusConflict {
		t.Fatalf("remove with a settled task = %v, want 409", err)
	}
	if _, err := m.Archive(busyTask.ID); err != nil {
		t.Fatal(err)
	}

	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveProject(project); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	removed := map[string]bool{}
	for len(removed) < 2 {
		var id string
		decodeField(t, frameOf(t, sub, "session_removed"), "session_id", &id)
		removed[id] = true
	}
	var gone string
	decodeField(t, frameOf(t, sub, "project_removed"), "project_id", &gone)
	if !removed[busyTask.ID] || !removed[idleTask.ID] || gone != project {
		t.Fatalf("removal frames: sessions %v project %q", removed, gone)
	}
	if busyConv.Closes() != 1 || idleConv.Closes() != 1 {
		t.Fatalf("conversations not closed: %d %d", busyConv.Closes(), idleConv.Closes())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("project directory was touched: %v", err)
	}
	for _, id := range []string{busyTask.ID, idleTask.ID} {
		if _, ok := loadRecord(t, st, "fake", id); ok {
			t.Fatalf("task record %s survived its project", id)
		}
		if _, err := m.Detail(id); statusOf(err) != http.StatusNotFound {
			t.Fatalf("removed task still listed: %v", err)
		}
	}
	if _, ok := loadRecord(t, st, "fake", kept.ID); !ok || len(m.List()) != 1 {
		t.Fatal("a task of another project was removed")
	}
	if projects := m.Projects(); len(projects) != 1 || projects[0].ID != other {
		t.Fatalf("projects = %+v", projects)
	}
	if err := m.RemoveProject(project); statusOf(err) != http.StatusNotFound {
		t.Fatalf("remove again = %v, want 404", err)
	}
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("create in removed project = %v, want 400", err)
	}
}

func TestDeleteTaskOnlyOnceArchivedAndNeverDeletesConversation(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	rid := mustUUID(t)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: rid, Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); statusOf(err) != http.StatusConflict {
		t.Fatalf("delete while working = %v, want 409", err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if err := m.Delete(sum.ID); statusOf(err) != http.StatusConflict {
		t.Fatalf("delete an active task = %v, want 409", err)
	}
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); statusOf(err) != http.StatusConflict {
		t.Fatalf("delete a settled task = %v, want 409", err)
	}
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var id string
	decodeField(t, frameOf(t, sub, "session_removed"), "session_id", &id)
	if id != sum.ID || conv.Closes() != 1 {
		t.Fatalf("session_removed %q, closes %d", id, conv.Closes())
	}
	if _, ok := loadRecord(t, st, "fake", sum.ID); ok {
		t.Fatal("record survived delete")
	}
	if err := m.Delete(sum.ID); statusOf(err) != http.StatusNotFound {
		t.Fatalf("delete again = %v, want 404", err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend}); statusOf(err) != http.StatusNotFound {
		t.Fatalf("prompt after delete = %v, want 404", err)
	}
	if len(conv.Sends()) != 1 || len(prov.Opens()) != 1 {
		t.Fatalf("delete reached the provider: sends %d opens %d", len(conv.Sends()), len(prov.Opens()))
	}
}

func seedLegacyWebRecord(t *testing.T, st *store.Store, id, workdir string, web *store.WebState) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", id)] = store.SessionRecord{
			ID: id, Agent: "fake", Name: "old", Mode: store.ModeSafe, Workdir: workdir, CreatedAt: now, LastSeenAt: now,
			Status: store.StatusActive, Surface: store.SurfaceWeb, ProviderSessionID: "conv_" + id[:8], Web: web,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStartAssignsLegacyTasksToProjectsOnce(t *testing.T) {
	st := openTestStore(t)
	shared, lone, kept := t.TempDir(), t.TempDir(), t.TempDir()
	vanished := filepath.Join(t.TempDir(), "gone")
	a, b, c, d, assigned, orphan := mustUUID(t), mustUUID(t), mustUUID(t), mustUUID(t), mustUUID(t), mustUUID(t)
	now := time.Now().UTC()
	seedLegacyWebRecord(t, st, a, shared, &store.WebState{Turn: StateCompleted, UpdatedAt: now, RequestID: "r1", RequestStatus: SubmissionAccepted})
	seedLegacyWebRecord(t, st, b, shared, nil)
	seedLegacyWebRecord(t, st, c, lone, &store.WebState{Turn: StateIdle, UpdatedAt: now})
	seedLegacyWebRecord(t, st, d, vanished, &store.WebState{Turn: StateIdle, UpdatedAt: now})
	seedLegacyWebRecord(t, st, assigned, shared, &store.WebState{Turn: StateIdle, UpdatedAt: now, ProjectID: "kept-project"})
	// A project_id without a stored Project (written after a failed
	// migration save, or left by a dropped entry) is assigned again.
	seedLegacyWebRecord(t, st, orphan, lone, &store.WebState{Turn: StateIdle, UpdatedAt: now, ProjectID: "missing-project"})
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions["claude:term0001"] = store.SessionRecord{ID: "term0001", Agent: "claude", Workdir: shared, Mode: store.ModeSafe}
		cfg.WebProjects = map[string]store.WebProject{"kept-project": {ID: "kept-project", Name: "kept", Dir: kept, CreatedAt: now}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	m := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebProjects) != 4 {
		t.Fatalf("projects = %+v, want one per directory", cfg.WebProjects)
	}
	byDir := map[string]store.WebProject{}
	for _, p := range cfg.WebProjects {
		byDir[p.Dir] = p
	}
	for id, dir := range map[string]string{a: shared, b: shared, c: lone, d: vanished, orphan: lone} {
		rec := cfg.Sessions[store.Key("fake", id)]
		if rec.Web == nil || rec.Web.ProjectID != byDir[dir].ID {
			t.Fatalf("record %s web = %+v, want project for %s", id, rec.Web, dir)
		}
		if got := detail(t, m, id).ProjectID; got != byDir[dir].ID {
			t.Fatalf("summary project = %q", got)
		}
	}
	if byDir[shared].Name != filepath.Base(shared) || byDir[vanished].Name != "gone" {
		t.Fatalf("project names = %+v", byDir)
	}
	if web := cfg.Sessions[store.Key("fake", a)].Web; web.RequestID != "r1" || web.Turn != StateCompleted {
		t.Fatalf("migration lost web state: %+v", web)
	}
	if web := cfg.Sessions[store.Key("fake", assigned)].Web; web.ProjectID != "kept-project" {
		t.Fatalf("a record with a project was reassigned: %+v", web)
	}
	if cfg.Sessions["claude:term0001"].Web != nil {
		t.Fatal("a terminal record was touched")
	}
	if n := len(m.Projects()); n != 4 {
		t.Fatalf("manager projects = %d", n)
	}

	// Removing a Project removes its Tasks, so a restart has nothing to
	// reassign and the Project stays gone. A second start changes nothing.
	for _, id := range []string{c, orphan} {
		if _, err := m.Archive(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.RemoveProject(byDir[lone].ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	m2 := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	after, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || len(m2.Projects()) != 3 {
		t.Fatalf("second start changed the store or projects (%d)", len(m2.Projects()))
	}
}

// Fields a newer uam wrote inside a web record or a Project survive this
// version's writes.
func TestWritersKeepFieldsANewerUamWrote(t *testing.T) {
	st := openTestStore(t)
	dir := t.TempDir()
	id, projectID := mustUUID(t), mustUUID(t)
	key := store.Key("fake", id)
	raw := fmt.Sprintf(`{"schema_version":%d,"default_agent":"opencode","profiles":{},"ui":{"sort":"state","peek_width":60},
		"web_projects":{%q:{"id":%q,"name":"repo","dir":%q,"created_at":"2026-09-01T00:00:00Z","archived":true}},
		"sessions":{%q:{"id":%q,"agent":"fake","name":"old","mode":"safe","workdir":%q,"created_at":"2026-09-01T00:00:00Z",
		"last_seen_at":"2026-09-01T00:00:00Z","status":"active","provider_session_id":"conv_1","surface":"web",
		"web":{"turn":"completed","updated_at":"2026-09-01T00:00:00Z","project_id":%q,"sort":7}}}}`,
		store.CurrentSchemaVersion, projectID, projectID, dir, key, id, dir, projectID)
	if err := os.WriteFile(st.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	m := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if _, err := m.Rename(id, "new name"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateProject(projectID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close(id); err != nil { // flushes synchronously
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		WebProjects map[string]map[string]any `json:"web_projects"`
		Sessions    map[string]struct {
			Name string         `json:"name"`
			Web  map[string]any `json:"web"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if p := decoded.WebProjects[projectID]; p["archived"] != true || p["name"] != "renamed" {
		t.Fatalf("project after rename = %v", p)
	}
	rec := decoded.Sessions[key]
	if rec.Web["sort"] != float64(7) || rec.Web["turn"] != StateClosed || rec.Web["project_id"] != projectID || rec.Name != "new name" {
		t.Fatalf("record after flush: name %q web %v", rec.Name, rec.Web)
	}
}

// A provider that lists models must be able to switch them. One that cannot
// never ends up running a Task on a model other than the one it reports.
func TestProviderThatCannotSwitchNeverRunsOnAnotherModel(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, nil)
	prov.SetOpenSetModelError(agentapi.ErrUnsupported)
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetModel(sum.ID, setting("b"), nil, nil); statusOf(err) != http.StatusConflict {
		t.Fatalf("switch on an open conversation = %v, want 409", err)
	}
	if got := detail(t, m, sum.ID).Model; got != "a" {
		t.Fatalf("model after a refused switch = %q", got)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetModel(sum.ID, setting("b"), nil, nil); err != nil {
		t.Fatal(err)
	}
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend})
	if err != nil || sub.Status != SubmissionRejected {
		t.Fatalf("prompt after the stored switch = %+v, %v", sub, err)
	}
	reopened := prov.Last()
	d := detail(t, m, sum.ID)
	if d.Open || d.State != StateFailed || !strings.Contains(d.StateDetail, "apply model b") || reopened.Closes() != 1 || len(reopened.Sends()) != 0 {
		t.Fatalf("reopen that cannot switch: %+v closes=%d sends=%d", d.SessionSummary, reopened.Closes(), len(reopened.Sends()))
	}
}

// updated_at is Task activity: a restart, provider checks, a catalog reload
// and a viewer reopening the conversation leave it as stored.
func TestRestartAndViewingKeepUpdatedAt(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_known", StateCompleted)
	rec, _ := loadRecord(t, st, "fake", id)
	stored := rec.Web.UpdatedAt
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "auto", Name: "Auto"}}, nil)
	prov.AddConversation("conv_known", []agentapi.Item{{ID: "a1", Kind: agentapi.ItemAssistant, Text: "done"}})
	m := startManager(t, st, prov)
	later := stored.Add(time.Hour)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.modelsAt["fake"] = time.Time{}
	m.mu.Unlock()
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	m.RefreshModels()
	if err := m.View(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	d := detail(t, m, id)
	if !d.Open || len(d.Items) != 1 || !d.UpdatedAt.Equal(stored) {
		t.Fatalf("after restart and view: open=%v items=%d updated_at=%s, stored %s", d.Open, len(d.Items), d.UpdatedAt, stored)
	}
	for range 2 { // starting, then open
		var s SessionSummary
		decodeField(t, frameOf(t, sub, "session"), "session", &s)
		if !s.UpdatedAt.Equal(stored) {
			t.Fatalf("session frame updated_at = %s, stored %s", s.UpdatedAt, stored)
		}
	}
	if rec, _ := loadRecord(t, st, "fake", id); !rec.Web.UpdatedAt.Equal(stored) {
		t.Fatalf("stored updated_at moved to %s", rec.Web.UpdatedAt)
	}
	// Activity still moves it.
	prov.Last().EmitTurn(agentapi.TurnWorking, "")
	if got := detail(t, m, id).UpdatedAt; !got.Equal(later) {
		t.Fatalf("updated_at after a turn started = %s, want %s", got, later)
	}
}

func TestModelCatalogValidationAndRefresh(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "auto", Name: "Auto"}, {ID: "fast", Name: ""}, {ID: "fast", Name: "dup"}, {ID: ""}}, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	info := m.Providers()[0]
	if len(info.Models) != 2 || info.Models[0].ID != "auto" || info.Models[0].Name != "Auto" || info.Models[1].ID != "fast" || info.Models[1].Name != "fast" {
		t.Fatalf("catalog = %+v", info.Models)
	}
	project := addProject(t, m, t.TempDir())
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "gpt-9"}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("model outside the catalog = %v, want 400", err)
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "fast"})
	if err != nil || sum.Model != "fast" || prov.Last().Request().Model != "fast" {
		t.Fatalf("create with model = %+v, %v, open %+v", sum, err, prov.Last().Request())
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web == nil || rec.Web.Model != "fast" || rec.Web.ProjectID != project {
		t.Fatalf("stored web = %+v", rec.Web)
	}

	// Within five minutes the cached catalog is used; after that /api/meta
	// reloads it, and a failed reload keeps the previous one.
	calls := prov.ModelsCalls()
	prov.SetModels([]agentapi.Model{{ID: "auto", Name: "Auto"}, {ID: "new", Name: "New"}}, nil)
	m.RefreshModels()
	if prov.ModelsCalls() != calls || len(m.Providers()[0].Models) != 2 || m.Providers()[0].Models[1].ID != "fast" {
		t.Fatal("catalog refreshed before it was stale")
	}
	base := time.Now()
	m.mu.Lock()
	m.now = func() time.Time { return base.Add(6 * time.Minute) }
	m.mu.Unlock()
	m.RefreshModels()
	if prov.ModelsCalls() != calls+1 || m.Providers()[0].Models[1].ID != "new" {
		t.Fatalf("stale catalog not refreshed: %+v", m.Providers()[0].Models)
	}
	prov.SetModels(nil, errors.New("offline"))
	m.mu.Lock()
	m.now = func() time.Time { return base.Add(12 * time.Minute) }
	m.mu.Unlock()
	m.RefreshModels()
	if prov.ModelsCalls() != calls+2 || m.Providers()[0].Models[1].ID != "new" {
		t.Fatalf("failed refresh replaced the catalog: %+v", m.Providers()[0].Models)
	}

	unsupported := agenttest.NewProvider("plain", allCaps)
	unsupported.SetModels(nil, agentapi.ErrUnsupported)
	m2 := startManager(t, openTestStore(t), unsupported)
	if models := m2.Providers()[0].Models; models == nil || len(models) != 0 {
		t.Fatalf("unsupported catalog = %#v, want empty", models)
	}
	if _, err := m2.Create(CreateRequest{Provider: "plain", ProjectID: addProject(t, m2, t.TempDir()), Model: "auto"}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("model without a catalog = %v, want 400", err)
	}
}

func TestSetModelBetweenTurns(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	if _, err := m.SetModel(sum.ID, setting("zzz"), nil, nil); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown model = %v, want 400", err)
	}
	if _, err := m.SetModel("missing", setting("b"), nil, nil); statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown session = %v, want 404", err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.SetModel(sum.ID, setting("b"), nil, nil); statusOf(err) != http.StatusConflict {
		t.Fatalf("switch during a turn = %v, want 409", err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.SetModelError(errors.New("rpc broke"))
	if _, err := m.SetModel(sum.ID, setting("b"), nil, nil); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("provider refused switch = %v, want 502", err)
	}
	if got := detail(t, m, sum.ID).Model; got != "a" {
		t.Fatalf("model changed although the provider refused: %q", got)
	}
	conv.SetModelError(nil)
	switched, err := m.SetModel(sum.ID, setting("b"), nil, nil)
	if err != nil || switched.Model != "b" {
		t.Fatalf("SetModel = %+v, %v", switched, err)
	}
	if sets := conv.ModelSets(); strings.Join(sets, ",") != "b,b" { // refused, then accepted
		t.Fatalf("provider switches = %q", sets)
	}
	waitUntil(t, "model persisted", func() bool {
		rec, _ := loadRecord(t, st, "fake", sum.ID)
		return rec.Web != nil && rec.Web.Model == "b"
	})

	// With the conversation closed, the choice is stored and applied when
	// the conversation next opens, before the prompt is sent.
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetModel(sum.ID, setting("a"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(conv.ModelSets()); n != 2 {
		t.Fatalf("closed conversation was switched (%d)", n)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	reopened := prov.Last()
	if reopened == conv || reopened.Request().Model != "" {
		t.Fatalf("reopen request = %+v", reopened.Request())
	}
	if sets := reopened.ModelSets(); len(sets) != 1 || sets[0] != "a" || len(reopened.Sends()) != 1 {
		t.Fatalf("reopen switches = %q sends = %q", sets, reopened.Sends())
	}
}

func TestReopenAppliesStoredModelOrFails(t *testing.T) {
	st := openTestStore(t)
	ok, refused := mustUUID(t), mustUUID(t)
	now := time.Now().UTC()
	seedLegacyWebRecord(t, st, ok, t.TempDir(), &store.WebState{Turn: StateCompleted, UpdatedAt: now, Model: "b"})
	seedLegacyWebRecord(t, st, refused, t.TempDir(), &store.WebState{Turn: StateCompleted, UpdatedAt: now, Model: "b"})
	prov := agenttest.NewProvider("fake", allCaps)
	prov.AddConversation("conv_"+ok[:8], nil)
	prov.AddConversation("conv_"+refused[:8], nil)
	m := startManager(t, st, prov)
	if err := m.View(context.Background(), ok); err != nil {
		t.Fatal(err)
	}
	opened := prov.Last()
	if sets := opened.ModelSets(); len(sets) != 1 || sets[0] != "b" || !detail(t, m, ok).Open {
		t.Fatalf("reopen did not apply the stored model: %q", sets)
	}

	prov.SetOpenSetModelError(errors.New("switch refused"))
	if _, err := m.Submit(refused, PromptRequest{Text: "hello", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	failed := prov.Last()
	d := detail(t, m, refused)
	if d.Open || d.State != StateFailed || !strings.Contains(d.StateDetail, "apply model b") || failed.Closes() != 1 || len(failed.Sends()) != 0 {
		t.Fatalf("reopen with a refused model: %+v closes=%d sends=%d", d.SessionSummary, failed.Closes(), len(failed.Sends()))
	}
}

func TestTitleIsSanitizedPersistedAndShownUntilNamed(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Name: "   "})
	if err != nil || sum.Name != "" || sum.Title != "" {
		t.Fatalf("blank-name create = %+v, %v", sum, err)
	}
	conv := prov.Last()
	conv.EmitTitle("  Fix the\nbuild \x1b[31mnow ")
	if got := detail(t, m, sum.ID); got.Title != "Fix the build now" || got.Name != "" {
		t.Fatalf("title = %q name = %q", got.Title, got.Name)
	}
	waitUntil(t, "title persisted", func() bool {
		rec, _ := loadRecord(t, st, "fake", sum.ID)
		return rec.Web != nil && rec.Web.Title == "Fix the build now" && rec.Name == ""
	})
	conv.EmitTitle("   ")
	conv.EmitTitle(strings.Repeat("t", maxNameRunes+50))
	if got := detail(t, m, sum.ID).Title; len([]rune(got)) != maxNameRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long title = %d runes", len([]rune(got)))
	}
	if named, err := m.Rename(sum.ID, "mine"); err != nil || named.Name != "mine" {
		t.Fatalf("Rename = %+v, %v", named, err)
	}
	if cleared, err := m.Rename(sum.ID, ""); err != nil || cleared.Name != "" || cleared.Title == "" {
		t.Fatalf("empty rename = %+v, %v", cleared, err)
	}
	if _, err := m.Rename(sum.ID, strings.Repeat("n", maxNameRunes+1)); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("long name = %v, want 400", err)
	}
}

func TestTurnModelIsReportedAsLastModel(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCompleted, Model: "claude-haiku-4.5"}})
	if got := detail(t, m, sum.ID).LastModel; got != "claude-haiku-4.5" {
		t.Fatalf("last_model = %q", got)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if got := detail(t, m, sum.ID).LastModel; got != "claude-haiku-4.5" {
		t.Fatalf("a turn without a model cleared last_model: %q", got)
	}
}

func TestSubagentEventsStayOutOfTheMainTranscript(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(100, 0).UTC()
	conv.EmitItem(agentapi.Item{ID: "call_1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolRunning}})
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", ParentToolCallID: "call_1", Name: "General \x1b[1mpurpose", Status: agentapi.SubagentRunning, StartedAt: started})
	conv.EmitItem(agentapi.Item{ID: "u1", Kind: agentapi.ItemUser, Text: "delegated prompt", AgentID: "agent-1"})
	conv.Emit(agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: "m1", Kind: agentapi.ItemAssistant, Text: "sub ", AgentID: "agent-1"}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: "m1", Kind: agentapi.ItemAssistant, Text: "answer", AgentID: "agent-1"}})
	// The same item ID from another agent is a different item.
	conv.EmitDelta("m1", agentapi.ItemAssistant, "main answer")

	var sa agentapi.Subagent
	decodeField(t, frameOf(t, sub, "subagent"), "subagent", &sa)
	if sa.ID != "agent-1" || sa.Name != "General purpose" || sa.Status != agentapi.SubagentRunning || sa.ParentToolCallID != "call_1" {
		t.Fatalf("subagent frame = %+v", sa)
	}
	item := frameOf(t, sub, "item")
	var agentID string
	decodeField(t, item, "agent_id", &agentID)
	if agentID != "agent-1" {
		t.Fatalf("item frame agent_id = %q", agentID)
	}
	delta := frameOf(t, sub, "delta")
	decodeField(t, delta, "agent_id", &agentID)
	if agentID != "agent-1" {
		t.Fatalf("delta frame agent_id = %q", agentID)
	}
	d := detail(t, m, sum.ID)
	if len(d.Items) != 2 || d.Items[0].ID != "call_1" || d.Items[1].Text != "main answer" || d.Items[1].AgentID != "" {
		t.Fatalf("main transcript = %+v", d.Items)
	}
	if len(d.Subagents) != 1 || d.SubagentsRunning != 1 {
		t.Fatalf("detail subagents = %+v running %d", d.Subagents, d.SubagentsRunning)
	}
	sd, err := m.Subagent(sum.ID, "agent-1")
	if err != nil || len(sd.Items) != 2 || sd.Items[0].Text != "delegated prompt" || sd.Items[1].Text != "sub answer" {
		t.Fatalf("subagent transcript = %+v, %v", sd, err)
	}
	if _, err := m.Subagent(sum.ID, "nope"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown subagent = %v, want 404", err)
	}

	// The first terminal status wins; a later one (Copilot reports a second,
	// cancelled completion on disconnect) is ignored.
	ended := started.Add(time.Minute)
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", Status: agentapi.SubagentCompleted, EndedAt: ended})
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", Status: agentapi.SubagentCancelled, EndedAt: ended.Add(time.Minute)})
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", Status: agentapi.SubagentRunning})
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-2", Status: "bogus"})
	got, _ := m.Subagent(sum.ID, "agent-1")
	if got.Subagent.Status != agentapi.SubagentCompleted || !got.Subagent.EndedAt.Equal(ended) || got.Subagent.Name != "General purpose" ||
		got.Subagent.ParentToolCallID != "call_1" || !got.Subagent.StartedAt.Equal(started) {
		t.Fatalf("after duplicate terminal updates = %+v", got.Subagent)
	}
	if d := detail(t, m, sum.ID); d.SubagentsRunning != 0 || len(d.Subagents) != 1 {
		t.Fatalf("summary after completion = %+v", d.SessionSummary)
	}

	// Closing the conversation ends a subagent that is still running.
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-3", Name: "bg", Status: agentapi.SubagentRunning})
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Subagent(sum.ID, "agent-3"); got.Subagent.Status != agentapi.SubagentCancelled {
		t.Fatalf("running subagent after close = %+v", got.Subagent)
	}
}

func TestReopenRestoresSubagentsFromHistory(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_known", StateCompleted)
	prov := agenttest.NewProvider("fake", allCaps)
	prov.AddConversation("conv_known", []agentapi.Item{
		{ID: "u1", Kind: agentapi.ItemUser, Text: "do it"},
		{ID: "call_1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted}},
		{ID: "u1", Kind: agentapi.ItemUser, Text: "delegated", AgentID: "agent-1"},
		{ID: "a1", Kind: agentapi.ItemAssistant, Text: "done", AgentID: "agent-1"},
	}, agentapi.Subagent{ID: "agent-1", ParentToolCallID: "call_1", Name: "helper", Status: agentapi.SubagentCompleted})
	m := startManager(t, st, prov)
	if err := m.View(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	d := detail(t, m, id)
	if len(d.Items) != 2 || d.Items[0].Text != "do it" || len(d.Subagents) != 1 || d.Subagents[0].Status != agentapi.SubagentCompleted {
		t.Fatalf("reopened detail = items %+v subagents %+v", d.Items, d.Subagents)
	}
	if sd, err := m.Subagent(id, "agent-1"); err != nil || len(sd.Items) != 2 || sd.Items[0].Text != "delegated" {
		t.Fatalf("reopened subagent = %+v, %v", sd, err)
	}
}

func TestTaskDefaultsRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	ts.prov.SetModels(selectionModels(), nil)
	ts.m.mu.Lock()
	ts.m.modelsAt["fake"] = time.Time{}
	ts.m.mu.Unlock()
	ts.m.RefreshModels()
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH /api/settings %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	for _, body := range []string{
		`{"task_defaults":null}`, `{"task_defaults":"auto"}`, `{"task_defaults":{"provider":"fake","mode":""}}`,
		`{"task_defaults":{"provider":"nope","mode":"safe"}}`, `{"task_defaults":{"provider":"fake","model":"b","effort":"low","mode":"safe"}}`,
	} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, `"error"`) {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	if got := patch(`{"task_defaults":{"provider":"fake","model":"b","effort":"high","context_size":"","mode":"yolo"}}`, http.StatusOK); got != `{"send_default":"steer","task_defaults":{"provider":"fake","model":"b","effort":"high","context_size":"default","mode":"yolo"}}` {
		t.Fatalf("PATCH task defaults = %s", got)
	}
	if w := ts.do(http.MethodGet, "/api/settings", "", auth); !strings.Contains(w.Body.String(), `"task_defaults":{"provider":"fake","model":"b"`) {
		t.Fatalf("GET /api/settings = %d %s", w.Code, w.Body)
	}

	// Projects no longer carry defaults: a "defaults" key in their bodies is ignored, and they never list one.
	w := ts.do(http.MethodPost, "/api/projects", `{"dir":"`+t.TempDir()+`","name":"repo","defaults":{"provider":"fake","model":"a","mode":"safe"}}`, auth)
	var p Project
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != http.StatusCreated || strings.Contains(w.Body.String(), `"defaults"`) {
		t.Fatalf("POST /api/projects with defaults = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/projects/"+p.ID, `{"defaults":{"provider":"fake","model":"a","mode":"safe"}}`, auth); w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/projects defaults only = %d %s, want 400", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/projects/"+p.ID, `{"name":"renamed","defaults":{"provider":"fake","model":"a","mode":"safe"}}`, auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"renamed"`) || strings.Contains(w.Body.String(), `"defaults"`) {
		t.Fatalf("PATCH /api/projects name with defaults = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/projects", "", auth); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"defaults"`) {
		t.Fatalf("GET /api/projects = %d %s", w.Code, w.Body)
	}
}

func TestProjectAndTaskRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	ts.prov.SetModels([]agentapi.Model{{ID: "auto", Name: "Auto"}}, nil)
	ts.m.mu.Lock()
	ts.m.modelsAt["fake"] = time.Time{}
	ts.m.mu.Unlock()
	w := ts.do(http.MethodGet, "/api/meta", "", auth)
	var meta Meta
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || len(meta.Providers[0].Models) != 1 || meta.Providers[0].Models[0].ID != "auto" {
		t.Fatalf("meta = %d %s", w.Code, w.Body)
	}

	if w := ts.do(http.MethodGet, "/api/projects", "", auth); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"projects":[]}` {
		t.Fatalf("empty projects = %d %s", w.Code, w.Body)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"dir":"` + file + `"}`, `{"dir":"relative"}`, `{}`} {
		if w := ts.do(http.MethodPost, "/api/projects", body, auth); w.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/projects %s = %d, want 400", body, w.Code)
		}
	}
	dir := t.TempDir()
	w = ts.do(http.MethodPost, "/api/projects", `{"dir":"`+dir+`","name":"repo"}`, auth)
	var project Project
	if err := json.Unmarshal(w.Body.Bytes(), &project); err != nil || w.Code != http.StatusCreated || project.Name != "repo" || project.Dir != dir {
		t.Fatalf("add project = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPost, "/api/projects", `{"dir":"`+dir+`"}`, auth)
	var conflict map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil || w.Code != http.StatusConflict || conflict["project_id"] != project.ID || conflict["error"] == "" {
		t.Fatalf("duplicate project = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/projects/"+project.ID, `{"name":"renamed"}`, auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"renamed"`) {
		t.Fatalf("rename project = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/projects/missing", `{"name":"x"}`, auth); w.Code != http.StatusNotFound {
		t.Fatalf("rename unknown project = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/projects", "", auth); !strings.Contains(w.Body.String(), `"id":"`+project.ID+`"`) {
		t.Fatalf("projects = %s", w.Body)
	}

	for body, want := range map[string]int{
		`{"provider":"fake"}`: http.StatusBadRequest,
		`{"provider":"fake","project_id":"` + project.ID + `","model":"gpt-9"}`: http.StatusBadRequest,
		`{"provider":"fake","workdir":"` + dir + `"}`:                           http.StatusBadRequest,
	} {
		if w := ts.do(http.MethodPost, "/api/sessions", body, auth); w.Code != want {
			t.Fatalf("POST /api/sessions %s = %d, want %d", body, w.Code, want)
		}
	}
	w = ts.do(http.MethodPost, "/api/sessions", `{"provider":"fake","project_id":"`+project.ID+`","model":"auto"}`, auth)
	var sum SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || w.Code != http.StatusCreated || sum.Name != "" || sum.Model != "auto" || sum.ProjectID != project.ID {
		t.Fatalf("create task = %d %s", w.Code, w.Body)
	}
	for _, key := range []string{`"project_id"`, `"model"`, `"title"`, `"last_model"`, `"subagents_running"`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Fatalf("summary lacks %s: %s", key, w.Body)
		}
	}
	conv := ts.prov.Last()

	patch := "/api/sessions/" + sum.ID
	for body, want := range map[string]int{
		`{}`:                http.StatusBadRequest,
		`{"model":"gpt-9"}`: http.StatusBadRequest,
		`{"name":"` + strings.Repeat("n", maxNameRunes+1) + `"}`: http.StatusBadRequest,
	} {
		if w := ts.do(http.MethodPatch, patch, body, auth); w.Code != want {
			t.Fatalf("PATCH %s = %d, want %d", body, w.Code, want)
		}
	}
	if w := ts.do(http.MethodPatch, "/api/sessions/missing", `{"name":"x"}`, auth); w.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown session = %d", w.Code)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if w := ts.do(http.MethodPatch, patch, `{"model":"auto","name":"x"}`, auth); w.Code != http.StatusOK {
		t.Fatalf("PATCH to the current model while working = %d %s", w.Code, w.Body)
	}
	ts.prov.SetModels([]agentapi.Model{{ID: "auto", Name: "Auto"}, {ID: "fast", Name: "Fast"}}, nil)
	ts.m.mu.Lock()
	info := ts.m.infos["fake"]
	info.Models = []agentapi.Model{{ID: "auto", Name: "Auto"}, {ID: "fast", Name: "Fast"}}
	ts.m.infos["fake"] = info
	ts.m.mu.Unlock()
	if w := ts.do(http.MethodPatch, patch, `{"model":"fast"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("model switch while working = %d, want 409", w.Code)
	}
	if w := ts.do(http.MethodDelete, patch, "", auth); w.Code != http.StatusConflict {
		t.Fatalf("delete while working = %d, want 409", w.Code)
	}
	if w := ts.do(http.MethodDelete, "/api/projects/"+project.ID, "", auth); w.Code != http.StatusConflict {
		t.Fatalf("remove project while working = %d, want 409", w.Code)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if w := ts.do(http.MethodPatch, patch, `{"model":"fast","name":""}`, auth); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"model":"fast"`) || !strings.Contains(w.Body.String(), `"name":""`) {
		t.Fatalf("PATCH model and name = %d %s", w.Code, w.Body)
	}

	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", Name: "helper", Status: agentapi.SubagentRunning})
	conv.EmitItem(agentapi.Item{ID: "x", Kind: agentapi.ItemAssistant, Text: "sub", AgentID: "agent-1"})
	w = ts.do(http.MethodGet, patch+"/subagents/agent-1", "", auth)
	var sd SubagentDetail
	if err := json.Unmarshal(w.Body.Bytes(), &sd); err != nil || w.Code != http.StatusOK || sd.Subagent.ID != "agent-1" || len(sd.Items) != 1 {
		t.Fatalf("subagent route = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, patch+"/subagents/nope", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown subagent = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/missing/subagents/agent-1", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("subagent of unknown session = %d", w.Code)
	}
	w = ts.do(http.MethodGet, patch, "", auth)
	if !strings.Contains(w.Body.String(), `"subagents":[{"id":"agent-1"`) || strings.Contains(w.Body.String(), `"text":"sub"`) {
		t.Fatalf("detail = %s", w.Body)
	}

	// DELETE needs no body or JSON type, but keeps the origin and auth checks.
	del := func(target string, opts ...reqOpt) int {
		return ts.do(http.MethodDelete, target, "", append([]reqOpt{func(r *http.Request) { r.Header.Del("Content-Type") }}, opts...)...).Code
	}
	if code := del(patch); code != http.StatusUnauthorized {
		t.Fatalf("DELETE without cookie = %d, want 401", code)
	}
	if code := del(patch, auth, withHeader("Sec-Fetch-Site", "cross-site")); code != http.StatusForbidden {
		t.Fatalf("cross-site DELETE = %d, want 403", code)
	}
	if w := ts.do(http.MethodDelete, patch, `{"x":1}`, auth, withHeader("Content-Type", "text/plain")); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("DELETE with a text body = %d, want 415", w.Code)
	}
	if code := del(patch, auth); code != http.StatusConflict {
		t.Fatalf("DELETE an active task = %d, want 409", code)
	}
	if code := del("/api/projects/"+project.ID, auth); code != http.StatusConflict {
		t.Fatalf("DELETE a project with an active task = %d, want 409", code)
	}
	if w := ts.do(http.MethodPost, patch+"/archive", "", auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"stage":"archived"`) {
		t.Fatalf("archive = %d %s", w.Code, w.Body)
	}
	if code := del(patch, auth); code != http.StatusNoContent {
		t.Fatalf("DELETE task = %d, want 204", code)
	}
	if code := del(patch, auth); code != http.StatusNotFound {
		t.Fatalf("DELETE task again = %d, want 404", code)
	}
	if conv.Closes() != 1 {
		t.Fatalf("closes = %d", conv.Closes())
	}
	if code := del("/api/projects/"+project.ID, auth); code != http.StatusNoContent {
		t.Fatalf("DELETE project = %d, want 204", code)
	}
	if code := del("/api/projects/"+project.ID, auth); code != http.StatusNotFound {
		t.Fatalf("DELETE project again = %d, want 404", code)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("project directory touched: %v", err)
	}
}
