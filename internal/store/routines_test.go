package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Loading keeps valid routines with the fields a newer uam wrote, and drops
// those whose ID, Project, schedule, mode, limits, model or place could not
// be used.
func TestLoadDropsInvalidRoutines(t *testing.T) {
	routine := func(id, project, schedule, extra string) string {
		return `"` + id + `":{"id":"` + id + `","project_id":"` + project + `","name":"n","prompt":"p","provider":"copilot","model":"m",` +
			`"schedule":` + schedule + `,"enabled":true,"mode":"safe","max_runs_per_day":2,"max_minutes":5` + extra + `}`
	}
	daily := `{"kind":"daily","time":"09:00"}`
	cfg := loadRaw(t, `{"schema_version":4,"web_projects":{"p1":{"id":"p1","dir":"/srv/p1"}},"web_routines":{`+strings.Join([]string{
		routine("good", "p1", daily, `,"future":1`),
		routine("orphan", "gone", daily, ""),
		routine("cron", "p1", `{"kind":"cron","time":"* * * * *"}`, ""),
		routine("late", "p1", `{"kind":"daily","time":"24:00"}`, ""),
		routine("mode", "p1", daily, `,"mode":"auto"`),
		routine("limits", "p1", daily, `,"max_minutes":0`),
		routine("bad;id", "p1", daily, ""),
		routine("place", "p1", daily, `,"place":{"mode":"switch","branch":"main"}`),
		routine("tree", "p1", daily, `,"place":{"mode":"worktree","branch":"","base":"main"}`),
	}, ",")+`}}`)
	if tree := cfg.WebRoutines["tree"]; tree.Place == nil || *tree.Place != (WebPlace{Mode: "worktree", Base: "main"}) {
		t.Fatalf("tree routine = %+v, want its worktree place", tree)
	}
	delete(cfg.WebRoutines, "tree")
	if len(cfg.WebRoutines) != 1 {
		t.Fatalf("routines = %+v, want only good", cfg.WebRoutines)
	}
	out, err := json.Marshal(cfg.WebRoutines["good"])
	if err != nil || !strings.Contains(string(out), `"future":1`) {
		t.Fatalf("good routine = %s, %v; want its unknown field kept", out, err)
	}
	// A routine stored before autopilot existed keeps its mode, without it.
	if good := cfg.WebRoutines["good"]; good.Mode != ModeSafe || good.Autopilot || strings.Contains(string(out), "autopilot") {
		t.Fatalf("good routine = %+v, %s", good, out)
	}
}

// A routine's run history keeps its newest MaxRoutineRuns runs.
func TestLoadCapsRoutineRuns(t *testing.T) {
	runs := make([]string, MaxRoutineRuns+5)
	for i := range runs {
		runs[i] = `{"id":"r` + strings.Repeat("x", i) + `","outcome":"finished","trigger":"schedule"}`
	}
	cfg := loadRaw(t, `{"schema_version":4,"web_projects":{"p1":{"id":"p1","dir":"/srv/p1"}},"web_routines":{"a":{"id":"a","project_id":"p1","name":"n","prompt":"p","provider":"copilot","model":"m",`+
		`"schedule":{"kind":"hours","hours":2},"enabled":true,"mode":"yolo","max_runs_per_day":2,"max_minutes":5,"runs":[`+strings.Join(runs, ",")+`]}}}`)
	got := cfg.WebRoutines["a"].Runs
	if len(got) != MaxRoutineRuns || got[0].ID != "r"+strings.Repeat("x", 5) {
		t.Fatalf("runs = %d, first %q", len(got), got[0].ID)
	}
}

// Every saved routine field is known: an unknown key is kept as a newer
// version's field, and its loaded copy would overwrite later changes.
func TestEveryWebRoutineFieldIsKnown(t *testing.T) {
	for f := range reflect.TypeFor[WebRoutine]().Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := knownWebRoutineFields[name]; !ok {
			t.Errorf("%s (%s) is missing from knownWebRoutineFields", name, f.Name)
		}
	}
}
