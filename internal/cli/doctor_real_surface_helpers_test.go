package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type todo10Attachment struct {
	connection net.Conn
	generation uint64
}

func todo10EvidenceDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("UAM_TASK10_EVIDENCE_DIR")
	if dir == "" {
		t.Skip("UAM_TASK10_EVIDENCE_DIR is required for the real-surface fixture")
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("evidence directory must be absolute: %s", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildTodo10Binary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uam")
	command := exec.Command("go", "build", "-o", path, ".")
	command.Dir = todo7RepoRoot(t)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build uam: %v\n%s", err, output)
	}
	return path
}

func installTodo10ProviderStub(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func saveTodo10Config(t *testing.T) {
	t.Helper()
	provider := "claude"
	cfg := store.DefaultConfig()
	cfg.Profiles["stable"] = store.Profile{Provider: &provider}
	cfg.Sessions["claude:a1"] = store.SessionRecord{
		ID: "a1", Agent: provider, SessionName: "uam-claude-a1", Profile: "deleted",
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.DefaultPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.DefaultPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startTodo10Host(t *testing.T, binaryPath string) *exec.Cmd {
	t.Helper()
	command := exec.Command(
		binaryPath, "__host", "--dir", session.DefaultDir(), "--name", "uam-claude-a1",
		"--provider", "claude", "--", "/bin/sh", "-c", "trap 'exit 0' HUP TERM INT; while :; do sleep 1; done",
	)
	command.Env = os.Environ()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.Dial("unix", session.SocketPath(session.DefaultDir(), "uam-claude-a1"))
		if err == nil {
			_ = connection.Close()
			return command
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_ = command.Wait()
	t.Fatalf("host socket did not become ready: %s", stderr.String())
	return nil
}

func attachTodo10Client(t *testing.T, role string) *todo10Attachment {
	t.Helper()
	connection, err := net.Dial("unix", session.SocketPath(session.DefaultDir(), "uam-claude-a1"))
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"op": "attach", "version": 2, "requested_role": role, "cols": 80, "rows": 24,
		"hello": map[string]any{
			"tty":          true,
			"capabilities": []string{"framed_output", "role_events", "local_mouse_filter", "owned_screen"},
		},
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK         bool   `json:"ok"`
		Err        string `json:"err"`
		Generation uint64 `json:"generation"`
	}
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("attach rejected: %s", response.Err)
	}
	return &todo10Attachment{connection: connection, generation: response.Generation}
}

func writeTodo10Frame(t *testing.T, connection net.Conn, kind byte, payload []byte) {
	t.Helper()
	header := [5]byte{kind}
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := connection.Write(append(header[:], payload...)); err != nil {
		t.Fatal(err)
	}
}

func runTodo10DoctorBinary(t *testing.T, binaryPath string, args ...string) []byte {
	t.Helper()
	command := exec.Command(binaryPath, append([]string{"doctor"}, args...)...)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("uam doctor %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func waitTodo10Roles(t *testing.T, controller, standby, observer int) {
	t.Helper()
	client := session.NewClient()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		report, err := client.Doctor(context.Background(), "uam-claude-a1")
		if err == nil && report.Controller == controller && report.Standby == standby && report.Observer == observer {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runtime roles did not settle at controller=%d standby=%d observer=%d", controller, standby, observer)
}

func waitTodo10Event(t *testing.T, event string) {
	t.Helper()
	path := filepath.Join(os.Getenv("UAM_CACHE_DIR"), "uam.log")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(event)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("diagnostic event did not appear: %s", event)
}

func writeTodo10Artifact(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileAbsent(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}
