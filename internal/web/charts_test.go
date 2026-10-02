package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// chartTask creates a Task in mode in a new Project and returns it, its
// conversation and its directory.
func chartTask(t *testing.T, m *Manager, prov *agenttest.Provider, mode string) (SessionSummary, *agenttest.Conversation, string) {
	t.Helper()
	dir := t.TempDir()
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, dir), Name: "task", Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	return sum, prov.Last(), dir
}

func chartCall(t *testing.T, conv *agenttest.Conversation, callID, args string) agentapi.HostToolResult {
	t.Helper()
	res, err := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: chartToolName, CallID: callID, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func pinCount(m *Manager, projectID string) int {
	for _, p := range m.Projects() {
		if p.ID == projectID {
			return p.Charts
		}
	}
	return -1
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Stored pins this version cannot show (a hand edit, a newer kind) are not
// listed, so they neither count nor take a place under the cap.
func TestUnshownPinsNeitherCountNorFill(t *testing.T) {
	m, prov, st := newTestManager(t)
	task, conv, dir := chartTask(t, m, prov, "yolo")
	writeFile(t, filepath.Join(dir, "rows.csv"), "day,commits\n09-01,3\n")
	if res := chartCall(t, conv, "call-1", `{"title":"Commits","kind":"line","x":"day","y":["commits"],"command":"cat rows.csv","format":"csv"}`); res.Failed {
		t.Fatalf("uam_chart = %+v", res)
	}
	if err := st.Update(func(cfg *store.Config) error {
		p := cfg.WebProjects[task.ProjectID]
		for range maxPinnedCharts {
			p.Charts = append(p.Charts, store.WebChart{ID: mustUUID(t), Title: "From a newer uam", Kind: "radar", X: "a", Y: []string{"b"}, Command: "true", Format: "csv"})
		}
		cfg.WebProjects[task.ProjectID] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PinChart(task.ID, "call-1"); err != nil {
		t.Fatalf("pin = %v", err)
	}
	if n := pinCount(m, task.ProjectID); n != 1 {
		t.Fatalf("project charts = %d, want 1", n)
	}
	// Reading leaves what is stored as it was, the unshown pins included.
	for range 2 {
		if list, err := m.PinnedCharts(task.ProjectID); err != nil || len(list) != 1 {
			t.Fatalf("pinned = %+v, %v", list, err)
		}
	}
	if cfg, err := st.Load(); err != nil || len(cfg.WebProjects[task.ProjectID].Charts) != maxPinnedCharts+1 || cfg.WebProjects[task.ProjectID].Charts[0].Kind != "radar" {
		t.Fatalf("stored pins = %+v, %v", cfg.WebProjects[task.ProjectID].Charts, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := pinCount(startManager(t, st, prov), task.ProjectID); n != 1 {
		t.Fatalf("project charts after a restart = %d, want 1", n)
	}
}

// A yolo Task's chart reads its rows from a command run in the Task's
// directory; the model gets a summary, never the rows, and the chart keeps
// them for the browser. Pinned, it refreshes from the command without the
// model: on demand at most once a minute, on opening at most once an hour.
func TestChartFromACommandPinsAndRefreshes(t *testing.T) {
	m, prov, _ := newTestManager(t)
	task, conv, dir := chartTask(t, m, prov, "yolo")
	writeFile(t, filepath.Join(dir, "rows.csv"), "day,commits,files\n09-01,3,1\n09-02,5,4\n09-03,0,0\n")
	res := chartCall(t, conv, "call-1", `{"title":"Commits per day","kind":"line","x":"day","y":["commits","files"],"command":"cat rows.csv","format":"csv"}`)
	if res.Failed || !strings.Contains(res.Text, "from 3 rows") || !strings.Contains(res.Text, "day: 09-01 … 09-03") ||
		!strings.Contains(res.Text, "commits: min 0, max 5, total 8, last 0") || strings.Contains(res.Text, "09-02") {
		t.Fatalf("uam_chart = %+v", res)
	}
	sends := len(conv.Sends())
	chart, err := m.TaskChart(task.ID, "call-1")
	if err != nil || chart.Command != "cat rows.csv" || !slices.Equal(chart.Labels, []string{"09-01", "09-02", "09-03"}) ||
		len(chart.Series) != 2 || !slices.Equal(chart.Series[1].Values, []float64{1, 4, 0}) || chart.PinnedID != "" {
		t.Fatalf("chart = %+v, %v", chart, err)
	}

	pin, err := m.PinChart(task.ID, "call-1")
	if err != nil || pin.Command != "cat rows.csv" || pin.ProjectID != task.ProjectID || !slices.Equal(pin.Labels, chart.Labels) {
		t.Fatalf("pin = %+v, %v", pin, err)
	}
	if again, err := m.PinChart(task.ID, "call-1"); err != nil || again.ID != pin.ID {
		t.Fatalf("pinning again = %+v, %v", again, err)
	}
	if chart, _ := m.TaskChart(task.ID, "call-1"); chart.PinnedID != pin.ID {
		t.Fatalf("pinned id = %q", chart.PinnedID)
	}
	if n := pinCount(m, task.ProjectID); n != 1 {
		t.Fatalf("project charts = %d", n)
	}

	writeFile(t, filepath.Join(dir, "rows.csv"), "day,commits,files\n09-01,3,1\n09-02,5,4\n09-03,2,2\n09-04,7,3\n")
	if _, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false); statusOf(err) != 429 || !strings.Contains(err.Error(), "less than a minute") {
		t.Fatalf("refresh within a minute = %v", err)
	}
	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	got, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false)
	if err != nil || len(got.Labels) != 4 || got.Series[0].Values[3] != 7 || got.Error != "" {
		t.Fatalf("refresh = %+v, %v", got, err)
	}
	writeFile(t, filepath.Join(dir, "rows.csv"), "day,commits,files\n09-01,1,1\n")
	if got, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, true); err != nil || len(got.Labels) != 4 {
		t.Fatalf("an automatic refresh within the hour = %+v, %v", got, err)
	}
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if got, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, true); err != nil || len(got.Labels) != 1 {
		t.Fatalf("an automatic refresh after the hour = %+v, %v", got, err)
	}
	if len(conv.Sends()) != sends {
		t.Fatalf("a refresh reached the model: %q", conv.Sends())
	}

	// A failing refresh keeps the last good rows and says why.
	writeFile(t, filepath.Join(dir, "rows.csv"), "day,commits,files\n09-01,x,1\n")
	m.now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if got, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false); err != nil || len(got.Labels) != 1 || !strings.Contains(got.Error, `commits is "x", not a number`) {
		t.Fatalf("a failed refresh = %+v, %v", got, err)
	}
	if list, err := m.PinnedCharts(task.ProjectID); err != nil || len(list) != 1 || list[0].Error == "" {
		t.Fatalf("pinned = %+v, %v", list, err)
	}

	if err := m.UnpinChart(task.ProjectID, pin.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := m.PinnedCharts(task.ProjectID); err != nil || len(list) != 0 || pinCount(m, task.ProjectID) != 0 {
		t.Fatalf("after unpin = %+v, %v", list, err)
	}
	if _, err := os.Stat(m.pinFile(pin.ID)); !os.IsNotExist(err) {
		t.Fatalf("the rows outlived the pin: %v", err)
	}
}

// Rows passed inline make a chart in Safe mode too; a command there is
// refused, since it would run without the owner's approval. An inline chart
// pins as a snapshot that never re-runs.
func TestChartInSafeModeTakesRowsButNoCommand(t *testing.T) {
	m, prov, _ := newTestManager(t)
	task, conv, workdir := chartTask(t, m, prov, "safe")
	marker := filepath.Join(workdir, "ran")
	res := chartCall(t, conv, "c1", fmt.Sprintf(`{"title":"t","kind":"bar","x":"k","y":"v","command":"touch %s; echo k,v","format":"csv"}`, marker))
	if !res.Failed || !strings.Contains(res.Text, "Safe mode") {
		t.Fatalf("a command in safe mode = %+v", res)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the command ran in safe mode")
	}
	res = chartCall(t, conv, "c2", `{"title":"Lines per file type","kind":"bar","x":"ext","y":"lines","data":[{"ext":"go","lines":1200},{"ext":"ts","lines":"800"}]}`)
	if res.Failed || !strings.Contains(res.Text, "from 2 rows") {
		t.Fatalf("inline rows = %+v", res)
	}
	pin, err := m.PinChart(task.ID, "c2")
	if err != nil || pin.Command != "" || !slices.Equal(pin.Series[0].Values, []float64{1200, 800}) {
		t.Fatalf("pin = %+v, %v", pin, err)
	}
	if _, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, false); statusOf(err) != 409 {
		t.Fatalf("refreshing a snapshot = %v", err)
	}
	if got, err := m.RefreshChart(context.Background(), task.ProjectID, pin.ID, true); err != nil || got.ID != pin.ID {
		t.Fatalf("opening a snapshot = %+v, %v", got, err)
	}
	if _, err := m.TaskChart(task.ID, "c1"); statusOf(err) != 404 {
		t.Fatalf("a refused call left a chart: %v", err)
	}
}

// Bad input is refused with a message the model can act on.
func TestChartRefusals(t *testing.T) {
	m, prov, _ := newTestManager(t)
	_, conv, _ := chartTask(t, m, prov, "yolo")
	many := make([]string, maxChartRows+1)
	for i := range many {
		many[i] = fmt.Sprintf(`{"k":"%d","v":1}`, i)
	}
	for _, tc := range []struct{ args, want string }{
		{`{"title":"t","kind":"pie","x":"k","y":["v"],"data":[{"k":"a","v":1}]}`, "kind must be line or bar"},
		{`{"title":"","kind":"bar","x":"k","y":["v"],"data":[{"k":"a","v":1}]}`, "title is required"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"]}`, "pass a command"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"echo","format":"csv","data":[]}`, "not both"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"echo k,v"}`, "format must be csv or json"},
		{`{"title":"t","kind":"bar","x":"k","y":["a","b","c","d","e"],"data":[{"k":"a"}]}`, "at most 4 series"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"data":[{"k":"a","v":1},{"k":"a","v":2}]}`, "same x value \"a\""},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"data":[{"k":"a","v":"many"}]}`, `row 1: v is "many", not a number`},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"data":[` + strings.Join(many, ",") + `]}`, "501 rows; a chart takes at most 500"},
		{`{"title":"t","kind":"bar","x":"k","y":["n"],"command":"printf 'k,v\\na,1\\n'","format":"csv"}`, `no field "n"; their fields are: k, v`},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"echo oops >&2; exit 3","format":"csv"}`, "exit status 3: oops"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"head -c 1100000 /dev/zero","format":"csv"}`, "more than 1024 KiB"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"echo '[1,'","format":"json"}`, "not a JSON array of objects"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"true","format":"json"}`, "no rows"},
		{`{"title":"t","kind":"bar","x":"k","y":["v"],"command":"echo a\u0007b","format":"csv"}`, "control character"},
	} {
		if res := chartCall(t, conv, "bad", tc.args); !res.Failed || !strings.Contains(res.Text, tc.want) {
			t.Errorf("uam_chart %.80s = %+v; want %q", tc.args, res, tc.want)
		}
	}
}

// JSON output is an array of objects or one object per line; numbers may
// be strings, and x values may be numbers.
func TestChartReadsJSONOutput(t *testing.T) {
	spec := ChartSpec{Title: "t", Kind: "line", X: "n", Y: []string{"v"}, Command: "x", Format: "json"}
	for _, out := range []string{`[{"n":1,"v":2.5},{"n":2,"v":"3"}]`, "{\"n\":1,\"v\":2.5}\n{\"n\":2,\"v\":\"3\"}\n"} {
		rows, fields, err := parseChartOutput("json", []byte(out))
		if err != nil {
			t.Fatal(err)
		}
		labels, series, err := chartRows(spec, rows, fields)
		if err != nil || !slices.Equal(labels, []string{"1", "2"}) || !slices.Equal(series[0].Values, []float64{2.5, 3}) {
			t.Fatalf("%q: %q %+v %v", out, labels, series, err)
		}
	}
}
