package version

import "testing"

func TestStringUsesOverride(t *testing.T) {
	old := Override
	Override = "v1.2.3"
	t.Cleanup(func() { Override = old })

	if got := String(); got != "v1.2.3" {
		t.Fatalf("String() = %q", got)
	}
}
