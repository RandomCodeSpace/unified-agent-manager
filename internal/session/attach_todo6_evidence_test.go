package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

type todo6Evidence struct {
	controller     *todo6PTYAttach
	standby        *todo6PTYAttach
	observer       *todo6PTYAttach
	providerOutput string
	termiosAfter   map[string][]byte
	termiosEqual   map[string]bool
	cleanupOrder   map[string]todo6CleanupOrder
	cleanup        todo6CleanupObservation
}

type todo6ControlEvent struct {
	Client  string `json:"client"`
	Message string `json:"message"`
}

type todo6CleanupOrder struct {
	ResetOffset int  `json:"reset_offset"`
	ExitOffset  int  `json:"exit_offset"`
	Valid       bool `json:"valid"`
}

type todo6CleanupObservation struct {
	hostPID        int
	hostAlive      bool
	childPID       int
	childAlive     bool
	socketPath     string
	socketPresent  bool
	runtimeEntries []string
}

type todo6CleanupReceipt struct {
	HostPID        int      `json:"host_pid"`
	HostAlive      bool     `json:"host_alive"`
	ChildPID       int      `json:"child_pid"`
	ChildAlive     bool     `json:"child_alive"`
	SocketPath     string   `json:"socket_path"`
	SocketPresent  bool     `json:"socket_present"`
	RuntimeEntries []string `json:"runtime_entries"`
}

type todo6Assertions struct {
	ProviderByteValues        []int                        `json:"provider_byte_values"`
	RejectedByteValuesPresent []int                        `json:"rejected_byte_values_present"`
	ControlEventCount         int                          `json:"control_event_count"`
	TermiosEqual              map[string]bool              `json:"termios_equal"`
	CleanupOrder              map[string]todo6CleanupOrder `json:"cleanup_order"`
}

func TestTodo6EvidenceDirectoryRejectsRelativePath(t *testing.T) {
	// Given
	getenv := func(string) string { return ".omo/evidence/persistent-agent-multiplexer/task-6-attach" }

	// When
	_, err := todo6EvidenceDirectory(getenv)

	// Then
	if err == nil {
		t.Fatal("relative Todo 6 evidence directory was accepted")
	}
}

func (observation todo6CleanupObservation) validate() error {
	switch {
	case observation.hostAlive:
		return fmt.Errorf("host process %d remains alive", observation.hostPID)
	case observation.childAlive:
		return fmt.Errorf("child process %d remains alive", observation.childPID)
	case observation.socketPresent:
		return fmt.Errorf("session socket remains at %s", observation.socketPath)
	case len(observation.runtimeEntries) != 0:
		return fmt.Errorf("runtime entries remain: %v", observation.runtimeEntries)
	default:
		return nil
	}
}

func (observation todo6CleanupObservation) receipt() todo6CleanupReceipt {
	return todo6CleanupReceipt{
		HostPID: observation.hostPID, HostAlive: observation.hostAlive,
		ChildPID: observation.childPID, ChildAlive: observation.childAlive,
		SocketPath: observation.socketPath, SocketPresent: observation.socketPresent,
		RuntimeEntries: observation.runtimeEntries,
	}
}

func todo6TermStateBytes(state *term.State) []byte {
	return fmt.Appendf(nil, "%#v", state)
}

func observeTodo6CleanupOrder(output string) todo6CleanupOrder {
	resetOffset := strings.LastIndex(output, screenReset)
	exitOffset := strings.LastIndex(output, screenExit)
	return todo6CleanupOrder{ResetOffset: resetOffset, ExitOffset: exitOffset, Valid: resetOffset >= 0 && resetOffset <= exitOffset}
}

func todo6EvidenceDirectory(getenv func(string) string) (string, error) {
	dir := getenv("UAM_TASK6_EVIDENCE_DIR")
	if dir == "" {
		return "", nil
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("UAM_TASK6_EVIDENCE_DIR must be absolute: %q", dir)
	}
	return filepath.Clean(dir), nil
}

func writeTodo6Evidence(t *testing.T, evidence todo6Evidence) {
	t.Helper()
	dir, err := todo6EvidenceDirectory(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := bytes.Join([][]byte{
		[]byte(evidence.controller.snapshot()), []byte(evidence.standby.snapshot()),
		[]byte(evidence.observer.snapshot()), []byte(evidence.providerOutput),
	}, []byte("\n---CLIENT---\n"))
	if err := os.WriteFile(filepath.Join(dir, "pty-transcript.bin"), transcript, 0o600); err != nil {
		t.Fatal(err)
	}
	events := append(parseTodo6ControlEvents("controller", evidence.controller.snapshot()), parseTodo6ControlEvents("standby", evidence.standby.snapshot())...)
	events = append(events, parseTodo6ControlEvents("observer", evidence.observer.snapshot())...)
	requireTodo6ControlEvents(t, events)
	writeTodo6ControlEvents(t, filepath.Join(dir, "control-events.jsonl"), events)
	providerBytes, err := parseTodo6ProviderBytes(evidence.providerOutput)
	if err != nil {
		t.Fatal(err)
	}
	rejectedPresent := presentTodo6ProviderBytes(providerBytes, []int{79, 83, 112})
	if len(rejectedPresent) != 0 {
		t.Fatalf("rejected provider bytes observed: %v", rejectedPresent)
	}
	before := map[string][]byte{
		"controller": todo6TermStateBytes(evidence.controller.before),
		"standby":    todo6TermStateBytes(evidence.standby.before),
		"observer":   todo6TermStateBytes(evidence.observer.before),
	}
	writeTodo6JSON(t, filepath.Join(dir, "termios-before.json"), before)
	writeTodo6JSON(t, filepath.Join(dir, "termios-after.json"), evidence.termiosAfter)
	writeTodo6JSON(t, filepath.Join(dir, "assertions.json"), todo6Assertions{
		ProviderByteValues: providerBytes, RejectedByteValuesPresent: rejectedPresent,
		ControlEventCount: len(events), TermiosEqual: evidence.termiosEqual, CleanupOrder: evidence.cleanupOrder,
	})
	writeTodo6JSON(t, filepath.Join(dir, "cleanup-receipt.json"), evidence.cleanup.receipt())
}

func parseTodo6ControlEvents(client, snapshot string) []todo6ControlEvent {
	const prefix = "[uam: "
	events := make([]todo6ControlEvent, 0)
	for {
		start := strings.Index(snapshot, prefix)
		if start < 0 {
			return events
		}
		snapshot = snapshot[start+len(prefix):]
		end := strings.IndexByte(snapshot, ']')
		if end < 0 {
			return events
		}
		events = append(events, todo6ControlEvent{Client: client, Message: snapshot[:end]})
		snapshot = snapshot[end+1:]
	}
}

func requireTodo6ControlEvents(t *testing.T, events []todo6ControlEvent) {
	t.Helper()
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encoded)
	for _, expected := range []string{
		"control requested", "control transfer requested", "selected profile focused", "effective profile focused+session", "mouse passthrough false",
		`"client":"controller"`, `"client":"standby"`, `"client":"observer"`,
	} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("captured control events lack %q: %s", expected, raw)
		}
	}
}

func writeTodo6ControlEvents(t *testing.T, path string, events []todo6ControlEvent) {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func parseTodo6ProviderBytes(output string) ([]int, error) {
	values := make([]int, 0)
	for _, line := range strings.Split(output, "\n") {
		raw, found := strings.CutPrefix(strings.TrimSpace(line), "BYTE:")
		if !found {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("parse provider byte %q: %w", raw, err)
		}
		values = append(values, value)
	}
	return values, nil
}

func presentTodo6ProviderBytes(observed, candidates []int) []int {
	present := make([]int, 0)
	for _, candidate := range candidates {
		for _, value := range observed {
			if value == candidate {
				present = append(present, candidate)
				break
			}
		}
	}
	return present
}

func writeTodo6JSON[T any](t *testing.T, path string, value T) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
