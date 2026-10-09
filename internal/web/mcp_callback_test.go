package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

type callbackConversation struct {
	*statusConversation
	starts, legacy, completes             int
	redirect, authorization, completedURL string
	startErr, completeErr                 error
	start                                 func()
	complete                              func()
}

func (c *callbackConversation) MCPSignInCallback(_ context.Context, _ string, _ bool, redirect string) (agentapi.MCPCallbackSignIn, error) {
	c.starts++
	c.redirect = redirect
	if c.start != nil {
		c.start()
	}
	if c.startErr != nil {
		return agentapi.MCPCallbackSignIn{}, c.startErr
	}
	q := url.Values{"state": {c.authorization}, "redirect_uri": {redirect}}
	return agentapi.MCPCallbackSignIn{AuthorizationID: c.authorization, URL: "https://authorize.example/auth?" + q.Encode()}, nil
}
func (c *callbackConversation) CompleteMCPSignIn(_ context.Context, authorization, callback string) error {
	c.completes++
	if c.complete != nil {
		c.complete()
	}
	if authorization != c.authorization {
		return errors.New("wrong SDK authorization ID")
	}
	c.completedURL = callback
	return c.completeErr
}
func (c *callbackConversation) MCPSignIn(context.Context, string, bool) (string, error) {
	c.legacy++
	return "https://authorize.example/auth?state=legacy&redirect_uri=http%3A%2F%2F127.0.0.1%3A9999%2Fcallback", nil
}

func newMCPCallbackServer(t *testing.T, origins []string) (*testServer, string, *callbackConversation) {
	t.Helper()
	ts := newTestServer(t, ServerConfig{PublicOrigins: origins, LogHeaders: true})
	sum, conv := createSession(t, ts.m, ts.prov)
	c := &callbackConversation{statusConversation: &statusConversation{Conversation: conv}, authorization: "private-sdk-state"}
	ts.m.mu.Lock()
	ts.m.sessions[sum.ID].conv = c
	info := ts.m.infos[sum.Provider]
	info.Capabilities.MCP = true
	ts.m.infos[sum.Provider] = info
	ts.m.mu.Unlock()
	return ts, sum.ID, c
}
func beginMCPCallback(t *testing.T, ts *testServer, id, host string) string {
	t.Helper()
	w := ts.do(http.MethodPost, "/api/sessions/"+id+"/mcp/servers/notes/sign-in", `{}`, withHost(host), withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestMCPCallbackUsesTrustedOriginAndConsumesBeforeSDK(t *testing.T) {
	ts, id, c := newMCPCallbackServer(t, []string{"https://UAM.example.com/"})
	body := beginMCPCallback(t, ts, id, "uam.example.com")
	if !strings.Contains(body, `"callback":true`) || strings.Contains(body, `"relay":true`) || c.redirect != "https://uam.example.com/api/mcp/oauth/callback" || c.starts != 1 || c.legacy != 0 {
		t.Fatalf("start=%s redirect=%s native=%d legacy=%d", body, c.redirect, c.starts, c.legacy)
	}
	c.complete = func() {
		ts.m.signIns.mu.Lock()
		pending := ts.m.signIns.pending[signInKey(id, "notes")]
		ts.m.signIns.mu.Unlock()
		if pending != nil {
			t.Error("pending state was not consumed before the SDK exchange")
		}
	}
	var buf bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer uamlog.SetLogger(previous)
	raw := "code=private-code&state=private-sdk-state"
	target := "/api/mcp/oauth/callback?" + raw
	w := ts.do(http.MethodGet, target, "", withHost("uam.example.com"), withHeader("Referer", "https://uam.example.com"+target))
	if w.Code != http.StatusOK || c.completes != 1 || c.completedURL != "https://uam.example.com"+target {
		t.Fatalf("completion status=%d calls=%d", w.Code, c.completes)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("callback lacks response privacy policy")
	}
	for _, secret := range []string{"private-code", "private-sdk-state"} {
		if strings.Contains(w.Body.String(), secret) || strings.Contains(buf.String(), secret) {
			t.Fatal("callback leaked query into page or request log")
		}
	}
	replay := ts.do(http.MethodGet, target, "", withHost("uam.example.com"))
	if replay.Code == http.StatusOK || c.completes != 1 {
		t.Fatalf("replay status=%d calls=%d", replay.Code, c.completes)
	}
	if c.ID() != detail(t, ts.m, id).ConversationID {
		t.Fatal("callback changed conversation")
	}
}

func TestMCPCallbackRejectsHostQueryExpiryAndChangedTask(t *testing.T) {
	for _, mode := range []string{"wrong-host", "wrong-state", "duplicate-state", "both-results", "missing-result", "malformed-query", "expired", "closed", "removed", "head"} {
		t.Run(mode, func(t *testing.T) {
			ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
			beginMCPCallback(t, ts, id, "uam.example.com")
			host, method, raw := "uam.example.com", http.MethodGet, "code=private-code&state=private-sdk-state"
			switch mode {
			case "wrong-host":
				host = "evil.example"
			case "wrong-state":
				raw = "code=x&state=other"
			case "duplicate-state":
				raw += "&state=private-sdk-state"
			case "both-results":
				raw += "&error=denied"
			case "missing-result":
				raw = "state=private-sdk-state"
			case "malformed-query":
				raw += "&bad=%"
			case "expired":
				ts.m.signIns.mu.Lock()
				for _, p := range ts.m.signIns.pending {
					p.expires = time.Now().Add(-time.Second)
				}
				ts.m.signIns.mu.Unlock()
			case "closed":
				if _, err := ts.m.Close(id); err != nil {
					t.Fatal(err)
				}
			case "removed":
				ts.m.mu.Lock()
				ts.m.sessions[id].removed = true
				ts.m.mu.Unlock()
			case "head":
				method = http.MethodHead
			}
			w := ts.do(method, "/api/mcp/oauth/callback?"+raw, "", withHost(host), withHeader("X-Forwarded-Host", "uam.example.com"))
			if w.Code == http.StatusOK || c.completes != 0 || strings.Contains(w.Body.String(), "private-code") {
				t.Fatalf("refusal status=%d calls=%d", w.Code, c.completes)
			}
			if mode == "head" || mode == "wrong-host" {
				w = ts.do(http.MethodGet, "/api/mcp/oauth/callback?code=x&state=private-sdk-state", "", withHost("uam.example.com"))
				if w.Code != http.StatusOK || c.completes != 1 {
					t.Fatal("invalid request consumed a matching pending callback")
				}
			}
		})
	}
}

func TestMCPCallbackDenialAndSupportedFailureDoNotRetry(t *testing.T) {
	ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
	beginMCPCallback(t, ts, id, "uam.example.com")
	c.completeErr = errors.New("provider diagnostic code=private-code&state=private-sdk-state")
	target := "/api/mcp/oauth/callback?error=access_denied&error_description=private-description&state=private-sdk-state"
	w := ts.do(http.MethodGet, target, "", withHost("uam.example.com"))
	if w.Code == http.StatusOK || c.completes != 1 || !strings.Contains(c.completedURL, "error=access_denied") || strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("denial status=%d completes=%d", w.Code, c.completes)
	}
	ts.do(http.MethodGet, target, "", withHost("uam.example.com"))
	if c.completes != 1 || c.legacy != 0 {
		t.Fatal("completion failure retried the exchange")
	}
	c.startErr = errors.New("provider diagnostic code=private-code")
	w = ts.do(http.MethodPost, "/api/sessions/"+id+"/mcp/servers/notes/sign-in", `{}`, withHost("uam.example.com"), withCookie(ts))
	if w.Code != http.StatusBadGateway || c.legacy != 0 || strings.Contains(w.Body.String(), "private-code") {
		t.Fatal("supported start failure fell back or leaked diagnostics")
	}
}

func TestMCPCallbackFallbackIsOnlyAbsentCapabilityOrTrustedHTTPSOrigin(t *testing.T) {
	for _, mode := range []string{"no-origin", "http-origin", "foreign-host", "unsupported", "absent"} {
		t.Run(mode, func(t *testing.T) {
			origins := []string{"https://uam.example.com"}
			if mode == "no-origin" {
				origins = nil
			}
			if mode == "http-origin" {
				origins = []string{"http://uam.example.com"}
			}
			ts, id, c := newMCPCallbackServer(t, origins)
			host := "uam.example.com"
			if mode == "foreign-host" {
				host = "other.example"
			}
			if mode == "unsupported" {
				c.startErr = agentapi.ErrUnsupported
			}
			if mode == "absent" {
				ts.m.mu.Lock()
				ts.m.sessions[id].conv = &struct {
					agentapi.Conversation
					agentapi.MCPController
				}{c.Conversation, c}
				ts.m.mu.Unlock()
			}
			w := ts.do(http.MethodPost, "/api/sessions/"+id+"/mcp/servers/notes/sign-in", `{}`, withHost(host), withCookie(ts), withHeader("X-Forwarded-Host", "uam.example.com"), withHeader("X-Forwarded-Proto", "https"))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"relay":true`) || strings.Contains(w.Body.String(), `"callback":true`) || c.legacy != 1 {
				t.Fatalf("legacy status=%d calls=%d", w.Code, c.legacy)
			}
			if mode != "unsupported" && c.starts != 0 {
				t.Fatal("untrusted host or absent capability attempted callback login")
			}
		})
	}
}

func TestMCPCallbackRejectsLateStartAndReadOnlyTask(t *testing.T) {
	for _, mode := range []string{"exit", "archived", "settled"} {
		t.Run(mode, func(t *testing.T) {
			ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
			if mode == "exit" {
				c.start = func() {
					ts.m.mu.Lock()
					s := ts.m.sessions[id]
					gen := s.gen
					ts.m.mu.Unlock()
					ts.m.handleEvent(s, gen, agentapi.Event{Kind: agentapi.EventExit})
				}
			} else {
				ts.m.mu.Lock()
				ts.m.sessions[id].stage = mode
				ts.m.mu.Unlock()
			}
			w := ts.do(http.MethodPost, "/api/sessions/"+id+"/mcp/servers/notes/sign-in", `{}`, withHost("uam.example.com"), withCookie(ts))
			if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "authorize.example") || c.legacy != 0 {
				t.Fatalf("late/read-only status=%d native=%d legacy=%d", w.Code, c.starts, c.legacy)
			}
			if mode != "exit" && c.starts != 0 {
				t.Fatal("read-only Task started OAuth")
			}
		})
	}
}

func TestMCPCallbackLimitsPendingStatesAndNeverRoutesDuplicateState(t *testing.T) {
	ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
	redirect := "https://uam.example.com/api/mcp/oauth/callback"
	if _, err := ts.m.startMCPSignIn(id, "first", false, redirect); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.m.startMCPSignIn(id, "second", false, redirect); statusOf(err) != http.StatusBadGateway {
		t.Fatal("duplicate SDK state could select a different server")
	}
	if err := ts.m.completeMCPCallback("uam.example.com", "code=x&state=private-sdk-state"); err != nil || c.completes != 1 {
		t.Fatal("duplicate attempt replaced the original authorization")
	}
	for i := 0; i < maxMCPEntries; i++ {
		c.authorization = fmt.Sprintf("state-%d", i)
		if _, err := ts.m.startMCPSignIn(id, fmt.Sprintf("server-%d", i), false, redirect); err != nil {
			t.Fatal(err)
		}
	}
	before := c.starts
	c.authorization = "overflow"
	if _, err := ts.m.startMCPSignIn(id, "overflow", false, redirect); statusOf(err) != http.StatusConflict || c.starts != before {
		t.Fatal("capacity refusal started another SDK authorization")
	}
	ts.m.signIns.mu.Lock()
	count := len(ts.m.signIns.pending)
	ts.m.signIns.pending[signInKey(id, "server-0")].expires = time.Now().Add(-time.Second)
	ts.m.signIns.mu.Unlock()
	if count != maxMCPEntries {
		t.Fatalf("pending count=%d", count)
	}
	if _, err := ts.m.startMCPSignIn(id, "overflow", false, redirect); err != nil {
		t.Fatal("expired authorization kept the registry full")
	}
}

func TestMCPCallbackHolderAndLateCompletionDoNotClaimSuccess(t *testing.T) {
	for _, mode := range []string{"held", "exit"} {
		t.Run(mode, func(t *testing.T) {
			ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
			beginMCPCallback(t, ts, id, "uam.example.com")
			if mode == "held" {
				ts.m.mu.Lock()
				s := ts.m.sessions[id]
				s.imported = true
				info := ts.m.infos[s.provider]
				info.Capabilities.Import = true
				ts.m.infos[s.provider] = info
				ts.m.mu.Unlock()
				ts.prov.SetInUse([]string{c.ID()}, nil)
			} else {
				c.complete = func() {
					ts.m.mu.Lock()
					s := ts.m.sessions[id]
					gen := s.gen
					ts.m.mu.Unlock()
					ts.m.handleEvent(s, gen, agentapi.Event{Kind: agentapi.EventExit})
				}
			}
			w := ts.do(http.MethodGet, "/api/mcp/oauth/callback?code=x&state=private-sdk-state", "", withHost("uam.example.com"))
			if w.Code != http.StatusConflict || (mode == "held" && c.completes != 0) {
				t.Fatalf("%s completion status=%d calls=%d", mode, w.Code, c.completes)
			}
		})
	}
}

func TestMCPCallbackDoesNotUseLoopbackFinish(t *testing.T) {
	ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
	beginMCPCallback(t, ts, id, "uam.example.com")
	if err := ts.m.FinishMCPSignIn(id, "notes", "http://localhost/api/mcp/oauth/callback?code=x&state=private-sdk-state"); statusOf(err) != http.StatusConflict {
		t.Fatalf("public callback accepted loopback finish: %v", err)
	}
	w := ts.do(http.MethodGet, "/api/mcp/oauth/callback?code=x&state=private-sdk-state", "", withHost("uam.example.com"))
	if w.Code != http.StatusOK || c.completes != 1 {
		t.Fatal("loopback finish consumed the public SDK authorization")
	}
}

// Weak reachability checks the lifetime contract without asserting struct fields.
func abandonedMCPCallback(t *testing.T) weak.Pointer[callbackConversation] {
	t.Helper()
	ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
	beginMCPCallback(t, ts, id, "uam.example.com")
	pointer := weak.Make(c)
	if _, err := ts.m.Close(id); err != nil {
		t.Fatal(err)
	}
	return pointer
}

func TestMCPCallbackAbandonedStateDoesNotRetainClosedConversation(t *testing.T) {
	pointer := abandonedMCPCallback(t)
	limit := time.Now().Add(time.Second)
	for time.Now().Before(limit) {
		runtime.GC()
		if pointer.Value() == nil {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("an abandoned pending sign-in retained the closed SDK conversation")
}

func TestMCPCallbackRequiresTheCurrentConversationCapability(t *testing.T) {
	ts, id, c := newMCPCallbackServer(t, []string{"https://uam.example.com"})
	beginMCPCallback(t, ts, id, "uam.example.com")
	ts.m.mu.Lock()
	ts.m.sessions[id].conv = &struct {
		agentapi.Conversation
		agentapi.MCPController
	}{c.Conversation, c}
	ts.m.mu.Unlock()
	w := ts.do(http.MethodGet, "/api/mcp/oauth/callback?code=x&state=private-sdk-state", "", withHost("uam.example.com"))
	if w.Code != http.StatusConflict || c.completes != 0 || c.legacy != 0 {
		t.Fatalf("stale controller invoked: status=%d completes=%d", w.Code, c.completes)
	}
}
