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
}
