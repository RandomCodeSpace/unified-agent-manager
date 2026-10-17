package web

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/toolargs"
)

const (
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

// patchFilePaths is the files a patch names (toolargs.PatchFilePaths).
func patchFilePaths(patch string) []string { return toolargs.PatchFilePaths(patch) }
