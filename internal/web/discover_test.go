package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const discoverKey = "sk-discover-secret"

func TestDiscoverModels(t *testing.T) {
	t.Setenv("UAM_BYOM_DISCOVER", discoverKey)
	var logs bytes.Buffer
	prev := log.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { log.SetLogger(prev) })
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { otherHits.Add(1) }))
	t.Cleanup(other.Close)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+discoverKey {
			http.Error(w, "unexpected request", http.StatusTeapot)
			return
		}
		switch r.URL.Path {
		case "/ok/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"qwen3.5:397b"},{"id":"gpt-oss:20b"},{"id":"gpt-oss:20b"},{"id":"bad id"},{"id":""}]}`)
		case "/denied/models":
			http.Error(w, "SECRET-UPSTREAM-BODY "+discoverKey, http.StatusUnauthorized)
		case "/html/models":
			fmt.Fprint(w, "<html>hi</html>")
		case "/big/models":
			fmt.Fprint(w, `{"data":[`+strings.Repeat(" ", maxDiscoverBytes)+`]}`)
		case "/many/models":
			ids := make([]string, maxDiscoverIDs+3)
			for i := range ids {
				ids[i] = fmt.Sprintf(`{"id":"m%04d"}`, i)
			}
			fmt.Fprint(w, `{"data":[`+strings.Join(ids, ",")+`]}`)
		case "/slow/models":
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/moved/models":
			http.Redirect(w, r, other.URL+"/v1/models", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { close(release); upstream.Close() })
	old := discoverTimeout
	discoverTimeout = 300 * time.Millisecond
	t.Cleanup(func() { discoverTimeout = old })

	m := startManager(t, openTestStore(t))
	ctx := context.Background()
	discover := func(path, env string) (DiscoverResult, error) {
		return m.DiscoverModels(ctx, DiscoverRequest{BaseURL: upstream.URL + path, APIKeyEnv: env})
	}
	got, err := discover("/ok/", "UAM_BYOM_DISCOVER")
	if err != nil || strings.Join(got.Models, ",") != "gpt-oss:20b,qwen3.5:397b" || !got.KeyPresent || got.Truncated {
		t.Fatalf("discover = %+v, %v", got, err)
	}
	if got, err := discover("/many", "UAM_BYOM_DISCOVER"); err != nil || len(got.Models) != maxDiscoverIDs || !got.Truncated || got.Models[0] != "m0000" {
		t.Fatalf("many = %d truncated %v, %v", len(got.Models), got.Truncated, err)
	}
	for name, c := range map[string]struct {
		path, env, want string
		status          int
	}{
		"unset key": {"/ok", "UAM_BYOM_UNSET", "UAM_BYOM_UNSET is not set", http.StatusBadRequest},
		"no prefix": {"/ok", "HOME", "UAM_BYOM_", http.StatusBadRequest},
		"denied":    {"/denied", "UAM_BYOM_DISCOVER", "answered 401 Unauthorized", http.StatusBadGateway},
		"not json":  {"/html", "UAM_BYOM_DISCOVER", "did not return an OpenAI model list", http.StatusBadGateway},
		"oversize":  {"/big", "UAM_BYOM_DISCOVER", "larger than 1 MiB", http.StatusBadGateway},
		"timeout":   {"/slow", "UAM_BYOM_DISCOVER", "did not answer within", http.StatusBadGateway},
		"redirect":  {"/moved", "UAM_BYOM_DISCOVER", "redirect, which is not followed", http.StatusBadGateway},
		"not found": {"/nothing", "UAM_BYOM_DISCOVER", "answered 404", http.StatusBadGateway},
	} {
		_, err := discover(c.path, c.env)
		if statusOf(err) != c.status || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "SECRET-UPSTREAM-BODY") || strings.Contains(err.Error(), discoverKey) {
			t.Errorf("%s: %v (status %d), want %d containing %q", name, err, statusOf(err), c.status, c.want)
		}
	}
	if _, err := m.DiscoverModels(ctx, DiscoverRequest{BaseURL: "file:///etc", APIKeyEnv: "UAM_BYOM_DISCOVER"}); statusOf(err) != http.StatusBadRequest {
		t.Errorf("file URL = %v", err)
	}
	for range cap(discoverSlots) {
		discoverSlots <- struct{}{}
	}
	_, err = discover("/ok/", "UAM_BYOM_DISCOVER")
	for range cap(discoverSlots) {
		<-discoverSlots
	}
	if statusOf(err) != http.StatusTooManyRequests {
		t.Errorf("busy discovery = %v (status %d), want 429", err, statusOf(err))
	}
	if otherHits.Load() != 0 {
		t.Errorf("the redirect target was requested %d times", otherHits.Load())
	}
	if strings.Contains(logs.String(), discoverKey) || strings.Contains(logs.String(), "SECRET-UPSTREAM-BODY") {
		t.Errorf("log holds the key or an upstream body: %s", logs.String())
	}
}

func TestDiscoverModelsRoute(t *testing.T) {
	t.Setenv("UAM_BYOM_DISCOVER", discoverKey)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"b"},{"id":"a"}]}`)
	}))
	t.Cleanup(upstream.Close)
	ts := newTestServer(t, ServerConfig{})
	body := `{"base_url":"` + upstream.URL + `/v1","api_key_env":"UAM_BYOM_DISCOVER"}`
	if w := ts.do(http.MethodPost, "/api/settings/custom-models/discover", body); w.Code != http.StatusUnauthorized {
		t.Fatalf("without sign-in = %d", w.Code)
	}
	w := ts.do(http.MethodPost, "/api/settings/custom-models/discover", body, withCookie(ts))
	var got DiscoverResult
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || strings.Join(got.Models, ",") != "a,b" || !got.KeyPresent || strings.Contains(w.Body.String(), discoverKey) {
		t.Fatalf("POST discover = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/settings/custom-models/discover", "", withCookie(ts)); w.Code == http.StatusOK {
		t.Fatalf("GET discover = %d", w.Code)
	}
}
