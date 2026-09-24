package copilot

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestWebConfigDiscoveryOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	if cfg := h.fc.create[0]; cfg.EnableConfigDiscovery == nil || !*cfg.EnableConfigDiscovery {
		t.Fatalf("create config discovery = %v", cfg.EnableConfigDiscovery)
	}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "s-1", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if cfg := h.fc.resume[0]; cfg.EnableConfigDiscovery == nil || !*cfg.EnableConfigDiscovery {
		t.Fatalf("resume config discovery = %v", cfg.EnableConfigDiscovery)
	}
}

var composerFiles = []agentapi.File{{Path: "/work/src/a.go", Rel: "src/a.go"}, {Path: "/work/docs", Rel: "docs", Dir: true}}

func wantFileAttachments() []copilot.Attachment {
	return []copilot.Attachment{
		&rpc.AttachmentFile{Path: "/work/src/a.go", DisplayName: "src/a.go"},
		&rpc.AttachmentDirectory{Path: "/work/docs", DisplayName: "docs"},
	}
}

func TestWebSendAttachesReferencedFiles(t *testing.T) {
	h := openWeb(t)
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "see @src/a.go and @docs", Files: composerFiles}); err != nil {
		t.Fatal(err)
	}
	msg := h.fs.msgs[0]
	if msg.Prompt != "see @src/a.go and @docs" || msg.DisplayPrompt != "" || msg.Mode != string(rpc.SendModeEnqueue) || !reflect.DeepEqual(msg.Attachments, wantFileAttachments()) {
		t.Fatalf("sent %+v", msg)
	}
	if err := h.conv.Steer(context.Background(), "steer"); err != nil || h.fs.msgs[1].Attachments != nil {
		t.Fatalf("steer %v sent attachments %+v", err, h.fs.msgs[1].Attachments)
	}
}

func TestWebCommandsListSkillsInitAndReview(t *testing.T) {
	h := openWeb(t)
	h.fs.commands = []rpc.SlashCommandInfo{
		{Name: "review", Kind: rpc.SlashCommandKindBuiltin, Description: "Review changes", Input: &rpc.SlashCommandInput{Hint: "focus"}},
		{Name: "init", Kind: rpc.SlashCommandKindBuiltin},
		{Name: "model", Kind: rpc.SlashCommandKindBuiltin},
		{Name: "plan", Kind: rpc.SlashCommandKindBuiltin},
		{Name: "add-dir", Kind: rpc.SlashCommandKindBuiltin},
		{Name: "probe-skill", Kind: rpc.SlashCommandKindSkill, Description: "Probe"},
		{Name: "extension", Kind: rpc.SlashCommandKindClient},
	}
	got, err := h.conv.Commands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []agentapi.Command{
		{Name: "review", Description: "Review changes", Kind: agentapi.CommandPrompt, InputHint: "focus"},
		{Name: "init", Kind: agentapi.CommandPrompt},
		{Name: "probe-skill", Description: "Probe", Kind: agentapi.CommandSkill},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %+v", got)
	}
}

func TestWebRunCommandSendsOnlyAPromptWithoutModeChange(t *testing.T) {
	h := openWeb(t)
	h.fs.invoke = &rpc.SlashCommandAgentPromptResult{Prompt: "<skill-context>probe</skill-context>\nARGUMENTS: alpha", DisplayPrompt: "/probe-skill alpha"}
	if err := h.conv.RunCommand(context.Background(), "probe-skill", agentapi.Prompt{Text: "alpha", Files: composerFiles}); err != nil {
		t.Fatal(err)
	}
	msg := h.fs.msgs[0]
	if h.fs.invoked[0] != "probe-skill alpha" || msg.Prompt != "<skill-context>probe</skill-context>\nARGUMENTS: alpha" || msg.DisplayPrompt != "/probe-skill alpha" ||
		msg.Mode != string(rpc.SendModeEnqueue) || !reflect.DeepEqual(msg.Attachments, wantFileAttachments()) {
		t.Fatalf("invoked %q, sent %+v", h.fs.invoked, msg)
	}
	if last := h.sink.last(); last.Kind != agentapi.EventTurn || last.Turn.State != agentapi.TurnWorking {
		t.Fatalf("last event = %+v", last)
	}
	h.fs.invoke = &rpc.SlashCommandAgentPromptResult{Prompt: "review this", DisplayPrompt: "/review"}
	if err := h.conv.RunCommand(context.Background(), "review", agentapi.Prompt{}); err != nil || h.fs.msgs[1].DisplayPrompt != "/review" {
		t.Fatalf("no-argument command: %v, %+v", err, h.fs.msgs[1])
	}

	plan := rpc.SessionModePlan
	for _, tc := range []struct {
		res  rpc.SlashCommandInvocationResult
		err  error
		want string
	}{
		{res: &rpc.SlashCommandTextResult{Text: "usage"}, want: "with text, not a prompt"},
		{res: &rpc.SlashCommandAgentPromptResult{Prompt: "plan it", Mode: &plan}, want: "switch the session to plan mode"},
		{res: &rpc.SlashCommandCompletedResult{}, want: "with completed"},
		{err: errors.New("Unknown slash command: /x"), want: "refused /x"},
	} {
		h.fs.invoke, h.fs.invokeErr = tc.res, tc.err
		err := h.conv.RunCommand(context.Background(), "x", agentapi.Prompt{Text: "y"})
		if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("result %T/%v = %v, want rejection %q", tc.res, tc.err, err, tc.want)
		}
	}
	if len(h.fs.msgs) != 2 {
		t.Fatalf("a refused command sent %d messages", len(h.fs.msgs)-2)
	}
}
