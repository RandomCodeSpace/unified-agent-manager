package copilot

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const (
	declarationToolName   = "uam_show_file"
	maxDeclarationPath    = 4096
	maxDeclarationTitle   = 128
	maxDeclarationType    = 32
	maxDeclarationJSON    = 32 << 10
	maxDeclarationCalls   = 256
	maxDeclarationSuccess = 128
	maxDeclarationCatalog = 256
)

type declarationInput struct {
	Path     string `json:"path" jsonschema:"Existing file to show to the owner; this does not open or grant access"`
	Title    string `json:"title,omitempty" jsonschema:"Optional display title"`
	TypeHint string `json:"type_hint,omitempty" jsonschema:"Optional display hint; does not control file handling"`
}

type declarationCall struct {
	args   string
	done   chan struct{}
	result copilot.ToolResult
	err    error
}

// declarationTool owns only bounded call idempotence and the SDK registration
// observation. The journal holds successful display metadata for history.
type declarationTool struct {
	mu          sync.Mutex
	validate    func(context.Context, string) (string, error)
	stopCtx     context.Context
	stopCancel  context.CancelFunc
	session     string
	ready       bool
	closed      bool
	invalidated bool
	calls       map[string]*declarationCall
	successes   int
}

func newDeclarationTool(validate func(context.Context, string) (string, error)) *declarationTool {
	if validate == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &declarationTool{validate: validate, stopCtx: ctx, stopCancel: cancel, calls: make(map[string]*declarationCall)}
}

func declarationText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	return !containsControl(s)
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func (d *declarationTool) tool() copilot.Tool {
	tool := copilot.DefineTool(declarationToolName,
		"Declare one existing file for the owner's display. Returns metadata only; never reads file bytes or grants access.", d.declare)
	tool.SkipPermission, tool.Defer = true, copilot.ToolDeferNever
	tool.IsTerminal, tool.OverridesBuiltInTool = false, false
	tool.Parameters["additionalProperties"] = false
	properties := tool.Parameters["properties"].(map[string]any)
	for name, limit := range map[string]int{"path": maxDeclarationPath, "title": maxDeclarationTitle, "type_hint": maxDeclarationType} {
		properties[name].(map[string]any)["maxLength"] = limit
	}
	properties["path"].(map[string]any)["minLength"] = 1
	typed := tool.Handler
	tool.Handler = func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
		ctx := inv.TraceContext
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(d.stopCtx, cancel)
		defer func() { stop(); cancel() }()
		inv.TraceContext = ctx
		args, ok := inv.Arguments.(map[string]any)
		if !ok || len(args) == 0 || len(args) > 3 {
			return copilot.ToolResult{}, errors.New("use path and optional title/type_hint strings")
		}
		for name, value := range args {
			limit := map[string]int{"path": maxDeclarationPath, "title": maxDeclarationTitle, "type_hint": maxDeclarationType}[name]
			s, isString := value.(string)
			if limit == 0 || !isString || !declarationText(s, limit) {
				return copilot.ToolResult{}, errors.New("declaration fields must be bounded plain strings")
			}
		}
		if path, ok := args["path"].(string); !ok || path == "" {
			return copilot.ToolResult{}, errors.New("path is required")
		}
		encoded, err := json.Marshal(args)
		if err != nil || len(encoded) > maxDeclarationJSON {
			return copilot.ToolResult{}, errors.New("declaration arguments are too large")
		}
		if inv.ToolCallID == "" || len(inv.ToolCallID) > 256 || inv.SessionID == "" {
			return copilot.ToolResult{}, errors.New("declaration call identity is unavailable")
		}
		key := inv.SessionID + "\x00" + inv.ToolCallID
		d.mu.Lock()
		if !d.ready || d.closed || inv.SessionID != d.session {
			d.mu.Unlock()
			return copilot.ToolResult{}, errors.New("file declaration is unavailable; continue without a declaration")
		}
		if previous := d.calls[key]; previous != nil {
			if previous.args != string(encoded) {
				d.mu.Unlock()
				return copilot.ToolResult{}, errors.New("the declaration call was repeated with different arguments")
			}
			d.mu.Unlock()
			select {
			case <-previous.done:
				return previous.result, previous.err
			case <-ctx.Done():
				return copilot.ToolResult{}, ctx.Err()
			}
		}
		if len(d.calls) >= maxDeclarationCalls || d.successes >= maxDeclarationSuccess {
			d.mu.Unlock()
			return copilot.ToolResult{}, errors.New("file declaration limit reached for this session")
		}
		call := &declarationCall{args: string(encoded), done: make(chan struct{})}
		d.calls[key] = call
		d.mu.Unlock()
		result, err := typed(inv)
		d.mu.Lock()
		if d.closed || !d.ready {
			result, err = copilot.ToolResult{}, errors.New("file declaration is unavailable; continue without a declaration")
		} else if err == nil {
			if d.successes >= maxDeclarationSuccess {
				result, err = copilot.ToolResult{}, errors.New("file declaration limit reached for this session")
			} else {
				d.successes++
			}
		}
		call.result, call.err = result, err
		close(call.done)
		d.mu.Unlock()
		return result, err
	}
	return tool
}

func (d *declarationTool) declare(in declarationInput, inv copilot.ToolInvocation) (copilot.ToolResult, error) {
	ctx := inv.TraceContext
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return copilot.ToolResult{}, err
	}
	path, err := d.validate(ctx, in.Path)
	if err != nil {
		return copilot.ToolResult{}, errors.New("file is unavailable or outside the allowed workdir and temporary-file scope; correct the path")
	}
	if !filepath.IsAbs(path) || !declarationText(path, maxDeclarationPath) {
		return copilot.ToolResult{}, errors.New("normalized file path is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return copilot.ToolResult{}, err
	}
	metadata := agentapi.FileDeclaration{ArtifactID: rand.Text(), Path: path, Title: in.Title, TypeHint: in.TypeHint}
	data, err := json.Marshal(metadata)
	if err != nil || len(data) > maxDeclarationJSON {
		return copilot.ToolResult{}, errors.New("declaration metadata is too large")
	}
	result := string(data)
	return copilot.ToolResult{ResultType: "success", TextResultForLLM: result, SessionLog: result}, nil
}

// A completed result is still untrusted display metadata. The owning file
// route revalidates the object on every actual open.
func parseDeclarationResult(raw string) *agentapi.FileDeclaration {
	if raw == "" || len(raw) > maxDeclarationJSON {
		return nil
	}
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	var value agentapi.FileDeclaration
	if dec.Decode(&value) != nil || dec.Decode(new(any)) != io.EOF {
		return nil
	}
	if !declarationText(value.ArtifactID, 64) || value.ArtifactID == "" ||
		!filepath.IsAbs(value.Path) || !declarationText(value.Path, maxDeclarationPath) ||
		!declarationText(value.Title, maxDeclarationTitle) || !declarationText(value.TypeHint, maxDeclarationType) {
		return nil
	}
	return &value
}

func declarationDefinition(tool copilot.Tool) rpc.ProtocolExternalToolDefinition {
	definition := rpc.ProtocolExternalToolDefinition{
		Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, Metadata: tool.Metadata,
		OverridesBuiltInTool: &tool.OverridesBuiltInTool, SkipPermission: &tool.SkipPermission, IsTerminal: &tool.IsTerminal,
	}
	if tool.Defer != "" {
		mode := rpc.ProtocolExternalToolDefer(tool.Defer)
		definition.Defer = &mode
	}
	return definition
}

type declarationCatalogName struct {
	name, server, tool, namespace    string
	serverSet, toolSet, namespaceSet bool
}

func declarationCatalogNames(tools []rpc.CurrentToolMetadata, excluding string) map[declarationCatalogName]int {
	names := make(map[declarationCatalogName]int, len(tools))
	for _, tool := range tools {
		if tool.Name == excluding {
			continue
		}
		name := declarationCatalogName{name: tool.Name}
		if tool.MCPServerName != nil {
			name.server, name.serverSet = *tool.MCPServerName, true
		}
		if tool.MCPToolName != nil {
			name.tool, name.toolSet = *tool.MCPToolName, true
		}
		if tool.NamespacedName != nil {
			name.namespace, name.namespaceSet = *tool.NamespacedName, true
		}
		names[name]++
	}
	return names
}

func (a sdkSessionAdapter) ToolCatalog(ctx context.Context) ([]rpc.CurrentToolMetadata, error) {
	if _, err := a.s.RPC.Tools.InitializeAndValidate(ctx); err != nil {
		return nil, err
	}
	metadata, err := a.s.RPC.Tools.GetCurrentMetadata(ctx)
	if err != nil || metadata == nil {
		return nil, err
	}
	return metadata.Tools, nil
}

func (a sdkSessionAdapter) SetTools(ctx context.Context, tools []rpc.ProtocolExternalToolDefinition) error {
	_, err := a.s.RPC.Tools.Set(ctx, &rpc.ToolsSetRequest{Tools: tools})
	return err
}

func (d *declarationTool) catalog(ctx context.Context, session sdkSession, original copilot.Tool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := session.SetTools(ctx, []rpc.ProtocolExternalToolDefinition{}); err != nil {
		return fmt.Errorf("clear declaration tools: %w", err)
	}
	before, err := session.ToolCatalog(ctx)
	if err != nil || before == nil || len(before) > maxDeclarationCatalog {
		return errors.New("unshadowed declaration tool catalog is unavailable")
	}
	for _, tool := range before {
		if tool.Name == declarationToolName {
			return errors.New("uam_show_file already exists in the unshadowed tool catalog")
		}
	}
	d.mu.Lock()
	invalidated := d.closed || d.invalidated
	d.mu.Unlock()
	if invalidated || ctx.Err() != nil {
		return errors.New("declaration catalog observation was invalidated")
	}
	if err := session.SetTools(ctx, []rpc.ProtocolExternalToolDefinition{declarationDefinition(original)}); err != nil {
		return fmt.Errorf("restore declaration tool: %w", err)
	}
	after, err := session.ToolCatalog(ctx)
	if err != nil || after == nil || len(after) > maxDeclarationCatalog {
		return errors.New("restored declaration tool catalog is unavailable")
	}
	matches := 0
	for _, tool := range after {
		if tool.Name != declarationToolName {
			continue
		}
		matches++
		if tool.MCPServerName != nil || tool.MCPToolName != nil || tool.NamespacedName != nil {
			return errors.New("uam_show_file has ambiguous tool origin")
		}
	}
	if matches != 1 || !maps.Equal(declarationCatalogNames(before, declarationToolName), declarationCatalogNames(after, declarationToolName)) {
		return errors.New("restored declaration catalog changed or is ambiguous")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.invalidated || ctx.Err() != nil {
		return errors.New("declaration catalog observation was invalidated")
	}
	d.session, d.ready = session.ID(), true
	return nil
}

func (d *declarationTool) observe(ev copilot.SessionEvent) {
	if _, changed := ev.Data.(*rpc.MCPToolsListChangedData); !changed {
		return
	}
	d.mu.Lock()
	d.ready, d.invalidated = false, true
	d.mu.Unlock()
}

func (d *declarationTool) stop() {
	d.mu.Lock()
	d.closed, d.ready = true, false
	d.mu.Unlock()
	d.stopCancel()
}
