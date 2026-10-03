package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Pinned charts (docs/web.md "Charts"): the owner pins a Task's chart to
// its Project. The spec, command included, is stored with the Project in
// sessions.json; pinning is the owner's approval of the command. The rows
// live in charts/<id>.json beside it. Refresh runs the command again in
// the Project directory, without any model: on demand at most once a
// minute, and when the owner opens the Project's charts at most once an
// hour.

const (
	maxPinnedCharts  = 12
	chartManualEvery = time.Minute
	chartAutoEvery   = time.Hour
)

// PinnedChart is a chart pinned to a Project with its latest rows. Error
// says why the latest refresh failed; the rows are then the last good ones.
type PinnedChart struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	PinnedAt  time.Time `json:"pinned_at"`
	ChartSpec
	ChartData
	Error   string    `json:"error,omitempty"`
	ErrorAt time.Time `json:"error_at,omitzero"`
}

// pinData is a pinned chart's file: its rows and its latest failure.
type pinData struct {
	ChartData
	Error   string    `json:"error,omitempty"`
	ErrorAt time.Time `json:"error_at,omitzero"`
}

func pinSpec(c store.WebChart) ChartSpec {
	return ChartSpec{Title: c.Title, Kind: c.Kind, XLabel: c.XLabel, YLabel: c.YLabel, Command: c.Command, Format: c.Format, X: c.X, Y: c.Y}
}

func (m *Manager) pinRoot() string { return filepath.Join(filepath.Dir(m.store.Path()), chartsDir) }

func (m *Manager) pinFile(id string) string { return filepath.Join(m.pinRoot(), id+".json") }

// storedPins returns the valid charts pinned to the Project id, oldest
// first.
func (m *Manager) storedPins(id string) ([]store.WebChart, error) {
	cfg, err := m.store.Load()
	if err != nil {
		return nil, err
	}
	p, ok := cfg.WebProjects[id]
	if !ok {
		return nil, errProjectNotFound
	}
	return slices.DeleteFunc(p.Charts, func(c store.WebChart) bool { return !shownPin(c) }), nil
}

// shownPin reports whether this version shows the stored pin c. Only shown
// pins count, toward the cap too; the others stay stored untouched.
func shownPin(c store.WebChart) bool { return validRequestID(c.ID) && pinSpec(c).check() == nil }

// shownPins counts the shown pins in charts.
func shownPins(charts []store.WebChart) int {
	n := 0
	for _, c := range charts {
		if shownPin(c) {
			n++
		}
	}
	return n
}

// pinned is the API view of c with the rows its file holds, if any.
func (m *Manager) pinned(projectID string, c store.WebChart) PinnedChart {
	out := PinnedChart{ID: c.ID, ProjectID: projectID, PinnedAt: c.PinnedAt, ChartSpec: pinSpec(c)}
	var d pinData
	if data, err := os.ReadFile(m.pinFile(c.ID)); err == nil && json.Unmarshal(data, &d) == nil && len(d.Series) == len(c.Y) {
		out.ChartData, out.Error, out.ErrorAt = d.ChartData, d.Error, d.ErrorAt
	}
	if out.Labels == nil {
		out.Labels, out.Series = []string{}, []ChartSeries{}
	}
	return out
}

// PinnedCharts returns the charts pinned to the Project id with their rows.
func (m *Manager) PinnedCharts(id string) ([]PinnedChart, error) {
	pins, err := m.storedPins(id)
	if err != nil {
		return nil, err
	}
	out := make([]PinnedChart, 0, len(pins))
	for _, c := range pins {
		out = append(out, m.pinned(id, c))
	}
	return out, nil
}

// setPinCountLocked records and publishes how many charts the Project id
// has pinned. The caller holds mu.
func (m *Manager) setPinCountLocked(id string, n int) {
	if p := m.projects[id]; p != nil && p.Charts != n {
		p.Charts = n
		m.publishProjectLocked(*p)
	}
}

// PinChart pins the chart the Task taskID drew with the call callID to the
// Task's Project, with its rows; a chart pinned already is returned as it
// is.
func (m *Manager) PinChart(taskID, callID string) (PinnedChart, error) {
	c, err := m.TaskChart(taskID, callID)
	if err != nil {
		return PinnedChart{}, err
	}
	m.mu.Lock()
	projectID := ""
	if s := m.sessions[taskID]; s != nil {
		projectID = s.projectID
	}
	m.mu.Unlock()
	id, err := newUUID()
	if err != nil {
		return PinnedChart{}, fmt.Errorf("generate chart id: %w", err)
	}
	rec := store.WebChart{ID: id, Title: c.Title, Kind: c.Kind, XLabel: c.XLabel, YLabel: c.YLabel, Command: c.Command, Format: c.Format,
		X: c.X, Y: c.Y, PinnedAt: m.now().UTC(), TaskID: taskID, CallID: callID}
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	count, existing := 0, -1
	err = m.store.Update(func(cfg *store.Config) error {
		p, ok := cfg.WebProjects[projectID]
		if !ok {
			return errProjectNotFound
		}
		if existing = slices.IndexFunc(p.Charts, func(w store.WebChart) bool { return w.TaskID == taskID && w.CallID == callID }); existing >= 0 {
			rec = p.Charts[existing]
			return nil
		}
		if shownPins(p.Charts) >= maxPinnedCharts {
			return newError(http.StatusConflict, "a project keeps at most %d pinned charts; unpin one first", maxPinnedCharts)
		}
		p.Charts = append(p.Charts, rec)
		cfg.WebProjects[projectID] = p
		count = shownPins(p.Charts)
		return nil
	})
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) {
			return PinnedChart{}, err
		}
		return PinnedChart{}, fmt.Errorf("save pinned chart: %w", err)
	}
	if existing < 0 {
		if err := writePrivateJSON(filepath.Dir(m.pinRoot()), m.pinFile(rec.ID), pinData{ChartData: c.ChartData}); err != nil {
			log.Warn("store a pinned chart's rows failed", "project", projectID, "error", err)
		}
		m.mu.Lock()
		m.setPinCountLocked(projectID, count)
		m.mu.Unlock()
	}
	return m.pinned(projectID, rec), nil
}

// UnpinChart removes the chart id from the Project projectID, rows and all.
func (m *Manager) UnpinChart(projectID, id string) error {
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	count := 0
	err := m.store.Update(func(cfg *store.Config) error {
		p, ok := cfg.WebProjects[projectID]
		if !ok {
			return errProjectNotFound
		}
		i := slices.IndexFunc(p.Charts, func(c store.WebChart) bool { return c.ID == id })
		if i < 0 {
			return errChartNotFound
		}
		p.Charts = slices.Delete(p.Charts, i, i+1)
		cfg.WebProjects[projectID] = p
		count = shownPins(p.Charts)
		return nil
	})
	if err != nil {
		if errors.Is(err, errProjectNotFound) || errors.Is(err, errChartNotFound) {
			return err
		}
		return fmt.Errorf("save pinned charts: %w", err)
	}
	if err := os.Remove(m.pinFile(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("remove a pinned chart's rows failed", "error", err)
	}
	m.mu.Lock()
	m.setPinCountLocked(projectID, count)
	m.mu.Unlock()
	return nil
}

var errChartNotFound = newError(http.StatusNotFound, "chart not found")

// RefreshChart runs the command of the chart id pinned to the Project
// projectID again in the Project directory and keeps the rows it reads, or
// the reason it failed beside the last good rows. No model takes part.
//
// On demand (auto false) a chart refreshes at most once a minute: sooner,
// or while it refreshes, the call is refused. auto is the refresh when the
// owner opens the Project's charts: at most once an hour, and otherwise it
// returns the chart as it is, as it does for a snapshot.
func (m *Manager) RefreshChart(ctx context.Context, projectID, id string, auto bool) (PinnedChart, error) {
	pins, err := m.storedPins(projectID)
	if err != nil {
		return PinnedChart{}, err
	}
	i := slices.IndexFunc(pins, func(c store.WebChart) bool { return c.ID == id })
	if i < 0 {
		return PinnedChart{}, errChartNotFound
	}
	rec := pins[i]
	m.mu.Lock()
	dir := ""
	if p := m.projects[projectID]; p != nil {
		dir = p.Dir
	}
	m.mu.Unlock()
	if dir == "" {
		return PinnedChart{}, errProjectNotFound
	}
	cur := m.pinned(projectID, rec)
	if rec.Command == "" {
		if auto {
			return cur, nil
		}
		return PinnedChart{}, newError(http.StatusConflict, "this chart is a snapshot of its rows; it has no command to run again")
	}
	every := chartManualEvery
	if auto {
		every = chartAutoEvery
	}
	now := m.now()
	r := &m.chartRuns
	r.mu.Lock()
	last := r.last[id]
	for _, t := range []time.Time{cur.At, cur.ErrorAt} {
		if t.After(last) {
			last = t
		}
	}
	switch wait := every - now.Sub(last); {
	case auto && (r.running[id] || wait > 0):
		r.mu.Unlock()
		return cur, nil
	case r.running[id]:
		r.mu.Unlock()
		return PinnedChart{}, newError(http.StatusConflict, "this chart is refreshing already")
	case wait > 0:
		r.mu.Unlock()
		return PinnedChart{}, newError(http.StatusTooManyRequests, "this chart refreshed less than a minute ago; try again in %ds", int(math.Ceil(wait.Seconds())))
	}
	if r.running == nil {
		r.running, r.last = map[string]bool{}, map[string]time.Time{}
	}
	r.running[id], r.last[id] = true, now
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}()

	data, _, err := readChart(ctx, pinSpec(rec), dir, chartInput{})
	if ctx.Err() != nil {
		return PinnedChart{}, context.Cause(ctx)
	}
	next := pinData{ChartData: data}
	if err != nil {
		next = pinData{ChartData: cur.ChartData, Error: shortError(err), ErrorAt: m.now().UTC()}
	}
	if err := writePrivateJSON(filepath.Dir(m.pinRoot()), m.pinFile(id), next); err != nil {
		return PinnedChart{}, fmt.Errorf("store the chart's rows: %w", err)
	}
	return m.pinned(projectID, rec), nil
}

// sweepPins removes the rows of charts no Project pins any more: one
// unpinned while it refreshed, or a removed Project's.
func (m *Manager) sweepPins(cfg store.Config) {
	entries, err := os.ReadDir(m.pinRoot())
	if err != nil {
		return
	}
	keep := map[string]bool{}
	for _, p := range cfg.WebProjects {
		for _, c := range p.Charts {
			keep[c.ID+".json"] = true
		}
	}
	for _, e := range entries {
		if !keep[e.Name()] && (strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".chart-")) {
			if err := os.Remove(filepath.Join(m.pinRoot(), e.Name())); err != nil {
				log.Warn("remove an unpinned chart's rows failed", "error", err)
			}
		}
	}
}

func (s *Server) chartRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/chart", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.m.TaskChart(r.PathValue("id"), r.URL.Query().Get("call"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("POST /api/sessions/{id}/chart/pin", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CallID string `json:"call_id"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		c, err := s.m.PinChart(r.PathValue("id"), body.CallID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("GET /api/projects/{id}/charts", func(w http.ResponseWriter, r *http.Request) {
		charts, err := s.m.PinnedCharts(r.PathValue("id"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]PinnedChart{"charts": charts})
	})
	mux.HandleFunc("POST /api/projects/{id}/charts/{chart_id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Auto bool `json:"auto"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		c, err := s.m.RefreshChart(r.Context(), r.PathValue("id"), r.PathValue("chart_id"), body.Auto)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("DELETE /api/projects/{id}/charts/{chart_id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.m.UnpinChart(r.PathValue("id"), r.PathValue("chart_id")); err != nil {
			writeFailure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
