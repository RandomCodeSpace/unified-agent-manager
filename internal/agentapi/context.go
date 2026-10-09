package agentapi

import "context"

// ContextReader reads the current window of an already-open conversation.
// Nil means the runtime has not initialized that snapshot. These reads do not
// run a model, change the window, or resume a closed conversation.
type ContextReader interface {
	ContextInfo(context.Context) (*ContextInfo, error)
	ContextAttribution(context.Context) (*ContextAttribution, error)
}

// ContextInfo separates occupied tokens from the advertised prompt allowance
// and the effective input budget after the runtime's output reservation.
type ContextInfo struct {
	Model                string `json:"model"`
	TotalTokens          int64  `json:"total_tokens"`
	Limit                int64  `json:"limit"`
	PromptTokenLimit     int64  `json:"prompt_token_limit"`
	CompactionThreshold  int64  `json:"compaction_threshold"`
	BufferTokens         int64  `json:"buffer_tokens"`
	SystemTokens         int64  `json:"system_tokens"`
	ConversationTokens   int64  `json:"conversation_tokens"`
	ToolDefinitionTokens int64  `json:"tool_definition_tokens"`
	MCPToolsTokens       int64  `json:"mcp_tools_tokens"`
}

// ContextAttribution uses the runtime's native total. Capacity categories and
// parent/child source records overlap; neither is an additive occupied total.
type ContextAttribution struct {
	Model               string            `json:"model"`
	ModelSource         string            `json:"model_source"`
	TotalTokens         int64             `json:"total_tokens"`
	Limit               int64             `json:"limit"`
	PromptTokenLimit    int64             `json:"prompt_token_limit"`
	CompactionThreshold int64             `json:"compaction_threshold"`
	BufferTokens        int64             `json:"buffer_tokens"`
	Compactions         int64             `json:"compactions"`
	Categories          ContextCategories `json:"categories"`
	Entries             []ContextSource   `json:"entries"`
	Truncated           bool              `json:"truncated,omitempty"`
}

type ContextCategories struct {
	SystemPrompt       int64 `json:"system_prompt"`
	CustomInstructions int64 `json:"custom_instructions"`
	SystemTools        int64 `json:"system_tools"`
	MCPTools           int64 `json:"mcp_tools"`
	Messages           int64 `json:"messages"`
	FreeSpace          int64 `json:"free_space"`
	Buffer             int64 `json:"buffer"`
}

// ContextSource contains bounded display metadata, never source content or
// arbitrary provider attributes. ID and ParentID retain their exact identity.
type ContextSource struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	ParentID string `json:"parent_id,omitempty"`
	Tokens   int64  `json:"tokens"`
}
