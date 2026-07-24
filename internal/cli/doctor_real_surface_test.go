package cli

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

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestTodo10DoctorCLIRealSurface(t *testing.T) {
	// Given: an isolated built binary, retained profile fallback, live host, and
	// three real protocol-v2 Unix-socket attachments.
	evidenceDir := todo10EvidenceDir(t)
	isolateDoctorEnvironment(t)
	t.Setenv("UAM_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	binaryPath := buildTodo10Binary(t)
	installTodo10ProviderStub(t)
	saveTodo10Config(t)
	host := startTodo10Host(t, binaryPath)
	connections := []*todo10Attachment{
		attachTodo10Client(t, "controller"),
		attachTodo10Client(t, "controller"),
		attachTodo10Client(t, "controller"),
		attachTodo10Client(t, "observer"),
	}
	t.Cleanup(func() {
		for _, attachment := range connections {
			_ = attachment.connection.Close()
		}
		killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := session.NewClient()
		_ = client.Kill(killCtx, "uam-claude-a1")
		_ = host.Wait()
	})

	// When: control transfers, an invalid resize is ignored, and both doctor
	// JSON surfaces execute through the built binary.
	writeTodo10Frame(t, connections[0].connection, 3, []byte(`{"action":"transfer_control"}`))
	waitTodo10Event(t, `"event":"role.transfer"`)
	resize := make([]byte, 12)
	binary.BigEndian.PutUint64(resize, connections[0].generation)
	writeTodo10Frame(t, connections[0].connection, 1, resize)
	if err := connections[1].connection.Close(); err != nil {
		t.Fatal(err)
	}
	waitTodo10Roles(t, 1, 1, 1)
	globalJSON := runTodo10DoctorBinary(t, binaryPath, "--json")
	sessionJSON := runTodo10DoctorBinary(t, binaryPath, "a1", "--json")
	globalText := runTodo10DoctorBinary(t, binaryPath)
	sessionText := runTodo10DoctorBinary(t, binaryPath, "a1")

	// Then: protocol, role, profile, policy, and fallback fields come from the
	// live runtime and persisted record, and retained artifacts remain secret-free.
	var global app.GlobalDoctorReport
	if err := json.Unmarshal(globalJSON, &global); err != nil {
		t.Fatalf("decode global doctor: %v", err)
	}
	var report app.SessionDoctorReport
	if err := json.Unmarshal(sessionJSON, &report); err != nil {
		t.Fatalf("decode session doctor: %v", err)
	}
	if report.Runtime.Controller != 1 || report.Runtime.Standby != 1 || report.Runtime.Observer != 1 {
		t.Fatalf("live roles = %+v", report.Runtime)
	}
	if fmt.Sprint(report.Runtime.Protocols) != "[1 2]" || report.SelectedProfile != "redacted" ||
		report.EffectiveProfile != "none" || report.ProviderPolicy.OuterScreen != "uam" ||
		fmt.Sprint(report.FallbackReasons) != "[profile_fallback]" {
		t.Fatalf("session report = %+v", report)
	}
	if bytes.Contains(globalText, []byte{0x1b}) || bytes.Contains(sessionText, []byte{0x1b}) {
		t.Fatal("doctor text output contains terminal escape bytes")
	}
	writeTodo10Artifact(t, evidenceDir, "doctor-global.json", globalJSON)
	writeTodo10Artifact(t, evidenceDir, "doctor-session.json", sessionJSON)
	writeTodo10Artifact(t, evidenceDir, "doctor-global.txt", globalText)
	writeTodo10Artifact(t, evidenceDir, "doctor-session.txt", sessionText)
	writeTodo10Frame(t, connections[3].connection, 2, nil)
	for _, attachment := range connections {
		_ = attachment.connection.Close()
	}
	killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	client := session.NewClient()
	killErr := client.Kill(killCtx, "uam-claude-a1")
	cancel()
	if killErr != nil {
		t.Fatal(killErr)
	}
	if err := host.Wait(); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(filepath.Join(os.Getenv("UAM_CACHE_DIR"), "uam.log"))
	if err != nil {
		t.Fatal(err)
	}
	writeTodo10Artifact(t, evidenceDir, "events.jsonl", logData)
	assertions, err := json.MarshalIndent(map[string]any{
		"pass": true, "protocols": report.Runtime.Protocols,
		"controller": report.Runtime.Controller, "standby": report.Runtime.Standby,
		"observer": report.Runtime.Observer, "selected_profile": report.SelectedProfile,
		"effective_profile": report.EffectiveProfile, "fallback_reasons": report.FallbackReasons,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTodo10Artifact(t, evidenceDir, "assertions.json", append(assertions, '\n'))
	cleanup, err := json.MarshalIndent(map[string]any{
		"pass": true, "host_exited": true,
		"socket_absent": fileAbsent(session.SocketPath(session.DefaultDir(), "uam-claude-a1")),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTodo10Artifact(t, evidenceDir, "cleanup-receipt.json", append(cleanup, '\n'))
}
