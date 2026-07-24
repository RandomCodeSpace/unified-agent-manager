package session

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestAttachV2RoleAndControlEvents(t *testing.T) {
	t.Run("role_change", func(t *testing.T) {
		// Given
		var wire bytes.Buffer
		writer := newAttachFrameWriter(&wire, protocolV2, "client-2", 4)
		writer.SetAssignedRole(roleStandby)

		// When
		err := writer.ObserveControl([]byte(`{"type":"role","client_id":"client-2","role":"controller","generation":5,"reason":"promoted"}`))

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if writer.AssignedRole() != roleController || writer.Generation() != 5 {
			t.Fatalf("client state = role %q generation %d", writer.AssignedRole(), writer.Generation())
		}
	})
	t.Run("role_rejection", func(t *testing.T) {
		// Given
		writer := newAttachFrameWriter(&bytes.Buffer{}, protocolV2, "client-2", 4)
		writer.SetAssignedRole(roleStandby)

		// When
		err := writer.ObserveControl([]byte(`{"type":"role","client_id":"client-2","role":"owner"}`))

		// Then
		if err == nil {
			t.Fatal("malformed role event was accepted")
		}
	})
	t.Run("promotion_flush", func(t *testing.T) {
		// Given
		writer := newAttachFrameWriter(&bytes.Buffer{}, protocolV2, "client-2", 4)
		writer.SetAssignedRole(roleStandby)
		flushed := false

		// When
		_, _, err := writer.observeRoleEvent(
			[]byte(`{"type":"role","client_id":"client-2","role":"controller","generation":5,"reason":"promoted"}`),
			func() error {
				if writer.role != roleStandby {
					t.Fatalf("role during input flush = %q, want %q", writer.role, roleStandby)
				}
				flushed = true
				return nil
			},
		)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if !flushed {
			t.Fatal("promotion did not flush queued input")
		}
		if role := writer.AssignedRole(); role != roleController {
			t.Fatalf("role after input flush = %q, want %q", role, roleController)
		}
	})
}

func TestObserverInputIsDiscarded(t *testing.T) {
	t.Run("observer_discard", func(t *testing.T) {
		// Given
		filter := &stdinFilter{backDetach: true, role: roleObserver}
		input := append([]byte("secret-input-7f3a"), []byte("\x1b[6n\x1b[<0;10;20M\x1b[200~prompt-like\x1b[201~")...)

		// When
		got, detached := filter.filter(input)

		// Then
		if detached || len(got) != 0 {
			t.Fatalf("observer forwarded %x, detached=%t", got, detached)
		}
		got, detached = filter.filter([]byte{detachPrefix, detachPrefix, detachPrefix, 'c'})
		if detached || len(got) != 0 {
			t.Fatalf("observer forwarded UAM literal/interrupt bytes %x, detached=%t", got, detached)
		}
	})
}

func TestControlPrefixModes(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		wantOutput  []byte
		wantCommand attachCommand
		wantDetach  bool
	}{
		{name: "prefix prefix is literal", input: []byte{detachPrefix, detachPrefix}, wantOutput: []byte{detachPrefix}},
		{name: "detach", input: []byte{detachPrefix, 'd'}, wantDetach: true},
		{name: "interrupt", input: []byte{detachPrefix, 'c'}, wantOutput: []byte{ctrlC}},
		{name: "request control", input: []byte{detachPrefix, 'r'}, wantCommand: commandRequestControl},
		{name: "transfer control", input: []byte{detachPrefix, 'o'}, wantCommand: commandTransferControl},
		{name: "info", input: []byte{detachPrefix, 'i'}, wantCommand: commandShowInfo},
		{name: "mouse toggle", input: []byte{detachPrefix, 'm'}, wantCommand: commandToggleMouse},
		{name: "unknown stays provider native", input: []byte{detachPrefix, 'x'}, wantOutput: []byte{detachPrefix, 'x'}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			filter := &stdinFilter{role: roleController}

			// When
			got, detached := filter.filter(test.input)
			commands := filter.drainCommands()

			// Then
			if !bytes.Equal(got, test.wantOutput) || detached != test.wantDetach {
				t.Fatalf("filter = %x, %t; want %x, %t", got, detached, test.wantOutput, test.wantDetach)
			}
			if test.wantCommand == commandNone {
				if len(commands) != 0 {
					t.Fatalf("commands = %v, want none", commands)
				}
				return
			}
			if len(commands) != 1 || commands[0] != test.wantCommand {
				t.Fatalf("commands = %v, want %q", commands, test.wantCommand)
			}
		})
	}
	t.Run("profile_info", func(t *testing.T) {
		// Given
		var output bytes.Buffer
		t.Setenv(AttachSelectedProfileEnv, "focused")
		t.Setenv(AttachEffectiveProfileEnv, "focused+session")
		t.Setenv("UAM_TEST_SECRET", "secret-must-not-appear")
		runtime := newAttachRuntime(attachRuntimeConfig{
			session: "uam-fake-11112222", output: &output, profile: attachProfileFromEnv(os.Getenv),
		})
		frames := newAttachFrameWriter(&bytes.Buffer{}, protocolV2, "client-safe", 7)
		frames.SetAssignedRole(roleController)

		// When
		err := runtime.runCommand(commandShowInfo, frames)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		info := output.String()
		for _, expected := range []string{"selected profile focused", "effective profile focused+session"} {
			if !strings.Contains(info, expected) {
				t.Fatalf("info notice %q lacks %q", info, expected)
			}
		}
		for _, forbidden := range []string{"profile unavailable", "secret-must-not-appear", "UAM_TEST_SECRET"} {
			if strings.Contains(info, forbidden) {
				t.Fatalf("info notice leaked %q: %q", forbidden, info)
			}
		}
	})
}
