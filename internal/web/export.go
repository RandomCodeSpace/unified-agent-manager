package web

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Export as Markdown writes a Task's whole main conversation as one
// Markdown document: every message, with each tool call folded into a
// <details> block whose summary names the command and its exit code, and a
// subagent's call carrying its generated summary. When the provider can page
// its record, the transcript is read from it whole, older history the
// service no longer holds included; otherwise the held transcript is used
// and the document says what it lacks.

// exportWindowItems is how many items one record read takes.
const exportWindowItems = 1000

// exportTask is what ExportMarkdown copies from the Task under mu.
type exportTask struct {
	id, name, project, dir, provider, model, mode, stage string
	created                                              time.Time
	spawnedBy, rerunOf, routineID                        string
	items                                                []agentapi.Item
	truncated                                            bool
	subagents                                            map[string]agentapi.Subagent // by parent tool call
	pager                                                agentapi.HistoryPager
	read                                                 agentapi.ReadRequest
}

// ExportMarkdown returns Task id's conversation as Markdown and a file name
// for it.
func (m *Manager) ExportMarkdown(ctx context.Context, id string) ([]byte, string, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return nil, "", newError(http.StatusNotFound, msgSessionNotFound)
	}
	t := exportTask{
		id: s.id, name: cleanTitle(s.name), provider: s.provider, model: s.model, mode: string(s.mode), stage: s.stage,
		created: s.createdAt, spawnedBy: s.spawnedBy, rerunOf: s.rerunOf, routineID: s.routineID,
		items: s.agentItems(""), truncated: s.truncated,
		subagents: map[string]agentapi.Subagent{},
		pager:     m.pagerLocked(s), read: agentapi.ReadRequest{ConversationID: s.convID, Workdir: s.workdir},
	}
	if t.name == "" {
		t.name = s.title
	}
	if p := m.projects[s.projectID]; p != nil {
		t.project, t.dir = p.Name, p.Dir
	}
	for _, sa := range s.subagents {
		if sa.ParentToolCallID != "" {
			t.subagents[sa.ParentToolCallID] = *sa
		}
	}
	m.mu.Unlock()

	note := ""
	if t.pager != nil {
		recorded, err := m.readWholeRecord(ctx, t.pager, t.read)
		if err == nil {
			t.items = mergeHeld(recorded, t.items)
			t.truncated = false
		} else {
			log.Warn("export could not read the task's record", "session", id, "error", err)
			note = "The recorded transcript could not be read, so this export holds only the part the service keeps in memory."
		}
	}
	if t.truncated && note == "" {
		note = "The service holds only the most recent part of this conversation, and its provider record cannot be read, so earlier history is missing."
	}
	return renderExport(t, note, time.Now()), exportFileName(t.name), nil
}

// readWholeRecord reads the main agent's whole record, a window at a time
// from its end, each in one of the history read slots.
func (m *Manager) readWholeRecord(ctx context.Context, pager agentapi.HistoryPager, read agentapi.ReadRequest) ([]agentapi.Item, error) {
	var out []agentapi.Item
	boundary := ""
	for {
		release, err := m.readSlot(ctx)
		if err != nil {
			return nil, err
		}
		rctx, cancel := context.WithTimeout(ctx, openTimeout)
		w, err := pager.ReadHistoryWindow(rctx, agentapi.WindowRequest{ReadRequest: read, ItemID: boundary, Before: exportWindowItems})
		cancel()
		release()
		if err != nil {
			return nil, err
		}
		at := min(max(w.At, 0), len(w.Items))
		out = append(slices.Clone(w.Items[:at]), out...)
		if w.Start || at == 0 {
			return out, nil
		}
		boundary = w.Items[0].ID
	}
}

// mergeHeld appends to recorded the held items newer than its last one,
// which the record may not have yet.
func mergeHeld(recorded, held []agentapi.Item) []agentapi.Item {
	if len(recorded) == 0 {
		return held
	}
	last := recorded[len(recorded)-1].ID
	if i := slices.IndexFunc(held, func(it agentapi.Item) bool { return it.ID == last }); i >= 0 {
		return append(recorded, held[i+1:]...)
	}
	return recorded
}

var fileNameRE = regexp.MustCompile(`[^a-z0-9]+`)

// exportFileName is a file name for a Task named name.
func exportFileName(name string) string {
	slug := strings.Trim(fileNameRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 60 {
		slug = strings.TrimRight(slug[:60], "-")
	}
	if slug == "" {
		slug = "task"
	}
	return slug + ".md"
}

// fence returns a code fence longer than any backtick run in text.
func fence(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func writeBlock(b *bytes.Buffer, lang, text string) {
	f := fence(text)
	fmt.Fprintf(b, "%s%s\n%s\n%s\n\n", f, lang, strings.TrimRight(text, "\n"), f)
}

// toolSummary is the one line a tool call folds into, as HTML.
func toolSummary(tc *agentapi.ToolCall, sa *agentapi.Subagent) string {
	name := strings.ToLower(tc.Name)
	status := ""
	switch tc.Status {
	case agentapi.ToolFailed:
		status = " · failed"
	case agentapi.ToolRunning, agentapi.ToolPending:
		status = " · no result"
	}
	code := func(s string) string { return "<code>" + html.EscapeString(oneLine(s, 200)) + "</code>" }
	switch {
	case commandTools[name]:
		line := "Ran " + code(toolCommand(tc))
		if tc.ExitCode != nil {
			return fmt.Sprintf("%s · exit %d%s", line, *tc.ExitCode, status)
		}
		return line + status
	case sa != nil:
		label := cmpOr(sa.Description, sa.Name, "subagent")
		line := "Subagent: " + html.EscapeString(oneLine(label, 200)) + " · " + string(sa.Status)
		if n := len(sa.Runs); n > 1 {
			line += fmt.Sprintf(" · %d runs", n)
		}
		return line + status
	}
	if paths := localToolFilePaths(tc); len(paths) > 0 {
		return html.EscapeString(cmpOr(tc.Title, tc.Name)) + " " + code(strings.Join(paths, ", ")) + status
	}
	return html.EscapeString(oneLine(cmpOr(tc.Title, tc.Name), 200)) + status
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// oneLine is text on one line, cut to n runes.
func oneLine(text string, n int) string {
	return clipRunes(strings.Join(strings.Fields(text), " "), n)
}

func renderExport(t exportTask, note string, now time.Time) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n", oneLine(cmpOr(t.name, "New task"), 200))
	if t.project != "" {
		fmt.Fprintf(&b, "- Project: %s (`%s`)\n", oneLine(t.project, 200), t.dir)
	}
	fmt.Fprintf(&b, "- Provider: %s · model %s · %s mode\n", t.provider, cmpOr(t.model, "default"), cmpOr(t.mode, "safe"))
	if t.stage != "" {
		fmt.Fprintf(&b, "- Stage: %s\n", t.stage)
	}
	fmt.Fprintf(&b, "- Started: %s\n", t.created.Local().Format(time.DateTime))
	fmt.Fprintf(&b, "- Exported: %s\n", now.Local().Format(time.DateTime))
	if t.spawnedBy != "" {
		fmt.Fprintf(&b, "- Started by task %s\n", t.spawnedBy)
	}
	if t.rerunOf != "" {
		fmt.Fprintf(&b, "- Runs again the last message of task %s\n", t.rerunOf)
	}
	if t.routineID != "" {
		fmt.Fprintf(&b, "- Started by routine %s\n", t.routineID)
	}
	b.WriteString("\n")
	if note != "" {
		fmt.Fprintf(&b, "> %s\n\n", note)
	}
	for _, it := range t.items {
		when := ""
		if !it.Time.IsZero() {
			when = " · " + it.Time.Local().Format(time.DateTime)
		}
		switch it.Kind {
		case agentapi.ItemUser:
			label := "You"
			if it.Delivery == agentapi.DeliverySteer {
				label = "You (steer)"
			}
			fmt.Fprintf(&b, "## %s%s\n\n%s\n\n", label, when, strings.TrimSpace(it.Text))
			if len(it.Attachments) > 0 {
				names := make([]string, len(it.Attachments))
				for i, a := range it.Attachments {
					names[i] = "`" + oneLine(a.Name, 200) + "`"
				}
				fmt.Fprintf(&b, "Attachments: %s\n\n", strings.Join(names, ", "))
			}
		case agentapi.ItemAssistant:
			fmt.Fprintf(&b, "## Assistant%s\n\n%s\n\n", when, strings.TrimSpace(it.Text))
		case agentapi.ItemReasoning:
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			b.WriteString("<details><summary>Thought</summary>\n\n")
			writeBlock(&b, "text", it.Text)
			b.WriteString("</details>\n\n")
		case agentapi.ItemNotice:
			fmt.Fprintf(&b, "> %s\n\n", oneLine(it.Text, 2000))
		case agentapi.ItemTool:
			if it.Tool == nil {
				continue
			}
			var sa *agentapi.Subagent
			if v, ok := t.subagents[it.ID]; ok {
				sa = &v
			}
			fmt.Fprintf(&b, "<details><summary>%s</summary>\n\n", toolSummary(it.Tool, sa))
			if !commandTools[strings.ToLower(it.Tool.Name)] && strings.TrimSpace(it.Tool.Input) != "" {
				writeBlock(&b, "json", it.Tool.Input)
			} else if cmd := toolCommand(it.Tool); strings.Contains(cmd, "\n") {
				writeBlock(&b, "sh", cmd)
			}
			if strings.TrimSpace(it.Tool.Output) != "" {
				writeBlock(&b, "text", it.Tool.Output)
			}
			if n := len(it.Images); n > 0 {
				fmt.Fprintf(&b, "%d %s returned.\n\n", n, plural(n, "image", "images"))
			}
			b.WriteString("</details>\n\n")
		}
	}
	return b.Bytes()
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	body, name, err := s.m.ExportMarkdown(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	w.Header().Set(headerContentType, "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		log.Debug("write export failed", "error", err)
	}
}
