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

func TestExtractLinesTreatsCRLFAsLineEnding(t *testing.T) {
	// PTY drivers translate '\n' on input to '\r\n' on output; ExtractLines
	// must not interpret that trailing '\r' as cursor-rewind.
	in := []byte("hello-host\r\nworld\r\n")
	got := ExtractLines(in)
	want := []string{"hello-host", "world", ""}
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

func TestTailLinesShorterThanN(t *testing.T) {
	in := []string{"x", "y"}
	got := TailLines(in, 5)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("expected slice returned as-is, got %q", got)
	}
}

func TestTailLinesNonPositiveN(t *testing.T) {
	if got := TailLines([]string{"a", "b"}, 0); got != nil {
		t.Fatalf("n=0 should return nil, got %q", got)
	}
	if got := TailLines([]string{"a", "b"}, -1); got != nil {
		t.Fatalf("n<0 should return nil, got %q", got)
	}
}

func TestTailCombinesExtractAndTrim(t *testing.T) {
	raw := []byte("first\nsecond\n\x1b[31mthird\x1b[0m\nfourth\n")
	got := Tail(raw, 2)
	// ExtractLines yields ["first","second","third","fourth",""]; last 2 are ["fourth",""].
	if !reflect.DeepEqual(got, []string{"fourth", ""}) {
		t.Fatalf("got %q", got)
	}
}

func TestTailReturnsAllWhenShortInput(t *testing.T) {
	raw := []byte("only\n")
	got := Tail(raw, 10)
	// ExtractLines("only\n") -> ["only",""]; n=10 > len, returns whole slice.
	if !reflect.DeepEqual(got, []string{"only", ""}) {
		t.Fatalf("got %q", got)
	}
}
