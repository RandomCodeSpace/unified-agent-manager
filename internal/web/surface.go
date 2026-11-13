package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	surfaceToolName  = "uam_surface"
	surfaceCatalogID = "urn:uam:catalog:v1"
	// surfacesFile keeps a Thread's surfaces in its upload directory, so it
	// goes with the Thread.
	surfacesFile             = "surfaces.json"
	maxSurfaceCall           = 32 << 10
	maxSurfaceData           = 64 << 10
	maxSurfaceComponentBytes = 64 << 10
	maxSurfaceParts          = 200
	maxSurfaces              = 8
	maxSurfaceID             = 64
	maxSurfaceErrors         = 12
	maxSurfaceContext        = 4 << 10
	// maxRetiredSurfaces bounds the deleted surfaces kept for the calls that
	// drew them after a new surface took their id.
	maxRetiredSurfaces = 64
	// surfaceCreatedLine starts the result's first line when the call
	// created a surface; the transcript marks the call by it (createdSurface).
	surfaceCreatedLine = "created surface "
	// surfaceActionPrefix starts the prompt a Button press queues.
	surfaceActionPrefix = "[uam_surface action] "
)

var surfaceTool = agentapi.HostTool{
	Name: surfaceToolName,
	Description: "Draw or update an interactive card in this conversation from A2UI v1.0 messages (catalog " + surfaceCatalogID + "); it returns at once. " +
		"Use it for a card the owner acts on or that you will keep updating, never for a static report (write Markdown). " +
		"Read the uam skill's references/surfaces.md before the first call. A Button press comes back later as a prompt starting " + strings.TrimSpace(surfaceActionPrefix) + ". " +
		"Once drawn, change values with updateDataModel at a path rather than redrawing.",
	Parameters: toolSchema([]string{"messages"}, map[string]any{
		"messages": listProp(fmt.Sprintf("A2UI messages applied in order, all or none; each has version \"1.0\" and one of createSurface, updateComponents, updateDataModel, deleteSurface. One createSurface per call; at most %d KiB in all.", maxSurfaceCall>>10),
			map[string]any{"type": "object"}),
	}),
}

// surface is one card a Thread drew, as GET /api/sessions/{id}/surface sends
// it and the Thread's surfaces file keeps it.
type surface struct {
	SurfaceID string `json:"surface_id"`
	// CallID is the uam_surface call that created it.
	CallID     string           `json:"call_id"`
	CatalogID  string           `json:"catalog_id"`
	Version    int              `json:"version"`
	Deleted    bool             `json:"deleted"`
	Components []map[string]any `json:"components"`
	DataModel  map[string]any   `json:"data_model"`
}

// threadSurfaces is a Thread's surfaces file.
type threadSurfaces struct {
	// Surfaces are the live and deleted surfaces by id, at most maxSurfaces.
	Surfaces []*surface `json:"surfaces"`
	// Retired are deleted surfaces whose id a later createSurface took,
	// oldest first, so their calls still read as deleted.
	Retired []*surface `json:"retired,omitempty"`
}

func (t *threadSurfaces) byID(id string) *surface {
	for _, s := range t.Surfaces {
		if s.SurfaceID == id {
			return s
		}
	}
	return nil
}

// byCall is the surface callID created, live, deleted or retired.
func (t *threadSurfaces) byCall(callID string) *surface {
	for _, s := range slices.Concat(t.Surfaces, t.Retired) {
		if s.CallID == callID {
			return s
		}
	}
	return nil
}

type surfaceEvent struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	CallID    string `json:"call_id"`
	SurfaceID string `json:"surface_id"`
	Version   int    `json:"version"`
}

type surfaceMessage struct {
	Version          string          `json:"version"`
	CreateSurface    json.RawMessage `json:"createSurface"`
	UpdateComponents json.RawMessage `json:"updateComponents"`
	UpdateDataModel  json.RawMessage `json:"updateDataModel"`
	DeleteSurface    json.RawMessage `json:"deleteSurface"`
}

type surfaceBody struct {
	SurfaceID  string           `json:"surfaceId"`
	CatalogID  string           `json:"catalogId"`
	Components []map[string]any `json:"components"`
	DataModel  json.RawMessage  `json:"dataModel"`
	Path       *string          `json:"path"`
	Value      json.RawMessage  `json:"value"`
}

// surfaceMessages decodes a uam_surface call's arguments, refusing an
// oversized call before decoding it.
func surfaceMessages(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) > maxSurfaceCall {
		return nil, fmt.Errorf("the call is %d bytes; the limit is %d KiB. Send fewer components per call, or a smaller data model", len(raw), maxSurfaceCall>>10)
	}
	var args struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := decodeToolArgs(raw, &args); err != nil {
		return nil, err
	}
	if len(args.Messages) == 0 {
		return nil, errors.New("messages is empty; send at least one A2UI message")
	}
	return args.Messages, nil
}

// decodeStrict decodes raw into v, refusing unknown fields.
func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// applySurfaceCall validates and applies the call callID's messages to t in
// place; media paths must lie in workdir. It returns the changed surfaces
// and one line per message, the created surface's first, or the errors; on
// error t is part-changed and the caller drops it.
func applySurfaceCall(t *threadSurfaces, callID, workdir string, msgs []json.RawMessage) ([]*surface, []string, error) {
	var errs surfaceErrs
	var lines []string
	created := ""
	var changed []*surface
	for i, msgRaw := range msgs {
		where := fmt.Sprintf("message %d", i+1)
		var msg surfaceMessage
		if err := decodeStrict(msgRaw, &msg); err != nil {
			errs.add(`%s: %v. A message has "version" and one of createSurface, updateComponents, updateDataModel, deleteSurface`, where, err)
			continue
		}
		if msg.Version != "1.0" {
			errs.add(`%s: "version" must be "1.0"`, where)
		}
		kinds := map[string]json.RawMessage{"createSurface": msg.CreateSurface, "updateComponents": msg.UpdateComponents, "updateDataModel": msg.UpdateDataModel, "deleteSurface": msg.DeleteSurface}
		kind := ""
		for k, v := range kinds {
			if v != nil {
				if kind != "" {
					kind = "many"
					break
				}
				kind = k
			}
		}
		if kind == "" || kind == "many" {
			errs.add(`%s must have exactly one of createSurface, updateComponents, updateDataModel, deleteSurface`, where)
			continue
		}
		var body surfaceBody
		if err := decodeStrict(kinds[kind], &body); err != nil {
			errs.add(`%s %s: %v`, where, kind, err)
			continue
		}
		allowed := map[string]bool{
			"createSurface":    body.Path == nil && body.Value == nil,
			"updateComponents": body.CatalogID == "" && body.DataModel == nil && body.Path == nil && body.Value == nil,
			"updateDataModel":  body.CatalogID == "" && body.Components == nil && body.DataModel == nil,
			"deleteSurface":    body.CatalogID == "" && body.Components == nil && body.DataModel == nil && body.Path == nil && body.Value == nil,
		}[kind]
		if !allowed {
			errs.add(`%s %s has fields of another message kind`, where, kind)
			continue
		}
		if !validSurfaceID(body.SurfaceID) {
			errs.add(`%s %s: "surfaceId" must be 1 to %d letters, digits, _ - or .`, where, kind, maxSurfaceID)
			continue
		}
		s := t.byID(body.SurfaceID)
		if kind == "createSurface" {
			s = createSurface(&errs, t, where, callID, created, body)
			if s == nil {
				continue
			}
			created = s.SurfaceID
		} else if s == nil || s.Deleted {
			errs.add(`%s %s: there is no surface %q; create it first`, where, kind, body.SurfaceID)
			continue
		}
		if !slices.Contains(changed, s) {
			changed = append(changed, s)
		}
		s.Version++
		switch kind {
		case "createSurface", "updateComponents":
			if kind == "updateComponents" && len(body.Components) == 0 {
				errs.add(`%s updateComponents: "components" is empty`, where)
			}
			upsertComponents(&errs, s, where, body.Components, workdir)
			if kind == "updateComponents" {
				lines = append(lines, fmt.Sprintf("updated %d components of %s", len(body.Components), s.SurfaceID))
			}
		case "updateDataModel":
			if line := updateDataModel(&errs, s, where, body); line != "" {
				lines = append(lines, line)
			}
		case "deleteSurface":
			s.Deleted, s.Components, s.DataModel = true, []map[string]any{}, map[string]any{}
			lines = append(lines, "deleted surface "+s.SurfaceID)
		}
	}
	for _, s := range changed {
		checkSurface(&errs, s)
	}
	if len(errs) > 0 {
		return nil, nil, errors.New("nothing was applied:\n- " + strings.Join(errs, "\n- "))
	}
	if created != "" {
		lines = slices.Insert(lines, 0, surfaceCreatedLine+created)
	}
	return changed, lines, nil
}

// createSurface adds the surface a createSurface message makes to t, in the
// place of a deleted one of its id, which t then retires. It returns nil
// after adding an error. created is the surface the call already created.
func createSurface(errs *surfaceErrs, t *threadSurfaces, where, callID, created string, body surfaceBody) *surface {
	old := t.byID(body.SurfaceID)
	switch {
	case created != "":
		errs.add(`%s: this call already created surface %q; a call creates one surface, so send this createSurface in another call`, where, created)
		return nil
	case old != nil && !old.Deleted:
		errs.add(`%s: surface %q already exists; use updateComponents or updateDataModel, or deleteSurface first`, where, body.SurfaceID)
		return nil
	case body.CatalogID != surfaceCatalogID:
		errs.add(`%s: "catalogId" must be %q`, where, surfaceCatalogID)
		return nil
	case old == nil && len(t.Surfaces) >= maxSurfaces:
		errs.add(`%s: this thread already has %d surfaces, the limit; update one, or delete one and reuse its id`, where, maxSurfaces)
		return nil
	}
	data := map[string]any{}
	if body.DataModel != nil && string(body.DataModel) != "null" && json.Unmarshal(body.DataModel, &data) != nil {
		errs.add(`%s: "dataModel" must be an object`, where)
		return nil
	}
	s := &surface{SurfaceID: body.SurfaceID, CallID: callID, CatalogID: surfaceCatalogID, Components: []map[string]any{}, DataModel: cmpMap(data)}
	if old == nil {
		t.Surfaces = append(t.Surfaces, s)
		return s
	}
	// Versions go on rising for an id, so a stale event never wins.
	s.Version = old.Version
	t.Surfaces[slices.Index(t.Surfaces, old)] = s
	t.Retired = append(t.Retired, old)
	if len(t.Retired) > maxRetiredSurfaces {
		t.Retired = slices.Delete(t.Retired, 0, len(t.Retired)-maxRetiredSurfaces)
	}
	return s
}

// cmpMap is m, or an empty map for nil.
func cmpMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// upsertComponents checks components and puts each in s, replacing the one
// of its id whole or adding it at the end.
func upsertComponents(errs *surfaceErrs, s *surface, where string, components []map[string]any, workdir string) {
	seen := map[string]bool{}
	for j, c := range components {
		id := checkComponent(errs, fmt.Sprintf("%s component %d", where, j+1), c, workdir)
		if id == "" {
			continue
		}
		if seen[id] {
			errs.add(`%s: component id %q appears twice`, where, id)
		}
		seen[id] = true
		if k := slices.IndexFunc(s.Components, func(o map[string]any) bool { return o["id"] == id }); k >= 0 {
			s.Components[k] = c
		} else {
			s.Components = append(s.Components, c)
		}
	}
}

// updateDataModel applies an updateDataModel message to s and returns its
// line, or "" after adding an error.
func updateDataModel(errs *surfaceErrs, s *surface, where string, body surfaceBody) string {
	if body.Value == nil {
		errs.add(`%s updateDataModel: "value" is required (null deletes)`, where)
		return ""
	}
	var value any
	if err := json.Unmarshal(body.Value, &value); err != nil {
		errs.add(`%s updateDataModel: "value": %v`, where, err)
		return ""
	}
	path := ""
	if body.Path != nil {
		path = *body.Path
	}
	if path != "" && !validPointer(path, false) {
		errs.add(`%s updateDataModel: "path" %q is not a JSON Pointer such as /items/0/done`, where, path)
		return ""
	}
	data, err := setPointer(s.DataModel, path, value)
	if err != nil {
		errs.add(`%s updateDataModel at %q: %v`, where, path, err)
		return ""
	}
	model, ok := data.(map[string]any)
	if !ok && data != nil {
		errs.add(`%s updateDataModel: without a path, "value" must be an object (the whole model)`, where)
		return ""
	}
	s.DataModel = cmpMap(model)
	return fmt.Sprintf("updated data model of %s at %q", s.SurfaceID, path)
}

// createdSurface is the id of the surface a completed uam_surface call
// created, from its result's first line; "" for any other call.
func createdSurface(tool *agentapi.ToolCall) string {
	if tool.Name != surfaceToolName || tool.Status != agentapi.ToolCompleted {
		return ""
	}
	line, _, _ := strings.Cut(tool.Output, "\n")
	if id, ok := strings.CutPrefix(line, surfaceCreatedLine); ok && validSurfaceID(id) {
		return id
	}
	return ""
}

func (m *Manager) surfacesPath(taskID string) string {
	return filepath.Join(m.taskUploadDir(taskID), surfacesFile)
}

// readSurfaces reads the Thread taskID's surfaces file; none is no surfaces.
func (m *Manager) readSurfaces(taskID string) (*threadSurfaces, error) {
	t := &threadSurfaces{}
	data, err := os.ReadFile(m.surfacesPath(taskID))
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err == nil {
		err = json.Unmarshal(data, t)
	}
	if err != nil {
		log.Warn("read the thread's surfaces failed", "session", taskID, "error", err)
		return nil, errors.New("uam could not read this thread's cards; try again")
	}
	return t, nil
}

// surfaceCall runs a uam_surface call of the Task taskID.
func (m *Manager) surfaceCall(ctx context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.drawSurface(ctx, taskID, call)
	if err != nil {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	return agentapi.HostToolResult{Text: text}
}

func (m *Manager) drawSurface(ctx context.Context, taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this thread")
	}
	if call.CallID == "" || len(call.CallID) > maxToolCallID {
		return "", errors.New("the call has no usable ID")
	}
	msgs, err := surfaceMessages(call.Arguments)
	if err != nil {
		return "", err
	}
	_, end, err := m.startCall(ctx, taskID)
	if err != nil {
		return "", err
	}
	defer end()
	m.mu.Lock()
	s := m.sessions[taskID]
	var workdir string
	if s != nil {
		workdir = s.workdir
	}
	m.mu.Unlock()
	if s == nil {
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	// surfaceMu makes each call's read, change and write of the file one step.
	m.surfaceMu.Lock()
	t, err := m.readSurfaces(taskID)
	var changed []*surface
	var lines []string
	if err == nil {
		changed, lines, err = applySurfaceCall(t, call.CallID, workdir, msgs)
	}
	if err == nil {
		if werr := writePrivateJSON(m.uploadRoot(), m.surfacesPath(taskID), t); werr != nil {
			log.Warn("store the thread's surfaces failed", "session", taskID, "error", werr)
			err = errors.New("uam could not store the card; try again")
		}
	}
	m.surfaceMu.Unlock()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	removed := s.removed
	if !removed {
		for _, c := range changed {
			ev := surfaceEvent{SessionID: taskID, CallID: c.CallID, SurfaceID: c.SurfaceID, Version: c.Version}
			m.broadcastLocked("surface", taskID, func(seq uint64) any { ev.Seq = seq; return ev })
		}
	}
	m.mu.Unlock()
	if removed {
		// The Thread was deleted meanwhile; take the file written after its removal.
		removeUploads(m.taskUploadDir(taskID))
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	return strings.Join(lines, "\n"), nil
}

// TaskSurface returns the surface the Task id's call callID created, live
// or deleted; 404 when that call created none.
func (m *Manager) TaskSurface(id, callID string) (*surface, error) {
	if _, err := m.lookup(id); err != nil {
		return nil, err
	}
	if callID == "" || len(callID) > maxToolCallID {
		return nil, newError(http.StatusNotFound, "surface not found")
	}
	t, err := m.readSurfaces(id)
	if err != nil {
		return nil, err
	}
	if s := t.byCall(callID); s != nil {
		return s, nil
	}
	return nil, newError(http.StatusNotFound, "surface not found")
}

// SurfaceAction is the POST /api/sessions/{id}/surface/action body: a
// Button press.
type SurfaceAction struct {
	SurfaceID         string          `json:"surface_id"`
	Name              string          `json:"name"`
	SourceComponentID string          `json:"source_component_id"`
	Context           json.RawMessage `json:"context"`
}

// surfaceAction queues a Button press to the Thread as a one-line prompt.
// The Button must be on a live surface of that Thread and carry an action
// of that name, and the context may only fill the keys its action names.
func (m *Manager) surfaceAction(taskID string, a SurfaceAction) (Submission, error) {
	if !validSurfaceID(a.SurfaceID) || !validSurfaceID(a.Name) || !validSurfaceID(a.SourceComponentID) {
		return Submission{}, newError(http.StatusBadRequest, "surface_id, name and source_component_id are required")
	}
	if len(a.Context) > maxSurfaceContext {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, "the action context is over %d bytes", maxSurfaceContext)
	}
	ctx := map[string]any{}
	if len(a.Context) > 0 && string(a.Context) != "null" {
		if err := json.Unmarshal(a.Context, &ctx); err != nil {
			return Submission{}, newError(http.StatusBadRequest, "context must be an object")
		}
	}
	if _, err := m.lookup(taskID); err != nil {
		return Submission{}, err
	}
	t, err := m.readSurfaces(taskID)
	if err != nil {
		return Submission{}, err
	}
	s := t.byID(a.SurfaceID)
	if s == nil || s.Deleted {
		return Submission{}, newError(http.StatusNotFound, "the card is gone")
	}
	i := slices.IndexFunc(s.Components, func(c map[string]any) bool { return c["id"] == a.SourceComponentID })
	var event map[string]any
	if i >= 0 && s.Components[i]["component"] == "Button" {
		action, _ := s.Components[i]["action"].(map[string]any)
		event, _ = action["event"].(map[string]any)
	}
	if event == nil || event["name"] != a.Name {
		return Submission{}, newError(http.StatusBadRequest, "the component is not a Button with that action")
	}
	declared, _ := event["context"].(map[string]any)
	for k := range ctx {
		if _, ok := declared[k]; !ok {
			return Submission{}, newError(http.StatusBadRequest, "the action has no context key %q", k)
		}
	}
	type actionLine struct {
		Name              string         `json:"name"`
		SurfaceID         string         `json:"surfaceId"`
		SourceComponentID string         `json:"sourceComponentId"`
		Timestamp         string         `json:"timestamp"`
		Context           map[string]any `json:"context"`
	}
	line, err := json.Marshal(struct {
		Version string     `json:"version"`
		Action  actionLine `json:"action"`
	}{"1.0", actionLine{a.Name, a.SurfaceID, a.SourceComponentID, m.now().UTC().Format(time.RFC3339), ctx}})
	if err != nil {
		return Submission{}, err
	}
	reqID, err := newUUID()
	if err != nil {
		return Submission{}, err
	}
	return m.Submit(taskID, PromptRequest{Text: surfaceActionPrefix + string(line), RequestID: reqID, Mode: ModeQueue})
}

func (s *Server) surfaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/surface", func(w http.ResponseWriter, r *http.Request) {
		surface, err := s.m.TaskSurface(r.PathValue("id"), r.URL.Query().Get("call"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, surface)
	})
	mux.HandleFunc("POST /api/sessions/{id}/surface/action", func(w http.ResponseWriter, r *http.Request) {
		var body SurfaceAction
		if !decodeBody(w, r, &body) {
			return
		}
		sub, err := s.m.surfaceAction(r.PathValue("id"), body)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, sub)
	})
}
