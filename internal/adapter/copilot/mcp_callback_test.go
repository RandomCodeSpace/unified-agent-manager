package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type callbackSession struct {
	*mcpSwitchSession
	loginResult     *rpc.MCPOauthLoginResult
	loginError      error
	loginRequest    *rpc.MCPOauthLoginRequest
	completeRequest *rpc.MCPOauthCompleteRequest
	completeError   error
}

func (s *callbackSession) MCPLogin(_ context.Context, req *rpc.MCPOauthLoginRequest) (*rpc.MCPOauthLoginResult, error) {
	s.loginRequest = req
	return s.loginResult, s.loginError
}
func (s *callbackSession) MCPComplete(_ context.Context, req *rpc.MCPOauthCompleteRequest) (*rpc.SessionMCPOauthCompleteResult, error) {
	s.completeRequest = req
	return &rpc.SessionMCPOauthCompleteResult{}, s.completeError
}

func TestMCPCallbackDelegatesLoginAndCompletionToSDK(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	id, address, redirect := "opaque-sdk-state", "https://authority.example/authorize?state=opaque-sdk-state", "https://uam.example/api/mcp/oauth/callback"
	s := &callbackSession{mcpSwitchSession: &mcpSwitchSession{fakeSession: h.fs}, loginResult: &rpc.MCPOauthLoginResult{AuthorizationID: &id, AuthorizationURL: &address}}
	c.sess = s
	started, err := c.MCPSignInCallback(context.Background(), "notes", true, redirect)
	if err != nil || started.AuthorizationID != id || started.URL != address || s.loginRequest == nil || s.loginRequest.ServerName != "notes" || deref(s.loginRequest.RedirectURI) != redirect || s.loginRequest.ForceReauth == nil || !*s.loginRequest.ForceReauth {
		t.Fatalf("SDK callback login was not forwarded: error=%v", err)
	}
	callback := redirect + "?code=private-code&state=" + id
	if err := c.CompleteMCPSignIn(context.Background(), id, callback); err != nil {
		t.Fatal(err)
	}
	if s.completeRequest == nil || s.completeRequest.AuthorizationID != id || s.completeRequest.CallbackURL != callback {
		t.Fatal("SDK completion was not forwarded unchanged")
	}
	if h.fs.disconnected || len(h.fc.resume) != 0 {
		t.Fatal("callback authentication replaced the conversation")
	}
}

func TestMCPCallbackUnsupportedIsEffectFreeAndExact(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{"method absent", &copilot.RPCError{Code: -32601, Message: "Method not found"}, true},
		{"ordinary failure", &copilot.RPCError{Code: -32000, Message: "Method not found in configuration; code=private-code&state=private-state"}, false},
		{"lookalike text", errors.New("Method not found code=private-code&state=private-state"), false},
		{"timeout", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openWeb(t)
			c := h.conv.(*conversation)
			s := &callbackSession{mcpSwitchSession: &mcpSwitchSession{fakeSession: h.fs}, loginError: tc.err}
			c.sess = s
			_, err := c.MCPSignInCallback(context.Background(), "notes", false, "https://uam.example/api/mcp/oauth/callback")
			if err == nil || errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported || strings.Contains(err.Error(), "private-code") || strings.Contains(err.Error(), "private-state") || s.completeRequest != nil {
				t.Fatalf("login classification/sanitization=%v", err)
			}
		})
	}
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.sess = &mcpSwitchSession{fakeSession: h.fs}
	if _, err := c.MCPSignInCallback(context.Background(), "notes", false, "https://uam.example/api/mcp/oauth/callback"); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent complete capability=%v", err)
	}
}

func TestMCPCallbackCompletionErrorsNeverExposeQueries(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	s := &callbackSession{mcpSwitchSession: &mcpSwitchSession{fakeSession: h.fs}, completeError: errors.New("exchange failed for code=private-code&state=private-state")}
	c.sess = s
	if err := c.CompleteMCPSignIn(context.Background(), "private-state", "https://uam.example/api/mcp/oauth/callback?code=private-code&state=private-state"); err == nil || strings.Contains(err.Error(), "private-code") || strings.Contains(err.Error(), "private-state") {
		t.Fatalf("completion leaked RPC diagnostics: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.completeRequest = nil
	if err := c.CompleteMCPSignIn(context.Background(), "private-state", "https://uam.example/api/mcp/oauth/callback"); !errors.Is(err, agentapi.ErrClosed) || s.completeRequest != nil {
		t.Fatalf("closed completion=%v", err)
	}
}

func TestMCPCallbackRequiresUsableSDKResult(t *testing.T) {
	address := "https://authority.example/authorize"
	for _, tc := range []struct {
		name    string
		result  *rpc.MCPOauthLoginResult
		success bool
	}{
		{"empty result", nil, false},
		{"missing state", &rpc.MCPOauthLoginResult{AuthorizationURL: &address}, false},
		{"kept credentials", &rpc.MCPOauthLoginResult{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openWeb(t)
			c := h.conv.(*conversation)
			c.sess = &callbackSession{mcpSwitchSession: &mcpSwitchSession{fakeSession: h.fs}, loginResult: tc.result}
			_, err := c.MCPSignInCallback(context.Background(), "notes", false, "https://uam.example/api/mcp/oauth/callback")
			if (err == nil) != tc.success || errors.Is(err, agentapi.ErrUnsupported) {
				t.Fatalf("result error=%v success=%t", err, tc.success)
			}
		})
	}
}
