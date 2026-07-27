package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
)

func TestParseCPRColumn(t *testing.T) {
	cases := []struct {
		name     string
		response string
		wantCol  int
		wantOK   bool
	}{
		{"narrow glyph", "\x1b[1;2R", 2, true},
		{"wide glyph", "\x1b[1;3R", 3, true},
		{"large coordinates", "\x1b[120;240R", 240, true},
		{"keypress queued before the answer", "q\x1b[1;2R", 2, true},
		{"partial CSI then the answer", "\x1b[\x1b[5;2R", 2, true},
		{"empty", "", 0, false},
		{"no CSI", "1;2R", 0, false},
		{"missing column", "\x1b[5R", 0, false},
		{"garbage", "\x1b[a;bR", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			col, ok := parseCPRColumn([]byte(tc.response))
			if ok != tc.wantOK || col != tc.wantCol {
				t.Fatalf("parseCPRColumn(%q) = (%d, %v), want (%d, %v)",
					tc.response, col, ok, tc.wantCol, tc.wantOK)
			}
		})
	}
}

// answerCPR plays the terminal's half of the probe: read the master side until
// the CPR request arrives, then answer with the given cursor column. Returns
// everything the probe wrote so the test can assert it cleaned up after itself.
func answerCPR(t *testing.T, ptmx io.ReadWriter, col string, done chan<- string) {
	t.Helper()
	go func() {
		var seen bytes.Buffer
		buf := make([]byte, 64)
		for !strings.Contains(seen.String(), "\x1b[6n") {
			n, err := ptmx.Read(buf)
			if err != nil {
				done <- seen.String()
				return
			}
			seen.Write(buf[:n])
		}
		if col != "" {
			if _, err := ptmx.Write([]byte("\x1b[1;" + col + "R")); err != nil {
				t.Errorf("answering CPR: %v", err)
			}
		}
		// Drain the erase sequence the probe writes on its way out.
		deadline := time.Now().Add(time.Second)
		for !strings.Contains(seen.String(), "\x1b[K") && time.Now().Before(deadline) {
			n, err := ptmx.Read(buf)
			if err != nil {
				break
			}
			seen.Write(buf[:n])
		}
		done <- seen.String()
	}()
}

func TestMeasureAmbiguousWidthOverARealPTY(t *testing.T) {
	cases := []struct {
		name      string
		column    string // cursor column the fake terminal reports; "" = never answers
		wantWidth int
		wantOK    bool
	}{
		{"narrow font", "2", 1, true},
		{"wide font", "3", 2, true},
		{"terminal never answers", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ptmx, tty, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ptmx.Close() }()
			defer func() { _ = tty.Close() }()
			done := make(chan string, 1)
			answerCPR(t, ptmx, tc.column, done)
			width, ok := measureAmbiguousWidth(tty, tty)
			if ok != tc.wantOK || width != tc.wantWidth {
				t.Fatalf("measureAmbiguousWidth = (%d, %v), want (%d, %v)", width, ok, tc.wantWidth, tc.wantOK)
			}
			_ = tty.Close()
			wrote := <-done
			if !strings.Contains(wrote, app.ProbeGlyph) {
				t.Fatalf("probe never printed its glyph; wrote %q", wrote)
			}
			if !strings.Contains(wrote, "\r\x1b[K") {
				t.Fatalf("probe did not erase its own output; wrote %q", wrote)
			}
		})
	}
}

func TestMeasureAmbiguousWidthSkipsNonTerminals(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	if width, ok := measureAmbiguousWidth(r, w); ok || width != 0 {
		t.Fatalf("a pipe is not a terminal; got (%d, %v)", width, ok)
	}
}
