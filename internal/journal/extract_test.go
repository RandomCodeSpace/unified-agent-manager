package journal

import (
	"reflect"
	"testing"
)

func TestExtractLinesStripsANSI(t *testing.T) {
	in := []byte("hello \x1b[31mworld\x1b[0m\n")
	got := ExtractLines(in)
	want := []string{"hello world", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExtractLinesCollapsesCR(t *testing.T) {
	in := []byte("Generating...\r\x1b[KDone!\n")
	got := ExtractLines(in)
	if len(got) != 2 || got[0] != "Done!" {
		t.Fatalf("expected last-CR collapse, got %q", got)
	}
}

func TestExtractLinesHandlesOSC(t *testing.T) {
	in := []byte("\x1b]0;window title\x07hello\n")
	got := ExtractLines(in)
	if len(got) != 2 || got[0] != "hello" {
		t.Fatalf("expected OSC stripped, got %q", got)
	}
}

func TestExtractLinesMultipleSpinnerFrames(t *testing.T) {
	in := []byte("\r✻ frame1\r✻ frame2\r✻ frame3\n")
	got := ExtractLines(in)
	if len(got) != 2 || got[0] != "✻ frame3" {
		t.Fatalf("expected only last frame, got %q", got)
	}
}

func TestTailLinesUsesLastN(t *testing.T) {
	in := []byte("a\nb\nc\nd\ne\n")
	got := TailLines(ExtractLines(in), 3)
	// ExtractLines("a\nb\nc\nd\ne\n") -> ["a","b","c","d","e",""]; TailLines last 3 -> ["d","e",""]
	if !reflect.DeepEqual(got, []string{"d", "e", ""}) {
		t.Fatalf("got %q", got)
	}
}
