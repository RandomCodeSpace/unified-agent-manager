package copilot

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// toolOutputCap is the tool output size, in bytes, above which the CLI saves
// the output to a file and gives the model a short preview and the file's
// path instead. The CLI's own default (about 20 KB in CLI 1.0.98) lets a
// single call put several thousand tokens into the context.
const toolOutputCap int64 = 8 << 10

// spillDir is the folder, inside the session's folder, that keeps saved tool
// output after the session closes; the CLI deletes its own copy then.
const spillDir = "tool-output"

func largeOutput() *copilot.LargeToolOutputConfig {
	limit := toolOutputCap
	return &copilot.LargeToolOutputConfig{MaxSizeBytes: &limit}
}

// keepSpill copies the file a tool result's output was saved to into the
// session's folder and returns the result with the copy's path in place of
// the CLI's; nil when the result names no saved output or the copy fails.
// Only files the CLI writes to the temp directory are copied.
func (c *conversation) keepSpill(result any) any {
	m, _ := result.(map[string]any)
	contents, _ := m["contents"].([]any)
	var from string
	for _, item := range contents {
		entry, _ := item.(map[string]any)
		if p, _ := entry["outputFilePath"].(string); p != "" {
			from = p
			break
		}
	}
	if from == "" || filepath.Dir(from) != filepath.Clean(os.TempDir()) || !strings.Contains(filepath.Base(from), "-copilot-tool-output-") {
		return nil
	}
	c.mu.Lock()
	sess := c.sess
	c.mu.Unlock()
	if sess == nil || sess.WorkspacePath() == "" {
		return nil
	}
	dir := filepath.Join(sess.WorkspacePath(), spillDir)
	to := filepath.Join(dir, filepath.Base(from))
	if err := copySpill(from, dir, to); err != nil {
		log.Warn("keep saved tool output failed", "session", c.id, "error", err)
		return nil
	}
	return replaceStrings(m, from, to)
}

func copySpill(from, dir, to string) error {
	src, err := os.OpenFile(from, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 -- a CLI output file directly in the temp directory (keepSpill).
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	if info, err := src.Stat(); err != nil || !info.Mode().IsRegular() {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the session folder joined with the CLI file's base name.
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(to)
		return err
	}
	return dst.Close()
}

// replaceStrings replaces from with to in every string in v, in place.
func replaceStrings(v any, from, to string) any {
	switch t := v.(type) {
	case string:
		return strings.ReplaceAll(t, from, to)
	case []any:
		for i := range t {
			t[i] = replaceStrings(t[i], from, to)
		}
	case map[string]any:
		for k := range t {
			t[k] = replaceStrings(t[k], from, to)
		}
	}
	return v
}
