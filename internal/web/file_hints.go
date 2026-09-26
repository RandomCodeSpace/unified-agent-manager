package web

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const (
	maxFileHintBytes = 16 << 10
	// Copilot clips serialized input at 64 KiB. Never infer a complete hint set
	// from input at that boundary, even if the clipped prefix looks valid.
	maxFileHintInputBytes = 64 << 10
)

// Hints identify eligible local references, never existence or authority. Keep
// this exact allowlist separate from generic display paths and historical names.
func localToolFilePaths(tool *agentapi.ToolCall) []string {
	switch tool.Name {
	case "view", "create", "edit", "apply_patch":
	default:
		return nil
	}
	if len(tool.Input) >= maxFileHintInputBytes || !utf8.ValidString(tool.Input) {
		return nil
	}
	if tool.Name == "apply_patch" {
		patch := tool.Input
		if strings.HasPrefix(strings.TrimSpace(patch), `"`) {
			if json.Unmarshal([]byte(patch), &patch) != nil {
				return nil
			}
		}
		return patchFilePaths(patch)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(tool.Input), &obj) != nil {
		return nil
	}
	var path string
	for _, key := range []string{"path", "file_path", "filePath"} {
		if raw, ok := obj[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) != nil || !validResolvePath(value) || (path != "" && path != value) {
				return nil
			}
			path = value
		}
	}
	if path == "" {
		return nil
	}
	return []string{path}
}

// Parse only the supported patch framing and file headers. Content lines must
// belong to Add or Update sections; unknown/truncated syntax discards all hints.
func patchFilePaths(patch string) []string {
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	if len(lines) < 3 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return nil
	}
	var paths []string
	seen := make(map[string]bool)
	bytes := 0
	add := func(path string) bool {
		if !validResolvePath(path) {
			return false
		}
		if seen[path] {
			return true
		}
		bytes += len(path)
		if len(paths) == maxResolvePaths || bytes > maxFileHintBytes {
			return false
		}
		seen[path] = true
		paths = append(paths, path)
		return true
	}
	section, hunk, content, moved, ended := "", false, false, false, false
	complete := func() bool { return section != "update" || (hunk && content) }
	for _, line := range lines[1 : len(lines)-1] {
		next, path := "", ""
		for _, header := range []struct{ prefix, section string }{
			{"*** Add File: ", "add"}, {"*** Update File: ", "update"}, {"*** Delete File: ", "delete"},
		} {
			if strings.HasPrefix(line, header.prefix) {
				next, path = header.section, strings.TrimPrefix(line, header.prefix)
				break
			}
		}
		if next != "" {
			if !complete() || !add(path) {
				return nil
			}
			section, hunk, content, moved, ended = next, false, false, false, false
			continue
		}
		if strings.HasPrefix(line, "*** Move to: ") {
			if section != "update" || hunk || moved || !add(strings.TrimPrefix(line, "*** Move to: ")) {
				return nil
			}
			moved = true
			continue
		}
		if section == "update" && !ended {
			if line == "@@" || strings.HasPrefix(line, "@@ ") {
				if hunk && !content {
					return nil
				}
				hunk, content = true, false
				continue
			}
			if line == "*** End of File" && hunk && content {
				ended = true
				continue
			}
			if hunk && len(line) > 0 && strings.ContainsRune(" +-", rune(line[0])) {
				content = true
				continue
			}
		}
		if section == "add" && strings.HasPrefix(line, "+") {
			continue
		}
		return nil
	}
	if !complete() {
		return nil
	}
	return paths
}
