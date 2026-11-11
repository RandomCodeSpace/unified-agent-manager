package web

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The agent's edits of a drawing (uam_diagram_edit) and the owner's
// selection (docs/web.md "Drawings"). An edit changes the elements a
// drawing already has, so the owner's layout stays: rename, recolour,
// remove. Adding a shape or an arrow needs geometry that only the browser's
// converter builds, so that still takes a new mermaid block.

const (
	diagramEditToolName = "uam_diagram_edit"
	// maxDiagramEdits bounds one call; maxDiagramLabel one new label.
	maxDiagramEdits = 50
	maxDiagramLabel = 200
	// maxDrawingSelection bounds the elements one selection names, and
	// maxDrawingID one of their ids.
	maxDrawingSelection = 50
	maxDrawingID        = 128
)

var diagramEditTool = agentapi.HostTool{
	Name: diagramEditToolName,
	Description: "Change a drawing in place, keeping the owner's layout: rename, recolour or remove its nodes and arrows. " +
		"The edits apply together or not at all. To add a node or an arrow, write a new mermaid block.",
	Parameters: toolSchema([]string{"path", "edits"}, map[string]any{
		"path": stringProp("The drawing's path, such as docs/flow.excalidraw."),
		"edits": listProp(fmt.Sprintf("The changes, one per element, 1 to %d. Each names its element by node or by id.", maxDiagramEdits), toolSchema(nil, map[string]any{
			"node":   stringProp("The node as uam_diagram_read names it: its label."),
			"id":     stringProp("The element's id, where a selection note gives one. Arrows go by id."),
			"rename": stringProp("The new label. An .excalidraw element needs a label already."),
			"stroke": stringProp("The outline or text colour, as #rrggbb."),
			"fill":   stringProp("The fill colour, as #rrggbb or transparent."),
			"remove": map[string]any{"type": "boolean", "description": "Remove it with its label; a node's arrows go with it."},
		})),
	}),
}

// diagramEdit is one change of a uam_diagram_edit call.
type diagramEdit struct {
	Node   string  `json:"node"`
	ID     string  `json:"id"`
	Rename *string `json:"rename"`
	Stroke string  `json:"stroke"`
	Fill   string  `json:"fill"`
	Remove bool    `json:"remove"`
}

var diagramColour = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// ref names an element so the agent can pass it back: a node by its name,
// with its id when another node has the same name; an edge always with its
// id. Empty when id is neither.
func (d *diagram) ref(id string) string {
	if e := d.edge(id); e != nil {
		return "edge " + d.edgeText(*e) + " (id " + id + ")"
	}
	if d.node(id) == nil {
		return ""
	}
	name := d.name(id)
	for _, o := range d.nodes {
		if o.ID != id && d.name(o.ID) == name {
			return name + " (id " + id + ")"
		}
	}
	return name
}

// target is the id of the one element an edit names: by id, or a node by
// its label or as describe prints it.
func (d *diagram) target(e diagramEdit) (string, error) {
	node, id := strings.TrimSpace(e.Node), strings.TrimSpace(e.ID)
	switch {
	case (node == "") == (id == ""):
		return "", errors.New("each edit names its element by node or by id, not both")
	case id != "":
		if d.node(id) == nil && d.edge(id) == nil {
			return "", fmt.Errorf("no element with id %q in the drawing", id)
		}
		return id, nil
	}
	var ids []string
	for _, n := range d.nodes {
		if n.Label == node || d.name(n.ID) == node {
			ids = append(ids, n.ID)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", fmt.Errorf("no node %q in the drawing; uam_diagram_read lists its nodes", node)
	}
	return "", fmt.Errorf("%q names %d nodes; name one by id: %s", node, len(ids), strings.Join(ids, ", "))
}

// boundTextPadding is Excalidraw's gap between a shape and its label.
const boundTextPadding = 5

// glyphEm is a rune's width in the drawings' font as a share of the font
// size, by class. Go has no font metrics; these stay within a few percent
// of what Excalidraw measures for a diagram's labels.
func glyphEm(r rune) float64 {
	switch {
	case strings.ContainsRune("iljtfr.,:;'|!()[] -", r):
		return 0.33
	case strings.ContainsRune("mwMW", r):
		return 0.8
	case unicode.IsUpper(r):
		return 0.65
	case r > unicode.MaxLatin1:
		return 1
	}
	return 0.55
}

func labelEm(s string) float64 {
	em := 0.0
	for _, r := range s {
		em += glyphEm(r)
	}
	return em
}

// wrapLabel breaks label's lines at spaces so none is wider than max em; a
// word wider than that keeps its own line.
func wrapLabel(label string, max float64) []string {
	var out []string
	for line := range strings.SplitSeq(label, "\n") {
		cur := ""
		for word := range strings.FieldsSeq(line) {
			switch {
			case cur == "":
				cur = word
			case labelEm(cur+" "+word) <= max:
				cur += " " + word
			default:
				out, cur = append(out, cur), word
			}
		}
		out = append(out, cur)
	}
	return out
}

// num is the number el holds at key, as decoded or as set here.
func num(el map[string]any, key string) float64 {
	switch n := el[key].(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	}
	return 0
}

// relabel puts label on the text element text. Bound to a shape, the label
// is wrapped to the shape's width and centred in it, and a shape too low
// for it grows about its own centre, as Excalidraw does when a label is
// typed. An arrow's label keeps its centre, free text its corner.
func relabel(text, container map[string]any, label string) {
	size, lineHeight := num(text, "fontSize"), num(text, "lineHeight")
	if size <= 0 {
		size = 20
	}
	if lineHeight <= 0 {
		lineHeight = 1.25
	}
	// The share of a shape's width and height its label may take.
	share := map[any]float64{"rectangle": 1, "ellipse": math.Sqrt2 / 2, "diamond": 0.5}[container["type"]]
	lines := strings.Split(label, "\n")
	if share > 0 {
		lines = wrapLabel(label, (num(container, "width")*share-2*boundTextPadding)/size)
	}
	w := 0.0
	for _, line := range lines {
		w = max(w, labelEm(line)*size)
	}
	h := float64(len(lines)) * size * lineHeight
	x, y := num(text, "x")+(num(text, "width")-w)/2, num(text, "y")+(num(text, "height")-h)/2
	switch {
	case share > 0:
		if need := (h + 2*boundTextPadding) / share; need > num(container, "height") {
			container["y"] = num(container, "y") - (need-num(container, "height"))/2
			container["height"] = need
		}
		x, y = num(container, "x")+(num(container, "width")-w)/2, num(container, "y")+(num(container, "height")-h)/2
	case container == nil:
		// shortcut: right-aligned free text keeps its left edge, anchor the right one if a drawing ever has some
		y = num(text, "y")
		if text["textAlign"] != "center" {
			x = num(text, "x")
		}
	}
	text["text"], text["originalText"] = strings.Join(lines, "\n"), label
	text["x"], text["y"], text["width"], text["height"] = x, y, w, h
}

// check refuses an edit of the element name that asks for nothing, for
// more than a removal, or for a colour that is not one.
func (e diagramEdit) check(name string) error {
	switch {
	case e.Remove && (e.Rename != nil || e.Stroke != "" || e.Fill != ""):
		return fmt.Errorf("the edit of %s removes it, so it takes no other change", name)
	case !e.Remove && e.Rename == nil && e.Stroke == "" && e.Fill == "":
		return fmt.Errorf("the edit of %s changes nothing: give rename, stroke, fill or remove", name)
	case e.Stroke != "" && !diagramColour.MatchString(e.Stroke):
		return fmt.Errorf("stroke %q is not a colour: use #rrggbb", e.Stroke)
	case e.Fill != "" && e.Fill != "transparent" && !diagramColour.MatchString(e.Fill):
		return fmt.Errorf("fill %q is not a colour: use #rrggbb or transparent", e.Fill)
	case e.Rename != nil && (strings.TrimSpace(*e.Rename) == "" || len(*e.Rename) > maxDiagramLabel):
		return fmt.Errorf("a label takes 1 to %d characters", maxDiagramLabel)
	}
	return nil
}

// editTargets checks edits against the drawing d and returns the id of the
// element each one names, in their order.
func editTargets(d *diagram, edits []diagramEdit) ([]string, error) {
	ids := make([]string, 0, len(edits))
	seen := map[string]bool{}
	for _, e := range edits {
		id, err := d.target(e)
		if err != nil {
			return nil, err
		}
		name := d.ref(id)
		if seen[id] {
			return nil, fmt.Errorf("two edits name %s; put its changes in one edit", name)
		}
		seen[id] = true
		if err := e.check(name); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// apply makes the edit's rename and colours on the element el, named name,
// whose label is the text element label (nil without one).
func (e diagramEdit) apply(name string, el, label map[string]any) error {
	if e.Rename != nil {
		text, container := label, el
		if el["type"] == "text" {
			text, container = el, nil
		}
		if text == nil {
			return fmt.Errorf("%s has no label to rename, and uam cannot add text to an .excalidraw drawing yet", name)
		}
		relabel(text, container, strings.TrimSpace(*e.Rename))
	}
	if e.Stroke != "" {
		el["strokeColor"] = e.Stroke
	}
	if e.Fill != "" {
		el["backgroundColor"] = e.Fill
	}
	return nil
}

// dropElements is elements without the gone ones, and without what the
// rest kept of them: their entries in boundElements and their frame.
func dropElements(elements []map[string]any, gone map[string]bool) []map[string]any {
	kept := elements[:0]
	for _, el := range elements {
		if id, _ := el["id"].(string); gone[id] {
			continue
		}
		if bound, ok := el["boundElements"].([]any); ok {
			el["boundElements"] = slices.DeleteFunc(bound, func(b any) bool {
				ref, _ := b.(map[string]any)
				id, _ := ref["id"].(string)
				return gone[id]
			})
		}
		if frame, _ := el["frameId"].(string); gone[frame] {
			el["frameId"] = nil
		}
		kept = append(kept, el)
	}
	return kept
}

// editExcalidraw applies edits to the Excalidraw file content, whose model
// is d, and returns the new file. Elements it does not touch, and fields it
// does not know, are kept as they are.
func editExcalidraw(content []byte, d *diagram, edits []diagramEdit) ([]byte, error) {
	ids, err := editTargets(d, edits)
	if err != nil {
		return nil, err
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, errors.New("not an Excalidraw file")
	}
	// Numbers stay as written: an element's seed and nonce are not floats.
	dec := json.NewDecoder(bytes.NewReader(file["elements"]))
	dec.UseNumber()
	var elements []map[string]any
	if err := dec.Decode(&elements); err != nil {
		return nil, errors.New("not an Excalidraw file: it needs an elements list")
	}
	byID := map[string]map[string]any{}
	label := map[string]map[string]any{}
	for _, el := range elements {
		if deleted, _ := el["isDeleted"].(bool); deleted {
			continue
		}
		id, _ := el["id"].(string)
		byID[id] = el
		if container, _ := el["containerId"].(string); container != "" && el["type"] == "text" {
			label[container] = el
		}
	}
	gone := map[string]bool{}
	for i, e := range edits {
		id := ids[i]
		if e.Remove {
			gone[id] = true
		} else if err := e.apply(d.ref(id), byID[id], label[id]); err != nil {
			return nil, err
		}
	}
	// A removed node takes its arrows, and every removed element its label.
	for _, e := range d.edges {
		if gone[e.From] || gone[e.To] {
			gone[e.ID] = true
		}
	}
	for container, text := range label {
		if id, _ := text["id"].(string); gone[container] {
			gone[id] = true
		}
	}
	data, err := json.Marshal(dropElements(elements, gone))
	if err != nil {
		return nil, err
	}
	file["elements"] = data
	return json.Marshal(file)
}

// mxChange is what an edit makes of one draw.io cell: a new value, new
// colours in its style, or both.
type mxChange struct {
	value        *string
	stroke, fill string
}

// setStyle sets key=value in a draw.io style string.
func setStyle(style, key, value string) string {
	parts := strings.Split(strings.TrimSuffix(style, ";"), ";")
	for i, part := range parts {
		if strings.HasPrefix(part, key+"=") {
			parts[i] = key + "=" + value
			return strings.Join(parts, ";") + ";"
		}
	}
	if style == "" {
		return key + "=" + value + ";"
	}
	return strings.Join(parts, ";") + ";" + key + "=" + value + ";"
}

// setAttr sets the attribute name of an element's start tag.
func setAttr(start *xml.StartElement, name, value string) {
	for i, a := range start.Attr {
		if a.Name.Local == name && a.Name.Space == "" {
			start.Attr[i].Value = value
			return
		}
	}
	start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}

func attrOf(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

// apply makes the change on an <mxCell> start tag.
func (c mxChange) apply(start *xml.StartElement) {
	if c.value != nil {
		setAttr(start, "value", *c.value)
	}
	style := attrOf(*start, "style")
	if c.stroke != "" {
		style = setStyle(style, "strokeColor", c.stroke)
	}
	if c.fill != "" {
		style = setStyle(style, "fillColor", strings.Replace(c.fill, "transparent", "none", 1))
	}
	if style != attrOf(*start, "style") {
		setAttr(start, "style", style)
	}
}

// copyDrawio copies a .drawio document from dec to enc: the gone cells of
// its first page are left out and the changes made to the others; every
// other token passes as it is. A compressed page is written plain, which
// draw.io reads as well.
func copyDrawio(dec *xml.Decoder, enc *xml.Encoder, gone map[string]bool, change map[string]mxChange) error {
	var stack []string
	pages := 0
	// The id of the <object> or <UserObject> whose own <mxCell> comes next.
	wrapper := ""
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if name == "diagram" {
				pages++
			}
			cell := name == "mxCell" || name == "object" || name == "UserObject"
			switch {
			case pages > 1:
			case cell && parent == "root":
				id := attrOf(t, "id")
				if gone[id] {
					if err := dec.Skip(); err != nil {
						return err
					}
					continue
				}
				if c := change[id]; name == "mxCell" {
					c.apply(&t)
				} else {
					if c.value != nil {
						setAttr(&t, "label", *c.value)
					}
					wrapper = id
				}
			case name == "mxCell" && (parent == "object" || parent == "UserObject"):
				c := change[wrapper]
				c.value = nil
				c.apply(&t)
			}
			stack = append(stack, name)
			tok = t
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if pages == 1 && parent == "diagram" && len(bytes.TrimSpace(t)) > 0 {
				if inner, err := inflateDrawio(string(t)); err == nil {
					if err := copyDrawio(xml.NewDecoder(bytes.NewReader(inner)), enc, gone, change); err != nil {
						return err
					}
					continue
				}
			}
		}
		if err := enc.EncodeToken(tok); err != nil {
			return err
		}
	}
}

// editDrawio applies edits to the .drawio file content, whose model is d,
// and returns the new file. A removed cell takes its children and the edges
// that end at it; draw.io lays a new label out itself, so a rename also
// labels a cell that had none.
func editDrawio(content []byte, d *diagram, edits []diagramEdit) ([]byte, error) {
	ids, err := editTargets(d, edits)
	if err != nil {
		return nil, err
	}
	items, err := drawioItems(content)
	if err != nil {
		return nil, err
	}
	byID := map[string]mxItem{}
	// An edge's label when it is a child cell, not the edge's own value.
	edgeLabel := map[string]string{}
	for _, it := range items {
		byID[it.ID] = it
		if strings.Contains(it.Style, "edgeLabel") {
			edgeLabel[it.Parent] = it.ID
		}
	}
	gone := map[string]bool{}
	change := map[string]mxChange{}
	for i, e := range edits {
		id := ids[i]
		if e.Remove {
			gone[id] = true
			continue
		}
		if e.Rename != nil {
			at := id
			if child, ok := edgeLabel[id]; ok && drawioText(byID[id].Value) == "" {
				at = child
			}
			value := strings.TrimSpace(*e.Rename)
			if strings.Contains(byID[at].Style, "html=1") {
				value = strings.ReplaceAll(html.EscapeString(value), "\n", "<br>")
			}
			c := change[at]
			c.value = &value
			change[at] = c
		}
		c := change[id]
		c.stroke, c.fill = e.Stroke, e.Fill
		change[id] = c
	}
	for grew := true; grew; {
		grew = false
		for _, it := range items {
			if it.ID != "" && !gone[it.ID] && (gone[it.Parent] || it.Edge == "1" && (gone[it.Source] || gone[it.Target])) {
				gone[it.ID], grew = true, true
			}
		}
	}
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	if err := copyDrawio(xml.NewDecoder(bytes.NewReader(content)), enc, gone, change); err != nil {
		return nil, errors.New("not a draw.io file")
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// diagramEditCall runs a uam_diagram_edit call of the Task taskID.
func (m *Manager) diagramEditCall(_ context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, rel, err := m.editDiagram(taskID, call)
	if err != nil {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	if rel != "" {
		m.count("drawing.edit")
		m.drawingWritten(taskID, rel)
	}
	return agentapi.HostToolResult{Text: text}
}

// editDiagram applies the call's edits to its drawing as the agent's next
// version, and returns what changed and the path it wrote; the path is
// empty when the drawing already was that way.
func (m *Manager) editDiagram(taskID string, call agentapi.HostToolCall) (string, string, error) {
	if call.TaskID != taskID {
		return "", "", errors.New("the call does not belong to this thread")
	}
	var in struct {
		Path  string        `json:"path"`
		Edits []diagramEdit `json:"edits"`
	}
	if err := decodeToolArgs(call.Arguments, &in); err != nil {
		return "", "", err
	}
	if len(in.Edits) == 0 || len(in.Edits) > maxDiagramEdits {
		return "", "", fmt.Errorf("edits takes 1 to %d changes", maxDiagramEdits)
	}
	dir, err := m.sessionDir(taskID)
	if err != nil {
		return "", "", err
	}
	rel, format, err := drawingPath(strings.TrimSpace(in.Path))
	if err != nil {
		return "", "", err
	}
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	idx := m.drawingsLocked(taskID)
	e := idx.live(rel)
	if e == nil {
		return "", "", fmt.Errorf("there is no drawing at %s; uam_diagram_read without path lists them", rel)
	}
	// The agent names elements as its version has them, so it reads the
	// owner's changes before it edits over them.
	if e.Version > e.AgentVersion {
		return "", "", fmt.Errorf("the owner changed %s since your version (v%d → v%d); call uam_diagram_read on it first", rel, e.AgentVersion, e.Version)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = root.Close() }()
	content, err := readDrawingFile(root, rel)
	if err != nil {
		return "", "", fmt.Errorf("the drawing %s cannot be read: %w", rel, err)
	}
	current, err := parseDiagram(format, content)
	if err != nil {
		return "", "", err
	}
	editFile := editExcalidraw
	if format == "drawio" {
		editFile = editDrawio
	}
	edited, err := editFile(content, current, in.Edits)
	if err != nil {
		return "", "", err
	}
	after, err := parseDiagram(format, edited)
	if err != nil {
		return "", "", err
	}
	changes, _ := diffDiagrams(current, after)
	switch {
	case len(changes) == 0:
		return rel + " already is that way; nothing changed.", "", nil
	case len(edited) > maxDrawingBytes:
		return "", "", fmt.Errorf("a drawing can be at most %d MiB", maxDrawingBytes>>20)
	}
	if err := writeDrawingFile(root, rel, edited); err != nil {
		log.Warn("write a drawing failed", "session", taskID, "error", err)
		return "", "", fmt.Errorf("uam could not write %s", rel)
	}
	e.Version++
	catchUpLocked(e, edited)
	if err := m.saveDrawingsLocked(taskID, idx); err != nil {
		log.Warn("write the drawings index failed", "session", taskID, "error", err)
	}
	lines := []string{fmt.Sprintf("%s is now v%d:", rel, e.Version)}
	for _, c := range changes {
		lines = append(lines, c.Kind+" "+c.Text)
	}
	return strings.Join(lines, "\n"), rel, nil
}

// selectionNote is the note of the elements the owner picked for their next
// message; empty when none of ids is a node or an edge of current.
func selectionNote(path string, ids []string, current *diagram) string {
	var names []string
	for _, id := range ids {
		if ref := current.ref(id); ref != "" {
			names = append(names, ref)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("The user selected in %s: %s. Their message refers to these.", path, strings.Join(names, ", "))
}

// note is what the next prompt carries for the drawing rel: the owner's
// changes, then their selection.
func (e *drawingEntry) note(rel string, changes []DrawingChange, current *diagram) string {
	return strings.TrimSpace(drawingNote(rel, e.AgentVersion, e.Version, changes, current) + "\n\n" + selectionNote(rel, e.Selected, current))
}

// SelectDrawing keeps the elements of the drawing p the owner picked for
// their next message (PATCH); an empty list forgets them. The next prompt's
// note names them, once.
func (m *Manager) SelectDrawing(id, p string, ids []string) error {
	if _, err := m.sessionDir(id); err != nil {
		return err
	}
	rel, _, err := drawingPath(p)
	if err != nil {
		return err
	}
	if len(ids) > maxDrawingSelection {
		return newError(http.StatusBadRequest, "a selection names at most %d elements", maxDrawingSelection)
	}
	for _, el := range ids {
		if el == "" || len(el) > maxDrawingID {
			return newError(http.StatusBadRequest, "an element id takes 1 to %d characters", maxDrawingID)
		}
	}
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	idx := m.drawingsLocked(id)
	e := idx.live(rel)
	if e == nil {
		return newError(http.StatusNotFound, "drawing not found")
	}
	e.Selected = slices.Clone(ids)
	if err := m.saveDrawingsLocked(id, idx); err != nil {
		log.Warn("write the drawings index failed", "session", id, "error", err)
		return newError(http.StatusInternalServerError, "uam could not record the selection")
	}
	if len(ids) > 0 {
		m.count("drawing.select")
	}
	return nil
}
