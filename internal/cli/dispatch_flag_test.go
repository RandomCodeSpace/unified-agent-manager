package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// C2-3 — Go's flag package stops parsing at the first positional, so a flag
// placed AFTER <agent> is silently swallowed into the prompt: --safe never takes
// effect and corrupts the prompt text. RunDispatch must reject a leftover
// "-"-prefixed token sitting in the agent or #name slot rather than treat it as
// prompt text.
func TestRunDispatchRejectsFlagsAfterAgent(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"safe after agent", []string{"fake", "--safe", "do", "work"}},
		{"short flag after agent", []string{"fake", "-x", "do", "work"}},
		{"flag in agent slot", []string{"--safe"}},
		{"cwd flag after agent", []string{"fake", "--cwd", "/tmp", "do", "work"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newCLITestService(t)
			err := RunDispatch(context.Background(), svc, tc.args)
			if err == nil {
				t.Fatalf("expected RunDispatch to reject a misplaced flag, args=%v", tc.args)
			}
			if len(fake.sessions) != 0 {
				t.Fatalf("a rejected dispatch must not launch a session, got %d", len(fake.sessions))
			}
		})
	}
}

// C2-3 — legitimate dispatch forms must keep working, and an interior "--" in
// the prompt must survive verbatim (the prompt-may-contain-"--" contract).
func TestRunDispatchAcceptsValidFormsAndPreservesInteriorDashes(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantName   string
		wantPrompt string
		wantMode   store.Mode
	}{
		{"plain prompt", []string{"fake", "do", "work"}, "", "do work", store.ModeYolo},
		{"named prompt", []string{"fake", "#bugfix", "fix", "thing"}, "bugfix", "fix thing", store.ModeYolo},
		{"interior double dash", []string{"fake", "fix", "the", "--", "flag", "bug"}, "", "fix the -- flag bug", store.ModeYolo},
		{"safe before agent", []string{"--safe", "fake", "do", "work"}, "", "do work", store.ModeSafe},
		{"cwd before agent", []string{"--cwd", "/tmp", "fake", "go"}, "", "go", store.ModeYolo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newCLITestService(t)
			out := captureCLIStdout(t, func() { must(t, RunDispatch(context.Background(), svc, tc.args)) })
			if strings.TrimSpace(out) == "" {
				t.Fatal("expected a dispatched session id on stdout")
			}
			if len(fake.sessions) != 1 {
				t.Fatalf("expected exactly one dispatched session, got %d", len(fake.sessions))
			}
			sess := fake.sessions[0]
			if sess.Prompt != tc.wantPrompt {
				t.Fatalf("prompt = %q, want %q", sess.Prompt, tc.wantPrompt)
			}
			cfg, err := svc.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			rec := cfg.Sessions[store.Key("fake", sess.ID)]
			if tc.wantName != "" && rec.Name != tc.wantName {
				t.Fatalf("name = %q, want %q", rec.Name, tc.wantName)
			}
			if rec.Mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", rec.Mode, tc.wantMode)
			}
		})
	}
}
