package opencode

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Uses the fake `opencode serve` subprocess from supervisor_test.go.
func TestWebServerProcessOwnershipAndExit(t *testing.T) {
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{})
	t.Cleanup(func() { killFakeProcesses(fixture.records(t)) })
	newProvider := func() *webProvider {
		provider := newWebProvider(func(context.Context) (providerCommand, error) { return fixture.options.Command, nil }, startWebRuntime)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = provider.Shutdown(ctx)
		})
		return provider
	}
	owner, other := newProvider(), newProvider()
	open := func(provider *webProvider) (agentapi.Conversation, *recordingSink) {
		t.Helper()
		sink := &recordingSink{}
		conversation, err := provider.Open(testContext(t), agentapi.OpenRequest{Workdir: fixture.options.Directory, Title: "Demo", Events: sink})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return conversation, sink
	}
	ownerConversation, _ := open(owner)
	_, otherSink := open(other)
	serves := fixture.recordsOfKind(t, "serve")
	if len(serves) != 2 || ownerConversation.ID() != "ses_created123" {
		t.Fatalf("serve records = %#v, conversation %q", serves, ownerConversation.ID())
	}
	for _, serve := range serves {
		if !serve.PasswordReplaced || strings.Contains(strings.Join(serve.Args, " "), "--auto") {
			t.Fatalf("server launch = %#v", serve)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := syscall.Kill(serves[0].PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("owned server still running after Shutdown: %v", err)
	}
	if err := syscall.Kill(serves[1].PID, 0); err != nil {
		t.Fatalf("Shutdown stopped a server it did not start: %v", err)
	}
	if err := ownerConversation.Send(t.Context(), "hello"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("Send after Shutdown = %v", err)
	}

	other.mu.Lock()
	password := other.servers[fixture.options.Directory].runtime.client.password
	other.mu.Unlock()
	if err := syscall.Kill(serves[1].PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	exit := otherSink.waitFor(t, "exit after server death", func(event agentapi.Event) bool { return event.Kind == agentapi.EventExit })
	if !strings.Contains(exit.Error, "exited unexpectedly") || strings.Contains(exit.Error, password) {
		t.Fatalf("exit error = %q", exit.Error)
	}
	if _, err := owner.Open(t.Context(), agentapi.OpenRequest{Workdir: fixture.options.Directory, Events: &recordingSink{}}); err == nil {
		t.Fatal("Open after Shutdown succeeded")
	}
}
