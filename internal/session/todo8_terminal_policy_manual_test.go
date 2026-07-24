package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

type todo8PTYAssertion struct {
	SharedProviderMarker bool `json:"shared_provider_marker"`
	FirstPreservedMouse  bool `json:"first_preserved_mouse"`
	SecondFilteredMouse  bool `json:"second_filtered_mouse"`
	ProviderInputExact   bool `json:"provider_input_exact"`
	RuntimeClean         bool `json:"runtime_clean"`
}

func TestTodo8TerminalPolicyRealPTYFixture(t *testing.T) {
	client := newTestClient(t)
	providerInputPath := filepath.Join(t.TempDir(), "provider-input.bin")
	command := "stty raw -echo; printf '\033[?1;1000;1004;1006;2004hTASK8-READY'; cat >\"$1\""
	name := "uam-fake-80808080"
	if err := client.CreateProviderSession(t.Context(), CreateSpec{
		Name: name, Cwd: t.TempDir(), ProviderIdentity: "claude", ScrollbackLines: 123,
		Command: []string{"/bin/sh", "-c", command, "todo8-provider", providerInputPath},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.KillAll(ctx)
	})

	first := startTodo8Attach(t, client.Dir, name, attachPolicySnapshot{
		mouse: "on", controlPrefix: "C-a", backDetach: false, backDetachSet: true,
	})
	waitFor(t, "first Todo 8 PTY replay", func() bool { return bytes.Contains(first.bytes(), []byte("TASK8-READY")) })
	second := startTodo8Attach(t, client.Dir, name, attachPolicySnapshot{
		mouse: "off", controlPrefix: "C-z", backDetach: true, backDetachSet: true,
	})
	waitFor(t, "second Todo 8 PTY replay", func() bool { return bytes.Contains(second.bytes(), []byte("TASK8-READY")) })

	payload := []byte("\x1b[200~uam-like:\x01d:\x02d\x1b[201~\x1b[I\x1b[O\x1b[1;5A\x1b[>1u")
	if _, err := first.ptmx.Write(payload); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "byte-exact provider input", func() bool {
		got, err := os.ReadFile(providerInputPath)
		return err == nil && bytes.Equal(got, payload)
	})
	providerInput, err := os.ReadFile(providerInputPath)
	if err != nil {
		t.Fatal(err)
	}
	firstLiveOutput, secondLiveOutput := first.bytes(), second.bytes()

	first.detach(t, []byte{0x01, 'd'})
	waitFor(t, "second Todo 8 client promotion", func() bool {
		return bytes.Contains(second.bytes(), []byte("role controller"))
	})
	second.detach(t, []byte{0x1a, 'd'})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := client.KillAll(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(client.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("runtime residue after Todo 8 fixture: %+v", entries)
	}

	firstOutput, secondOutput := first.bytes(), second.bytes()
	assertion := todo8PTYAssertion{
		SharedProviderMarker: bytes.Contains(firstLiveOutput, []byte("TASK8-READY")) && bytes.Contains(secondLiveOutput, []byte("TASK8-READY")),
		FirstPreservedMouse:  containsEnabledDECMode(firstLiveOutput, "1000") && containsEnabledDECMode(firstLiveOutput, "1006"),
		SecondFilteredMouse:  !containsEnabledDECMode(secondLiveOutput, "1000") && !containsEnabledDECMode(secondLiveOutput, "1006"),
		ProviderInputExact:   bytes.Equal(providerInput, payload),
		RuntimeClean:         true,
	}
	if !assertion.SharedProviderMarker || !assertion.FirstPreservedMouse || !assertion.SecondFilteredMouse || !assertion.ProviderInputExact {
		t.Fatalf("Todo 8 PTY assertion failed: %+v", assertion)
	}
	writeTodo8Evidence(t, firstOutput, secondOutput, providerInput, assertion)
}

func containsEnabledDECMode(data []byte, mode string) bool {
	for len(data) > 0 {
		start := bytes.Index(data, []byte("\x1b[?"))
		if start < 0 {
			return false
		}
		data = data[start+3:]
		final := bytes.IndexByte(data, 'h')
		if final < 0 {
			continue
		}
		params := data[:final]
		valid := len(params) > 0
		for _, value := range params {
			if value != ';' && (value < '0' || value > '9') {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		for _, param := range bytes.Split(params, []byte{';'}) {
			if string(param) == mode {
				return true
			}
		}
		data = data[final+1:]
	}
	return false
}

type todo8Attach struct {
	ptmx     *os.File
	done     <-chan error
	snapshot func() string
}

func startTodo8Attach(t *testing.T, dir, name string, policy attachPolicySnapshot) todo8Attach {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true, policy: policy})
	}()
	return todo8Attach{ptmx: ptmx, done: done, snapshot: capturePTYOutput(ptmx)}
}

func (attach todo8Attach) bytes() []byte { return []byte(attach.snapshot()) }

func (attach todo8Attach) detach(t *testing.T, chord []byte) {
	t.Helper()
	if _, err := attach.ptmx.Write(chord); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-attach.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("attach did not detach with chord %x", chord)
	}
}

func writeTodo8Evidence(t *testing.T, first, second, providerInput []byte, assertion todo8PTYAssertion) {
	t.Helper()
	dir := os.Getenv("UAM_TASK8_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("UAM_TASK8_EVIDENCE_DIR must be absolute: %q", dir)
	}
	var transcript bytes.Buffer
	for _, chunk := range [][]byte{first, second} {
		if err := binary.Write(&transcript, binary.BigEndian, uint32(len(chunk))); err != nil {
			t.Fatal(err)
		}
		transcript.Write(chunk)
	}
	artifacts := map[string][]byte{
		"dual-client-transcript.bin": transcript.Bytes(),
		"provider-input.bin":         providerInput,
	}
	data, err := json.MarshalIndent(assertion, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	artifacts["assertions.json"] = append(data, '\n')
	for name, content := range artifacts {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(fmt.Errorf("write %s: %w", name, err))
		}
	}
}
