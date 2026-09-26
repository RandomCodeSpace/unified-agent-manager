package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf8"
)

// declarationValidator checks display intent only. File-opening authority
// stays with the existing workdir view and exact-file grant routes.
func (m *Manager) declarationValidator(id, workdir string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, candidate string) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if candidate == "" || len(candidate) > maxGrantPathBytes || !utf8.ValidString(candidate) {
			return "", errGrantUnavailable
		}
		rawCandidate := candidate
		absolute := filepath.IsAbs(candidate)
		if !absolute {
			candidate = filepath.Join(workdir, candidate)
		}
		candidate = filepath.Clean(candidate)
		m.mu.Lock()
		s := m.sessions[id]
		current := !m.closed && s != nil && !s.removed && s.workdir == workdir
		var grants *tempGrants
		for g := range m.fileGrants {
			grants = g
			break
		}
		m.mu.Unlock()
		if !current {
			return "", errGrantUnavailable
		}
		if rel, err := filepath.Rel(workdir, candidate); err == nil && rel != "." && filepath.IsLocal(rel) {
			root, relative, err := resolveTaskFile(workdir, candidate)
			if err != nil {
				return "", err
			}
			defer func() { _ = root.Close() }()
			// A name swapped to a FIFO after resolveTaskFile's stat must not
			// leave the provider's tool goroutine blocked in open(2).
			f, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return "", errGrantUnavailable
			}
			defer func() { _ = f.Close() }()
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				return "", errGrantUnavailable
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return filepath.Join(workdir, relative), nil
		}
		if !absolute || grants == nil {
			return "", errGrantUnavailable
		}
		operationCtx, release, err := grants.operation(ctx)
		if err != nil {
			return "", err
		}
		defer release()
		roots, err := grants.rootsForUse()
		if err != nil {
			return "", err
		}
		rel, err := roots.relative(rawCandidate)
		if err != nil {
			return "", err
		}
		f, err := roots.checkedOpen(operationCtx, rel, nil, nil)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		if _, err := grantFileInfo(f); err != nil {
			return "", err
		}
		if operationCtx.Err() != nil {
			return "", errors.Join(errGrantUnavailable, operationCtx.Err())
		}
		return filepath.Join(roots.canonical, rel), nil
	}
}
