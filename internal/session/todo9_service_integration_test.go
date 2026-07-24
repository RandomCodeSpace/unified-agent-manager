package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestServiceReplyRealHostOwnershipIntegration(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("UAM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backend := &session.Client{Dir: runtimeDir, Exe: executable}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = backend.KillAll(context.Background()) })
	const name = "uam-fake-abcdef12"
	if err := backend.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "stty raw -echo; cat"}); err != nil {
		t.Fatal(err)
	}
	agent := adapter.NewAgent("fake", "Fake", []adapter.CommandCandidate{{Display: "sh", Args: []string{"/bin/sh"}}}, nil, backend)
	service := app.NewService(nil, adapter.NewRegistryWithBackend(backend, []adapter.AgentAdapter{agent}))

	if err := service.Reply(ctx, "abcdef12", "detached"); err != nil {
		t.Fatalf("detached service reply: %v", err)
	}

	conn, err := net.Dial("unix", session.SocketPath(runtimeDir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	const attachRequest = `{"op":"attach","version":2,"cols":80,"rows":24,"requested_role":"controller","hello":{"tty":true,"term_hint":"xterm-256color","color_hint":"truecolor","capabilities":["framed_output","role_events","local_mouse_filter","owned_screen"]}}` + "\n"
	if _, err := io.WriteString(conn, attachRequest); err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK           bool   `json:"ok"`
		AssignedRole string `json:"assigned_role"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.AssignedRole != "controller" {
		t.Fatalf("attach response = %+v", response)
	}

	err = service.Reply(ctx, "abcdef12", "attached")
	if !errors.Is(err, session.ErrSessionBusy) {
		t.Fatalf("attached service reply = %v, want ErrSessionBusy", err)
	}
}
