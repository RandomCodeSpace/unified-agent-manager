package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testAPIUsername  = "uam"
	testAPIPassword  = "test-password-must-not-leak"
	testAPIDirectory = "/tmp/uam project"
)

type deadlineTransport struct {
	base    http.RoundTripper
	seen    atomic.Int64
	missing atomic.Bool
}

func (t *deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.seen.Add(1)
	if _, ok := req.Context().Deadline(); !ok {
		t.missing.Store(true)
	}
	return t.base.RoundTrip(req)
}

func TestAPIClientContracts(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, ok := r.BasicAuth()
		if !ok || username != testAPIUsername || password != testAPIPassword {
			t.Errorf("BasicAuth = (%q, %q, %v)", username, password, ok)
		}
		if got := r.Header.Get("X-OpenCode-Directory"); got != testAPIDirectory {
			t.Errorf("X-OpenCode-Directory = %q, want %q", got, testAPIDirectory)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"1.18.1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			assertJSONBody(t, r, map[string]any{
				"title":    "UAM: workspace name",
				"metadata": map[string]any{"uam": true},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ses_abc123","directory":"/tmp/uam project","title":"UAM: workspace name"}`)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/session/ses_abc123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ses_abc123","directory":"/tmp/uam project","title":"UAM: workspace name"}`)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/permission/per_abc123/reply":
			assertJSONBody(t, r, map[string]any{"reply": "once"})
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				t.Errorf("Accept = %q, want text/event-stream", got)
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = io.WriteString(w, ": keepalive\n\nunknown: ignored\ndata: {\"type\":\"session.created\",\ndata: \"properties\":{\"id\":\"ses_abc123\"}}\n\n")
		default:
			http.Error(w, fmt.Sprintf("unexpected %s %s", r.Method, r.URL.EscapedPath()), http.StatusNotFound)
		}
	}))
	defer server.Close()

	transport := &deadlineTransport{base: http.DefaultTransport}
	client, err := newAPIClient(server.URL, testAPIUsername, testAPIPassword, testAPIDirectory, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := client.health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health != (serverHealth{Healthy: true, Version: "1.18.1"}) {
		t.Fatalf("health = %#v", health)
	}

	created, err := client.createSession(ctx, "work\x1b[31mspace\x1b[0m\nname")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if created.ID != "ses_abc123" || created.ParentID != "" || created.Directory != testAPIDirectory || created.Title != "UAM: workspace name" {
		t.Fatalf("created session = %#v", created)
	}

	got, err := client.getSession(ctx, "ses_abc123")
	if err != nil {
		t.Fatalf("getSession: %v", err)
	}
	if got != created {
		t.Fatalf("getSession = %#v, want %#v", got, created)
	}

	if err := client.replyPermission(ctx, "per_abc123"); err != nil {
		t.Fatalf("replyPermission: %v", err)
	}

	ready := make(chan struct{})
	events := make(chan eventEnvelope, 1)
	if err := client.subscribe(ctx, ready, events); err == nil {
		t.Fatal("subscribe returned nil at EOF")
	}
	select {
	case <-ready:
	default:
		t.Fatal("subscribe did not close ready")
	}
	select {
	case event := <-events:
		if event.Type != "session.created" || string(event.Properties) != `{"id":"ses_abc123"}` {
			t.Fatalf("event = %#v", event)
		}
	default:
		t.Fatal("subscribe did not emit event")
	}

	if got := requests.Load(); got != 5 {
		t.Fatalf("requests = %d, want 5", got)
	}
	if got := transport.seen.Load(); got != 5 {
		t.Fatalf("deadline transport saw %d requests, want 5", got)
	}
	if transport.missing.Load() {
		t.Fatal("a request did not propagate the bounded caller context")
	}
}

func TestAPIClientRejectsUnsafeBaseURLs(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"http://127.0.0.1",
		"http://127.0.0.1:not-a-port",
		"http://127.0.0.2:4096",
		"http://localhost:4096",
		"http://[::1]:4096",
		"https://127.0.0.1:4096",
		"http://user:secret@127.0.0.1:4096",
		"http://127.0.0.1:4096/prefix",
		"http://127.0.0.1:4096?query=value",
		"http://127.0.0.1:4096#fragment",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := newAPIClient(value, testAPIUsername, testAPIPassword, testAPIDirectory, nil); err == nil {
				t.Fatalf("newAPIClient(%q) succeeded", value)
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatalf("constructor error leaked URL credentials: %v", err)
			}
		})
	}
}

func TestAPIClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"healthy":true,"version":"1.18.1"}`)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	client, err := newAPIClient(source.URL, testAPIUsername, testAPIPassword, testAPIDirectory, source.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.health(testContext(t))
	assertSafeAPIError(t, err)
	if redirected.Load() {
		t.Fatal("client followed a redirect outside the validated base URL")
	}
	if !strings.Contains(err.Error(), "302") {
		t.Fatalf("redirect error = %q, want status 302", err)
	}
}

func TestAPIClientHTTPFailures(t *testing.T) {
	t.Parallel()

	t.Run("unauthorized", func(t *testing.T) {
		client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
		_, err := client.health(testContext(t))
		assertSafeAPIError(t, err)
		if !strings.Contains(err.Error(), "401") {
			t.Fatalf("error = %q, want status 401", err)
		}
	})

	t.Run("exact resume not found", func(t *testing.T) {
		client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "missing", http.StatusNotFound)
		})
		_, err := client.getSession(testContext(t), "ses_abc123")
		if !errors.Is(err, errSessionNotFound) {
			t.Fatalf("getSession error = %v, want errSessionNotFound", err)
		}
		assertSafeAPIError(t, err)
	})

	t.Run("server body is sanitized redacted and capped", func(t *testing.T) {
		client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "\x1b[31mboom\x1b[0m\r\n"+testAPIPassword+strings.Repeat("x", 4096))
		})
		_, err := client.health(testContext(t))
		assertSafeAPIError(t, err)
		if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("error = %q, want status and sanitized excerpt", err)
		}
		if len([]rune(err.Error())) > 512 {
			t.Fatalf("error excerpt was not capped: %d runes", len([]rune(err.Error())))
		}
	})
}

func TestAPIClientRejectsInvalidIDsWithoutRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	ctx := testContext(t)
	for _, id := range []string{"", "ses_ab", "ses_abc/def", "-ses_abc123", strings.Repeat("s", 65)} {
		if _, err := client.getSession(ctx, id); err == nil {
			t.Errorf("getSession(%q) succeeded", id)
		}
	}
	for _, id := range []string{"", "per_ab", "per_abc/def", "-per_abc123", strings.Repeat("p", 65)} {
		if err := client.replyPermission(ctx, id); err == nil {
			t.Errorf("replyPermission(%q) succeeded", id)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid IDs issued %d requests", got)
	}
}

func TestAPIClientRejectsInvalidJSONResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "wrong content type", contentType: "text/plain", body: `{"healthy":true,"version":"1.18.1"}`},
		{name: "malformed JSON", contentType: "application/json", body: `{"healthy":`},
		{name: "trailing JSON", contentType: "application/json", body: `{"healthy":true,"version":"1.18.1"}{}`},
		{name: "body over one MiB", contentType: "application/json", body: `{"healthy":true,"version":"` + strings.Repeat("x", (1<<20)+1) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := client.health(testContext(t))
			assertSafeAPIError(t, err)
		})
	}
}

func TestSSERejectsInvalidResponseBeforeReady(t *testing.T) {
	t.Parallel()

	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	ready := make(chan struct{})
	err := client.subscribe(testContext(t), ready, make(chan eventEnvelope, 1))
	assertSafeAPIError(t, err)
	select {
	case <-ready:
		t.Fatal("ready closed before content-type validation")
	default:
	}
}

func TestSSERejectsOversizedEvent(t *testing.T) {
	t.Parallel()

	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", (256<<10)+1)+"\n\n")
	})
	ready := make(chan struct{})
	err := client.subscribe(testContext(t), ready, make(chan eventEnvelope, 1))
	assertSafeAPIError(t, err)
	select {
	case <-ready:
	default:
		t.Fatal("ready was not closed after valid SSE headers")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "large") {
		t.Fatalf("oversized SSE error = %q", err)
	}
}

func TestSSECancellationWhileReading(t *testing.T) {
	t.Parallel()

	client := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- client.subscribe(ctx, ready, make(chan eventEnvelope))
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("subscribe error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe did not stop after cancellation")
	}
}

func TestSSECancellationWhileSending(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	client := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"session.created\",\"properties\":{}}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- client.subscribe(ctx, ready, make(chan eventEnvelope))
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("subscribe error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked SSE delivery ignored cancellation")
	}
}

func assertJSONBody(t *testing.T, r *http.Request, want map[string]any) {
	t.Helper()
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read request body: %v", err)
		return
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Errorf("decode request body %q: %v", data, err)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request body = %#v, want %#v", got, want)
	}
}

func newTestAPIClient(t *testing.T, handler http.HandlerFunc) *apiClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := newAPIClient(server.URL, testAPIUsername, testAPIPassword, testAPIDirectory, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func assertSafeAPIError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	if strings.Contains(message, testAPIPassword) {
		t.Fatalf("error leaked password: %q", message)
	}
	for _, r := range message {
		if r == '\x1b' || r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f {
			t.Fatalf("error contains terminal control: %q", message)
		}
	}
}
