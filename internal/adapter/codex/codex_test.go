package codex

import "testing"

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "codex" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}
