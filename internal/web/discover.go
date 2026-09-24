package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Discovery bounds: the whole request, the response body, and the IDs kept.
var discoverTimeout = 10 * time.Second

const (
	maxDiscoverBytes = 1 << 20
	maxDiscoverIDs   = 500
)

// DiscoverRequest names an OpenAI-compatible endpoint whose models to list.
type DiscoverRequest struct {
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env"`
	WireAPI   string `json:"wire_api"`
}

// DiscoverResult is what the endpoint lists: sorted, distinct model IDs
// that could be stored, at most maxDiscoverIDs (Truncated when more were
// listed). KeyPresent is true: discovery refuses a missing key.
type DiscoverResult struct {
	Models     []string `json:"models"`
	Truncated  bool     `json:"truncated,omitempty"`
	KeyPresent bool     `json:"key_present"`
}

var errRedirect = errors.New("redirect refused")

// discoverClient never follows a redirect, so the only request is GET
// base_url/models.
var discoverClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect }}

// DiscoverModels lists the models an OpenAI-compatible endpoint serves with
// one GET of base_url + "/models", authenticated with the key read from the
// named UAM_BYOM_ variable. Only the IDs come back; an upstream failure is
// reported by status line or kind, never by its body.
func (m *Manager) DiscoverModels(ctx context.Context, req DiscoverRequest) (DiscoverResult, error) {
	probe := store.WebCustomModel{Name: "discover", ModelID: "discover", BaseURL: req.BaseURL, WireAPI: req.WireAPI, APIKeyEnv: req.APIKeyEnv}
	if err := store.ValidCustomModel(probe); err != nil {
		return DiscoverResult{}, newError(http.StatusBadRequest, "%s", err.Error())
	}
	key := os.Getenv(req.APIKeyEnv)
	if key == "" {
		return DiscoverResult{}, newError(http.StatusBadRequest, "%s is not set in the uam web service's environment; export it where the service starts and restart the service", req.APIKeyEnv)
	}
	ctx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(req.BaseURL, "/")+"/models", nil)
	if err != nil {
		return DiscoverResult{}, newError(http.StatusBadRequest, "base URL is not usable")
	}
	hreq.Header.Set("Authorization", "Bearer "+key)
	hreq.Header.Set("Accept", "application/json")
	resp, err := discoverClient.Do(hreq)
	if err != nil {
		reason := "could not reach the endpoint"
		switch {
		case errors.Is(err, errRedirect):
			reason = "the endpoint answered with a redirect, which is not followed; use the final URL as the base URL"
		case errors.Is(err, context.DeadlineExceeded):
			reason = fmt.Sprintf("the endpoint did not answer within %s", discoverTimeout)
		}
		log.Warn("custom model discovery failed", "base_url", req.BaseURL, "reason", reason)
		return DiscoverResult{}, newError(http.StatusBadGateway, "%s", reason)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		log.Warn("custom model discovery failed", "base_url", req.BaseURL, "status", resp.StatusCode)
		return DiscoverResult{}, newError(http.StatusBadGateway, "the endpoint answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoverBytes+1))
	switch {
	case err != nil:
		return DiscoverResult{}, newError(http.StatusBadGateway, "could not read the endpoint's model list")
	case len(body) > maxDiscoverBytes:
		return DiscoverResult{}, newError(http.StatusBadGateway, "the endpoint's model list is larger than 1 MiB")
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &list) != nil || list.Data == nil {
		return DiscoverResult{}, newError(http.StatusBadGateway, "the endpoint did not return an OpenAI model list (data[].id)")
	}
	ids := make([]string, 0, len(list.Data))
	for _, d := range list.Data {
		if store.ValidHiddenModel(d.ID) && !strings.ContainsFunc(d.ID, unicode.IsSpace) {
			ids = append(ids, d.ID)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := DiscoverResult{Models: ids, KeyPresent: true}
	if len(ids) > maxDiscoverIDs {
		out.Models, out.Truncated = ids[:maxDiscoverIDs], true
	}
	return out, nil
}
