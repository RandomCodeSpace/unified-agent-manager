// Package journal implements a per-session append-only byte log on disk
// with a configurable max-size cap. The journal is the source of truth
// for "what the agent has emitted lately."
package journal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	defaultCap = 4 * 1024 * 1024 // 4 MiB per session
	minCap     = 1024
)

// Journal is an append-only file with size-capped truncation.
type Journal struct {
	Path string

	mu       sync.Mutex
	f        *os.File
	w        *bufio.Writer
	capBytes int64
	bytes    int64
}

// Open is OpenWithCap(path, 4*MiB).
func Open(path string) (*Journal, error) {
	return OpenWithCap(path, defaultCap)
}

// OpenWithCap creates or truncates the journal file. If a file already
// exists, its content is preserved (append mode) and `bytes` reflects
// current size.
func OpenWithCap(path string, capBytes int64) (*Journal, error) {
	if capBytes < minCap {
		return nil, fmt.Errorf("journal cap must be >= %d bytes, got %d", minCap, capBytes)
	}
	// O_RDWR (no O_APPEND): we need WriteAt/Seek for in-place compaction.
	// We manually seek to end on open and rely on the single-writer
	// invariant (one journal owns its file) to preserve append semantics.
	//
	// #nosec G304 — path is supplied by the supervisor (caller-controlled),
	// which constructs it under its own state directory. Scoping is the
	// caller's responsibility; this primitive intentionally does not assume
	// a chroot.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal open %q: %w", path, err)
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("journal seek-end: %w", err)
	}
	return &Journal{
		Path:     path,
		f:        f,
		w:        bufio.NewWriterSize(f, 16*1024),
		capBytes: capBytes,
		bytes:    stat.Size(),
	}, nil
}

// Write appends p to the journal. If the journal exceeds its cap, the
// oldest portion of the file is rotated away (via in-place truncation +
// rewrite of the tail). Goroutine-safe.
func (j *Journal) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	n, err := j.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("journal write: %w", err)
	}
	j.bytes += int64(n)
	if j.bytes > j.capBytes {
		if err := j.compactLocked(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Flush forces buffered bytes to disk. Tail calls Flush internally.
func (j *Journal) Flush() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.w.Flush()
}

// Tail returns the last `max` bytes from the journal.
func (j *Journal) Tail(max int64) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.w.Flush(); err != nil {
		return nil, err
	}
	stat, err := j.f.Stat()
	if err != nil {
		return nil, err
	}
	size := stat.Size()
	if max > size {
		max = size
	}
	if max <= 0 {
		return nil, nil
	}
	buf := make([]byte, max)
	if _, err := j.f.ReadAt(buf, size-max); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

// Close closes the underlying file. Idempotent.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	if err := j.w.Flush(); err != nil {
		_ = j.f.Close()
		j.f = nil
		return err
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// compactLocked rewrites the file so it contains only the most recent
// `cap` bytes. Caller must hold j.mu.
func (j *Journal) compactLocked() error {
	if err := j.w.Flush(); err != nil {
		return err
	}
	stat, err := j.f.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()
	if size <= j.capBytes {
		j.bytes = size
		return nil
	}
	// Keep last `capBytes` bytes. Read tail, rewrite from offset 0, truncate.
	buf := make([]byte, j.capBytes)
	if _, err := j.f.ReadAt(buf, size-j.capBytes); err != nil {
		return err
	}
	if _, err := j.f.WriteAt(buf, 0); err != nil {
		return err
	}
	if err := j.f.Truncate(j.capBytes); err != nil {
		return err
	}
	if _, err := j.f.Seek(j.capBytes, io.SeekStart); err != nil {
		return err
	}
	j.bytes = j.capBytes
	return nil
}
