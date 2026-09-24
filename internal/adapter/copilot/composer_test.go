package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestWebCommandsListSupportedAndDisabledNativeCommands(t *testing.T) {
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
	if len(got) != len(h.fs.commands) {
		t.Fatalf("catalog dropped commands: %+v", got)
	}
	for _, cmd := range got {
		disabled := cmd.Name == "plan" || cmd.Name == "add-dir" || cmd.Name == "extension"
		if (cmd.DisabledReason != "") != disabled {
			t.Fatalf("wrong support state: %+v", cmd)
		}
	}

}

func TestWebRunCommandSendsPromptAndRejectsUnsupportedBeforeInvoke(t *testing.T) {
	h := openWeb(t)
	h.fs.commands = []rpc.SlashCommandInfo{{Name: "probe-skill", Kind: rpc.SlashCommandKindSkill}, {Name: "review", Kind: rpc.SlashCommandKindBuiltin}, {Name: "plan", Kind: rpc.SlashCommandKindBuiltin}}
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

	for _, name := range []string{"plan", "unknown"} {
		if err := h.conv.RunCommand(context.Background(), name, agentapi.Prompt{}); err == nil {
			t.Fatal("unsupported command accepted")
		}
	}
	if len(h.fs.invoked) != 2 || len(h.fs.msgs) != 2 {
		t.Fatal("unsupported command reached provider")
	}

}

func TestWebModelsReportTheMediaGate(t *testing.T) {
	vision := rpc.ModelCapabilities{
		Supports: &rpc.ModelCapabilitiesSupports{Vision: copilot.Bool(true)},
		Limits:   &rpc.ModelCapabilitiesLimits{Vision: &rpc.ModelCapabilitiesLimitsVision{MaxPromptImages: 3, SupportedMediaTypes: []string{"image/png", "application/pdf"}}},
	}
	fc := &fakeClient{models: []rpc.Model{
		{ID: "auto", Name: "Auto"},
		{ID: "seeing", Name: "Seeing", Capabilities: vision},
		{ID: "text", Name: "Text", Capabilities: rpc.ModelCapabilities{Supports: &rpc.ModelCapabilitiesSupports{Vision: copilot.Bool(false)}}},
	}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []*agentapi.Media{nil, {Images: true, PDF: true, MaxImages: 3, Types: []string{"image/png", "application/pdf"}}, {}}
	for i, m := range models {
		if !reflect.DeepEqual(m.Media, want[i]) {
			t.Fatalf("%s media = %+v, want %+v", m.ID, m.Media, want[i])
		}
	}
}

func TestWebSendCarriesUploadsAsBlobsAndShowsThem(t *testing.T) {
	h := openWeb(t)
	png := []byte("\x89PNG\r\n\x1a\nimage")
	blobs := []agentapi.Blob{{Name: "shot.png", MIME: "image/png", Data: png}}
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "look", Files: composerFiles[:1], Attachments: blobs}); err != nil {
		t.Fatal(err)
	}
	data, name := base64.StdEncoding.EncodeToString(png), "shot.png"
	want := append(wantFileAttachments()[:1], &rpc.AttachmentBlob{Data: &data, MIMEType: "image/png", DisplayName: &name})
	if got := h.fs.msgs[0].Attachments; !reflect.DeepEqual(got, want) {
		t.Fatalf("attachments = %+v", got)
	}

	sum := sha256.Sum256(png)
	digest := hex.EncodeToString(sum[:])
	live := userMessage("msg-1", rpc.UserMessageDeliveryIdle, "look")
	live.Attachments = h.fs.msgs[0].Attachments
	h.fs.onEvent(ev("u1", live))
	wantItem := []agentapi.Attachment{{Name: "shot.png", MIME: "image/png", Size: int64(len(png)), SHA256: digest}}
	if it := h.sink.last().Item; it == nil || !reflect.DeepEqual(it.Attachments, wantItem) {
		t.Fatalf("live item = %+v", it)
	}

	// The recorded event keeps a content address instead of the bytes.
	asset, size := "sha256:"+strings.ToUpper(digest), int64(len(png))
	recorded := userMessage("msg-1", rpc.UserMessageDeliveryIdle, "look")
	recorded.Attachments = []copilot.Attachment{&rpc.AttachmentFile{Path: "/work/src/a.go", DisplayName: "src/a.go"}, &rpc.AttachmentBlob{AssetID: &asset, ByteLength: &size, MIMEType: "image/png", DisplayName: &name}}
	h.fs.events = []copilot.SessionEvent{ev("u1", recorded)}
	history, err := h.conv.History(context.Background())
	if err != nil || len(history.Items) != 1 || !reflect.DeepEqual(history.Items[0].Attachments, wantItem) {
		t.Fatalf("history = %+v, %v", history.Items, err)
	}
}
