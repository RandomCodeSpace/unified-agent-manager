package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	maxResolvePaths     = 64
	maxResolvePathBytes = 4096
	maxResolveBodyBytes = 512 << 10
)

type resolvedFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind"`
}

func validResolvePath(path string) bool {
	return path != "" && len(path) <= maxResolvePathBytes && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
}

func (s *Server) handleResolveFiles(w http.ResponseWriter, r *http.Request) {
	// Read the complete bounded envelope so an oversized trailing suffix also
	// returns 413, and reject invalid UTF-8 before JSON replaces it with U+FFFD.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxResolveBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
		}
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &req) != nil || len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "paths must be a nonempty array of file paths")
		return
	}
	paths := make([]string, 0, min(len(req.Paths), maxResolvePaths))
	seen := make(map[string]bool)
	for _, path := range req.Paths {
		if !validResolvePath(path) {
			writeError(w, http.StatusBadRequest, "invalid file path")
			return
		}
		if !seen[path] {
			if len(paths) == maxResolvePaths {
				writeError(w, http.StatusBadRequest, "at most 64 unique paths are allowed")
				return
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	files, err := s.m.resolveFiles(r.Context(), r.PathValue("id"), paths)
	if err != nil {
		if r.Context().Err() == nil {
			writeFailure(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Files []resolvedFile `json:"files"`
	}{files})
}

func (m *Manager) resolveFiles(ctx context.Context, id string, paths []string) ([]resolvedFile, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return nil, newError(http.StatusNotFound, "session not found")
	}
	workdir := s.workdir
	m.mu.Unlock()

	files := make([]resolvedFile, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file := resolvedFile{Path: path, Kind: "unavailable"}
		if readableTaskFile(workdir, path) {
			file.Exists, file.Kind = true, "file"
		}
		files = append(files, file)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

// The opened descriptor proves readability and type at this instant. It does
// not authorize a later view, which repeats its own checks. No content is read.
func readableTaskFile(workdir, path string) bool {
	root, rel, err := resolveTaskFile(workdir, path)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular()
}
