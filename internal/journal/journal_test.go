package journal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesFile(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(filepath.Join(dir, "session.log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = j.Close() }()
	if _, err := os.Stat(j.Path); err != nil {
		t.Fatalf("journal file should exist: %v", err)
	}
}

func TestAppendAndTail(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(filepath.Join(dir, "session.log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = j.Close() }()
	for _, chunk := range [][]byte{[]byte("hello "), []byte("world\n"), []byte("second line\n")} {
		if _, err := j.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	tail, err := j.Tail(64)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	expected := []byte("hello world\nsecond line\n")
	if !bytes.Equal(tail, expected) {
		t.Fatalf("tail mismatch: got %q want %q", tail, expected)
	}
}

func TestTailBoundsBySizeCap(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(filepath.Join(dir, "session.log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = j.Close() }()
	bulk := bytes.Repeat([]byte("a"), 1024)
	if _, err := j.Write(bulk); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tail, err := j.Tail(128)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(tail) != 128 {
		t.Fatalf("expected tail of 128 bytes, got %d", len(tail))
	}
}

func TestSizeCapTruncatesOldestBytes(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenWithCap(filepath.Join(dir, "session.log"), 1024)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = j.Close() }()
	if _, err := j.Write(bytes.Repeat([]byte("x"), 800)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := j.Write(bytes.Repeat([]byte("y"), 800)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tail, err := j.Tail(2048)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if int64(len(tail)) > 1024 {
		t.Fatalf("expected tail <= cap of 1024, got %d", len(tail))
	}
	// The newest bytes ('y') must dominate the retained tail.
	if !bytes.Contains(tail, bytes.Repeat([]byte("y"), 100)) {
		t.Fatalf("expected tail to contain recent 'y' bytes")
	}
}

func TestOpenWithCapRejectsTooSmall(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenWithCap(filepath.Join(dir, "x.log"), 16); err == nil {
		t.Fatalf("expected error for sub-minimum cap")
	}
}

func TestOpenWithCapBadPath(t *testing.T) {
	if _, err := OpenWithCap("/nonexistent-dir-12345/session.log", 4096); err == nil {
		t.Fatalf("expected error opening unwritable path")
	}
}

func TestFlushBeforeTailIsTransparent(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(filepath.Join(dir, "session.log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = j.Close() }()
	if _, err := j.Write([]byte("buffered ")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := j.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	stat, err := os.Stat(j.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if stat.Size() != int64(len("buffered ")) {
		t.Fatalf("Flush should have made all bytes durable, got file size %d", stat.Size())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(filepath.Join(dir, "session.log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := j.Write([]byte("data\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must be a no-op.
	if err := j.Close(); err != nil {
		t.Fatalf("second Close should be nil, got %v", err)
	}
}

func TestTailEmptyJournal(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(filepath.Join(dir, "empty.log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = j.Close() }()
	got, err := j.Tail(64)
	if err != nil {
		t.Fatalf("Tail on empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty tail, got %q", got)
	}
	// max <= 0 -> nil.
	got, err = j.Tail(0)
	if err != nil {
		t.Fatalf("Tail(0): %v", err)
	}
	if got != nil {
		t.Fatalf("Tail(0) should return nil, got %q", got)
	}
}
