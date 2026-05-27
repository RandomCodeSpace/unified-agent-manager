package cli

import (
	"bytes"
	"testing"
)

// TestScanForDetachStates exercises the Ctrl-b prefix state machine
// across the cases that matter at runtime: plain bytes pass through,
// Ctrl-b alone is held back across chunk boundaries, Ctrl-b d detaches,
// Ctrl-b Ctrl-b forwards a single literal Ctrl-b, and Ctrl-b X
// forwards Ctrl-b plus X.
func TestScanForDetachStates(t *testing.T) {
	tests := []struct {
		name       string
		inPrefix   bool
		in         []byte
		wantOut    []byte
		wantDetach bool
		wantNext   bool
	}{
		{
			name:    "plain bytes pass through",
			in:      []byte("hello\n"),
			wantOut: []byte("hello\n"),
		},
		{
			name:     "trailing Ctrl-b holds prefix for next chunk",
			in:       []byte("hi\x02"),
			wantOut:  []byte("hi"),
			wantNext: true,
		},
		{
			name:       "Ctrl-b then d detaches",
			in:         []byte{prefixKey, detachByte},
			wantOut:    []byte{},
			wantDetach: true,
		},
		{
			name:    "Ctrl-b Ctrl-b forwards one literal Ctrl-b",
			in:      []byte{prefixKey, prefixKey},
			wantOut: []byte{prefixKey},
		},
		{
			name:    "Ctrl-b x forwards both",
			in:      []byte{prefixKey, 'x'},
			wantOut: []byte{prefixKey, 'x'},
		},
		{
			name:       "carryover prefix from prior chunk + d detaches",
			inPrefix:   true,
			in:         []byte{detachByte, 'z'},
			wantOut:    []byte{},
			wantDetach: true,
		},
		{
			name:     "carryover prefix + non-detach byte forwards Ctrl-b + byte",
			inPrefix: true,
			in:       []byte{'a', 'b'},
			wantOut:  []byte{prefixKey, 'a', 'b'},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, detach, next := scanForDetach(tc.in, tc.inPrefix)
			if !bytes.Equal(out, tc.wantOut) {
				t.Fatalf("out: got %q want %q", out, tc.wantOut)
			}
			if detach != tc.wantDetach {
				t.Fatalf("detach: got %v want %v", detach, tc.wantDetach)
			}
			if next != tc.wantNext {
				t.Fatalf("next: got %v want %v", next, tc.wantNext)
			}
		})
	}
}

// TestScanForDetachDoesNotForwardAfterDetach asserts that once detach
// fires, the buffer trailing the detach byte is dropped (the conn is
// closing and any remaining input is meaningless).
func TestScanForDetachDoesNotForwardAfterDetach(t *testing.T) {
	out, detach, next := scanForDetach([]byte{prefixKey, detachByte, 'x', 'y', 'z'}, false)
	if !detach {
		t.Fatalf("expected detach")
	}
	if next {
		t.Fatalf("expected next=false after detach")
	}
	if len(out) != 0 {
		t.Fatalf("expected no forwarded bytes, got %q", out)
	}
}
