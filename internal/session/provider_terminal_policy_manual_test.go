package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type providerTerminalAssertion struct {
	Name             string   `json:"name"`
	SessionName      string   `json:"session_name"`
	ProviderIdentity string   `json:"provider_identity,omitempty"`
	Argv             []string `json:"argv"`
	ScreenEnter      bool     `json:"screen_enter"`
	ScreenExit       bool     `json:"screen_exit"`
	NoAltScreenCount int      `json:"no_alt_screen_count"`
}

func TestProviderTerminalPolicyRealPTYFixture(t *testing.T) {
	client := newTestClient(t)
	provider := filepath.Join(t.TempDir(), "fake-provider")
	script := "#!/bin/sh\nprintf 'TASK3-ARGV'\nfor arg in \"$@\"; do printf '<%s>' \"$arg\"; done\nprintf '\\n'\nsleep 60\n"
	if err := os.WriteFile(provider, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name             string
		sessionName      string
		providerIdentity string
		args             []string
		wantOuterScreen  bool
	}{
		{name: "explicit generic", sessionName: "uam-codex-d1d1d1d1", providerIdentity: "claude", args: []string{"--generic"}, wantOuterScreen: true},
		{name: "explicit Codex", sessionName: "uam-fake-e2e2e2e2", providerIdentity: "codex", args: []string{"--no-alt-screen", "resume", "--last"}},
		{name: "legacy Codex fallback", sessionName: "uam-codex-f3f3f3f3", args: []string{"--no-alt-screen"}},
	}

	assertions := make([]providerTerminalAssertion, 0, len(tests))
	var transcript bytes.Buffer
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := append([]string{provider}, test.args...)
			spec := CreateSpec{Name: test.sessionName, Cwd: t.TempDir(), ProviderIdentity: test.providerIdentity, Command: command}
			if test.providerIdentity == "" {
				if err := client.CreateSession(context.Background(), spec.Name, spec.Cwd, nil, spec.Command); err != nil {
					t.Fatalf("CreateSession: %v", err)
				}
			} else if err := client.CreateProviderSession(context.Background(), spec); err != nil {
				t.Fatalf("CreateProviderSession: %v", err)
			}
			state, err := readState(client.Dir, test.sessionName)
			if err != nil {
				t.Fatalf("readState: %v", err)
			}
			if strings.Join(state.Command, "\x00") != strings.Join(command, "\x00") {
				t.Fatalf("provider argv = %#v, want %#v", state.Command, command)
			}

			attached := startQuietAttach(t, client.Dir, test.sessionName, 80, 24)
			waitFor(t, "task 3 provider argv", func() bool { return strings.Contains(attached.Snapshot(), "TASK3-ARGV") })
			attached.Detach(t)
			waitFor(t, "task 3 terminal cleanup", func() bool { return strings.Contains(attached.Snapshot(), screenReset) })
			output := []byte(attached.Snapshot())
			enter := bytes.Contains(output, []byte(screenEnter))
			exit := bytes.Contains(output, []byte(screenExit))
			if enter != test.wantOuterScreen || exit != test.wantOuterScreen {
				t.Fatalf("screen ownership enter=%v exit=%v, want %v", enter, exit, test.wantOuterScreen)
			}
			noAltScreenCount := 0
			for _, arg := range state.Command {
				if arg == "--no-alt-screen" {
					noAltScreenCount++
				}
			}
			assertions = append(assertions, providerTerminalAssertion{
				Name: test.name, SessionName: test.sessionName, ProviderIdentity: test.providerIdentity,
				Argv: state.Command, ScreenEnter: enter, ScreenExit: exit,
				NoAltScreenCount: noAltScreenCount,
			})
			writeTask3TranscriptChunk(t, &transcript, test.name, output)
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.KillAll(ctx); err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	entries, err := os.ReadDir(client.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("runtime directory not empty after cleanup: %+v", entries)
	}
	writeTask3ManualEvidence(t, transcript.Bytes(), assertions)
}

func writeTask3TranscriptChunk(t *testing.T, transcript *bytes.Buffer, name string, output []byte) {
	t.Helper()
	if err := binary.Write(transcript, binary.BigEndian, uint32(len(name))); err != nil {
		t.Fatal(err)
	}
	transcript.WriteString(name)
	if err := binary.Write(transcript, binary.BigEndian, uint32(len(output))); err != nil {
		t.Fatal(err)
	}
	transcript.Write(output)
}

func writeTask3ManualEvidence(t *testing.T, transcript []byte, assertions []providerTerminalAssertion) {
	t.Helper()
	dir := os.Getenv("UAM_TASK3_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "task-3-pty-transcript.bin"), transcript, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(struct {
		Assertions   []providerTerminalAssertion `json:"assertions"`
		RuntimeClean bool                        `json:"runtime_clean"`
	}{Assertions: assertions, RuntimeClean: true}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "task-3-manual-assertions.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup, err := json.MarshalIndent(struct {
		KillAllSucceeded bool `json:"kill_all_succeeded"`
		RuntimeEntries   int  `json:"runtime_entries"`
		SocketsRemaining int  `json:"sockets_remaining"`
	}{KillAllSucceeded: true}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	cleanup = append(cleanup, '\n')
	if err := os.WriteFile(filepath.Join(dir, "task-3-cleanup.json"), cleanup, 0o600); err != nil {
		t.Fatal(err)
	}
}
