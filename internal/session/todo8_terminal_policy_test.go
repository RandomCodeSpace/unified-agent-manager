package session

import (
	"bytes"
	"testing"
)

func TestProfileMousePolicyMatrix(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    bool
		wantErr bool
	}{
		{name: "auto preserves provider modes", profile: "auto", want: true},
		{name: "on preserves provider modes", profile: "on", want: true},
		{name: "off filters provider modes", profile: "off", want: false},
		{name: "invalid policy is rejected", profile: "sometimes", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := resolveAttachPolicy(attachPolicySnapshot{mouse: test.profile, controlPrefix: "C-b", backDetach: true}, func(string) string { return "" })
			if test.wantErr {
				if err == nil {
					t.Fatal("resolveAttachPolicy accepted an invalid mouse policy")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAttachPolicy: %v", err)
			}
			if policy.mouseEnabled != test.want {
				t.Fatalf("mouse enabled = %v, want %v", policy.mouseEnabled, test.want)
			}
		})
	}
}

func TestTemporaryMouseBypassIsClientLocal(t *testing.T) {
	var first, second bytes.Buffer
	firstRuntime := newAttachRuntime(attachRuntimeConfig{output: &first, mouseEnabled: true})
	secondRuntime := newAttachRuntime(attachRuntimeConfig{output: &second, mouseEnabled: true})
	firstRuntime.toggleMouse()

	providerReplay := []byte("shared\x1b[?1000;1006hstate")
	firstFilter := newAttachOutputFilterWithMouse(&first, firstRuntime.mouseEnabled)
	secondFilter := newAttachOutputFilterWithMouse(&second, secondRuntime.mouseEnabled)
	if _, err := firstFilter.Write(providerReplay); err != nil {
		t.Fatal(err)
	}
	if _, err := secondFilter.Write(providerReplay); err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(first.Bytes(), []byte("\x1b[?1000;1006h")) {
		t.Fatalf("temporarily bypassed client retained provider mouse modes: %q", first.Bytes())
	}
	if !bytes.Contains(second.Bytes(), []byte("\x1b[?1000;1006h")) {
		t.Fatalf("second client lost shared provider modes: %q", second.Bytes())
	}
	if !secondRuntime.mouseEnabled() {
		t.Fatal("first client's temporary toggle changed second client")
	}
}

func TestProfileControlPrefix(t *testing.T) {
	policy, err := resolveAttachPolicy(attachPolicySnapshot{mouse: "auto", controlPrefix: "C-a", backDetach: true}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	filter := stdinFilter{prefix: policy.controlPrefix, backDetach: policy.backDetach, role: roleController}
	got, detached := filter.filter([]byte{0x01, 0x01, 0x02, 'd'})
	if detached {
		t.Fatal("legacy Ctrl+B detached a C-a policy attachment")
	}
	want := []byte{0x01, 0x02, 'd'}
	if !bytes.Equal(got, want) {
		t.Fatalf("provider bytes = %q, want %q", got, want)
	}
	if _, detached := filter.filter([]byte{0x01, 'd'}); !detached {
		t.Fatal("configured C-a prefix did not detach")
	}
	for _, invalid := range []string{"C-A", "C-aa", "M-a", "C-{"} {
		if _, err := resolveAttachPolicy(attachPolicySnapshot{mouse: "auto", controlPrefix: invalid}, func(string) string { return "" }); err == nil {
			t.Fatalf("resolveAttachPolicy accepted invalid prefix %q", invalid)
		}
	}
}

func TestProfileBackDetach(t *testing.T) {
	left := []byte("\x1b[D")
	for _, test := range []struct {
		name       string
		backDetach bool
		wantDetach bool
	}{
		{name: "enabled", backDetach: true, wantDetach: true},
		{name: "disabled", backDetach: false, wantDetach: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			filter := stdinFilter{prefix: detachPrefix, backDetach: test.backDetach, role: roleController}
			got, detached := filter.filter(left)
			if detached != test.wantDetach {
				t.Fatalf("detached = %v, want %v", detached, test.wantDetach)
			}
			if !test.wantDetach && !bytes.Equal(got, left) {
				t.Fatalf("left arrow = %q, want byte-exact %q", got, left)
			}
		})
	}
}

func TestProfileScrollbackBound(t *testing.T) {
	for _, test := range []struct {
		lines   int
		want    int
		wantErr bool
	}{
		{lines: 0, want: historyLines},
		{lines: 100, want: 100},
		{lines: 100000, want: 100000},
		{lines: 99, wantErr: true},
		{lines: 100001, wantErr: true},
	} {
		got, err := validatedScrollbackLines(test.lines)
		if test.wantErr {
			if err == nil {
				t.Fatalf("validatedScrollbackLines(%d) accepted invalid input", test.lines)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("validatedScrollbackLines(%d) = %d, %v; want %d", test.lines, got, err, test.want)
		}
	}
}

func TestEnvironmentOverridePrecedence(t *testing.T) {
	env := map[string]string{
		AttachMouseEnv:      "off",
		AttachPrefixEnv:     "C-z",
		AttachBackDetachEnv: "0",
	}
	policy, err := resolveAttachPolicy(attachPolicySnapshot{mouse: "on", controlPrefix: "C-a", backDetach: true}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if policy.mouseEnabled || policy.controlPrefix != 0x1a || policy.backDetach {
		t.Fatalf("environment did not win: %+v", policy)
	}
}

func TestPasteFocusAndKeyboardRemainByteExact(t *testing.T) {
	filter := stdinFilter{prefix: 0x01, backDetach: true, role: roleController}
	want := []byte("\x1b[200~pasted\x01d\x02d\x1b[201~\x1b[I\x1b[O\x1b[1;5A\x1b[>1u")
	got, detached := filter.filter(want)
	if detached {
		t.Fatal("UAM-like pasted bytes detached the client")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("provider input changed:\n got %x\nwant %x", got, want)
	}
}
