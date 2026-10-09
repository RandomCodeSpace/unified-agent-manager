package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type fakeContextSession struct {
	*fakeSession
	info                        *rpc.MetadataContextInfoResult
	attribution                 *rpc.MetadataContextAttributionResult
	err                         error
	infoCalls, attributionCalls int
	request                     *rpc.MetadataContextInfoRequest
}

func (f *fakeContextSession) ContextInfo(_ context.Context, request *rpc.MetadataContextInfoRequest) (*rpc.MetadataContextInfoResult, error) {
	f.infoCalls++
	f.request = request
	return f.info, f.err
}

func (f *fakeContextSession) ContextAttribution(context.Context) (*rpc.MetadataContextAttributionResult, error) {
	f.attributionCalls++
	return f.attribution, f.err
}

func TestContextReaderPreservesNativeCountsAndSessionSelection(t *testing.T) {
	h := openWeb(t)
	reader := &fakeContextSession{fakeSession: h.fs, info: &rpc.MetadataContextInfoResult{ContextInfo: &rpc.SessionContextInfo{
		ModelName: "resolved-model", TotalTokens: 170, Limit: 2000, PromptTokenLimit: 1600, CompactionThreshold: 1280, BufferTokens: 400,
		SystemTokens: 20, ConversationTokens: 100, ToolDefinitionsTokens: 50, MCPToolsTokens: 30,
	}}}
	c := h.conv.(*conversation)
	c.sess = reader
	if !h.p.Capabilities().ContextBreakdown {
		t.Fatal("native context capability missing")
	}
	got, err := c.ContextInfo(context.Background())
	if err != nil || got == nil || got.TotalTokens != 170 || got.Limit != 2000 || got.PromptTokenLimit != 1600 || got.BufferTokens != 400 || got.MCPToolsTokens != 30 || got.Model != "resolved-model" {
		t.Fatalf("context = %+v, %v", got, err)
	}
	if reader.request.SelectedModel != nil || reader.request.PromptTokenLimit != 0 || reader.request.OutputTokenLimit != 0 {
		t.Fatalf("overrode live/Auto selection: %+v", reader.request)
	}
	if len(h.fs.sent) != 0 || len(h.fc.resume) != 0 || len(h.sink.all()) != 0 {
		t.Fatal("metadata read ran a turn, resumed or emitted transcript data")
	}
	reader.info.ContextInfo.SystemTokens = -1
	if _, err := c.ContextInfo(context.Background()); err == nil {
		t.Fatal("negative native counts became a valid snapshot")
	}
}

func TestContextAttributionBoundsMetadataAndKeepsOverlappingIdentity(t *testing.T) {
	h := openWeb(t)
	parent := "plugin:tools"
	attr := &rpc.SessionContextAttribution{ModelID: "resolved-model", ModelSource: "autoResolved", TotalTokens: 75, Limit: 200, PromptTokenLimit: 160, BufferTokens: 40,
		Categories: rpc.SessionContextAttributionCategories{SystemPrompt: 5, CustomInstructions: 5, SystemTools: 10, MCPTools: 5, Messages: 50, FreeSpace: 85, Buffer: 40},
		Entries: []rpc.SessionContextAttributionEntriesItem{
			{ID: parent, Kind: "plugin", Label: "Tools", Tokens: 30, Attributes: map[string]string{"content": "body must not leave SDK"}},
			{ID: "future:child", Kind: "future-kind", Label: "\x1b[31m" + strings.Repeat("x", 600), Tokens: 20, ParentID: &parent},
			{ID: parent, Kind: "duplicate", Tokens: 90},
			{ID: "bad\nidentity", Tokens: 1},
		}}
	for i := range maxContextSources {
		attr.Entries = append(attr.Entries, rpc.SessionContextAttributionEntriesItem{ID: fmt.Sprintf("source:%d", i), Kind: "skill", Label: "row", Tokens: 1})
	}
	reader := &fakeContextSession{fakeSession: h.fs, attribution: &rpc.MetadataContextAttributionResult{ContextAttribution: attr}}
	c := h.conv.(*conversation)
	c.sess = reader
	got, err := c.ContextAttribution(context.Background())
	if err != nil || got == nil || got.TotalTokens != 75 || got.Categories.FreeSpace != 85 || got.Categories.Buffer != 40 || !got.Truncated || len(got.Entries) != maxContextSources {
		t.Fatalf("attribution = %+v, %v", got, err)
	}
	child := got.Entries[1]
	if child.ID != "future:child" || child.ParentID != parent || child.Kind != "future-kind" || child.Tokens != 20 || len(child.Label) > maxContextLabel || strings.Contains(child.Label, "\x1b") {
		t.Fatalf("source identity/presentation = %+v", child)
	}
	wire, _ := json.Marshal(got)
	if strings.Contains(string(wire), "attributes") || strings.Contains(string(wire), "body must not leave SDK") {
		t.Fatal("source body/arbitrary attributes leaked")
	}
	attr.TotalTokens = -1
	if _, err := c.ContextAttribution(context.Background()); err == nil {
		t.Fatal("negative attribution total accepted")
	}
}

func TestContextReaderUninitializedUnsupportedAndClosedAreDistinct(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	if _, err := c.ContextInfo(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent optional capability = %v", err)
	}
	reader := &fakeContextSession{fakeSession: h.fs, info: &rpc.MetadataContextInfoResult{}, attribution: &rpc.MetadataContextAttributionResult{}}
	c.sess = reader
	if info, err := c.ContextInfo(context.Background()); info != nil || err != nil {
		t.Fatalf("uninitialized info = %+v, %v", info, err)
	}
	if attr, err := c.ContextAttribution(context.Background()); attr != nil || err != nil {
		t.Fatalf("uninitialized attribution = %+v, %v", attr, err)
	}
	reader.err = &copilot.RPCError{Code: -32601, Message: "method missing"}
	if _, err := c.ContextInfo(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("typed unsupported = %v", err)
	}
	reader.err = errors.New("method not found while loading one source")
	if _, err := c.ContextAttribution(context.Background()); errors.Is(err, agentapi.ErrUnsupported) || err == nil {
		t.Fatalf("partial failure disguised as unsupported: %v", err)
	}
	reader.err = nil
	reader.info = nil
	if _, err := c.ContextInfo(context.Background()); err == nil {
		t.Fatal("missing entire RPC result disguised as uninitialized")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := reader.infoCalls + reader.attributionCalls
	if _, err := c.ContextInfo(context.Background()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed info = %v", err)
	}
	if _, err := c.ContextAttribution(context.Background()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed attribution = %v", err)
	}
	if before != reader.infoCalls+reader.attributionCalls {
		t.Fatal("closed reader reached native SDK")
	}
}
