package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const compactRepresentation = "compact-v1"

// Compact projections are web-only. Retained/provider records stay authoritative.
type compactBody struct {
	HasReasoning bool `json:"has_reasoning"`
	HasText      bool `json:"has_text"`
}
type compactItem struct {
	agentapi.Item
	Compact *compactBody `json:"compact,omitempty"`
	Tool    *compactTool `json:"tool,omitempty"`
}
type compactTool struct {
	agentapi.ToolCall
	DisplayArg string   `json:"display_arg,omitempty"`
	Path       string   `json:"path,omitempty"`
	FilePaths  []string `json:"file_paths,omitempty"`
	HasInput   bool     `json:"has_input"`
	HasOutput  bool     `json:"has_output"`
}
type compactSubagent struct {
	agentapi.Subagent
	Preview       string `json:"preview"`
	ResultSummary string `json:"result_summary"`
	Summary       string `json:"summary,omitempty"`
}
type compactSessionDetail struct {
	SessionDetail
	Representation string            `json:"representation"`
	Epoch          string            `json:"epoch"`
	DetailStream   bool              `json:"detail_stream"`
	Items          []compactItem     `json:"items"`
	Subagents      []compactSubagent `json:"subagents"`
}
type compactHistoryPage struct {
	Seq            uint64        `json:"seq"`
	Epoch          string        `json:"epoch"`
	Representation string        `json:"representation"`
	Items          []compactItem `json:"items"`
	Before         string        `json:"before"`
	After          string        `json:"after"`
}
type compactSubagentDetail struct {
	Seq            uint64          `json:"seq"`
	Epoch          string          `json:"epoch"`
	Representation string          `json:"representation"`
	Subagent       compactSubagent `json:"subagent"`
	Items          []compactItem   `json:"items"`
	Before         string          `json:"before"`
}

func boundedPreview(s string, limit int) string {
	var out strings.Builder
	out.Grow(min(len(s), limit))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = out.Len() > 0
			continue
		}
		size := utf8.RuneLen(r)
		if space {
			size++
		}
		if out.Len()+size > limit {
			break
		}
		if space {
			out.WriteByte(' ')
			space = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

// Keep line boundaries for the browser's existing Markdown first-line formatter.
func boundedResultSummary(s string) string {
	const limit = 512
	if len(s) <= limit {
		return s
	}
	end := limit
	for !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

var toolArgumentKeys = map[string][]string{
	"bash": {"command", "cmd"}, "shell": {"command", "cmd"}, "powershell": {"command", "cmd"}, "sql": {"query", "sql"},
	"web_fetch": {"url"}, "webfetch": {"url"}, "fetch": {"url"},
	"view": {"path", "file_path", "filePath"}, "read": {"path", "file_path", "filePath"}, "create": {"path", "file_path", "filePath"}, "edit": {"path", "file_path", "filePath"}, "write": {"path", "file_path", "filePath"}, "list": {"path"},
	"grep": {"pattern", "query"}, "glob": {"pattern"}, "task": {"description", "name", "agent_type"}, "skill": {"skill", "name"}, "store_memory": {"fact"}, "ask_user": {"question"},
}
var genericArgumentKeys = []string{"command", "cmd", "url", "path", "file_path", "filePath", "pattern", "query", "skill", "name", "description", "question", "prompt", "fact", "intent"}

func compactArgument(tool *agentapi.ToolCall) (arg, path string) {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(tool.Input), &obj) == nil && obj != nil {
		value := func(key string) string { var s string; _ = json.Unmarshal(obj[key], &s); return s }
		for _, key := range []string{"path", "file_path", "filePath"} {
			if s := value(key); strings.TrimSpace(s) != "" {
				path = s
				break
			}
		}
		for _, keys := range [][]string{toolArgumentKeys[strings.ToLower(tool.Name)], genericArgumentKeys} {
			for _, key := range keys {
				if s := value(key); strings.TrimSpace(s) != "" {
					return boundedPreview(s, 512), path
				}
			}
		}
		// Preserve JSON field order for the same fallback the browser used.
		dec := json.NewDecoder(strings.NewReader(tool.Input))
		_, _ = dec.Token()
		for dec.More() {
			_, _ = dec.Token()
			var v any
			if dec.Decode(&v) != nil {
				break
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return boundedPreview(s, 512), path
			}
		}
	}
	return boundedPreview(tool.Input, 512), path
}

func projectItem(it agentapi.Item) compactItem {
	out := compactItem{Item: cloneBody(it)}
	if it.Kind == agentapi.ItemTool {
		out.Compact = &compactBody{HasText: it.Text != ""}
		out.Text = ""
	}
	if it.Kind == agentapi.ItemReasoning {
		out.Compact = &compactBody{HasReasoning: it.Text != ""}
		out.Text = ""
	}
	if it.Tool != nil {
		tool := *it.Tool
		arg, path := compactArgument(&tool)
		out.Compact = &compactBody{HasText: it.Text != ""}
		out.Text = ""
		paths := localToolFilePaths(&tool)
		if tool.Name == "uam_show_file" {
			paths = nil
			if tool.Declaration != nil {
				paths = []string{tool.Declaration.Path}
			}
		}
		projected := compactTool{ToolCall: tool, HasInput: tool.Input != "", HasOutput: tool.Output != "", DisplayArg: arg, Path: path, FilePaths: paths}
		// Question text and its recorded answer are semantic UI, including after reload.
		if tool.Name != "ask_user" {
			tool.Input, tool.Output = "", ""
		}
		projected.ToolCall = tool
		out.Tool = &projected
	}
	return out
}
func compactPage(items []agentapi.Item, end int) compactHistoryPage {
	start, size := end, 0
	projected := make([]compactItem, 0, historyPageItems)
	for start > 0 && end-start < historyPageItems {
		it := projectItem(items[start-1])
		raw, _ := json.Marshal(it)
		if start < end && size+len(raw)+1 > historyPageBytes {
			break
		}
		size += len(raw) + 1
		start--
		projected = append(projected, it)
	}
	slices.Reverse(projected)
	page := compactHistoryPage{Representation: compactRepresentation, Items: projected}
	if start > 0 {
		page.Before = base64.RawURLEncoding.EncodeToString([]byte(items[start].ID))
	}

	if start < end && end < len(items) {
		page.After = base64.RawURLEncoding.EncodeToString([]byte(items[end-1].ID))
	}
	return page
}

func compactForwardPage(items []agentapi.Item, start int) compactHistoryPage {
	end, size := start, 0
	projected := make([]compactItem, 0, historyPageItems)
	for end < len(items) && end-start < historyPageItems {
		it := projectItem(items[end])
		raw, _ := json.Marshal(it)
		if end > start && size+len(raw)+1 > historyPageBytes {
			break
		}
		size += len(raw) + 1
		end++
		projected = append(projected, it)
	}
	page := compactHistoryPage{Representation: compactRepresentation, Items: projected}
	if start < end && start > 0 {
		page.Before = base64.RawURLEncoding.EncodeToString([]byte(items[start].ID))
	}
	if start < end && end < len(items) {
		page.After = base64.RawURLEncoding.EncodeToString([]byte(items[end-1].ID))
	}
	return page
}

func (s *webSession) compactSubagent(sa agentapi.Subagent) compactSubagent {
	preview := sa.Error
	result := sa.Error
	if i, ok := s.itemIdx[itemKey("", sa.ParentToolCallID)]; ok && s.items[i].Tool != nil && result == "" {
		result = s.items[i].Tool.Output
	}
	if preview == "" && sa.Status != agentapi.SubagentRunning {
		if i, ok := s.itemIdx[itemKey("", sa.ParentToolCallID)]; ok && s.items[i].Tool != nil {
			preview = s.items[i].Tool.Output
		}
	}
	if preview == "" {
		for i := len(s.items) - 1; i >= 0; i-- {
			it := s.items[i]
			if it.AgentID != sa.ID {
				continue
			}
			switch it.Kind {
			case agentapi.ItemAssistant, agentapi.ItemNotice:
				preview = it.Text
			case agentapi.ItemReasoning:
				preview = "Thinking…"
			case agentapi.ItemTool:
				if it.Tool != nil {
					arg, _ := compactArgument(it.Tool)
					preview = "Running: " + it.Tool.Name
					if arg != "" {
						preview += " " + arg
					}
				}
			}
			if preview != "" {
				break
			}
		}
	}
	return compactSubagent{Subagent: sa, Preview: boundedPreview(preview, 512), ResultSummary: boundedResultSummary(result), Summary: s.generatedSubagentSummary(sa)}
}
func (s *webSession) compactSubagents() []compactSubagent {
	out := make([]compactSubagent, 0, len(s.subagents))
	for _, sa := range s.subagents {
		out = append(out, s.compactSubagent(*sa))
	}
	return out
}
func (m *Manager) compactDetailLocked(s *webSession, d SessionDetail) compactSessionDetail {
	page := compactPage(d.Items, len(d.Items))
	d.HistoryBefore = &page.Before
	return compactSessionDetail{SessionDetail: d, Representation: compactRepresentation, Epoch: m.epoch, DetailStream: true, Items: page.Items, Subagents: s.compactSubagents()}
}
func (m *Manager) CompactDetail(id string) (compactSessionDetail, error) {
	if _, err := m.Detail(id); err != nil {
		return compactSessionDetail{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return compactSessionDetail{}, newError(http.StatusNotFound, "session not found")
	}
	// Recapture transcript and barrier together after Detail's provider-free view work.
	d := m.detailLocked(s)
	return m.compactDetailLocked(s, d), nil
}
func (m *Manager) CompactSubagent(id, agent string) (compactSubagentDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return compactSubagentDetail{}, newError(404, "session not found")
	}
	sa := s.subIdx[agent]
	if sa == nil {
		return compactSubagentDetail{}, newError(404, "subagent not found")
	}
	s.historyUsed = m.now()
	items := s.agentItems(agent)
	page := compactPage(items, len(items))
	return compactSubagentDetail{Seq: m.seq, Epoch: m.epoch, Representation: compactRepresentation, Subagent: s.compactSubagent(*sa), Items: page.Items, Before: page.Before}, nil
}
func (m *Manager) CompactOlderHistory(id, agent, before string) (compactHistoryPage, error) {
	return m.CompactHistoryPage(id, agent, before, "")
}

func (m *Manager) CompactHistoryPage(id, agent, before, after string) (compactHistoryPage, error) {
	if (before == "") == (after == "") {
		return compactHistoryPage{}, newError(400, "provide exactly one history cursor")
	}
	cursor := before
	if after != "" {
		cursor = after
	}
	boundary, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(boundary) == 0 || len(boundary) > 4096 {
		return compactHistoryPage{}, newError(400, "invalid history cursor")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return compactHistoryPage{}, newError(404, "session not found")
	}
	if agent != "" && s.subIdx[agent] == nil {
		return compactHistoryPage{}, newError(404, "subagent not found")
	}
	s.historyUsed = m.now()
	items := s.agentItems(agent)
	index := slices.IndexFunc(items, func(it agentapi.Item) bool { return it.ID == string(boundary) })
	if index < 0 {
		return compactHistoryPage{}, newError(409, "history changed; reload the task")
	}
	var page compactHistoryPage
	if after != "" {
		page = compactForwardPage(items, index+1)
		if len(page.Items) == 0 {
			page.Before = cursor
		}
	} else {
		page = compactPage(items, index)
		if len(page.Items) == 0 {
			page.After = cursor
		}
	}
	page.Seq = m.seq
	page.Epoch = m.epoch
	return page, nil
}
