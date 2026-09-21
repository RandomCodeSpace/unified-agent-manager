package app

import (
	"context"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestTodo7CharacterizationDispatchKeepsExplicitLaunchInputs(t *testing.T) {
	// Given
	svc, fake := newWorkflowService(t)

	// When
	_, err := svc.DispatchNamedWithAlias(context.Background(), "fake", "review", "named", "work", "/tmp", "safe")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if fake.dispatched == nil || fake.dispatched.CommandAlias != "review" || fake.dispatched.Mode != "safe" {
		t.Fatalf("dispatch request = %+v", fake.dispatched)
	}
}

func TestTodo7CharacterizationFindExactRejectsIDPrefixes(t *testing.T) {
	// Given
	svc, fake := newWorkflowService(t)
	fake.sessions = []adapter.Session{{ID: "12345678", AgentType: "fake", SessionName: "uam-fake-12345678"}}

	// When
	_, _, err := svc.FindExact(context.Background(), "fake", "1234")

	// Then
	if err == nil {
		t.Fatal("FindExact accepted a session ID prefix")
	}
}

func TestTodo7CharacterizationLaunchPadSeedsProviderBeforeLaunchFields(t *testing.T) {
	// Given
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
	m.defaultAgent = "fake"

	// When
	m.openLaunchPad()

	// Then
	if m.launchAgent != "fake" || m.launchField != launchFieldName || m.input != "" {
		t.Fatalf("launch state = agent=%q field=%d input=%q", m.launchAgent, m.launchField, m.input)
	}
}
