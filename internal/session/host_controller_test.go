package session

import (
	"context"
	"strings"
	"testing"
)

func TestSplitSubmitSeparatesTrailingEnter(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		text  string
		enter string
	}{
		{name: "prompt", in: "hello\r", text: "hello", enter: "\r"},
		{name: "multiline", in: "a\nb\r", text: "a\nb", enter: "\r"},
		{name: "bare enter", in: "\r", text: "\r"},
		{name: "no enter", in: "hello", text: "hello"},
		{name: "empty", in: ""},
	}
	for _, tt := range tests {
		text, enter := splitSubmit([]byte(tt.in))
		if string(text) != tt.text || string(enter) != tt.enter {
			t.Errorf("%s: splitSubmit(%q) = %q, %q; want %q, %q", tt.name, tt.in, text, enter, tt.text, tt.enter)
		}
	}
}

// A provider that is still starting owns the terminal in canonical mode, where
// the line discipline turns the submitting CR into LF. SendPrompt must wait
// for the switch to raw mode so the composer receives the CR itself.
func TestSendPromptWaitsForRawInput(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-abc12347"
	command := `stty -echo; printf 'cooked\n'; sleep 0.7; stty raw; head -c 3 | od -c; sleep 0.2`
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "fixture in canonical mode", func() bool {
		out, err := c.Capture(ctx, name, 20)
		return err == nil && strings.Contains(out, "cooked")
	})
	if err := c.SendPrompt(ctx, name, "hi"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	waitFor(t, "raw reader output", func() bool {
		out, err := c.Capture(ctx, name, 20)
		return err == nil && strings.Contains(out, "0000003")
	})
	out, err := c.Capture(ctx, name, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `h   i  \r`) {
		t.Fatalf("raw reader did not receive the CR itself; output=%q", out)
	}
}
