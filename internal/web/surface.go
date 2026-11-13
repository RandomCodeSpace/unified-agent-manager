// PROTOTYPE: uam_surface (A2UI v1.0). Throwaway.

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const (
	surfaceToolName   = "uam_surface"
	surfaceCatalogID  = "urn:uam:catalog:v1"
	maxSurfaceCall    = 32 << 10
	maxSurfaceData    = 64 << 10
	maxSurfaceParts   = 200
	maxSurfaces       = 8
	maxSurfaceID      = 64
	maxSurfaceErrors  = 12
	maxSurfaceContext = 4 << 10
)

var surfaceTool = agentapi.HostTool{
	Name: surfaceToolName,
	Description: "Draw or update an interactive card in this conversation from A2UI v1.0 messages (catalog " + surfaceCatalogID + "); it returns at once. " +
		"Use it for a card the owner acts on or that you will keep updating, never for a static report (write Markdown). " +
		"Read the uam skill's references/surfaces.md before the first call. A Button press comes back later as a prompt starting [uam_surface action]. " +
		"Once drawn, change values with updateDataModel at a path rather than redrawing.",
	Parameters: toolSchema([]string{"messages"}, map[string]any{
		"messages": listProp(fmt.Sprintf("A2UI messages applied in order, all or none; each has version \"1.0\" and one of createSurface, updateComponents, updateDataModel, deleteSurface. At most %d KiB in all.", maxSurfaceCall>>10),
			map[string]any{"type": "object"}),
	}),
}

// surface is one card a Thread drew: its components by id, in first-seen order.
type surface struct {
	id      string
	callID  string
	version int
	deleted bool
	order   []string
	parts   map[string]map[string]any
	data    any
}

func (s *surface) clone() *surface {
	c := *s
	c.order = slices.Clone(s.order)
	c.parts = make(map[string]map[string]any, len(s.parts))
	for k, v := range s.parts {
		c.parts[k] = v
	}
	c.data = cloneJSON(s.data)
	return &c
}

func cloneJSON(v any) any {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// MarshalJSON is the GET /api/sessions/{id}/surface body.
func (s *surface) MarshalJSON() ([]byte, error) {
	parts := make([]map[string]any, 0, len(s.order))
	for _, id := range s.order {
		parts = append(parts, s.parts[id])
	}
	data := s.data
	if data == nil {
		data = map[string]any{}
	}
	return json.Marshal(struct {
		SurfaceID  string           `json:"surface_id"`
		CallID     string           `json:"call_id"`
		CatalogID  string           `json:"catalog_id"`
		Version    int              `json:"version"`
		Deleted    bool             `json:"deleted"`
		Components []map[string]any `json:"components"`
		DataModel  any              `json:"data_model"`
	}{s.id, s.callID, surfaceCatalogID, s.version, s.deleted, parts, data})
}

type surfaceEvent struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	CallID    string `json:"call_id"`
	SurfaceID string `json:"surface_id"`
	Version   int    `json:"version"`
}

// surfaceErrs collects validation errors, each naming where it is.
type surfaceErrs []string

func (e *surfaceErrs) add(format string, args ...any) {
	if len(*e) < maxSurfaceErrors {
		*e = append(*e, fmt.Sprintf(format, args...))
	}
}

// Component catalog: props each component takes; required ones first, up to req.
type surfaceSpec struct {
	props []string
	req   int
}

var surfaceCatalog = map[string]surfaceSpec{
	"Text":          {[]string{"text", "variant"}, 1},
	"Row":           {[]string{"children", "justify", "align"}, 1},
	"Column":        {[]string{"children", "justify", "align"}, 1},
	"List":          {[]string{"children", "direction", "align"}, 1},
	"Card":          {[]string{"child"}, 1},
	"Tabs":          {[]string{"tabs"}, 1},
	"Divider":       {[]string{"axis"}, 0},
	"Button":        {[]string{"child", "action", "variant", "@checks"}, 2},
	"CheckBox":      {[]string{"label", "value", "@checks"}, 2},
	"TextField":     {[]string{"label", "value", "placeholder", "variant", "@checks"}, 2},
	"ChoicePicker":  {[]string{"options", "value", "label", "variant", "@checks"}, 2},
	"Slider":        {[]string{"value", "max", "min", "label"}, 2},
	"Status":        {[]string{"label", "tone"}, 1},
	"Progress":      {[]string{"value", "max", "label"}, 1},
	"Image":         {[]string{"url", "description", "fit", "variant"}, 1},
	"Video":         {[]string{"url", "posterUrl"}, 1},
	"AudioPlayer":   {[]string{"url", "description"}, 1},
	"Icon":          {[]string{"name"}, 1},
	"Modal":         {[]string{"trigger", "content"}, 2},
	"DateTimeInput": {[]string{"value", "enableDate", "enableTime", "min", "max", "label", "@checks"}, 1},
}

// Media a surface shows are files of the Thread's working folder, served by
// the raw file route, by extension (imageExtensions and mediaTypes in files.go).
var surfaceMediaExts = map[string][]string{
	"Image.url":       {".png", ".jpg", ".jpeg", ".gif", ".webp"},
	"Video.posterUrl": {".png", ".jpg", ".jpeg", ".gif", ".webp"},
	"Video.url":       {".mp4", ".webm"},
	"AudioPlayer.url": {".mp3", ".ogg", ".wav", ".m4a"},
}

var (
	urlSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	svgPathRE   = regexp.MustCompile(`^[MmLlHhVvCcSsQqTtAaZz0-9.,\s-]+$`)
)

const maxSvgPath = 2048

var surfaceEnums = map[string][]string{
	"Text.variant":         {"caption", "body"},
	"Row.justify":          {"start", "center", "end", "spaceBetween", "spaceAround", "spaceEvenly", "stretch"},
	"Column.justify":       {"start", "center", "end", "spaceBetween", "spaceAround", "spaceEvenly", "stretch"},
	"Row.align":            {"start", "center", "end", "stretch"},
	"Column.align":         {"start", "center", "end", "stretch"},
	"List.align":           {"start", "center", "end", "stretch"},
	"List.direction":       {"vertical", "horizontal"},
	"Divider.axis":         {"horizontal", "vertical"},
	"Button.variant":       {"default", "primary", "borderless"},
	"TextField.variant":    {"shortText", "longText", "number"},
	"ChoicePicker.variant": {"mutuallyExclusive", "multipleSelection"},
	"Status.tone":          {"neutral", "running", "ok", "warn", "fail"},
	"Image.fit":            {"contain", "cover", "fill", "none", "scaleDown"},
	"Image.variant":        {"icon", "avatar", "smallFeature", "mediumFeature", "largeFeature", "header"},
	"Icon.name": {"accountCircle", "add", "arrowBack", "arrowForward", "attachFile", "calendarToday", "call", "camera", "check", "close",
		"delete", "download", "edit", "event", "error", "fastForward", "favorite", "favoriteOff", "folder", "help", "home", "info",
		"locationOn", "lock", "lockOpen", "mail", "menu", "moreVert", "moreHoriz", "notificationsOff", "notifications", "pause",
		"payment", "person", "phone", "photo", "play", "print", "refresh", "rewind", "search", "send", "settings", "share",
		"shoppingCart", "skipNext", "skipPrevious", "star", "starHalf", "starOff", "stop", "upload", "visibility", "visibilityOff",
		"volumeDown", "volumeMute", "volumeOff", "volumeUp", "warning"},
}

var surfaceIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func validSurfaceID(id string) bool { return surfaceIDRE.MatchString(id) }

// validPointer checks JSON Pointer syntax; relative allows a pointer
// without the leading slash (inside a list template).
func validPointer(p string, relative bool) bool {
	if !strings.HasPrefix(p, "/") && (!relative || p == "") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' && (i+1 == len(p) || p[i+1] != '0' && p[i+1] != '1') {
			return false
		}
	}
	return true
}

// checkDynamic checks a dynamic value: a literal of kind ("string",
// "number", "boolean", "list", "any") or {"@path": pointer}.
func checkDynamic(errs *surfaceErrs, where string, v any, kind string, enum []string) {
	if m, ok := v.(map[string]any); ok {
		if p, ok := m["@path"]; ok {
			s, isStr := p.(string)
			if len(m) != 1 || !isStr || !validPointer(s, true) {
				errs.add(`%s: a binding is {"@path": "/json/pointer"} with a valid JSON Pointer`, where)
			}
			return
		}
		if c, ok := m["@call"]; ok && c == "formatString" && kind == "string" {
			if _, ok := m["args"].(map[string]any); !ok {
				errs.add(`%s: formatString needs an args object`, where)
			}
			return
		}
		if _, ok := m["@call"]; ok {
			errs.add(`%s: @call is not supported here; use a literal or {"@path": ...}`, where)
			return
		}
		if kind != "any" {
			errs.add(`%s: expected a %s or {"@path": ...}, got an object`, where, kind)
		}
		return
	}
	ok := false
	switch kind {
	case "any":
		ok = true
	case "string":
		_, ok = v.(string)
	case "number":
		_, ok = v.(float64)
	case "boolean":
		_, ok = v.(bool)
	case "list":
		l, isList := v.([]any)
		ok = isList
		for _, x := range l {
			if _, s := x.(string); !s {
				ok = false
			}
		}
	}
	if !ok {
		errs.add(`%s: expected a %s or {"@path": ...}`, where, kind)
		return
	}
	if s, isStr := v.(string); isStr && enum != nil && !slices.Contains(enum, s) {
		errs.add(`%s: %q is not one of %s`, where, s, strings.Join(enum, ", "))
	}
}

// checkMediaPath checks a media URL: a literal path of a file in the
// Thread's working folder, relative or absolute under it, with one of exts.
// The page loads it from uam's own raw file route, never from another host.
func checkMediaPath(errs *surfaceErrs, where string, v any, workdir string, exts []string) {
	const fix = "save the file into the thread's working folder and pass its path, such as shots/home.png; URLs are not loaded"
	p, ok := v.(string)
	local := ok && p != "" && !strings.HasPrefix(p, "//") && !urlSchemeRE.MatchString(p)
	if local && filepath.IsAbs(p) {
		rel, err := filepath.Rel(workdir, p)
		local = workdir != "" && err == nil && filepath.IsLocal(rel)
	} else if local {
		local = filepath.IsLocal(p)
	}
	if !local {
		errs.add(`%s must be the literal path of a file in the thread's working folder; %s`, where, fix)
		return
	}
	if !slices.Contains(exts, strings.ToLower(filepath.Ext(p))) {
		errs.add(`%s must name a %s file`, where, strings.Join(exts, ", "))
	}
}

// isoShape reports whether s is ISO 8601 of the shape a DateTimeInput with
// date and clock takes.
func isoShape(s string, date, clock bool) bool {
	layouts := []string{"15:04", time.TimeOnly}
	if date {
		layouts = []string{time.DateOnly}
	}
	if date && clock {
		layouts = []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02T15:04Z07:00", time.RFC3339}
	}
	for _, l := range layouts {
		if _, err := time.Parse(l, s); err == nil {
			return true
		}
	}
	return false
}

// checkComponent validates one component definition and returns its id.
// workdir is the Thread's working folder, where media paths must point.
func checkComponent(errs *surfaceErrs, where string, c map[string]any, workdir string) string {
	id, _ := c["id"].(string)
	if !validSurfaceID(id) {
		errs.add(`%s: "id" must be 1 to %d letters, digits, _ - or .`, where, maxSurfaceID)
		return ""
	}
	name, _ := c["component"].(string)
	spec, ok := surfaceCatalog[name]
	if !ok {
		names := make([]string, 0, len(surfaceCatalog))
		for k := range surfaceCatalog {
			names = append(names, k)
		}
		slices.Sort(names)
		errs.add(`component %q: %q is not in the catalog; use one of %s`, id, name, strings.Join(names, ", "))
		return id
	}
	at := func(prop string) string { return fmt.Sprintf("component %q (%s) %q", id, name, prop) }
	for _, p := range spec.props[:spec.req] {
		if _, ok := c[p]; !ok {
			errs.add(`%s is required`, at(p))
		}
	}
	for prop, v := range c {
		if prop == "id" || prop == "component" {
			continue
		}
		if prop == "weight" { // every basic-catalog component takes a flex weight; the renderer applies it
			if w, ok := v.(float64); !ok || w <= 0 || w > 12 {
				errs.add(`%s must be a number from 0 to 12`, at(prop))
			}
			continue
		}
		if !slices.Contains(spec.props, prop) {
			errs.add(`%s is not a property of %s; it takes %s`, at(prop), name, strings.Join(spec.props, ", "))
			continue
		}
		enum := surfaceEnums[name+"."+prop]
		if exts := surfaceMediaExts[name+"."+prop]; exts != nil {
			checkMediaPath(errs, at(prop), v, workdir, exts)
			continue
		}
		if name == "DateTimeInput" && (prop == "value" || prop == "min" || prop == "max") {
			checkDynamic(errs, at(prop), v, "string", nil)
			date, clock := c["enableDate"] == true, c["enableTime"] == true
			if s, ok := v.(string); ok && s != "" && (date || clock) && !isoShape(s, date, clock) {
				shape := map[[2]bool]string{{true, false}: "a date such as 2026-11-13", {false, true}: "a time such as 14:30", {true, true}: "a date and time such as 2026-11-13T14:30"}[[2]bool{date, clock}]
				errs.add(`%s %q is not ISO 8601 %s`, at(prop), s, shape)
			}
			continue
		}
		switch prop {
		case "text", "label", "placeholder", "description":
			checkDynamic(errs, at(prop), v, "string", nil)
		case "tone":
			checkDynamic(errs, at(prop), v, "string", enum)
		case "enableDate", "enableTime":
			if _, ok := v.(bool); !ok {
				errs.add(`%s must be true or false`, at(prop))
			}
		case "name":
			if m, ok := v.(map[string]any); ok && m["@path"] == nil {
				p, isStr := m["svgPath"].(string)
				if len(m) != 1 || !isStr || len(p) > maxSvgPath || !svgPathRE.MatchString(p) {
					errs.add(`%s: {"svgPath": ...} must be a literal SVG path of at most %d characters: path command letters, digits, ".", "-", "," and spaces; or use an icon name`, at(prop), maxSvgPath)
				}
				continue
			}
			checkDynamic(errs, at(prop), v, "string", enum)
		case "variant", "justify", "align", "direction", "axis", "fit":
			if s, ok := v.(string); !ok || !slices.Contains(enum, s) {
				errs.add(`%s must be one of %s`, at(prop), strings.Join(enum, ", "))
			}
		case "min", "max":
			if _, ok := v.(float64); !ok && name == "Slider" {
				errs.add(`%s must be a number`, at(prop))
			} else if name != "Slider" {
				checkDynamic(errs, at(prop), v, "number", nil)
			}
		case "value":
			kind := map[string]string{"CheckBox": "boolean", "TextField": "string", "ChoicePicker": "list", "Slider": "number", "Progress": "number"}[name]
			checkDynamic(errs, at(prop), v, kind, nil)
		case "child", "trigger", "content":
			if s, ok := v.(string); !ok || s == "" {
				errs.add(`%s must be a component id`, at(prop))
			}
		case "children":
			checkChildren(errs, at(prop), v)
		case "tabs":
			tabs, ok := v.([]any)
			if !ok || len(tabs) == 0 {
				errs.add(`%s must be a non-empty list of {"title", "child"}`, at(prop))
			}
			for i, t := range tabs {
				tm, ok := t.(map[string]any)
				child, isStr := tm["child"].(string)
				if !ok || len(tm) != 2 || !isStr || child == "" || tm["title"] == nil {
					errs.add(`%s[%d] must be {"title": text, "child": id}`, at(prop), i)
					continue
				}
				checkDynamic(errs, fmt.Sprintf("%s[%d].title", at(prop), i), tm["title"], "string", nil)
			}
		case "options":
			opts, ok := v.([]any)
			if !ok || len(opts) == 0 {
				errs.add(`%s must be a non-empty list of {"label", "value"}`, at(prop))
			}
			for i, o := range opts {
				om, ok := o.(map[string]any)
				_, isStr := om["value"].(string)
				if !ok || len(om) != 2 || !isStr || om["label"] == nil {
					errs.add(`%s[%d] must be {"label": text, "value": string}`, at(prop), i)
					continue
				}
				checkDynamic(errs, fmt.Sprintf("%s[%d].label", at(prop), i), om["label"], "string", nil)
			}
		case "action":
			checkAction(errs, at(prop), v)
		case "@checks":
			checkChecks(errs, at(prop), v)
		}
	}
	if name == "DateTimeInput" && c["enableDate"] != true && c["enableTime"] != true {
		errs.add(`%s and "enableTime" are both off; set one or both to true`, at("enableDate"))
	}
	return id
}

func checkChildren(errs *surfaceErrs, where string, v any) {
	switch c := v.(type) {
	case []any:
		for i, x := range c {
			if s, ok := x.(string); !ok || s == "" {
				errs.add(`%s[%d] must be a component id`, where, i)
			}
		}
	case map[string]any:
		id, okID := c["componentId"].(string)
		p, okPath := c["path"].(string)
		if len(c) != 2 || !okID || id == "" || !okPath || !validPointer(p, true) {
			errs.add(`%s: a template is {"componentId": id, "path": "/json/pointer/to/list"}`, where)
		}
	default:
		errs.add(`%s must be a list of component ids or a template {"componentId", "path"}`, where)
	}
}

func checkAction(errs *surfaceErrs, where string, v any) {
	a, _ := v.(map[string]any)
	ev, ok := a["event"].(map[string]any)
	if len(a) != 1 || !ok {
		errs.add(`%s must be {"event": {"name": ..., "context": {...}}}; functionCall is not supported`, where)
		return
	}
	if name, _ := ev["name"].(string); !validSurfaceID(name) {
		errs.add(`%s.event.name must be 1 to %d letters, digits, _ - or .`, where, maxSurfaceID)
	}
	for k, x := range ev {
		switch k {
		case "name":
		case "context":
			ctx, ok := x.(map[string]any)
			if !ok {
				errs.add(`%s.event.context must be an object`, where)
			}
			for ck, cv := range ctx {
				checkDynamic(errs, where+".event.context."+ck, cv, "any", nil)
			}
		default:
			errs.add(`%s.event.%s is not supported; use name and context`, where, k)
		}
	}
}

func checkChecks(errs *surfaceErrs, where string, v any) {
	list, ok := v.([]any)
	if !ok {
		errs.add(`%s must be a list`, where)
		return
	}
	for i, x := range list {
		rule, _ := x.(map[string]any)
		cond, _ := rule["condition"].(map[string]any)
		args, _ := cond["args"].(map[string]any)
		if cond["@call"] != "required" || args == nil || len(args) != 1 {
			errs.add(`%s[%d]: only {"condition": {"@call": "required", "args": {"value": {"@path": ...}}}, "message": ...} is supported`, where, i)
			continue
		}
		checkDynamic(errs, fmt.Sprintf("%s[%d].condition.args.value", where, i), args["value"], "any", nil)
	}
}

// refs lists the component ids a component points at.
func surfaceRefs(c map[string]any) []string {
	var out []string
	for _, k := range []string{"child", "trigger", "content"} {
		if s, ok := c[k].(string); ok {
			out = append(out, s)
		}
	}
	switch ch := c["children"].(type) {
	case []any:
		for _, x := range ch {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case map[string]any:
		if s, ok := ch["componentId"].(string); ok {
			out = append(out, s)
		}
	}
	if tabs, ok := c["tabs"].([]any); ok {
		for _, t := range tabs {
			if tm, ok := t.(map[string]any); ok {
				if s, ok := tm["child"].(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// checkSurface checks a surface after a call: a root, references that
// resolve, no cycles, and size limits.
func checkSurface(errs *surfaceErrs, s *surface) {
	if s.deleted {
		return
	}
	if len(s.parts) > maxSurfaceParts {
		errs.add(`surface %q has %d components; the limit is %d`, s.id, len(s.parts), maxSurfaceParts)
	}
	if b, _ := json.Marshal(s.data); len(b) > maxSurfaceData {
		errs.add(`surface %q: the data model is %d bytes; the limit is %d`, s.id, len(b), maxSurfaceData)
	}
	if s.parts["root"] == nil {
		errs.add(`surface %q has no component with id "root"; send one in components`, s.id)
	}
	for _, id := range s.order {
		for _, ref := range surfaceRefs(s.parts[id]) {
			if s.parts[ref] == nil {
				errs.add(`surface %q component %q refers to %q, which does not exist`, s.id, id, ref)
			}
		}
	}
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			errs.add(`surface %q: component %q contains itself`, s.id, id)
			return false
		case 2:
			return true
		}
		state[id] = 1
		if c := s.parts[id]; c != nil {
			for _, ref := range surfaceRefs(c) {
				if !visit(ref) {
					return false
				}
			}
		}
		state[id] = 2
		return true
	}
	for _, id := range s.order {
		if !visit(id) {
			return
		}
	}
}

// setPointer sets (or, for nil, deletes) the value at an absolute JSON
// Pointer in doc, creating objects on the way, and returns the new doc.
func setPointer(doc any, path string, value any) (any, error) {
	if path == "" {
		return value, nil
	}
	tokens := strings.Split(path[1:], "/")
	for i, t := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	var set func(node any, i int) (any, error)
	set = func(node any, i int) (any, error) {
		tok, last := tokens[i], i == len(tokens)-1
		switch n := node.(type) {
		case []any:
			idx := len(n)
			if tok != "-" {
				var err error
				if idx, err = strconv.Atoi(tok); err != nil || idx < 0 || idx > len(n) {
					return nil, fmt.Errorf("%q is not an index of the list at that path", tok)
				}
			}
			if last {
				switch {
				case value == nil && idx < len(n):
					return slices.Delete(n, idx, idx+1), nil
				case value == nil:
					return n, nil
				case idx == len(n):
					return append(n, value), nil
				}
				n[idx] = value
				return n, nil
			}
			if idx == len(n) {
				n = append(n, nil)
			}
			child, err := set(n[idx], i+1)
			n[idx] = child
			return n, err
		case map[string]any:
			if last {
				if value == nil {
					delete(n, tok)
				} else {
					n[tok] = value
				}
				return n, nil
			}
			child, err := set(n[tok], i+1)
			n[tok] = child
			return n, err
		case nil:
			return set(map[string]any{}, i)
		default:
			return nil, fmt.Errorf("the path goes through a %T, which is not an object or list", node)
		}
	}
	return set(doc, 0)
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
	DataModel  any              `json:"dataModel"`
	Path       *string          `json:"path"`
	Value      json.RawMessage  `json:"value"`
}

// applySurfaceCall validates and applies one call's messages to a copy of
// the Thread's surfaces; media paths must lie in workdir. It returns the new map, the changed surfaces and
// one line per message, or the errors; on error nothing changes.
func applySurfaceCall(current map[string]*surface, callID, workdir string, raw json.RawMessage) (map[string]*surface, []*surface, []string, error) {
	if len(raw) > maxSurfaceCall {
		return nil, nil, nil, fmt.Errorf("the call is %d bytes; the limit is %d KiB. Send fewer components per call, or a smaller data model", len(raw), maxSurfaceCall>>10)
	}
	var args struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := decodeToolArgs(raw, &args); err != nil {
		return nil, nil, nil, err
	}
	if len(args.Messages) == 0 {
		return nil, nil, nil, errors.New("messages is empty; send at least one A2UI message")
	}
	next := make(map[string]*surface, len(current))
	for k, v := range current {
		next[k] = v
	}
	var errs surfaceErrs
	var lines []string
	touched := map[string]bool{}
	for i, msgRaw := range args.Messages {
		where := fmt.Sprintf("message %d", i+1)
		var msg surfaceMessage
		dec := json.NewDecoder(bytes.NewReader(msgRaw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&msg); err != nil {
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
		dec = json.NewDecoder(bytes.NewReader(kinds[kind]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
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
		s := next[body.SurfaceID]
		if kind == "createSurface" {
			if s != nil && !s.deleted {
				errs.add(`%s: surface %q already exists; use updateComponents or updateDataModel, or deleteSurface first`, where, body.SurfaceID)
				continue
			}
			if body.CatalogID != surfaceCatalogID {
				errs.add(`%s: "catalogId" must be %q`, where, surfaceCatalogID)
				continue
			}
			if s == nil && len(next) >= maxSurfaces {
				errs.add(`%s: this thread already has %d surfaces, the limit; update one or reuse a deleted surface's id`, where, maxSurfaces)
				continue
			}
			version := 0
			if s != nil {
				version = s.version
			}
			s = &surface{id: body.SurfaceID, callID: callID, version: version, parts: map[string]map[string]any{}, data: body.DataModel}
			if body.DataModel != nil {
				if _, ok := body.DataModel.(map[string]any); !ok {
					errs.add(`%s: "dataModel" must be an object`, where)
				}
			}
			lines = append(lines, "created surface "+s.id)
		} else {
			if s == nil || s.deleted {
				errs.add(`%s %s: there is no surface %q; create it first`, where, kind, body.SurfaceID)
				continue
			}
			if !touched[s.id] {
				s = s.clone()
			}
		}
		touched[s.id] = true
		next[s.id] = s
		s.version++
		switch kind {
		case "createSurface", "updateComponents":
			if kind == "updateComponents" && len(body.Components) == 0 {
				errs.add(`%s updateComponents: "components" is empty`, where)
			}
			seen := map[string]bool{}
			for j, c := range body.Components {
				id := checkComponent(&errs, fmt.Sprintf("%s component %d", where, j+1), c, workdir)
				if id == "" {
					continue
				}
				if seen[id] {
					errs.add(`%s: component id %q appears twice`, where, id)
				}
				seen[id] = true
				if s.parts[id] == nil {
					s.order = append(s.order, id)
				}
				s.parts[id] = c
			}
			if kind == "updateComponents" {
				lines = append(lines, fmt.Sprintf("updated %d components of %s", len(body.Components), s.id))
			}
		case "updateDataModel":
			var value any
			if body.Value == nil {
				errs.add(`%s updateDataModel: "value" is required (null deletes)`, where)
				continue
			}
			_ = json.Unmarshal(body.Value, &value)
			path := ""
			if body.Path != nil {
				path = *body.Path
				if path != "" && !validPointer(path, false) {
					errs.add(`%s updateDataModel: "path" %q is not a JSON Pointer such as /items/0/done`, where, path)
					continue
				}
			}
			if path == "" {
				if _, ok := value.(map[string]any); !ok && value != nil {
					errs.add(`%s updateDataModel: without a path, "value" must be an object (the whole model)`, where)
					continue
				}
			}
			data, err := setPointer(s.data, path, value)
			if err != nil {
				errs.add(`%s updateDataModel at %q: %v`, where, path, err)
				continue
			}
			s.data = data
			lines = append(lines, fmt.Sprintf("updated data model of %s at %q", s.id, path))
		case "deleteSurface":
			s.deleted = true
			lines = append(lines, "deleted surface "+s.id)
		}
	}
	var changed []*surface
	for id := range touched {
		checkSurface(&errs, next[id])
		changed = append(changed, next[id])
	}
	if len(errs) > 0 {
		return nil, nil, nil, errors.New("nothing was applied:\n- " + strings.Join(errs, "\n- "))
	}
	return next, changed, lines, nil
}

// surfaceCall runs a uam_surface call of the Task taskID.
func (m *Manager) surfaceCall(ctx context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	fail := func(err error) agentapi.HostToolResult {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	if call.TaskID != taskID {
		return fail(errors.New("the call does not belong to this thread"))
	}
	if call.CallID == "" || len(call.CallID) > maxToolCallID {
		return fail(errors.New("the call has no usable ID"))
	}
	_, end, err := m.startCall(ctx, taskID)
	if err != nil {
		return fail(err)
	}
	defer end()
	m.mu.Lock()
	defer m.mu.Unlock()
	workdir := ""
	if s := m.sessions[taskID]; s != nil {
		workdir = s.workdir
	}
	next, changed, lines, err := applySurfaceCall(m.surfaces[taskID], call.CallID, workdir, call.Arguments)
	if err != nil {
		return fail(err)
	}
	if m.surfaces == nil {
		m.surfaces = map[string]map[string]*surface{}
	}
	m.surfaces[taskID] = next
	for _, s := range changed {
		ev := surfaceEvent{SessionID: taskID, CallID: s.callID, SurfaceID: s.id, Version: s.version}
		m.broadcastLocked("surface", taskID, func(seq uint64) any { ev.Seq = seq; return ev })
	}
	return agentapi.HostToolResult{Text: strings.Join(lines, "\n")}
}

func (m *Manager) surfaceByCall(taskID, callID string) ([]byte, error) {
	if _, err := m.lookup(taskID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.surfaces[taskID] {
		if s.callID == callID && callID != "" {
			return json.Marshal(s)
		}
	}
	return nil, newError(http.StatusNotFound, "surface not found")
}

// SurfaceAction is the POST /api/sessions/{id}/surface/action body.
type SurfaceAction struct {
	SurfaceID         string          `json:"surface_id"`
	Name              string          `json:"name"`
	SourceComponentID string          `json:"source_component_id"`
	Context           json.RawMessage `json:"context"`
}

// surfaceAction queues a Button press to the Thread as a prompt line.
func (m *Manager) surfaceAction(taskID string, a SurfaceAction) (Submission, error) {
	if len(a.Context) > maxSurfaceContext {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, "the action context is over %d bytes", maxSurfaceContext)
	}
	ctx := map[string]any{}
	if len(a.Context) > 0 && string(a.Context) != "null" {
		if err := json.Unmarshal(a.Context, &ctx); err != nil {
			return Submission{}, newError(http.StatusBadRequest, "context must be an object")
		}
	}
	m.mu.Lock()
	s := m.surfaces[taskID][a.SurfaceID]
	var c map[string]any
	if s != nil && !s.deleted {
		c = s.parts[a.SourceComponentID]
	}
	m.mu.Unlock()
	if c == nil {
		return Submission{}, newError(http.StatusNotFound, "no such surface or component")
	}
	act, _ := c["action"].(map[string]any)
	ev, _ := act["event"].(map[string]any)
	if c["component"] != "Button" || ev["name"] != a.Name {
		return Submission{}, newError(http.StatusBadRequest, "the component is not a Button with that action")
	}
	type surfaceActionLine struct {
		Name              string         `json:"name"`
		SurfaceID         string         `json:"surfaceId"`
		SourceComponentID string         `json:"sourceComponentId"`
		Timestamp         string         `json:"timestamp"`
		Context           map[string]any `json:"context"`
	}
	line, err := json.Marshal(struct {
		Version string            `json:"version"`
		Action  surfaceActionLine `json:"action"`
	}{"1.0", surfaceActionLine{a.Name, a.SurfaceID, a.SourceComponentID, time.Now().UTC().Format(time.RFC3339), ctx}})
	if err != nil {
		return Submission{}, err
	}
	reqID, err := newUUID()
	if err != nil {
		return Submission{}, err
	}
	return m.Submit(taskID, PromptRequest{Text: "[uam_surface action] " + string(line), RequestID: reqID, Mode: ModeQueue})
}

func (s *Server) surfaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/surface", func(w http.ResponseWriter, r *http.Request) {
		body, err := s.m.surfaceByCall(r.PathValue("id"), r.URL.Query().Get("call"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, json.RawMessage(body))
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
