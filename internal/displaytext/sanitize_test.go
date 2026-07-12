package displaytext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeRemovesTerminalControls(t *testing.T) {
	in := "safe\tname\npath\r" +
		"\x1b[31mred\x1b[0m" +
		"\x1b]52;c;Y2xpcGJvYXJk\x07" +
		"\x1bPignored\x1b\\" +
		"\u009b2Jhidden\u009c" +
		"\x00\x08\x7f世界"
	want := "safe name path redhidden世界"
	if got := Sanitize(in); got != want {
		t.Fatalf("Sanitize() = %q, want %q", got, want)
	}
}

func TestSanitizeIsUTF8SafeAndIdempotent(t *testing.T) {
	in := string([]byte{'o', 'k', 0xff, 0xfe}) + " 🚀\x1b[999999999999999999999Cdone"
	got := Sanitize(in)
	if !utf8.ValidString(got) {
		t.Fatalf("Sanitize() emitted invalid UTF-8: %q", got)
	}
	if twice := Sanitize(got); twice != got {
		t.Fatalf("Sanitize() is not idempotent: once %q, twice %q", got, twice)
	}
}

func FuzzSanitize(f *testing.F) {
	for _, seed := range []string{
		"plain text",
		"café 世界 🚀",
		"\x1b[31mred\x1b[0m",
		"\x1b]52;c;YQ==\x07visible",
		"\x1bPpayload\x1b\\visible",
		string([]byte{0xff, 'x', 0x9b, '2', 'J'}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Sanitize(in)
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8: %q", got)
		}
		if strings.ContainsAny(got, "\x1b\x07\x00\x7f") {
			t.Fatalf("unsafe control survived: %q", got)
		}
		if twice := Sanitize(got); twice != got {
			t.Fatalf("not idempotent: %q != %q", twice, got)
		}
	})
}
