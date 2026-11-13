package web

import (
	"encoding/json"
	"fmt"
	"iter"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// surfaceErrs collects validation errors, each naming where it is.
type surfaceErrs []string

func (e *surfaceErrs) add(format string, args ...any) {
	if len(*e) < maxSurfaceErrors {
		*e = append(*e, fmt.Sprintf(format, args...))
	}
}

// surfaceSpec lists the props a component takes, its required ones first, up to req.
type surfaceSpec struct {
	props []string
	req   int
}

// surfaceCatalog is urn:uam:catalog:v1: the 18 A2UI v1.0 basic-catalog
// components and uam's Status and Progress.
var surfaceCatalog = map[string]surfaceSpec{
	"Text":          {[]string{"text", "variant"}, 1},
	"Row":           {[]string{"children", "justify", "align"}, 1},
	"Column":        {[]string{"children", "justify", "align"}, 1},
	"List":          {[]string{"children", "direction", "align"}, 1},
	"Card":          {[]string{"child"}, 1},
	"Tabs":          {[]string{"tabs"}, 1},
	"Divider":       {[]string{"axis"}, 0},
	"Button":        {[]string{"child", "action", "variant", "@checks"}, 1},
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

// surfaceMediaExts are the extensions each media prop takes. Media are files
// of the Thread's working folder, served by the raw file route by extension
// (imageExtensions and mediaTypes in files.go).
var surfaceMediaExts = map[string][]string{
	"Image.url":       {".png", ".jpg", ".jpeg", ".gif", ".webp"},
	"Video.posterUrl": {".png", ".jpg", ".jpeg", ".gif", ".webp"},
	"Video.url":       {".mp4", ".webm"},
	"AudioPlayer.url": {".mp3", ".ogg", ".wav", ".m4a"},
}

var (
	urlSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	svgPathRE   = regexp.MustCompile(`^[MmLlHhVvCcSsQqTtAaZz0-9.,\s-]+$`)
	surfaceIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

const maxSvgPath = 2048

var surfaceEnums = map[string][]string{
	"Text.variant":         {"title", "body", "caption"},
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

func validSurfaceID(id string) bool { return surfaceIDRE.MatchString(id) }

// validPointer checks JSON Pointer syntax (RFC 6901) and a depth of at most
// maxSurfaceDepth; relative allows a pointer without the leading slash
// (inside a list template).
func validPointer(p string, relative bool) bool {
	if !strings.HasPrefix(p, "/") && (!relative || p == "") || strings.Count(p, "/") > maxSurfaceDepth {
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
				errs.add(`%s: a binding is {"@path": "/json/pointer"} with a valid JSON Pointer of at most %d levels`, where, maxSurfaceDepth)
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
// The page loads it from uam's own raw file route, which resolves links and
// serves nothing outside the folder; this check only tells the model early.
func checkMediaPath(errs *surfaceErrs, where string, v any, workdir string, exts []string) {
	const fix = "save the file into the thread's working folder and pass its path with / separators, such as shots/home.png; URLs are not loaded"
	p, ok := v.(string)
	// No scheme, no host, no backslash or percent escape to be read another way.
	local := ok && p != "" && !strings.HasPrefix(p, "//") && !urlSchemeRE.MatchString(p) && !strings.ContainsAny(p, "\\%\x00")
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
		if prop == "weight" { // every component takes a flex weight in a Row or Column
			if w, ok := v.(float64); !ok || w <= 0 || w > 12 {
				errs.add(`%s must be a number above 0 and at most 12`, at(prop))
			}
			continue
		}
		if !slices.Contains(spec.props, prop) {
			errs.add(`%s is not a property of %s; it takes %s`, at(prop), name, strings.Join(spec.props, ", "))
			continue
		}
		checkProp(errs, at(prop), c, name, prop, v, workdir)
	}
	if name == "DateTimeInput" && c["enableDate"] != true && c["enableTime"] != true {
		errs.add(`%s and "enableTime" are both off; set one or both to true`, at("enableDate"))
	}
	return id
}

// checkProp checks one catalog property's value; where names it.
func checkProp(errs *surfaceErrs, where string, c map[string]any, name, prop string, v any, workdir string) {
	enum := surfaceEnums[name+"."+prop]
	if exts := surfaceMediaExts[name+"."+prop]; exts != nil {
		checkMediaPath(errs, where, v, workdir, exts)
		return
	}
	if name == "DateTimeInput" && (prop == "value" || prop == "min" || prop == "max") {
		checkDynamic(errs, where, v, "string", nil)
		date, clock := c["enableDate"] == true, c["enableTime"] == true
		if s, ok := v.(string); ok && s != "" && (date || clock) && !isoShape(s, date, clock) {
			shape := map[[2]bool]string{{true, false}: "a date such as 2026-11-13", {false, true}: "a time such as 14:30", {true, true}: "a date and time such as 2026-11-13T14:30"}[[2]bool{date, clock}]
			errs.add(`%s %q is not ISO 8601 %s`, where, s, shape)
		}
		return
	}
	switch prop {
	case "text", "label", "placeholder", "description":
		checkDynamic(errs, where, v, "string", nil)
	case "tone":
		checkDynamic(errs, where, v, "string", enum)
	case "enableDate", "enableTime":
		if _, ok := v.(bool); !ok {
			errs.add(`%s must be true or false`, where)
		}
	case "name":
		if m, ok := v.(map[string]any); ok && m["@path"] == nil {
			p, isStr := m["svgPath"].(string)
			if len(m) != 1 || !isStr || len(p) > maxSvgPath || !svgPathRE.MatchString(p) {
				errs.add(`%s: {"svgPath": ...} must be a literal SVG path of at most %d characters: path command letters, digits, ".", "-", "," and spaces; or use an icon name`, where, maxSvgPath)
			}
			return
		}
		checkDynamic(errs, where, v, "string", enum)
	case "variant", "justify", "align", "direction", "axis", "fit":
		if s, ok := v.(string); !ok || !slices.Contains(enum, s) {
			errs.add(`%s must be one of %s`, where, strings.Join(enum, ", "))
		}
	case "min", "max":
		if _, ok := v.(float64); !ok && name == "Slider" {
			errs.add(`%s must be a number`, where)
		} else if name != "Slider" {
			checkDynamic(errs, where, v, "number", nil)
		}
	case "value":
		kind := map[string]string{"CheckBox": "boolean", "TextField": "string", "ChoicePicker": "list", "Slider": "number", "Progress": "number"}[name]
		checkDynamic(errs, where, v, kind, nil)
	case "child", "trigger", "content":
		if s, ok := v.(string); !ok || s == "" {
			errs.add(`%s must be a component id`, where)
		}
	case "children":
		checkChildren(errs, where, v)
	case "tabs":
		checkPairs(errs, where, v, "title", "child")
	case "options":
		checkPairs(errs, where, v, "label", "value")
	case "action":
		checkAction(errs, where, v)
	case "@checks":
		checkChecks(errs, where, v)
	}
}

// checkPairs checks a non-empty list of {text: dynamic string, key: id or
// value string}: Tabs' {title, child} and ChoicePicker's {label, value}.
func checkPairs(errs *surfaceErrs, where string, v any, text, key string) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		errs.add(`%s must be a non-empty list of {%q, %q}`, where, text, key)
	}
	for i, x := range list {
		m, ok := x.(map[string]any)
		s, isStr := m[key].(string)
		if !ok || len(m) != 2 || !isStr || s == "" && key == "child" || m[text] == nil {
			errs.add(`%s[%d] must be {%q: text, %q: string}`, where, i, text, key)
			continue
		}
		checkDynamic(errs, fmt.Sprintf("%s[%d].%s", where, i, text), m[text], "string", nil)
	}
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
			errs.add(`%s: a template is {"componentId": id, "path": "/json/pointer/to/list"}, at most %d levels deep`, where, maxSurfaceDepth)
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
		if cond["@call"] != "required" || len(args) != 1 {
			errs.add(`%s[%d]: only {"condition": {"@call": "required", "args": {"value": {"@path": ...}}}, "message": ...} is supported`, where, i)
			continue
		}
		checkDynamic(errs, fmt.Sprintf("%s[%d].condition.args.value", where, i), args["value"], "any", nil)
	}
}

// surfaceRefs lists the component ids a component points at.
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

// checkSurface checks a surface after a call: size limits, a root,
// references that resolve, no cycles, and an action on every Button but a
// Modal's trigger.
func checkSurface(errs *surfaceErrs, s *surface) {
	if s.Deleted {
		return
	}
	if len(s.Components) > maxSurfaceParts {
		errs.add(`surface %q has %d components; the limit is %d`, s.SurfaceID, len(s.Components), maxSurfaceParts)
		return
	}
	if b, err := json.Marshal(s.Components); err != nil {
		errs.add(`surface %q: its components do not encode as JSON: %v`, s.SurfaceID, err)
	} else if len(b) > maxSurfaceComponentBytes {
		errs.add(`surface %q: its components are %d bytes; the limit is %d. Use a template over a data list instead of one component per row`, s.SurfaceID, len(b), maxSurfaceComponentBytes)
	}
	if b, err := json.Marshal(s.DataModel); err != nil {
		errs.add(`surface %q: the data model does not encode as JSON: %v`, s.SurfaceID, err)
	} else if len(b) > maxSurfaceData {
		errs.add(`surface %q: the data model is %d bytes; the limit is %d`, s.SurfaceID, len(b), maxSurfaceData)
	}
	if jsonDepth(s.DataModel, maxSurfaceDepth+1) > maxSurfaceDepth {
		errs.add(`surface %q: the data model nests more than %d levels deep; flatten it`, s.SurfaceID, maxSurfaceDepth)
	}
	parts := make(map[string]map[string]any, len(s.Components))
	triggers := map[string]bool{}
	for _, c := range s.Components {
		id, _ := c["id"].(string)
		parts[id] = c
		if t, ok := c["trigger"].(string); ok && c["component"] == "Modal" {
			triggers[t] = true
		}
	}
	if parts["root"] == nil {
		errs.add(`surface %q has no component with id "root"; send one in components`, s.SurfaceID)
	}
	for _, c := range s.Components {
		id, _ := c["id"].(string)
		for _, ref := range surfaceRefs(c) {
			if parts[ref] == nil {
				errs.add(`surface %q component %q refers to %q, which does not exist`, s.SurfaceID, id, ref)
			}
		}
		if c["component"] == "Button" && c["action"] == nil && !triggers[id] {
			errs.add(`component %q (Button) "action" is required unless the Button is a Modal's trigger`, id)
		}
	}
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			errs.add(`surface %q: component %q contains itself`, s.SurfaceID, id)
			return false
		case 2:
			return true
		}
		state[id] = 1
		for _, ref := range surfaceRefs(parts[id]) {
			if !visit(ref) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for _, c := range s.Components {
		if id, _ := c["id"].(string); !visit(id) {
			return
		}
	}
	if n := renderedCount(parts, s.DataModel); n > maxSurfaceRender {
		errs.add(`surface %q would show at least %d components (a component counts each time it is referred to, and a template's once per item of its list); the limit is %d. Refer to fewer components, or show fewer items`, s.SurfaceID, n, maxSurfaceRender)
	}
}

// renderedCount counts the components a surface of parts shows from root
// over data: a component each time it is referred to, a template's once per
// item of its list now. It stops counting past maxSurfaceRender. parts must
// have no cycles.
func renderedCount(parts map[string]map[string]any, data any) int {
	n := 0
	var walk func(id, base string)
	walk = func(id, base string) {
		c := parts[id]
		if c == nil || n > maxSurfaceRender {
			return
		}
		n++
		// Row, Column and List refer to nothing but their children.
		if t, ok := c["children"].(map[string]any); ok {
			child, _ := t["componentId"].(string)
			path, _ := t["path"].(string)
			at := surfaceAbsolute(path, base)
			prefix := at
			if at == "/" {
				prefix = ""
			}
			// A missing item component counts nothing, so its list would not spend the budget.
			if parts[child] == nil {
				return
			}
			for k := range surfaceItems(getPointer(data, at)) {
				if n > maxSurfaceRender {
					return
				}
				walk(child, prefix+"/"+k)
			}
			return
		}
		for _, ref := range surfaceRefs(c) {
			walk(ref, base)
		}
	}
	walk("root", "")
	return n
}

// surfaceItems yields the pointer tokens of a template list's items: a
// list's indexes or an object's keys, escaped. It makes each only when
// asked, so a caller that stops early pays for what it took.
func surfaceItems(v any) iter.Seq[string] {
	return func(yield func(string) bool) {
		switch x := v.(type) {
		case []any:
			for i := range x {
				if !yield(strconv.Itoa(i)) {
					return
				}
			}
		case map[string]any:
			for k := range x {
				if !yield(strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")) {
					return
				}
			}
		}
	}
}

// surfaceAbsolute is the absolute pointer of a binding's path: one with a
// leading slash stands alone, any other is relative to base, a template
// item's pointer.
func surfaceAbsolute(path, base string) string {
	switch {
	case strings.HasPrefix(path, "/"):
		return path
	case path != "" && path != ".":
		return base + "/" + path
	case base != "":
		return base
	}
	return "/"
}

// surfaceString is a dynamic string's text against data at base, or "" when
// it does not resolve to a string, number or boolean.
func surfaceString(data, v any, base string) string {
	if m, ok := v.(map[string]any); ok {
		p, _ := m["@path"].(string)
		if p == "" {
			return ""
		}
		v = getPointer(data, surfaceAbsolute(p, base))
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// surfaceText is the text of the first Text at or under id, depth first,
// of variant if it is not "", against data at base; template children are
// not searched.
func surfaceText(parts map[string]map[string]any, data any, id, base, variant string) string {
	seen := map[string]bool{}
	var find func(id string) string
	find = func(id string) string {
		c := parts[id]
		if c == nil || seen[id] {
			return ""
		}
		seen[id] = true
		if c["component"] == "Text" && (variant == "" || c["variant"] == variant) {
			if s := surfaceString(data, c["text"], base); strings.TrimSpace(s) != "" {
				return s
			}
		}
		if _, ok := c["children"].(map[string]any); ok {
			return ""
		}
		for _, ref := range surfaceRefs(c) {
			if s := find(ref); s != "" {
				return s
			}
		}
		return ""
	}
	return find(id)
}

// jsonDepth is how deeply v nests objects and lists, counting at most to
// limit.
func jsonDepth(v any, limit int) int {
	var items []any
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			items = append(items, e)
		}
	case []any:
		items = x
	default:
		return 0
	}
	d := 1
	for _, e := range items {
		if d >= limit {
			break
		}
		d = max(d, 1+jsonDepth(e, limit-1))
	}
	return d
}

// pointerIndex parses a JSON Pointer token as an index of a list of n items:
// "-" or n appends, "0" to n-1 are items; no sign or leading zero (RFC 6901).
func pointerIndex(tok string, n int) (int, bool) {
	if tok == "-" {
		return n, true
	}
	if tok == "" || len(tok) > 1 && tok[0] == '0' || strings.Trim(tok, "0123456789") != "" {
		return 0, false
	}
	i, err := strconv.Atoi(tok)
	return i, err == nil && i <= n
}

// pointerTokens are an absolute JSON Pointer's decoded tokens; "" and "/"
// are the whole document (A2UI v1.0), so they have none.
func pointerTokens(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	tokens := strings.Split(path[1:], "/")
	for i, t := range tokens {
		// ~1 first, so "~01" decodes to "~1" (RFC 6901 section 4).
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return tokens
}

// getPointer is the value at an absolute JSON Pointer in doc, or nil.
func getPointer(doc any, path string) any {
	for _, tok := range pointerTokens(path) {
		switch n := doc.(type) {
		case []any:
			i, ok := pointerIndex(tok, len(n))
			if !ok || i == len(n) {
				return nil
			}
			doc = n[i]
		case map[string]any:
			doc = n[tok]
		default:
			return nil
		}
	}
	return doc
}

// setPointer sets (or, for nil, deletes) the value at an absolute JSON
// Pointer in doc, creating objects on the way, and returns the new doc. It
// changes doc in place. Deleting what is not there changes nothing. In a
// list, an index up to its length and "-" as the last token with a value
// append; any other "-" or a larger index is an error.
func setPointer(doc any, path string, value any) (any, error) {
	tokens := pointerTokens(path)
	if tokens == nil {
		return value, nil
	}
	var set func(node any, i int) (any, error)
	set = func(node any, i int) (any, error) {
		tok, last := tokens[i], i == len(tokens)-1
		switch n := node.(type) {
		case []any:
			if tok == "-" && (!last || value == nil) {
				return nil, fmt.Errorf(`"-" only appends a value to a list, as the path's last token, such as /jobs/-; use an index to change or delete an item`)
			}
			idx, ok := pointerIndex(tok, len(n))
			if !ok {
				return nil, fmt.Errorf("%q is not an index of the list at that path, which has %d items; use 0 to %d, or - to append", tok, len(n), len(n))
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
				if value == nil {
					return n, nil
				}
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
			cur, had := n[tok]
			child, err := set(cur, i+1)
			if had || child != nil {
				n[tok] = child
			}
			return n, err
		case nil:
			if value == nil { // nothing to delete
				return nil, nil
			}
			return set(map[string]any{}, i)
		default:
			return nil, fmt.Errorf("the path goes through a %T, which is not an object or list", node)
		}
	}
	return set(doc, 0)
}
