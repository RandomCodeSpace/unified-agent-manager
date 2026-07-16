package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	maxAPIResponseBytes     = 1 << 20
	maxAPIErrorExcerptRunes = 256
	maxErrorBodyBytes       = 4 << 10
	maxSSEEventBytes        = 256 << 10
	maxSSELineBytes         = maxSSEEventBytes + 16
	sessionPathPrefix       = "/session/"
)

var (
	errSessionNotFound = errors.New("OpenCode session not found")
	permissionIDRE     = regexp.MustCompile(`^per_[A-Za-z0-9_-]{3,60}$`)
)

type serverHealth struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

type sessionInfo struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentID,omitempty"`
	Directory string `json:"directory"`
	Title     string `json:"title"`
}

type eventEnvelope struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type apiClient struct {
	baseURL   *url.URL
	username  string
	password  string
	directory string
	http      *http.Client
}

func newAPIClient(baseURL, username, password, directory string, client *http.Client) (*apiClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, fmt.Errorf("OpenCode server base URL must be plain loopback HTTP")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawPath != "" {
		return nil, fmt.Errorf("OpenCode server base URL must not contain a path")
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || host != "127.0.0.1" {
		return nil, fmt.Errorf("OpenCode server base URL must use numeric 127.0.0.1 with an explicit port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("OpenCode server base URL has an invalid port")
	}

	parsed.Path = ""
	baseClient := client
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	httpClient := *baseClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &apiClient{
		baseURL:   parsed,
		username:  username,
		password:  password,
		directory: directory,
		http:      &httpClient,
	}, nil
}

func (c *apiClient) health(ctx context.Context) (serverHealth, error) {
	var health serverHealth
	if err := c.doJSON(ctx, http.MethodGet, "/global/health", "", nil, &health); err != nil {
		return serverHealth{}, err
	}
	return health, nil
}

func (c *apiClient) createSession(ctx context.Context, title string) (sessionInfo, error) {
	payload := struct {
		Title    string `json:"title"`
		Metadata struct {
			UAM bool `json:"uam"`
		} `json:"metadata"`
	}{Title: "UAM: " + displaytext.Sanitize(title)}
	payload.Metadata.UAM = true

	var session sessionInfo
	if err := c.doJSON(ctx, http.MethodPost, "/session", "", payload, &session); err != nil {
		return sessionInfo{}, err
	}
	return session, nil
}

func (c *apiClient) getSession(ctx context.Context, id string) (sessionInfo, error) {
	if !providerIDRE.MatchString(id) || !store.ValidProviderSessionID(id) {
		return sessionInfo{}, fmt.Errorf("invalid OpenCode session ID")
	}
	path := sessionPathPrefix + id
	rawPath := sessionPathPrefix + url.PathEscape(id)
	var session sessionInfo
	err := c.doJSON(ctx, http.MethodGet, path, rawPath, nil, &session)
	if errors.Is(err, errSessionNotFound) {
		return sessionInfo{}, errSessionNotFound
	}
	if err != nil {
		return sessionInfo{}, err
	}
	return session, nil
}

func (c *apiClient) replyPermission(ctx context.Context, requestID string) error {
	if !permissionIDRE.MatchString(requestID) || !store.ValidProviderSessionID(requestID) {
		return fmt.Errorf("invalid OpenCode permission request ID")
	}
	path := "/permission/" + requestID + "/reply"
	rawPath := "/permission/" + url.PathEscape(requestID) + "/reply"
	resp, err := c.do(ctx, http.MethodPost, path, rawPath, struct {
		Reply string `json:"reply"`
	}{Reply: "once"}, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if !successfulStatus(resp.StatusCode) {
		return c.statusError("permission reply", resp)
	}
	return nil
}

func (c *apiClient) subscribe(ctx context.Context, ready chan<- struct{}, events chan<- eventEnvelope) error {
	if ready == nil {
		return fmt.Errorf("OpenCode event readiness channel is required")
	}
	resp, err := c.do(ctx, http.MethodGet, "/event", "", nil, "text/event-stream")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return c.statusError("event subscription", resp)
	}
	if err := requireContentType(resp, "text/event-stream"); err != nil {
		return c.safeError("OpenCode event subscription", err)
	}
	close(ready)
	return c.readSSEEvents(ctx, resp.Body, events)
}

type sseEventData struct {
	bytes   []byte
	present bool
}

func (c *apiClient) readSSEEvents(ctx context.Context, body io.Reader, events chan<- eventEnvelope) error {
	reader := bufio.NewReader(body)
	var data sseEventData
	for {
		line, readErr := readSSELine(reader)
		if readErr != nil {
			return c.sseReadError(ctx, readErr)
		}
		if len(line) == 0 {
			if !data.present {
				continue
			}
			if err := c.sendSSEEvent(ctx, data.bytes, events); err != nil {
				return err
			}
			data.reset()
			continue
		}
		value, ok := sseDataValue(line)
		if !ok {
			continue
		}
		if err := data.append(value); err != nil {
			return err
		}
	}
}

func (c *apiClient) sseReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("OpenCode event stream ended: %w", io.EOF)
	}
	return c.safeError("read OpenCode event stream", err)
}

func (c *apiClient) sendSSEEvent(ctx context.Context, data []byte, events chan<- eventEnvelope) error {
	var event eventEnvelope
	if err := decodeStrictJSON(data, &event); err != nil {
		return c.safeError("decode OpenCode event", err)
	}
	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sseDataValue(line []byte) (string, bool) {
	field, value, found := strings.Cut(string(line), ":")
	if !found || field != "data" {
		return "", false
	}
	return strings.TrimPrefix(value, " "), true
}

func (d *sseEventData) append(value string) error {
	additional := len(value)
	if d.present {
		additional++
	}
	if len(d.bytes)+additional > maxSSEEventBytes {
		return fmt.Errorf("OpenCode SSE event is too large")
	}
	if d.present {
		d.bytes = append(d.bytes, '\n')
	}
	d.bytes = append(d.bytes, value...)
	d.present = true
	return nil
}

func (d *sseEventData) reset() {
	d.bytes = d.bytes[:0]
	d.present = false
}

func (c *apiClient) doJSON(ctx context.Context, method, path, rawPath string, payload, destination any) error {
	resp, err := c.do(ctx, method, path, rawPath, payload, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if !successfulStatus(resp.StatusCode) {
		if method == http.MethodGet && strings.HasPrefix(path, sessionPathPrefix) && resp.StatusCode == http.StatusNotFound {
			return errSessionNotFound
		}
		return c.statusError("API request", resp)
	}
	if err := requireContentType(resp, "application/json"); err != nil {
		return c.safeError("OpenCode API response", err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
	if err != nil {
		return c.safeError("read OpenCode API response", err)
	}
	if len(data) > maxAPIResponseBytes {
		return fmt.Errorf("OpenCode API response body is too large")
	}
	if err := decodeStrictJSON(data, destination); err != nil {
		return c.safeError("decode OpenCode API response", err)
	}
	return nil
}

func (c *apiClient) do(ctx context.Context, method, path, rawPath string, payload any, accept string) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, c.safeError("encode OpenCode API request", err)
		}
		body = bytes.NewReader(data)
	}

	endpoint := *c.baseURL
	endpoint.Path = path
	endpoint.RawPath = rawPath
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, c.safeError("construct OpenCode API request", err)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("X-OpenCode-Directory", c.directory)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.safeError("OpenCode API request", err)
	}
	return resp, nil
}

func (c *apiClient) statusError(operation string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if len(data) > maxErrorBodyBytes {
		return fmt.Errorf("OpenCode %s failed with HTTP %d: response body omitted (exceeds %d bytes)", operation, resp.StatusCode, maxErrorBodyBytes)
	}
	excerpt := c.safeText(string(data))
	if excerpt == "" {
		return fmt.Errorf("OpenCode %s failed with HTTP %d", operation, resp.StatusCode)
	}
	return fmt.Errorf("OpenCode %s failed with HTTP %d: %s", operation, resp.StatusCode, excerpt)
}

func (c *apiClient) safeError(operation string, err error) error {
	return fmt.Errorf("%s failed: %s", operation, c.safeText(err.Error()))
}

func (c *apiClient) safeText(value string) string {
	if c.password != "" {
		value = strings.ReplaceAll(value, c.password, "<redacted>")
	}
	value = strings.TrimSpace(displaytext.Sanitize(value))
	sanitizedPassword := strings.TrimSpace(displaytext.Sanitize(c.password))
	if sanitizedPassword != "" {
		value = strings.ReplaceAll(value, sanitizedPassword, "<redacted>")
	}
	runes := []rune(value)
	if len(runes) > maxAPIErrorExcerptRunes {
		value = string(runes[:maxAPIErrorExcerptRunes]) + "…"
	}
	return value
}

func successfulStatus(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

func requireContentType(resp *http.Response, want string) error {
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != want {
		return fmt.Errorf("unexpected response content type")
	}
	return nil
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func readSSELine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, reader.Size())
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxSSELineBytes {
			return nil, fmt.Errorf("OpenCode SSE line is too large")
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			line = line[:len(line)-1]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, io.EOF
		default:
			return nil, err
		}
	}
}
