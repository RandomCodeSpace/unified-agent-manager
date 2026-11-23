package web

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// uamCallerView is what the write ops read of the caller under mu.
type uamCallerView struct {
	project string
	yolo    bool
	spawned bool
}

func (m *Manager) uamCallerView(ctx context.Context, id string) (uamCallerView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	caller, err := m.uamCallerLocked(ctx, id)
	if err != nil {
		return uamCallerView{}, err
	}
	return uamCallerView{project: caller.projectID, yolo: caller.mode == store.ModeYolo && !caller.modeUnknown,
		spawned: caller.spawnedBy != "" || caller.routineID != ""}, nil
}

// uamThreadRow is the Thread id's row after a change.
func (m *Manager) uamThreadRow(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.removed {
		return "", errors.New(msgSessionNotFound)
	}
	return uamJSON(m.uamTaskRowLocked(s))
}

// uamWrite runs a uam op that changes state. Each calls the Manager method
// the owner's route calls, on the caller's own Thread, a Thread it started,
// or a routine of its Project; uam_ops_routes_test.go maps the routes.
func (m *Manager) uamWrite(ctx context.Context, id string, in uamRequest) (string, error) {
	caller, err := m.uamCallerView(ctx, id)
	if err != nil {
		return "", err
	}
	switch in.Op {
	case "rename":
		var args struct {
			Name *string `json:"name"`
		}
		if err := uamDecode(in.Args, &args); err != nil {
			return "", err
		}
		if args.Name == nil {
			return "", errors.New("name is required; an empty name shows the title again")
		}
		if _, err := m.Rename(id, *args.Name); err != nil {
			return "", err
		}
		return m.uamThreadRow(id)
	case "set_budget":
		var args struct {
			Credits      float64 `json:"credits"`
			QuotaPercent float64 `json:"quota_percent"`
		}
		if err := uamDecode(in.Args, &args); err != nil {
			return "", err
		}
		if _, err := m.setBudget(id, args.Credits, args.QuotaPercent, func(s *webSession) error {
			return budgetTightens(s.budget, s.budgetSpent(), args.Credits, args.QuotaPercent)
		}); err != nil {
			return "", err
		}
		return m.uamThreadRow(id)
	case "pin_chart", "unpin_chart":
		var args struct {
			CallID string `json:"call_id"`
		}
		if err := uamDecode(in.Args, &args); err != nil {
			return "", err
		}
		if args.CallID == "" {
			return "", errors.New("call_id is required")
		}
		if in.Op == "unpin_chart" {
			return m.uamUnpin(id, caller.project, args.CallID)
		}
		c, err := m.TaskChart(id, args.CallID)
		if err != nil {
			return "", err
		}
		// A pinned chart's command reruns on refresh without a permission request, as uam_chart's does.
		if c.Command != "" && !caller.yolo {
			return "", errors.New("this chart runs a command, so pinning it needs Yolo")
		}
		pin, err := m.PinChart(id, args.CallID)
		if err != nil {
			return "", err
		}
		return uamJSON(map[string]any{"pinned_id": pin.ID, "title": uamText(pin.Title), "project": pin.ProjectID})
	case "run_routine":
		var args struct {
			ID string `json:"id"`
		}
		if err := uamDecode(in.Args, &args); err != nil {
			return "", err
		}
		return m.uamRunRoutine(caller, args.ID)
	case "settle", "archive":
		var args struct {
			ID    string `json:"id"`
			Force bool   `json:"force"`
		}
		if err := uamDecode(in.Args, &args); err != nil {
			return "", err
		}
		return m.uamPutAway(ctx, id, in.Op, args.ID, args.Force)
	}
	return "", errors.New("unknown operation")
}

// budgetTightens refuses an agent's budget unless it is at least as strict
// as the one its owner set, b, of which spent credits are used: an agent may
// add or lower a limit, never raise or remove one.
func budgetTightens(b store.Budget, spent, credits, quotaPercent float64) error {
	if b.Reached {
		return errors.New("the budget is reached; only the owner can change it now")
	}
	// Only the limits that are set are named: "up to the 0.00 left" for a limit that does not exist reads as no way through.
	var limits []string
	if b.Credits > 0 {
		limits = append(limits, fmt.Sprintf("credits above 0 and up to the %.2f left", max(0, b.Credits-spent)))
	}
	if b.QuotaPercent > 0 {
		limits = append(limits, fmt.Sprintf("quota_percent above 0 and up to %g", b.QuotaPercent))
	}
	if b.Credits > 0 && !(credits > 0 && credits <= b.Credits-spent) || b.QuotaPercent > 0 && !(quotaPercent > 0 && quotaPercent <= b.QuotaPercent) {
		return fmt.Errorf("you can only tighten the budget the owner set: give %s", strings.Join(limits, ", and "))
	}
	return nil
}

// uamUnpin unpins a chart the caller pinned from its own Thread.
func (m *Manager) uamUnpin(id, project, callID string) (string, error) {
	pins, err := m.storedPins(project)
	if err != nil {
		return "", err
	}
	for _, p := range pins {
		if p.TaskID == id && p.CallID == callID {
			if err := m.UnpinChart(project, p.ID); err != nil {
				return "", err
			}
			return uamJSON(map[string]any{"unpinned_id": p.ID})
		}
	}
	return "", errors.New("no chart of this thread with that call_id is pinned")
}

// uamRunRoutine fires a routine of the caller's Project now, as the owner's
// Run now does, under the limits uam_create_task keeps: a Thread another
// Thread or a routine started cannot start Threads, and a Yolo routine
// needs a Yolo caller.
func (m *Manager) uamRunRoutine(caller uamCallerView, id string) (string, error) {
	if id == "" {
		return "", errors.New("id is required")
	}
	if caller.spawned {
		return "", errors.New("a thread started by another thread or a routine cannot start threads, so it cannot run routines")
	}
	// routines.mu is never taken while holding m.mu.
	var held *Routine
	if caller.project != "" {
		for _, r := range m.listRoutines(caller.project) {
			if r.ID == id {
				held = &r
				break
			}
		}
	}
	if held == nil {
		return "", errors.New("no routine of your project has that id (uam op routines lists them)")
	}
	if held.Mode == string(store.ModeYolo) && !caller.yolo {
		return "", errors.New("that routine runs in Yolo, so running it needs Yolo")
	}
	r, err := m.RunRoutine(id)
	if err != nil {
		return "", err
	}
	out := map[string]any{"id": r.ID, "name": uamText(r.Name)}
	if len(r.Runs) > 0 {
		run := r.Runs[0]
		out["outcome"] = uamText(run.Outcome)
		if run.Reason != "" {
			out["reason"] = uamText(run.Reason)
		}
	}
	return uamJSON(out)
}

// uamPutAway settles or archives a Thread the caller started, refused with
// unsaved work of its own unless force, as the owner's route does.
func (m *Manager) uamPutAway(ctx context.Context, id, op, target string, force bool) (string, error) {
	if target == "" {
		return "", errors.New("id is required")
	}
	m.mu.Lock()
	s := m.sessions[target]
	mine := s != nil && !s.removed && s.spawnedBy == id
	m.mu.Unlock()
	if !mine {
		return "", fmt.Errorf("you can %s only threads you started", op)
	}
	if !force {
		if err := m.checkUnsaved(ctx, target); err != nil {
			var refused *Error
			if errors.As(err, &refused) && refused.Code == codeUnsavedWork {
				files := ""
				if len(refused.Refs) > 0 {
					files = " Files: " + strings.Join(refused.Refs, ", ") + "."
				}
				return "", fmt.Errorf("%s%s Have it commit and push, or pass force true; a worktree with changes is kept", refused.Message, files)
			}
			return "", err
		}
	}
	move := (*Manager).Settle
	if op == "archive" {
		move = (*Manager).Archive
	}
	if _, err := move(m, target); err != nil {
		return "", err
	}
	return m.uamThreadRow(target)
}
