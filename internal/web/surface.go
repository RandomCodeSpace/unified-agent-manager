package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// maxSurfaceDepth bounds JSON Pointers and how deeply a data model nests.
	maxSurfaceDepth = 32
	// maxSurfaceRender bounds the components a card shows, counting shared
	// and template components each time they show.
	maxSurfaceRender = 2000
	// maxSurfacesFile is well above maxSurfaces cards at the component and
	// data limits; a larger surfaces file is not uam's.
	maxSurfacesFile = 2 << 20
	// maxSurfaceLabel and maxSurfaceTitle clip the pressed Button's text and
	// the card's title in an action line, in runes with the ellipsis.
	maxSurfaceLabel = 80
	maxSurfaceTitle = 120
	// surfacesFileVersion is the surfaces file's format; a file without one
	// is the same format.
	surfacesFileVersion = 1
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
	Version int `json:"version,omitempty"`
	// Surfaces are the live and deleted surfaces, oldest first, at most
	// maxSurfaces; a deleted one stays for the Thread's life, so its call
	// still reads as deleted and its id is never reused.
	Surfaces []*surface `json:"surfaces"`
}

// byID is the newest surface of id; only a file of an earlier build has
// older, deleted ones of the same id.
func (t *threadSurfaces) byID(id string) *surface {
	for _, s := range slices.Backward(t.Surfaces) {
		if s.SurfaceID == id {
			return s
		}
	}
	return nil
}

// byCall is the surface callID created, live or deleted.
func (t *threadSurfaces) byCall(callID string) *surface {
	for _, s := range t.Surfaces {
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

// createSurface adds the surface a createSurface message makes to t. It
// returns nil after adding an error. created is the surface the call
// already created.
func createSurface(errs *surfaceErrs, t *threadSurfaces, where, callID, created string, body surfaceBody) *surface {
	old := t.byID(body.SurfaceID)
	switch {
	case created != "":
		errs.add(`%s: this call already created surface %q; a call creates one surface, so send this createSurface in another call`, where, created)
		return nil
	case old != nil && old.Deleted:
		errs.add(`%s: surface %q was deleted in this thread and its id is not reused; choose a new id`, where, body.SurfaceID)
		return nil
	case old != nil:
		errs.add(`%s: surface %q already exists; use updateComponents or updateDataModel to change it`, where, body.SurfaceID)
		return nil
	case body.CatalogID != surfaceCatalogID:
		errs.add(`%s: "catalogId" must be %q`, where, surfaceCatalogID)
		return nil
	case len(t.Surfaces) >= maxSurfaces:
		errs.add(`%s: this thread's card limit is reached: it has drawn %d surfaces, deleted ones included; update one of its live surfaces instead`, where, maxSurfaces)
		return nil
	}
	data := map[string]any{}
	if body.DataModel != nil && string(body.DataModel) != "null" {
		if err := json.Unmarshal(body.DataModel, &data); err != nil {
			errs.add(`%s: "dataModel" must be an object of finite numbers, strings, booleans, lists and objects: %v`, where, err)
			return nil
		}
	}
	s := &surface{SurfaceID: body.SurfaceID, CallID: callID, CatalogID: surfaceCatalogID, Components: []map[string]any{}, DataModel: cmpMap(data)}
	t.Surfaces = append(t.Surfaces, s)
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
		errs.add(`%s updateDataModel: "path" %q is not a JSON Pointer such as /items/0/done, at most %d levels deep`, where, path, maxSurfaceDepth)
		return ""
	}
	data, err := setPointer(s.DataModel, path, value)
	if err != nil {
		errs.add(`%s updateDataModel at %q: %v`, where, path, err)
		return ""
	}
	model, ok := data.(map[string]any)
	if !ok && data != nil {
		errs.add(`%s updateDataModel: without a path, or at "/", "value" must be an object (the whole model)`, where)
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
// A file it cannot read, one over maxSurfacesFile, or one loadSurfaces
// refuses is an error, never an empty set a write would then put in its place.
func (m *Manager) readSurfaces(taskID string) (*threadSurfaces, error) {
	var t *threadSurfaces
	f, err := os.Open(m.surfacesPath(taskID))
	if errors.Is(err, fs.ErrNotExist) {
		return &threadSurfaces{Version: surfacesFileVersion}, nil
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(f, maxSurfacesFile+1))
		_ = f.Close()
	}
	if err == nil && len(data) > maxSurfacesFile {
		err = fmt.Errorf("the file is over %d bytes", maxSurfacesFile)
	}
	if err == nil {
		m.mu.Lock()
		var workdir string
		if s := m.sessions[taskID]; s != nil {
			workdir = s.workdir
		}
		m.mu.Unlock()
		t, err = loadSurfaces(data, workdir)
	}
	if err != nil {
		log.Warn("read the thread's surfaces failed", "session", taskID, "error", err)
		return nil, errors.New("uam could not read this thread's cards; try again")
	}
	return t, nil
}

// loadSurfaces decodes and checks a surfaces file as a call's changes are
// checked; media paths must lie in workdir. An earlier build's "retired"
// list is read as the deleted surfaces it is.
func loadSurfaces(data []byte, workdir string) (*threadSurfaces, error) {
	var file *struct {
		threadSurfaces
		Retired []*surface `json:"retired"`
	}
	if err := decodeStrict(data, &file); err != nil {
		return nil, err
	}
	switch {
	case file == nil:
		return nil, errors.New("the file is not an object")
	case file.Version != 0 && file.Version != surfacesFileVersion:
		return nil, fmt.Errorf("unknown version %d", file.Version)
	case len(file.Surfaces) > maxSurfaces:
		return nil, fmt.Errorf("%d surfaces; the limit is %d", len(file.Surfaces), maxSurfaces)
	}
	var errs surfaceErrs
	for _, s := range file.Retired {
		if s != nil && !s.Deleted {
			errs.add("retired surface %q is not deleted", s.SurfaceID)
		}
	}
	t := &threadSurfaces{Version: surfacesFileVersion, Surfaces: slices.Concat(file.Retired, file.Surfaces)}
	ids, calls := map[string]*surface{}, map[string]bool{}
	for i, s := range t.Surfaces {
		if s == nil {
			return nil, fmt.Errorf("surface %d is null", i+1)
		}
		where := fmt.Sprintf("surface %d (%q)", i+1, s.SurfaceID)
		if !validSurfaceID(s.SurfaceID) || s.CallID == "" || len(s.CallID) > maxToolCallID || s.CatalogID != surfaceCatalogID || s.Version < 1 {
			errs.add("%s: bad id, call id, catalog id or version", where)
		}
		// Only an earlier build's file repeats an id, after deleting it.
		if prev := ids[s.SurfaceID]; prev != nil && !prev.Deleted {
			errs.add("%s: the id appears twice", where)
		}
		if calls[s.CallID] {
			errs.add("%s: call %q appears twice", where, s.CallID)
		}
		ids[s.SurfaceID], calls[s.CallID] = s, true
		seen := map[string]bool{}
		for j, c := range s.Components {
			id := checkComponent(&errs, fmt.Sprintf("%s component %d", where, j+1), c, workdir)
			if id != "" && seen[id] {
				errs.add("%s: component id %q appears twice", where, id)
			}
			seen[id] = true
		}
		s.Components, s.DataModel = cmpSlice(s.Components), cmpMap(s.DataModel)
		checkSurface(&errs, s)
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return t, nil
}

// cmpSlice is l, or an empty list for nil.
func cmpSlice(l []map[string]any) []map[string]any {
	if l == nil {
		return []map[string]any{}
	}
	return l
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
	// BasePath is the template item the Button was pressed in, such as
	// /jobs/2; its text is read there.
	BasePath string `json:"base_path,omitempty"`
	// RequestID, a UUID per press, makes a retried POST queue nothing new.
	RequestID string `json:"request_id,omitempty"`
	// Version is the surface version the page showed; a press on any other
	// is refused, so the owner presses only what they saw.
	Version int `json:"version"`
}

// surfaceAction queues a Button press to the Thread as a one-line prompt.
// The Button must be on a live surface of that Thread and carry an action
// of that name, and the context may only fill the keys its action names.
// A request ID the Thread already has returns that press's outcome; a
// version other than the card's is a conflict.
func (m *Manager) surfaceAction(taskID string, a SurfaceAction) (Submission, error) {
	if !validSurfaceID(a.SurfaceID) || !validSurfaceID(a.Name) || !validSurfaceID(a.SourceComponentID) {
		return Submission{}, newError(http.StatusBadRequest, "surface_id, name and source_component_id are required")
	}
	if a.Version < 1 {
		return Submission{}, newError(http.StatusBadRequest, "version must be the card's version, a positive integer")
	}
	if a.BasePath != "" && (len(a.BasePath) > 512 || !validPointer(a.BasePath, false)) {
		return Submission{}, newError(http.StatusBadRequest, "base_path must be a JSON Pointer")
	}
	if a.RequestID != "" && !validRequestID(a.RequestID) {
		return Submission{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
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
	ws, err := m.lookup(taskID)
	if err != nil {
		return Submission{}, err
	}
	if a.RequestID != "" {
		// Answered before the card is checked, so a retry after the agent
		// changed the card still gets the first press's outcome.
		m.mu.Lock()
		sub, ok := ws.findRequest(a.RequestID)
		m.mu.Unlock()
		if ok {
			return sub, nil
		}
	}
	t, err := m.readSurfaces(taskID)
	if err != nil {
		return Submission{}, err
	}
	s := t.byID(a.SurfaceID)
	if s == nil || s.Deleted {
		return Submission{}, newError(http.StatusNotFound, "the card is gone")
	}
	// The label and title below are then those of the version the owner saw.
	if s.Version != a.Version {
		return Submission{}, newError(http.StatusConflict, "the card changed; review it and press again")
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
		// Label is the pressed Button's text and Title the card's, so the
		// model and the transcript name the press.
		Label string `json:"label,omitempty"`
		Title string `json:"title,omitempty"`
	}
	parts := make(map[string]map[string]any, len(s.Components))
	for _, c := range s.Components {
		id, _ := c["id"].(string)
		parts[id] = c
	}
	child, _ := s.Components[i]["child"].(string)
	title := surfaceText(parts, s.DataModel, "root", "", "title")
	if title == "" {
		title = surfaceText(parts, s.DataModel, "root", "", "")
	}
	line, err := json.Marshal(struct {
		Version string     `json:"version"`
		Action  actionLine `json:"action"`
	}{"1.0", actionLine{a.Name, a.SurfaceID, a.SourceComponentID, m.now().UTC().Format(time.RFC3339), ctx,
		clipRunes(surfaceText(parts, s.DataModel, child, a.BasePath, ""), maxSurfaceLabel-1), clipRunes(title, maxSurfaceTitle-1)}})
	if err != nil {
		return Submission{}, err
	}
	reqID := a.RequestID
	if reqID == "" {
		if reqID, err = newUUID(); err != nil {
			return Submission{}, err
		}
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
