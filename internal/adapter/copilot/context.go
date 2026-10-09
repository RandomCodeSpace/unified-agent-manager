package copilot

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const (
	maxContextSources = 200
	maxContextLabel   = 512
	maxContextID      = 1024
)

type contextSession interface {
	ContextInfo(context.Context, *rpc.MetadataContextInfoRequest) (*rpc.MetadataContextInfoResult, error)
	ContextAttribution(context.Context) (*rpc.MetadataContextAttributionResult, error)
}

func (a sdkSessionAdapter) ContextInfo(ctx context.Context, req *rpc.MetadataContextInfoRequest) (*rpc.MetadataContextInfoResult, error) {
	return a.s.RPC.Metadata.ContextInfo(ctx, req)
}

func (a sdkSessionAdapter) ContextAttribution(ctx context.Context) (*rpc.MetadataContextAttributionResult, error) {
	return a.s.RPC.Metadata.GetContextAttribution(ctx)
}

func (c *conversation) contextReader() (contextSession, error) {
	if c.isClosed() {
		return nil, agentapi.ErrClosed
	}
	reader, ok := c.sess.(contextSession)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	return reader, nil
}

func contextReadError(err error) error {
	var rpcErr *copilot.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
		return agentapi.ErrUnsupported
	}
	return err
}

func (c *conversation) ContextInfo(ctx context.Context) (*agentapi.ContextInfo, error) {
	reader, err := c.contextReader()
	if err != nil {
		return nil, err
	}
	// Zero limits and no model override resolve the actual session selection,
	// context tier and requested output allowance, including Auto's resolution.
	res, err := reader.ContextInfo(ctx, &rpc.MetadataContextInfoRequest{})
	if err != nil {
		return nil, contextReadError(err)
	}
	if res == nil {
		return nil, errors.New("copilot returned no context result")
	}
	if res.ContextInfo == nil {
		return nil, nil
	}
	i := res.ContextInfo
	if negativeContext(i.TotalTokens, i.Limit, i.PromptTokenLimit, i.CompactionThreshold, i.BufferTokens, i.SystemTokens, i.ConversationTokens, i.ToolDefinitionsTokens, i.MCPToolsTokens) {
		return nil, errors.New("copilot returned invalid context counts")
	}
	return &agentapi.ContextInfo{Model: contextLabel(i.ModelName), TotalTokens: i.TotalTokens, Limit: i.Limit, PromptTokenLimit: i.PromptTokenLimit, CompactionThreshold: i.CompactionThreshold, BufferTokens: i.BufferTokens, SystemTokens: i.SystemTokens, ConversationTokens: i.ConversationTokens, ToolDefinitionTokens: i.ToolDefinitionsTokens, MCPToolsTokens: i.MCPToolsTokens}, nil
}

func (c *conversation) ContextAttribution(ctx context.Context) (*agentapi.ContextAttribution, error) {
	reader, err := c.contextReader()
	if err != nil {
		return nil, err
	}
	res, err := reader.ContextAttribution(ctx)
	if err != nil {
		return nil, contextReadError(err)
	}
	if res == nil {
		return nil, errors.New("copilot returned no context attribution result")
	}
	if res.ContextAttribution == nil {
		return nil, nil
	}
	i, b := res.ContextAttribution, res.ContextAttribution.Categories
	if negativeContext(i.TotalTokens, i.Limit, i.PromptTokenLimit, i.CompactionThreshold, i.BufferTokens, i.Compactions.Count, b.SystemPrompt, b.CustomInstructions, b.SystemTools, b.MCPTools, b.Messages, b.FreeSpace, b.Buffer) {
		return nil, errors.New("copilot returned invalid context attribution counts")
	}
	out := &agentapi.ContextAttribution{Model: contextLabel(i.ModelID), ModelSource: contextLabel(i.ModelSource), TotalTokens: i.TotalTokens, Limit: i.Limit, PromptTokenLimit: i.PromptTokenLimit, CompactionThreshold: i.CompactionThreshold, BufferTokens: i.BufferTokens, Compactions: i.Compactions.Count, Categories: agentapi.ContextCategories{SystemPrompt: b.SystemPrompt, CustomInstructions: b.CustomInstructions, SystemTools: b.SystemTools, MCPTools: b.MCPTools, Messages: b.Messages, FreeSpace: b.FreeSpace, Buffer: b.Buffer}, Entries: []agentapi.ContextSource{}}
	seen := map[string]bool{}
	for _, entry := range i.Entries {
		if len(out.Entries) == maxContextSources {
			out.Truncated = true
			break
		}
		parent := deref(entry.ParentID)
		if !contextIdentity(entry.ID) || parent != "" && !contextIdentity(parent) || entry.Tokens < 0 || seen[entry.ID] {
			out.Truncated = true
			continue
		}
		seen[entry.ID] = true
		out.Entries = append(out.Entries, agentapi.ContextSource{ID: strings.Clone(entry.ID), Kind: contextLabel(entry.Kind), Label: contextLabel(entry.Label), ParentID: strings.Clone(parent), Tokens: entry.Tokens})
	}
	return out, nil
}

func contextLabel(s string) string {
	return strings.Clone(clip(strings.TrimSpace(displaytext.Sanitize(s)), maxContextLabel))
}

func contextIdentity(s string) bool {
	return s != "" && len(s) <= maxContextID && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func negativeContext(values ...int64) bool {
	for _, value := range values {
		if value < 0 {
			return true
		}
	}
	return false
}
