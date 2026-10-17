// Package toolargs picks the short argument a tool call is shown by, from its
// input as JSON, the same for the transcript and the live phase.
package toolargs

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxPaths and MaxPathBytes bound the paths read from a patch or asked
	// about at once, and each path; MaxHintBytes bounds a patch's paths
	// together.
	MaxPaths     = 64
	MaxPathBytes = 4096
	MaxHintBytes = 16 << 10
)

// previewBytes bounds the argument Compact returns.
const previewBytes = 512

// ValidPath reports whether path is a usable file path: present, bounded,
// valid UTF-8 and without NUL.
func ValidPath(path string) bool {
	return path != "" && len(path) <= MaxPathBytes && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
}

// BoundedPreview is s on one line, each run of white space one space, cut
// to at most limit bytes on a rune boundary.
func BoundedPreview(s string, limit int) string {
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

var toolArgumentKeys = map[string][]string{
	"bash": {"command", "cmd"}, "shell": {"command", "cmd"}, "powershell": {"command", "cmd"}, "sql": {"query", "sql"},
	"web_fetch": {"url"}, "webfetch": {"url"}, "fetch": {"url"},
	"view": {"path", "file_path", "filePath"}, "read": {"path", "file_path", "filePath"}, "create": {"path", "file_path", "filePath"}, "edit": {"path", "file_path", "filePath"}, "write": {"path", "file_path", "filePath"}, "list": {"path"},
	"grep": {"pattern", "query"}, "glob": {"pattern"}, "task": {"description", "name", "agent_type"}, "skill": {"skill", "name"}, "store_memory": {"fact"}, "ask_user": {"question"},
}
var genericArgumentKeys = []string{"command", "cmd", "url", "path", "file_path", "filePath", "pattern", "query", "skill", "name", "description", "question", "prompt", "fact", "intent"}

// Compact is the argument a call of tool name with input (its arguments as
// JSON) is shown by, and the file path it names, if any.
func Compact(name, input string) (arg, path string) {
	// An input that is one JSON string (apply_patch's patch): the files a
	// patch names, else the text decoded, never its quoted escapes.
	var text string
	if strings.HasPrefix(input, `"`) && json.Unmarshal([]byte(input), &text) == nil {
		if paths := PatchFilePaths(text); len(paths) > 0 {
			return BoundedPreview(strings.Join(paths, ", "), previewBytes), ""
		}
		return BoundedPreview(text, previewBytes), ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &obj) == nil && obj != nil {
		value := func(key string) string { var s string; _ = json.Unmarshal(obj[key], &s); return s }
		for _, key := range []string{"path", "file_path", "filePath"} {
			if s := value(key); strings.TrimSpace(s) != "" {
				path = s
				break
			}
		}
		for _, keys := range [][]string{toolArgumentKeys[strings.ToLower(name)], genericArgumentKeys} {
			for _, key := range keys {
				if s := value(key); strings.TrimSpace(s) != "" {
					return BoundedPreview(s, previewBytes), path
				}
			}
		}
		// Preserve JSON field order for the same fallback the browser used.
		dec := json.NewDecoder(strings.NewReader(input))
		_, _ = dec.Token()
		for dec.More() {
			_, _ = dec.Token()
			var v any
			if dec.Decode(&v) != nil {
				break
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return BoundedPreview(s, previewBytes), path
			}
		}
	}
	return BoundedPreview(input, previewBytes), path
}

// PatchFilePaths parses only the supported patch framing and file headers.
// Content lines must belong to Add or Update sections; unknown or truncated
// syntax discards all paths.
func PatchFilePaths(patch string) []string {
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
		if !ValidPath(path) {
			return false
		}
		if seen[path] {
			return true
		}
		bytes += len(path)
		if len(paths) == MaxPaths || bytes > MaxHintBytes {
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
