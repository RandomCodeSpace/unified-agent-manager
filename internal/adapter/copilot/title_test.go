package copilot

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func titleProvider(reply func(context.Context, copilot.MessageOptions) (string, error)) (*webProvider, *fakeClient) {
	fc := &fakeClient{reply: reply, models: []rpc.Model{
		{ID: "gpt-5-mini", SupportedReasoningEfforts: []string{"low", "medium", "high"}},
		{ID: "gpt-6-luna", SupportedReasoningEfforts: []string{"none", "low"}},
		{ID: "claude-haiku-4.5"},
	}}
	return newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour), fc
}

func TestTitleRunsInAToollessThrowawaySessionItDeletes(t *testing.T) {
	var prompt string
	p, fc := titleProvider(func(_ context.Context, msg copilot.MessageOptions) (string, error) {
		prompt = msg.Prompt
		return "Add dark mode toggle", nil
	})
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	var _ agentapi.Titler = p
	if !p.Capabilities().Titles {
		t.Fatal("copilot does not report the titles capability")
	}
	got, err := p.Title(context.Background(), agentapi.TitleRequest{Model: "gpt-5-mini", Workdir: "/work", Text: "Add a dark mode toggle"})
	if err != nil || got != "Add dark mode toggle" {
		t.Fatalf("Title = %q, %v", got, err)
	}
	if prompt != "<user_message>\nAdd a dark mode toggle\n</user_message>" {
		t.Fatalf("prompt = %q", prompt)
	}
	cfg := fc.create[0]
	off := func(b *bool) bool { return b != nil && !*b }
	if cfg.ClientName != "uam-title" || cfg.Model != "gpt-5-mini" || cfg.ReasoningEffort != "low" || cfg.WorkingDirectory != "/work" || cfg.SessionID != "" {
		t.Fatalf("session = %+v", cfg)
	}
	if cfg.AvailableTools == nil || len(cfg.AvailableTools) != 0 {
		t.Fatalf("available tools = %#v, want an empty list", cfg.AvailableTools)
	}
	if !off(cfg.EnableConfigDiscovery) || cfg.SkipCustomInstructions == nil || !*cfg.SkipCustomInstructions || !off(cfg.EnableOnDemandInstructionDiscovery) ||
		!off(cfg.EnableFileHooks) || !off(cfg.EnableHostGitOperations) || !off(cfg.EnableSessionStore) || !off(cfg.EnableSkills) || !off(cfg.Streaming) {
		t.Fatalf("discovery, store or streaming not off: %+v", cfg)
	}
	if cfg.InfiniteSessions == nil || !off(cfg.InfiniteSessions.Enabled) || cfg.Memory == nil || cfg.Memory.Enabled {
		t.Fatalf("infinite sessions %+v, memory %+v", cfg.InfiniteSessions, cfg.Memory)
	}
	if cfg.SystemMessage == nil || cfg.SystemMessage.Mode != "replace" || cfg.SystemMessage.Content != titleSystem {
		t.Fatalf("system message = %+v", cfg.SystemMessage)
	}
	if d, err := cfg.OnPermissionRequest(&rpc.PermissionRequestShell{FullCommandText: "ls"}, copilot.PermissionInvocation{}); err != nil {
		t.Fatal(err)
	} else if _, ok := d.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("permission decision = %T, want reject", d)
	}
	if !fc.sessions[0].disconnected || !slices.Equal(fc.deleted, []string{"created-1"}) {
		t.Fatalf("disconnected %v, deleted %v", fc.sessions[0].disconnected, fc.deleted)
	}
}

func TestTitleEffortIsTheLowestTheModelOffers(t *testing.T) {
	p, fc := titleProvider(func(context.Context, copilot.MessageOptions) (string, error) { return "t", nil })
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	for model, want := range map[string]string{"gpt-6-luna": "none", "claude-haiku-4.5": "", "unlisted": ""} {
		if _, err := p.Title(context.Background(), agentapi.TitleRequest{Model: model, Text: "x"}); err != nil {
			t.Fatal(err)
		}
		if got := fc.create[len(fc.create)-1].ReasoningEffort; got != want {
			t.Fatalf("%s effort = %q, want %q", model, got, want)
		}
	}
	fc.modelsErr = errors.New("catalog down")
	if _, err := p.Title(context.Background(), agentapi.TitleRequest{Model: "gpt-6-luna", Text: "x"}); err != nil {
		t.Fatalf("an unreadable catalog failed the title: %v", err)
	}
	if got := fc.create[len(fc.create)-1].ReasoningEffort; got != "" {
		t.Fatalf("effort without a catalog = %q", got)
	}
}

// Every path after a created session deletes it, with a context of its own;
// a failed create has nothing to delete.
func TestTitleAlwaysDeletesItsSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p, fc := titleProvider(func(ctx context.Context, _ copilot.MessageOptions) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if _, err := p.Title(ctx, agentapi.TitleRequest{Model: "gpt-5-mini", Text: "x"}); err == nil {
		t.Fatal("a timed-out title succeeded")
	}
	if !slices.Equal(fc.deleted, []string{"created-1"}) || fc.deleteCtx[0] != nil || !fc.sessions[0].disconnected {
		t.Fatalf("after a timeout: deleted %v with context errors %v", fc.deleted, fc.deleteCtx)
	}

	fc.reply = func(context.Context, copilot.MessageOptions) (string, error) {
		return "", errors.New("session error: boom")
	}
	fc.deleteErr = errors.New("delete failed")
	if _, err := p.Title(context.Background(), agentapi.TitleRequest{Model: "gpt-5-mini", Text: "x"}); err == nil {
		t.Fatal("a session error succeeded")
	}
	if !slices.Equal(fc.deleted, []string{"created-1", "created-2"}) {
		t.Fatalf("after a session error: deleted %v", fc.deleted)
	}

	fc.createErr = errors.New(`Model "no-such-model" is not available.`)
	if _, err := p.Title(context.Background(), agentapi.TitleRequest{Model: "no-such-model", Text: "x"}); err == nil {
		t.Fatal("a failed create succeeded")
	}
	if len(fc.deleted) != 2 {
		t.Fatalf("a failed create deleted %v", fc.deleted)
	}
}

func TestSetTitleNamesTheSession(t *testing.T) {
	h := openWeb(t)
	if err := h.conv.SetTitle(context.Background(), "Fix the build"); err != nil {
		t.Fatal(err)
	}
	h.fs.nameErr = errors.New("rpc failed")
	if err := h.conv.SetTitle(context.Background(), "Again"); err == nil {
		t.Fatal("a failed rename succeeded")
	}
	if !slices.Equal(h.fs.names, []string{"Fix the build", "Again"}) {
		t.Fatalf("names = %v", h.fs.names)
	}
	if err := h.conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.conv.SetTitle(context.Background(), "Late"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("SetTitle after Close = %v", err)
	}
}

func TestSubagentSummarySharesToollessUtilitySessionAndCleanup(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var prompt string
			p, fc := titleProvider(func(_ context.Context, msg copilot.MessageOptions) (string, error) {
				prompt = msg.Prompt
				switch outcome {
				case "failure":
					return "", errors.New("model failed")
				case "cancelled":
					cancel()
					return "", ctx.Err()
				default:
					return "Verified the implementation.", nil
				}
			})
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			var _ agentapi.SubagentSummarizer = p
			reply, err := p.SummarizeSubagent(ctx, agentapi.SubagentSummaryRequest{Model: "gpt-6-luna", Workdir: "/work", Description: "Review", Result: "All checks passed"})
			if (err == nil) != (outcome == "success") || outcome == "success" && reply != "Verified the implementation." {
				t.Fatalf("reply %q, error %v", reply, err)
			}
			if prompt != "<description>\nReview\n</description>\n<result>\nAll checks passed\n</result>" {
				t.Fatalf("prompt = %q", prompt)
			}
			cfg := fc.create[0]
			off := func(b *bool) bool { return b != nil && !*b }
			if cfg.ClientName != "uam-subagent-summary" || cfg.Model != "gpt-6-luna" || cfg.ReasoningEffort != "none" || cfg.WorkingDirectory != "/work" || cfg.SessionID != "" {
				t.Fatalf("utility session = %+v", cfg)
			}
			if cfg.AvailableTools == nil || len(cfg.AvailableTools) != 0 || len(cfg.Tools) != 0 {
				t.Fatal("summary session enabled tools")
			}
			if !off(cfg.EnableConfigDiscovery) || cfg.SkipCustomInstructions == nil || !*cfg.SkipCustomInstructions || !off(cfg.EnableOnDemandInstructionDiscovery) || !off(cfg.EnableFileHooks) || !off(cfg.EnableHostGitOperations) || !off(cfg.EnableSessionStore) || !off(cfg.EnableSkills) || !off(cfg.Streaming) {
				t.Fatal("summary session enabled discovered configuration or persistence")
			}
			if cfg.InfiniteSessions == nil || !off(cfg.InfiniteSessions.Enabled) || cfg.Memory == nil || cfg.Memory.Enabled {
				t.Fatal("summary session retained memory")
			}
			if cfg.SystemMessage == nil || cfg.SystemMessage.Mode != "replace" || cfg.SystemMessage.Content != subagentSummarySystem {
				t.Fatal("summary session did not replace the system prompt")
			}
			decision, err := cfg.OnPermissionRequest(&rpc.PermissionRequestShell{FullCommandText: "ls"}, copilot.PermissionInvocation{})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := decision.(*rpc.PermissionDecisionReject); !ok {
				t.Fatal("utility permission was not rejected")
			}
			if !fc.sessions[0].disconnected || !slices.Equal(fc.deleted, []string{"created-1"}) || fc.deleteCtx[0] != nil {
				t.Fatalf("cleanup = %v, %v", fc.deleted, fc.deleteCtx)
			}
			// A subsequent utility call reuses the same started SDK client.
			fc.reply = func(context.Context, copilot.MessageOptions) (string, error) { return "Title", nil }
			if _, err := p.Title(context.Background(), agentapi.TitleRequest{Model: "gpt-6-luna", Text: "Task"}); err != nil {
				t.Fatal(err)
			}
			if len(fc.create) != 2 || len(fc.deleted) != 2 {
				t.Fatal("utilities did not use the shared client")
			}
		})
	}
}
