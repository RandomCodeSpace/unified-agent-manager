package session

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type todo11HostHarness struct {
	client          *Client
	name            string
	reportPath      string
	diagnosticsPath string
	runtimeDir      string
	done            <-chan error
	hostStderr      *bytes.Buffer
	transcript      bytes.Buffer
}

type todo11HostDiagnostic struct {
	Event    string `json:"event"`
	ClientID string `json:"client_id"`
	TermHint string `json:"term_hint"`
}

func todo11StartHost(t *testing.T, fixture todo11Fixture, index int) *todo11HostHarness {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := socketTestDir(t)
	reportPath := filepath.Join(t.TempDir(), "provider.jsonl")
	diagnosticsPath := filepath.Join(t.TempDir(), "host-diagnostics.jsonl")
	cwd := t.TempDir()
	name := fmt.Sprintf("uam-fake-%08d", index)
	hostCommand := exec.Command(executable, "-test.run=^TestTodo11HostProcess$") // #nosec G204 -- current test binary.
	hostCommand.Env = append(os.Environ(),
		"UAM_TODO11_HOST_HELPER=1",
		"UAM_HOST_READY_FD=3",
		"UAM_TODO11_HOST_NAME="+name,
		"UAM_TODO11_HOST_CWD="+cwd,
		"UAM_TODO11_RUNTIME_DIR="+runtimeDir,
		"UAM_TODO11_TEST_BINARY="+executable,
		"UAM_TODO11_PROVIDER_REPORT="+reportPath,
		"UAM_TODO11_HOST_DIAGNOSTICS="+diagnosticsPath,
		"UAM_TODO11_PROVIDER_OUTPUT_HEX="+fixture.ProviderOutputHex,
	)
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	hostCommand.ExtraFiles = []*os.File{readyWriter}
	hostStderr := &bytes.Buffer{}
	hostCommand.Stderr = hostStderr
	if err := hostCommand.Start(); err != nil {
		_ = readyReader.Close()
		_ = readyWriter.Close()
		t.Fatal(err)
	}
	_ = readyWriter.Close()
	done := make(chan error, 1)
	go func() {
		done <- hostCommand.Wait()
	}()
	line, err := bufio.NewReader(readyReader).ReadString('\n')
	_ = readyReader.Close()
	if err != nil || line != "ok\n" {
		select {
		case waitErr := <-done:
			t.Fatalf("host readiness = %q, %v; host wait=%v; stderr=%s", line, err, waitErr, hostStderr.String())
		case <-time.After(10 * time.Second):
			_ = hostCommand.Process.Kill()
			<-done
			t.Fatalf("host readiness = %q, %v; host did not exit; stderr=%s", line, err, hostStderr.String())
		}
	}
	harness := &todo11HostHarness{
		client: &Client{Dir: runtimeDir, Exe: executable}, name: name,
		reportPath: reportPath, diagnosticsPath: diagnosticsPath,
		runtimeDir: runtimeDir, done: done, hostStderr: hostStderr,
	}
	waitFor(t, "fake provider startup", func() bool {
		events := harness.events(t)
		return len(events) > 0 && events[0].Type == "startup"
	})
	waitFor(t, "fake provider replay readiness", func() bool {
		capture, captureErr := harness.client.Capture(t.Context(), name, 50)
		return captureErr == nil && bytes.Contains([]byte(capture), []byte("TODO11-READY"))
	})
	return harness
}

func (harness *todo11HostHarness) negotiatedTermHint(t *testing.T, clientID string) string {
	t.Helper()
	var observed string
	waitFor(t, "host TERM diagnostic", func() bool {
		data, err := os.ReadFile(harness.diagnosticsPath)
		if err != nil {
			return false
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var event todo11HostDiagnostic
			if json.Unmarshal(line, &event) != nil {
				continue
			}
			if event.Event == "attach.negotiation" && event.ClientID == clientID {
				observed = event.TermHint
				return true
			}
		}
		return false
	})
	return observed
}

func (harness *todo11HostHarness) events(t *testing.T) []todo11ProviderEvent {
	t.Helper()
	data, err := os.ReadFile(harness.reportPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	var events []todo11ProviderEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var event todo11ProviderEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func (harness *todo11HostHarness) waitInputCount(t *testing.T, count int) []todo11ProviderEvent {
	t.Helper()
	var inputs []todo11ProviderEvent
	waitFor(t, fmt.Sprintf("provider input count %d", count), func() bool {
		inputs = inputs[:0]
		for _, event := range harness.events(t) {
			if event.Type == "input" {
				inputs = append(inputs, event)
			}
		}
		return len(inputs) >= count
	})
	return inputs
}

func todo11EventInput(t *testing.T, event todo11ProviderEvent) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(event.Data)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (harness *todo11HostHarness) cleanup(t *testing.T) (bool, int) {
	t.Helper()
	select {
	case err := <-harness.done:
		if err != nil {
			t.Fatalf("host process: %v; stderr=%s", err, harness.hostStderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit after provider shutdown")
	}
	_, socketErr := os.Stat(SocketPath(harness.runtimeDir, harness.name))
	entries, err := os.ReadDir(harness.runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	return errors.Is(socketErr, os.ErrNotExist), len(entries)
}

func todo11ObservedInputs(t *testing.T, events []todo11ProviderEvent, names []string) map[string]string {
	t.Helper()
	if len(events) < len(names) {
		t.Fatalf("provider inputs = %d, want at least %d", len(events), len(names))
	}
	result := make(map[string]string, len(names))
	for index, name := range names {
		result[name] = hex.EncodeToString(todo11EventInput(t, events[index]))
	}
	return result
}

func todo11ReadAllReport(t *testing.T, harness *todo11HostHarness) []byte {
	t.Helper()
	data, err := os.ReadFile(harness.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
