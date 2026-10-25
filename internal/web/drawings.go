package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Drawings are the editable diagrams of a Thread (docs/web.md "Drawings"):
// a mermaid block the owner converted into a .excalidraw or .drawio file
// under the Thread's docs/ folder, edited in the browser. The file is the
// owner's; uam keeps, in its own store, the agent's version of each drawing
// (the baseline) and the version counters, so the agent's next prompt can
// carry a note of what the owner changed, and uam_diagram_read can show
// the drawing as text.

const (
	diagramReadToolName = "uam_diagram_read"
	// maxDrawingBytes bounds one drawing file.
	maxDrawingBytes = 2 << 20
	// drawingsDir holds each Thread's drawing index beside sessions.json;
	// drawingDocs is the folder of the Thread's directory the files live in.
	drawingsDir = "drawings"
	drawingDocs = "docs"
)

var diagramReadTool = agentapi.HostTool{
	Name: diagramReadToolName,
	Description: "Read a drawing: a diagram the owner converted from one of your mermaid blocks into a .excalidraw or .drawio file in docs/ and edits in uam. " +
		"Without path, lists the drawings with their versions and unseen changes. With path, returns the drawing as text (node and edge lines) and, when the owner changed it since your version, the changes. " +
		"The file's layout is the owner's: do not edit it with file tools. To change the picture, write a new mermaid block and say the owner can convert it.",
	Parameters: toolSchema(nil, map[string]any{
		"path": stringProp("The drawing's path in the thread's folder, such as docs/flow.excalidraw. Omit to list the drawings."),
	}),
}

// DrawingSummary is one drawing as listed and in the session snapshot.
// ItemID and FenceIndex say which mermaid block of the conversation it was
// converted from; ChangesCount is how many changes the agent has not seen.
type DrawingSummary struct {
	Path         string `json:"path"`
	Format       string `json:"format"`
	Version      int    `json:"version"`
	AgentVersion int    `json:"agent_version"`
	ItemID       string `json:"item_id"`
	FenceIndex   int    `json:"fence_index"`
	ChangesCount int    `json:"changes_count"`
}

// DrawingView is GET /api/sessions/{id}/drawings/{path}: the file, the
// agent's version of it (nil when they are the same version), and the
// changes between them.
type DrawingView struct {
	Path         string            `json:"path"`
	Format       string            `json:"format"`
	Version      int               `json:"version"`
	AgentVersion int               `json:"agent_version"`
	Content      string            `json:"content"`
	Baseline     *string           `json:"baseline"`
	Changes      []DrawingChange   `json:"changes"`
	Status       map[string]string `json:"status"`
	Note         string            `json:"note"`
}

// DrawingSave is the PUT body. Origin "convert" creates the drawing from a
// mermaid block (ItemID, FenceIndex) and makes Content the agent's version;
// otherwise BaseVersion, when given, must be the version the editor loaded.
type DrawingSave struct {
	Content     string `json:"content"`
	Format      string `json:"format"`
	BaseVersion *int   `json:"base_version"`
	Origin      string `json:"origin"`
	ItemID      string `json:"item_id"`
	FenceIndex  int    `json:"fence_index"`
}

// DrawingSaveResult is the PUT response.
type DrawingSaveResult struct {
	Version      int               `json:"version"`
	AgentVersion int               `json:"agent_version"`
	Changes      []DrawingChange   `json:"changes"`
	Status       map[string]string `json:"status"`
	Note         string            `json:"note"`
}

// drawingEntry is what uam keeps of one drawing: Baseline is the file as
// the agent last saw it.
type drawingEntry struct {
	Format       string `json:"format"`
	Version      int    `json:"version"`
	AgentVersion int    `json:"agent_version"`
	ItemID       string `json:"item_id,omitempty"`
	FenceIndex   int    `json:"fence_index"`
	ChangesCount int    `json:"changes_count"`
	Baseline     string `json:"baseline"`
}

// drawingIndex is one Thread's drawings by path, as stored.
type drawingIndex struct {
	Drawings map[string]*drawingEntry `json:"drawings"`
}

// drawingStore is the loaded indexes by Thread. mu orders every read and
// write of them and of the drawing files; it is never held while taking
// Manager.mu.
type drawingStore struct {
	mu sync.Mutex
	by map[string]*drawingIndex
}

func (m *Manager) drawingsRoot() string {
	return filepath.Join(filepath.Dir(m.store.Path()), drawingsDir)
}

func (m *Manager) drawingIndexPath(taskID string) string {
	return filepath.Join(m.drawingsRoot(), filepath.Clean("/"+taskID)+".json")
}

// drawingsLocked is the Thread's index, read once. The caller holds
// drawings.mu.
func (m *Manager) drawingsLocked(taskID string) *drawingIndex {
	if idx := m.drawings.by[taskID]; idx != nil {
		return idx
	}
	idx := &drawingIndex{}
	data, err := os.ReadFile(m.drawingIndexPath(taskID))
	if err == nil {
		err = json.Unmarshal(data, idx)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("read the drawings index failed", "session", taskID, "error", err)
	}
	if idx.Drawings == nil {
		idx.Drawings = map[string]*drawingEntry{}
	}
	if m.drawings.by == nil {
		m.drawings.by = map[string]*drawingIndex{}
	}
	m.drawings.by[taskID] = idx
	return idx
}

func (m *Manager) saveDrawingsLocked(taskID string, idx *drawingIndex) error {
	return writePrivateJSON(m.drawingsRoot(), m.drawingIndexPath(taskID), idx)
}

// drawingPath checks a drawing path: a file under docs/ named
// *.excalidraw or *.drawio, and returns it with its format.
func drawingPath(p string) (string, string, error) {
	bad := newError(http.StatusBadRequest, "the path must name a .excalidraw or .drawio file under docs/, such as docs/flow.excalidraw")
	clean := path.Clean("/" + p)[1:]
	if clean != p || !strings.HasPrefix(clean, drawingDocs+"/") || strings.Contains(clean, "\x00") {
		return "", "", bad
	}
	for part := range strings.SplitSeq(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return "", "", bad
		}
	}
	format := strings.TrimPrefix(path.Ext(clean), ".")
	if format != "excalidraw" && format != "drawio" || path.Base(clean) == "."+format {
		return "", "", bad
	}
	return clean, format, nil
}

// readDrawingFile reads rel under root: a regular file, not a symbolic
// link, of at most maxDrawingBytes. A missing file is fs.ErrNotExist.
func readDrawingFile(root *os.Root, rel string) ([]byte, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	switch {
	case !info.Mode().IsRegular():
		return nil, newError(http.StatusBadRequest, "%s is not a regular file", rel)
	case info.Size() > maxDrawingBytes:
		return nil, newError(http.StatusRequestEntityTooLarge, "%s is larger than %d MiB", rel, maxDrawingBytes>>20)
	}
	return root.ReadFile(rel)
}

// writeDrawingFile replaces rel under root with data, owner-only, through
// a temporary file renamed over it.
func writeDrawingFile(root *os.Root, rel string, data []byte) error {
	dir := path.Dir(rel)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path.Join(dir, "."+path.Base(rel)+".tmp-"+rand.Text()[:12])
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// sessionDir is the directory of the Task id.
func (m *Manager) sessionDir(id string) (string, error) {
	s, err := m.lookup(id)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.removed {
		return "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	return s.workdir, nil
}

func (e *drawingEntry) summary(p string) DrawingSummary {
	return DrawingSummary{Path: p, Format: e.Format, Version: e.Version, AgentVersion: e.AgentVersion, ItemID: e.ItemID, FenceIndex: e.FenceIndex, ChangesCount: e.ChangesCount}
}

// drawingSummaries lists the Task's drawings by path.
func (m *Manager) drawingSummaries(taskID string) []DrawingSummary {
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	idx := m.drawingsLocked(taskID)
	out := make([]DrawingSummary, 0, len(idx.Drawings))
	for _, p := range slices.Sorted(maps.Keys(idx.Drawings)) {
		out = append(out, idx.Drawings[p].summary(p))
	}
	return out
}

// Drawings lists the drawings of the Task id.
func (m *Manager) Drawings(id string) ([]DrawingSummary, error) {
	if _, err := m.sessionDir(id); err != nil {
		return nil, err
	}
	return m.drawingSummaries(id), nil
}

// drawingDiff is the changes from the agent's version of e to current.
func drawingDiff(e *drawingEntry, current *diagram) ([]DrawingChange, map[string]string) {
	base, err := parseDiagram(e.Format, []byte(e.Baseline))
	if err != nil {
		base = &diagram{}
	}
	return diffDiagrams(base, current)
}

// Drawing returns the drawing p of the Task id with its changes since the
// agent's version.
func (m *Manager) Drawing(id, p string) (DrawingView, error) {
	dir, err := m.sessionDir(id)
	if err != nil {
		return DrawingView{}, err
	}
	rel, _, err := drawingPath(p)
	if err != nil {
		return DrawingView{}, err
	}
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	e := m.drawingsLocked(id).Drawings[rel]
	if e == nil {
		return DrawingView{}, newError(http.StatusNotFound, "drawing not found")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return DrawingView{}, err
	}
	defer func() { _ = root.Close() }()
	content, err := readDrawingFile(root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return DrawingView{}, newError(http.StatusNotFound, "the drawing file is missing")
	}
	if err != nil {
		return DrawingView{}, err
	}
	current, err := parseDiagram(e.Format, content)
	if err != nil {
		return DrawingView{}, newError(http.StatusUnprocessableEntity, "%s", err)
	}
	changes, status := drawingDiff(e, current)
	view := DrawingView{Path: rel, Format: e.Format, Version: e.Version, AgentVersion: e.AgentVersion, Content: string(content), Changes: changes, Status: status,
		Note: drawingNote(rel, e.AgentVersion, e.Version, changes, current)}
	if e.Version != e.AgentVersion {
		baseline := e.Baseline
		view.Baseline = &baseline
	}
	return view, nil
}

// SaveDrawing writes the drawing p of the Task id (PUT): a convert creates
// it as the agent's version 1; any other save bumps its version and
// reports the changes since the agent's version.
func (m *Manager) SaveDrawing(id, p string, req DrawingSave) (DrawingSaveResult, error) {
	dir, err := m.sessionDir(id)
	if err != nil {
		return DrawingSaveResult{}, err
	}
	rel, format, err := drawingPath(p)
	if err != nil {
		return DrawingSaveResult{}, err
	}
	switch {
	case req.Format != format:
		return DrawingSaveResult{}, newError(http.StatusBadRequest, "format must be %s for %s", format, rel)
	case len(req.Content) > maxDrawingBytes:
		return DrawingSaveResult{}, newError(http.StatusRequestEntityTooLarge, "a drawing can be at most %d MiB", maxDrawingBytes>>20)
	case req.Origin != "" && req.Origin != "convert":
		return DrawingSaveResult{}, newError(http.StatusBadRequest, "origin must be convert or absent")
	}
	current, err := parseDiagram(format, []byte(req.Content))
	if err != nil {
		return DrawingSaveResult{}, newError(http.StatusBadRequest, "%s", err)
	}
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	idx := m.drawingsLocked(id)
	e := idx.Drawings[rel]
	root, err := os.OpenRoot(dir)
	if err != nil {
		return DrawingSaveResult{}, err
	}
	defer func() { _ = root.Close() }()
	out := DrawingSaveResult{Changes: []DrawingChange{}, Status: map[string]string{}}
	if req.Origin == "convert" {
		if _, statErr := root.Lstat(rel); e != nil || statErr == nil {
			return DrawingSaveResult{}, newError(http.StatusConflict, "a drawing already exists at %s", rel)
		}
		e = &drawingEntry{Format: format, Version: 1, AgentVersion: 1, ItemID: req.ItemID, FenceIndex: req.FenceIndex, Baseline: req.Content}
	} else {
		if e == nil {
			return DrawingSaveResult{}, newError(http.StatusNotFound, "no drawing at %s; convert a diagram first", rel)
		}
		if req.BaseVersion != nil && *req.BaseVersion != e.Version {
			return DrawingSaveResult{}, newError(http.StatusConflict, "the drawing is at version %d, not %d; reload it", e.Version, *req.BaseVersion)
		}
	}
	if err := writeDrawingFile(root, rel, []byte(req.Content)); err != nil {
		log.Warn("write a drawing failed", "session", id, "error", err)
		return DrawingSaveResult{}, newError(http.StatusInternalServerError, "uam could not write %s", rel)
	}
	if req.Origin == "convert" {
		m.count("drawing.convert")
	} else {
		e.Version++
		out.Changes, out.Status = drawingDiff(e, current)
		out.Note = drawingNote(rel, e.AgentVersion, e.Version, out.Changes, current)
		e.ChangesCount = len(out.Changes)
		m.count("drawing.save")
	}
	idx.Drawings[rel] = e
	if err := m.saveDrawingsLocked(id, idx); err != nil {
		log.Warn("write the drawings index failed", "session", id, "error", err)
		return DrawingSaveResult{}, newError(http.StatusInternalServerError, "uam could not record the drawing")
	}
	out.Version, out.AgentVersion = e.Version, e.AgentVersion
	m.mu.Lock()
	if s := m.sessions[id]; s != nil && !s.removed {
		summary := m.summaryLocked(s)
		m.broadcastLocked("session", "", func(seq uint64) any { return sessionEvent{Seq: seq, Session: summary} })
	}
	m.mu.Unlock()
	return out, nil
}

// catchUpLocked makes the current file the agent's version of e. The
// caller holds drawings.mu.
func catchUpLocked(e *drawingEntry, content []byte) {
	e.AgentVersion, e.Baseline, e.ChangesCount = e.Version, string(content), 0
}

// takeDrawingNotes is the notes of the Task's drawings the owner changed
// since the agent's version, which then becomes the current one. It is
// taken once per prompt.
func (m *Manager) takeDrawingNotes(taskID, dir string) string {
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	idx := m.drawingsLocked(taskID)
	var notes []string
	var root *os.Root
	changed := false
	for _, p := range slices.Sorted(maps.Keys(idx.Drawings)) {
		e := idx.Drawings[p]
		if e.Version <= e.AgentVersion {
			continue
		}
		if root == nil {
			var err error
			if root, err = os.OpenRoot(dir); err != nil {
				log.Warn("open the thread's directory for drawing notes failed", "session", taskID, "error", err)
				return ""
			}
			defer func() { _ = root.Close() }()
		}
		content, err := readDrawingFile(root, p)
		if err != nil {
			continue
		}
		current, err := parseDiagram(e.Format, content)
		if err != nil {
			continue
		}
		changes, _ := drawingDiff(e, current)
		if note := drawingNote(p, e.AgentVersion, e.Version, changes, current); note != "" {
			notes = append(notes, note)
			m.count("drawing.note")
		}
		catchUpLocked(e, content)
		changed = true
	}
	if changed {
		if err := m.saveDrawingsLocked(taskID, idx); err != nil {
			log.Warn("write the drawings index failed", "session", taskID, "error", err)
		}
	}
	return strings.Join(notes, "\n\n")
}

// takeHostNotes hands uam's notes for the turn a prompt starts to the
// agent, once: the end notes of the Tasks s created, then its drawings'
// change notes.
func (m *Manager) takeHostNotes(s *webSession) string {
	m.mu.Lock()
	child := m.takeChildNotesLocked(s)
	id, dir := s.id, s.workdir
	m.mu.Unlock()
	return strings.TrimSpace(child + "\n\n" + m.takeDrawingNotes(id, dir))
}

// diagramReadCall runs a uam_diagram_read call of the Task taskID.
func (m *Manager) diagramReadCall(_ context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.readDiagram(taskID, call)
	if err != nil {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	m.count("drawing.read")
	return agentapi.HostToolResult{Text: text}
}

func (m *Manager) readDiagram(taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this thread")
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := decodeToolArgs(call.Arguments, &in); err != nil {
		return "", err
	}
	dir, err := m.sessionDir(taskID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Path) == "" {
		list := m.drawingSummaries(taskID)
		if len(list) == 0 {
			return "This thread has no drawings. The owner can convert a mermaid block you write into one.", nil
		}
		lines := make([]string, 0, len(list)+1)
		lines = append(lines, fmt.Sprintf("%d drawings:", len(list)))
		for _, d := range list {
			lines = append(lines, fmt.Sprintf("%s (%s) v%d, your version v%d, %d unseen changes", d.Path, d.Format, d.Version, d.AgentVersion, d.ChangesCount))
		}
		return strings.Join(lines, "\n"), nil
	}
	rel, _, err := drawingPath(strings.TrimSpace(in.Path))
	if err != nil {
		return "", err
	}
	m.drawings.mu.Lock()
	defer m.drawings.mu.Unlock()
	idx := m.drawingsLocked(taskID)
	e := idx.Drawings[rel]
	if e == nil {
		return "", fmt.Errorf("there is no drawing at %s; call without path to list them", rel)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	content, err := readDrawingFile(root, rel)
	if err != nil {
		return "", fmt.Errorf("the drawing %s cannot be read: %w", rel, err)
	}
	current, err := parseDiagram(e.Format, content)
	if err != nil {
		return "", err
	}
	text := current.describe()
	if e.Version > e.AgentVersion {
		changes, _ := drawingDiff(e, current)
		if len(changes) > 0 {
			lines := []string{text, "", fmt.Sprintf("Changes since your version (v%d → v%d):", e.AgentVersion, e.Version)}
			for _, c := range changes {
				lines = append(lines, c.Kind+" "+c.Text)
			}
			text = strings.Join(lines, "\n")
		}
		catchUpLocked(e, content)
		if err := m.saveDrawingsLocked(taskID, idx); err != nil {
			log.Warn("write the drawings index failed", "session", taskID, "error", err)
		}
	}
	return text, nil
}

func (s *Server) drawingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/drawings", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.m.Drawings(r.PathValue("id"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]DrawingSummary{"drawings": list})
	})
	mux.HandleFunc("GET /api/sessions/{id}/drawings/{path...}", func(w http.ResponseWriter, r *http.Request) {
		view, err := s.m.Drawing(r.PathValue("id"), r.PathValue("path"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("PUT /api/sessions/{id}/drawings/{path...}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxDrawingBytes+64<<10)
		var body DrawingSave
		if !decodeBody(w, r, &body) {
			return
		}
		res, err := s.m.SaveDrawing(r.PathValue("id"), r.PathValue("path"), body)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /api/settings/drawio/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Origin string `json:"origin"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		writeJSON(w, http.StatusOK, s.checkDrawIO(r.Context(), body.Origin))
	})
}

// checkDrawIO probes origin against this service's public origins.
func (s *Server) checkDrawIO(ctx context.Context, origin string) DrawIOCheck {
	return checkDrawIO(ctx, s.drawioClient, origin, hasScheme(s.publicOrigins, "https"), s.publicOrigins)
}

// maxDrawingBody bounds a drawing PUT: the file, JSON-escaped, in its body.
const maxDrawingBody = 3 << 20

// drawingSavePath says whether p is a drawing's PUT route.
func drawingSavePath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/api/sessions/")
	if !ok {
		return false
	}
	_, rest, ok = strings.Cut(rest, "/")
	return ok && strings.HasPrefix(rest, "drawings/")
}
