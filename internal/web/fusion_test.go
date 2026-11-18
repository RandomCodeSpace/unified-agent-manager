package web

import (
	"context"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// fusionProvider is a provider that keeps a record of Fusion turns.
type fusionProvider struct {
	*agenttest.Provider
	turns []agentapi.Fusion
	err   error
	reads []agentapi.ReadRequest
}

func (p *fusionProvider) ReadFusion(_ context.Context, req agentapi.ReadRequest) ([]agentapi.Fusion, error) {
	p.reads = append(p.reads, req)
	return p.turns, p.err
}

func TestFusionTurnsAreReadFromTheProviderOnDemand(t *testing.T) {
	prov := &fusionProvider{Provider: agenttest.NewProvider("fake", allCaps), turns: []agentapi.Fusion{{ID: "f1", Pattern: "critique"}}}
	m := startManager(t, openTestStore(t), prov)
	sum, _ := createSession(t, m, prov.Provider)
	got, err := m.FusionTurns(sum.ID)
	if err != nil || len(got) != 1 || got[0].ID != "f1" || len(prov.reads) != 1 || prov.reads[0].ConversationID == "" {
		t.Fatalf("turns = %+v, %v after reads %+v", got, err, prov.reads)
	}
	// A conversation the provider no longer has has no turns, and is no failure.
	prov.err = agentapi.ErrConversationNotFound
	if got, err := m.FusionTurns(sum.ID); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("gone conversation = %+v, %v", got, err)
	}
	if _, err := m.FusionTurns("missing"); err == nil {
		t.Fatal("an unknown Thread read Fusion turns")
	}
}

func TestFusionTurnsWithoutARecordAreNone(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	if got, err := m.FusionTurns(sum.ID); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("turns = %+v, %v", got, err)
	}
}
