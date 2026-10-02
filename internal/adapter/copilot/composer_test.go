package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
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

func TestWebSkillDirectoriesOnCreateAndResume(t *testing.T) {
	fc := &fakeClient{}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	skills := []string{"/state/skills"}
	for _, req := range []agentapi.OpenRequest{
		{SessionID: "s-1", Workdir: "/work", Events: &recSink{}, SkillDirectories: skills},
		{SessionID: "s-2", ConversationID: "s-1", Workdir: "/work", Events: &recSink{}, SkillDirectories: skills},
	} {
		if _, err := p.Open(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if got := fc.create[0].SkillDirectories; !reflect.DeepEqual(got, skills) {
		t.Fatalf("create skill directories = %v", got)
	}
	if got := fc.resume[0].SkillDirectories; !reflect.DeepEqual(got, skills) {
		t.Fatalf("resume skill directories = %v", got)
	}
}

// Every Task session, created or resumed, appends the ask_user and
// no-attribution rules to Copilot's system message and turns the CLI's
// co-author trailer off; a utility session keeps only its own message.
func TestWebTaskSystemMessageOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "s-1", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	want := copilot.SystemMessageConfig{Mode: "append", Content: taskSystem}
	if got := h.fc.create[0].SystemMessage; got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("create system message = %+v", got)
	}
	if got := h.fc.resume[0].SystemMessage; got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("resume system message = %+v", got)
	}
	if got := h.fc.create[0].CoauthorEnabled; got == nil || *got {
		t.Fatalf("create CoauthorEnabled = %v, want false", got)
	}
	if got := h.fc.resume[0].CoauthorEnabled; got == nil || *got {
		t.Fatalf("resume CoauthorEnabled = %v, want false", got)
	}
	h.fc.mu.Lock()
	h.fc.reply = func(context.Context, copilot.MessageOptions) (string, error) { return "ok", nil }
	h.fc.mu.Unlock()
	if _, err := h.p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Purpose: "title", System: "Write a title.", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := h.fc.create[1].SystemMessage; got == nil || got.Mode != "replace" || got.Content != "Write a title." {
		t.Fatalf("utility system message = %+v", got)
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
	if msg.Prompt != "see @src/a.go and @docs" || msg.DisplayPrompt != "" || msg.Mode != "" || !reflect.DeepEqual(msg.Attachments, wantFileAttachments()) {
		t.Fatalf("sent %+v", msg)
	}
	if err := h.conv.Steer(context.Background(), agentapi.Prompt{Text: "steer", Files: composerFiles}); err != nil ||
		h.fs.msgs[1].Mode != string(rpc.SendModeImmediate) || !reflect.DeepEqual(h.fs.msgs[1].Attachments, wantFileAttachments()) {
		t.Fatalf("steer %v sent %+v", err, h.fs.msgs[1])
	}
	if err := h.conv.Steer(context.Background(), agentapi.Prompt{Text: "text only"}); err != nil || h.fs.msgs[2].Attachments != nil {
		t.Fatalf("text-only steer %v sent attachments %+v", err, h.fs.msgs[2].Attachments)
	}
}

// A message of only an upload or a file goes with an empty prompt: nothing is
// added, and a steer the turn did not use is reported without an empty quote.
func TestWebAttachmentOnlyMessageSendsAnEmptyPrompt(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	data := "png"
	name := "shot.png"
	if err := h.conv.Send(ctx, agentapi.Prompt{Attachments: []agentapi.Blob{{Name: name, MIME: "image/png", Data: []byte(data)}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.conv.Steer(ctx, agentapi.Prompt{Files: composerFiles[:1]}); err != nil { // msg-2
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(data))
	want := [][]copilot.Attachment{{&rpc.AttachmentBlob{Data: &encoded, MIMEType: "image/png", DisplayName: &name}}, wantFileAttachments()[:1]}
	if len(h.fs.msgs) != len(want) {
		t.Fatalf("sent %d messages, want %d: %+v", len(h.fs.msgs), len(want), h.fs.msgs)
	}
	for i, msg := range h.fs.msgs {
		if msg.Prompt != "" || msg.DisplayPrompt != "" || !reflect.DeepEqual(msg.Attachments, want[i]) {
			t.Fatalf("message %d sent %+v", i, msg)
		}
	}
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if got := notices(h.sink.all()); len(got) != 1 || got[0] != "steer-undelivered:msg-2|Steer not delivered: the turn was stopped" {
		t.Fatalf("notices = %q", got)
	}
}

func TestWebCommandsListSupportedAndDisabledNativeCommands(t *testing.T) {
	h := openWeb(t)
	h.fs.commands = []rpc.SlashCommandInfo{
		{Name: "review", Kind: rpc.SlashCommandKindBuiltin, Description: "Review changes", Input: &rpc.SlashCommandInput{Hint: "focus"}},
		{Name: "init", Kind: rpc.SlashCommandKindBuiltin},
		{Name: "model", Kind: rpc.SlashCommandKindBuiltin},
		{Name: "compact", Kind: rpc.SlashCommandKindBuiltin},
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
		msg.Mode != "" || !reflect.DeepEqual(msg.Attachments, wantFileAttachments()) {
		t.Fatalf("invoked %q, sent %+v", h.fs.invoked, msg)
	}
	if last := h.sink.last(); last.Kind != agentapi.EventTurn || last.Turn.State != agentapi.TurnWorking {
		t.Fatalf("last event = %+v", last)
	}
	if err := h.conv.RunCommand(context.Background(), "review", agentapi.Prompt{}); !errors.Is(err, agentapi.ErrBusy) || len(h.fs.invoked) != 1 {
		t.Fatalf("prompt command during a turn: %v, invoked %q", err, h.fs.invoked)
	}
	h.fs.onEvent(ev("idle", &rpc.AssistantIdleData{}))
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

// A PDF with a named copy goes as that file, and the message shows it as the
// upload it is, hashed from disk; other files stay references.
func TestWebSendCarriesANamedPDFAsAFile(t *testing.T) {
	h := openWeb(t)
	config := t.TempDir()
	t.Setenv("UAM_CONFIG_DIR", config)
	dir := filepath.Join(config, agentapi.UploadsDir, "task-1", "up-1.d")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pdf, path := []byte("%PDF-1.4\n%%EOF\n"), filepath.Join(dir, "Forms.pdf")
	if err := os.WriteFile(path, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	blobs := []agentapi.Blob{{Name: "Forms.pdf", MIME: "application/pdf", Data: pdf, Path: path}}
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "read it", Files: composerFiles[:1], Attachments: blobs}); err != nil {
		t.Fatal(err)
	}
	want := append(wantFileAttachments()[:1], &rpc.AttachmentFile{Path: path, DisplayName: "Forms.pdf"})
	if got := h.fs.msgs[0].Attachments; !reflect.DeepEqual(got, want) {
		t.Fatalf("attachments = %+v", got)
	}

	// Sent natively, it is a plain upload; not listed as native, or listed
	// but fallen back to its path, the agent reads it with its tools.
	sum := sha256.Sum256(pdf)
	wantItem := []agentapi.Attachment{{Name: "Forms.pdf", MIME: "application/pdf", Size: int64(len(pdf)), SHA256: hex.EncodeToString(sum[:])}}
	live := userMessage("msg-1", rpc.UserMessageDeliveryIdle, "read it")
	live.Attachments = h.fs.msgs[0].Attachments
	for _, c := range []struct {
		native, fallback []string
		notNative        bool
	}{{[]string{"application/pdf"}, nil, false}, {nil, nil, true}, {[]string{"application/pdf"}, []string{path}, true}} {
		live.SupportedNativeDocumentMIMETypes, live.NativeDocumentPathFallbackPaths = c.native, c.fallback
		h.fs.onEvent(ev("u1", live))
		wantItem[0].NotNative = c.notNative
		if it := h.sink.last().Item; it == nil || !reflect.DeepEqual(it.Attachments, wantItem) {
			t.Fatalf("native %v, fallback %v: live item = %+v", c.native, c.fallback, it)
		}
	}

	// Removed from disk, it keeps its name and type.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	h.fs.events = []copilot.SessionEvent{ev("u1", live)}
	history, err := h.conv.History(context.Background())
	if err != nil || len(history.Items) != 1 || !reflect.DeepEqual(history.Items[0].Attachments, []agentapi.Attachment{{Name: "Forms.pdf", MIME: "application/pdf", NotNative: true}}) {
		t.Fatalf("history = %+v, %v", history.Items, err)
	}
}

// Only a named copy inside the web service's upload store is an upload; a
// file laid out the same way anywhere else stays a reference.
func TestUploadFileStaysInsideTheUploadStore(t *testing.T) {
	config, elsewhere := t.TempDir(), t.TempDir()
	t.Setenv("UAM_CONFIG_DIR", config)
	pdf := []byte("%PDF-1.4\n%%EOF\n")
	write := func(parts ...string) string {
		t.Helper()
		path := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pdf, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stored := write(config, agentapi.UploadsDir, "task-1", "up-1.d", "Forms.pdf")
	sum := sha256.Sum256(pdf)
	att, ok := uploadFile(&rpc.AttachmentFile{Path: stored, DisplayName: "Forms.pdf"})
	if want := (agentapi.Attachment{Name: "Forms.pdf", MIME: "application/pdf", Size: int64(len(pdf)), SHA256: hex.EncodeToString(sum[:])}); !ok || att != want {
		t.Fatalf("stored upload = %+v, %v", att, ok)
	}

	outside := write(elsewhere, agentapi.UploadsDir, "task-1", "up-1.d", "Forms.pdf")
	for _, path := range []string{
		outside,
		filepath.Join(config, agentapi.UploadsDir, "task-1", "..", "..", "..", filepath.Base(elsewhere), agentapi.UploadsDir, "task-1", "up-1.d", "Forms.pdf"),
		filepath.Join(config, agentapi.UploadsDir, "task-1", "up-1", "Forms.pdf"),
		filepath.Join(agentapi.UploadsDir, "task-1", "up-1.d", "Forms.pdf"),
	} {
		if att, ok := uploadFile(&rpc.AttachmentFile{Path: path, DisplayName: "Forms.pdf"}); ok {
			t.Fatalf("%s is an upload: %+v", path, att)
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

// A compaction threshold reaches the CLI on create and on resume; without
// one the CLI keeps its defaults.
func TestWebCompactionThresholdOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	if h.fc.create[0].InfiniteSessions != nil {
		t.Fatalf("default create = %+v", h.fc.create[0].InfiniteSessions)
	}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", Workdir: "/work", Events: &recSink{}, CompactionThreshold: 0.6}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-3", ConversationID: "s-1", Workdir: "/work", Events: &recSink{}, CompactionThreshold: 0.6}); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*copilot.InfiniteSessionConfig{"create": h.fc.create[1].InfiniteSessions, "resume": h.fc.resume[0].InfiniteSessions} {
		if got == nil || got.Enabled != nil || got.BackgroundCompactionThreshold == nil || *got.BackgroundCompactionThreshold != 0.6 || got.BufferExhaustionThreshold != nil {
			t.Fatalf("%s infinite sessions = %+v", name, got)
		}
	}
}
