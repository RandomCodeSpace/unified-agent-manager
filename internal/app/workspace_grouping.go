package app

import (
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

const unknownWorkspaceKey = "(unknown workspace)"

// workspaceKey returns the stable presentation key for a working directory.
// It deliberately does not resolve symlinks: rendering must not perform I/O or
// unexpectedly merge two paths that the user chose to keep distinct.
func workspaceKey(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return unknownWorkspaceKey
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(abs)
}

// projectSessions keeps the service's canonical order byte-for-byte when
// grouping is disabled. When enabled, it groups only inside each canonical
// pin partition, ordering workspaces by their first occurrence and
// retaining canonical order inside each workspace.
func projectSessions(canonical []adapter.Session, grouped bool) []adapter.Session {
	out := append([]adapter.Session(nil), canonical...)
	if !grouped || len(out) < 2 {
		return out
	}
	projected := make([]adapter.Session, 0, len(out))
	for start := 0; start < len(out); {
		end := start + 1
		for end < len(out) && samePartition(out[start], out[end]) {
			end++
		}
		order := make([]string, 0, end-start)
		groups := make(map[string][]adapter.Session, end-start)
		for _, sess := range out[start:end] {
			key := workspaceKey(sess.Cwd)
			if _, seen := groups[key]; !seen {
				order = append(order, key)
			}
			groups[key] = append(groups[key], sess)
		}
		for _, key := range order {
			projected = append(projected, groups[key]...)
		}
		start = end
	}
	return projected
}
