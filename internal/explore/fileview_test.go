package explore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFilePreview_PlainText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(p, []byte("line one\nline two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := readFilePreview(p, false) // colorEnabled=false => plain text
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") {
		t.Fatalf("content missing: %q", out)
	}
}

func TestReadFilePreview_OversizeGuard(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(p, make([]byte, maxPreviewBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readFilePreview(p, false)
	if !errors.Is(err, errPreviewTooLarge) {
		t.Fatalf("want errPreviewTooLarge, got %v", err)
	}
}

func TestReadFilePreview_BinaryGuard(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(p, []byte("ok\x00\x01\x02binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readFilePreview(p, false)
	if !errors.Is(err, errPreviewBinary) {
		t.Fatalf("want errPreviewBinary, got %v", err)
	}
}

func TestHighlight_DisabledReturnsPlain(t *testing.T) {
	src := "package main\n"
	if got := highlight(src, "main.go", false); got != src {
		t.Fatalf("expected plain passthrough, got %q", got)
	}
}

func TestHighlight_EnabledAddsANSI(t *testing.T) {
	src := "package main\nfunc main() {}\n"
	got := highlight(src, "main.go", true)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI escapes when colorEnabled, got %q", got)
	}
}

func TestReadFilePreview_AtSizeLimitAccepted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "exact.txt")
	// Exactly maxPreviewBytes of NON-NUL content: must pass the size guard (cap is
	// strictly `>`) AND the binary guard. Do NOT use make([]byte, n) here — that's
	// all-NUL and would trip the binary guard.
	if err := os.WriteFile(p, []byte(strings.Repeat("a", maxPreviewBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFilePreview(p, false); err != nil {
		t.Fatalf("file at exactly the limit should be accepted, got %v", err)
	}
}

func TestReadFilePreview_UnreadableFileErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "noperm.txt")
	if err := os.WriteFile(p, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) }) // let TempDir clean up
	if _, err := readFilePreview(p, false); err == nil {
		t.Fatal("expected an error reading a file with no permissions")
	}
}

func TestWithLineNumbers(t *testing.T) {
	input := "hello world\nsecond line\n"
	out := withLineNumbers(input)
	if !strings.Contains(out, "1 │ ") {
		t.Fatalf("expected line number '1 │ ' in output, got: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("expected original text 'hello world' in output, got: %q", out)
	}
	if !strings.Contains(out, "2 │ ") {
		t.Fatalf("expected line number '2 │ ' in output, got: %q", out)
	}
	if !strings.Contains(out, "second line") {
		t.Fatalf("expected original text 'second line' in output, got: %q", out)
	}
}
