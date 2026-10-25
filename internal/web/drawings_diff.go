package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// A deterministic model of a drawing and the diff between two of them: what
// uam_diagram_read returns and what the note on the owner's next prompt
// says. No model is involved. It is a port of the prototype's diff.js, whose
// wording real agents were tested with, so the texts here match it exactly.

// diagramMovePx is the distance past which a node counts as moved.
const diagramMovePx = 8

// diagramNode is a shape: a box, ellipse, diamond, frame or free text.
type diagramNode struct {
	ID, Type, Label string
	X, Y, W, H      float64
	Stroke, Fill    string
}

// diagramEdge is an arrow or line; From and To are the node IDs it binds,
// empty when loose.
type diagramEdge struct {
	ID, From, To, Label string
}

// diagram is a drawing's nodes and edges in file order.
type diagram struct {
	nodes []diagramNode
	edges []diagramEdge
}

// DrawingChange is one line of a diff: Kind is "+", "-" or "~".
type DrawingChange struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func (d *diagram) node(id string) *diagramNode {
	for i := range d.nodes {
		if d.nodes[i].ID == id {
			return &d.nodes[i]
		}
	}
	return nil
}

func (d *diagram) edge(id string) *diagramEdge {
	for i := range d.edges {
		if d.edges[i].ID == id {
			return &d.edges[i]
		}
	}
	return nil
}

// name is how a node is referred to: its label quoted, or for an unlabelled
// shape its kind and where it sits.
func (d *diagram) name(id string) string {
	n := d.node(id)
	if n == nil {
		return "(nothing)"
	}
	if n.Label != "" {
		return `"` + n.Label + `"`
	}
	kind := n.Type
	if kind == "rectangle" {
		kind = "box"
	}
	return "unlabelled " + kind + " " + d.near(n)
}

// near names the closest labelled node and which side of it n is on.
func (d *diagram) near(n *diagramNode) string {
	var best *diagramNode
	var bdx, bdy float64
	gap := math.Inf(1)
	for i := range d.nodes {
		o := &d.nodes[i]
		if o.Label == "" {
			continue
		}
		dx := o.X + o.W/2 - (n.X + n.W/2)
		dy := o.Y + o.H/2 - (n.Y + n.H/2)
		if dist := math.Hypot(dx, dy); dist < gap {
			best, bdx, bdy, gap = o, dx, dy, dist
		}
	}
	if best == nil {
		return "on its own"
	}
	var side string
	switch {
	case math.Abs(bdx) > math.Abs(bdy) && bdx > 0:
		side = "left of"
	case math.Abs(bdx) > math.Abs(bdy):
		side = "right of"
	case bdy > 0:
		side = "above"
	default:
		side = "below"
	}
	return side + ` "` + best.Label + `"`
}

func (d *diagram) edgeText(e diagramEdge) string {
	s := d.name(e.From) + " → " + d.name(e.To)
	if e.Label != "" {
		s += ` "` + e.Label + `"`
	}
	return s
}

// describe is the whole drawing as text: a count line, then one line per
// node and edge.
func (d *diagram) describe() string {
	lines := []string{fmt.Sprintf("%d nodes, %d edges", len(d.nodes), len(d.edges))}
	for _, n := range d.nodes {
		line := "node " + d.name(n.ID)
		if n.Type != "rectangle" {
			line += " (" + n.Type + ")"
		}
		lines = append(lines, line)
	}
	for _, e := range d.edges {
		lines = append(lines, "edge "+d.edgeText(e))
	}
	return strings.Join(lines, "\n")
}

// diffDiagrams lists the changes from the agent's version a to the owner's
// b, and each changed element's status: added, changed, moved or removed.
func diffDiagrams(a, b *diagram) ([]DrawingChange, map[string]string) {
	changes := []DrawingChange{}
	status := map[string]string{}
	var moved []string
	for _, n := range b.nodes {
		o := a.node(n.ID)
		if o == nil {
			changes = append(changes, DrawingChange{"+", "node " + b.name(n.ID)})
			status[n.ID] = "added"
			continue
		}
		if o.Label != n.Label {
			changes = append(changes, DrawingChange{"~", "node " + a.name(n.ID) + " renamed " + b.name(n.ID)})
			status[n.ID] = "changed"
		}
		if o.Stroke != n.Stroke || o.Fill != n.Fill {
			changes = append(changes, DrawingChange{"~", "node " + b.name(n.ID) + " restyled"})
			status[n.ID] = "changed"
		}
		if math.Abs(o.X-n.X) > diagramMovePx || math.Abs(o.Y-n.Y) > diagramMovePx || math.Abs(o.W-n.W) > diagramMovePx || math.Abs(o.H-n.H) > diagramMovePx {
			moved = append(moved, b.name(n.ID))
			if _, ok := status[n.ID]; !ok {
				status[n.ID] = "moved"
			}
		}
	}
	for _, o := range a.nodes {
		if b.node(o.ID) == nil {
			changes = append(changes, DrawingChange{"-", "node " + a.name(o.ID)})
			status[o.ID] = "removed"
		}
	}
	for _, e := range b.edges {
		o := a.edge(e.ID)
		switch {
		case o == nil:
			changes = append(changes, DrawingChange{"+", "edge " + b.edgeText(e)})
			status[e.ID] = "added"
		case o.From != e.From || o.To != e.To:
			changes = append(changes, DrawingChange{"~", "edge " + a.edgeText(*o) + " now " + b.edgeText(e)})
			status[e.ID] = "changed"
		case o.Label != e.Label:
			changes = append(changes, DrawingChange{"~", "edge " + b.edgeText(e) + ` label was "` + o.Label + `"`})
			status[e.ID] = "changed"
		}
	}
	for _, o := range a.edges {
		if b.edge(o.ID) == nil {
			changes = append(changes, DrawingChange{"-", "edge " + a.edgeText(o)})
			status[o.ID] = "removed"
		}
	}
	if len(moved) > 0 {
		changes = append(changes, DrawingChange{"~", "moved " + strings.Join(moved, ", ")})
	}
	return changes, status
}

// drawingNote is the note uam adds to the owner's next prompt; empty
// without changes.
func drawingNote(path string, from, to int, changes []DrawingChange, current *diagram) string {
	if len(changes) == 0 {
		return ""
	}
	lines := []string{fmt.Sprintf("The user edited %s since your version (v%d → v%d):", path, from, to)}
	for _, c := range changes {
		lines = append(lines, c.Kind+" "+c.Text)
	}
	lines = append(lines, fmt.Sprintf("Now %d nodes, %d edges. Build on the user's version; uam_diagram_read for the full diagram.", len(current.nodes), len(current.edges)))
	return strings.Join(lines, "\n")
}

// excalidrawElement is the part of an Excalidraw scene element the model
// reads.
type excalidrawElement struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	IsDeleted    bool    `json:"isDeleted"`
	ContainerID  string  `json:"containerId"`
	Text         string  `json:"text"`
	OriginalText *string `json:"originalText"`
	X, Y         float64
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	StrokeColor  string  `json:"strokeColor"`
	Background   string  `json:"backgroundColor"`
	StartBinding *struct {
		ElementID string `json:"elementId"`
	} `json:"startBinding"`
	EndBinding *struct {
		ElementID string `json:"elementId"`
	} `json:"endBinding"`
}

func (e excalidrawElement) label() string {
	if e.OriginalText != nil {
		return strings.TrimSpace(*e.OriginalText)
	}
	return strings.TrimSpace(e.Text)
}

// excalidrawDiagram models an Excalidraw file: shapes and free text are
// nodes, labelled by the text bound to them; arrows and lines are edges.
func excalidrawDiagram(content []byte) (*diagram, error) {
	var file struct {
		Elements *[]excalidrawElement `json:"elements"`
	}
	if err := json.Unmarshal(content, &file); err != nil || file.Elements == nil {
		return nil, errors.New("not an Excalidraw file: it needs an elements list")
	}
	var live []excalidrawElement
	text := map[string]string{}
	for _, e := range *file.Elements {
		if e.IsDeleted {
			continue
		}
		live = append(live, e)
		if e.Type == "text" && e.ContainerID != "" {
			text[e.ContainerID] = e.label()
		}
	}
	d := &diagram{}
	for _, e := range live {
		switch e.Type {
		case "text":
			if e.ContainerID != "" {
				continue
			}
			d.nodes = append(d.nodes, diagramNode{ID: e.ID, Type: e.Type, Label: e.label(), X: e.X, Y: e.Y, W: e.Width, H: e.Height, Stroke: e.StrokeColor, Fill: e.Background})
		case "rectangle", "ellipse", "diamond", "frame":
			d.nodes = append(d.nodes, diagramNode{ID: e.ID, Type: e.Type, Label: text[e.ID], X: e.X, Y: e.Y, W: e.Width, H: e.Height, Stroke: e.StrokeColor, Fill: e.Background})
		case "arrow", "line":
			edge := diagramEdge{ID: e.ID, Label: text[e.ID]}
			if e.StartBinding != nil {
				edge.From = e.StartBinding.ElementID
			}
			if e.EndBinding != nil {
				edge.To = e.EndBinding.ElementID
			}
			d.edges = append(d.edges, edge)
		}
	}
	return d, nil
}

// parseDiagram models content in format, excalidraw or drawio.
func parseDiagram(format string, content []byte) (*diagram, error) {
	if format == "drawio" {
		return drawioDiagram(content)
	}
	return excalidrawDiagram(content)
}
