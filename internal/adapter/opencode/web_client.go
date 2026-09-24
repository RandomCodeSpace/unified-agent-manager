package opencode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	// webMaxResponseBytes bounds message pages and diffs, which routinely
	// exceed the 1 MiB default used for small control responses.
	webMaxResponseBytes = 32 << 20
	webHistoryPageSize  = 25
	webMaxHistoryPages  = 10000
)

var (
	webMessageIDRE     = regexp.MustCompile(`^msg_[0-9A-Za-z]{1,64}$`)
	webPermissionIDRE  = regexp.MustCompile(`^per_[0-9A-Za-z]{1,64}$`)
	webQuestionIDRE    = regexp.MustCompile(`^que_[0-9A-Za-z]{1,64}$`)
	errWebNotFound     = errors.New("OpenCode resource not found")
	webBase62Alphabet  = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	webAscendingIDLock sync.Mutex
	webAscendingLastMS int64
	webAscendingCount  int64
)

// newAscendingMessageID replicates Identifier.create(prefix, "ascending") from
// the OpenCode 1.18.32 bundle (chunk exporting Identifier, ascending, create):
// prefix + "_" + hex of the low 48 bits of (Date.now()*4096 + counter), where
// counter restarts at 1 for each new millisecond, followed by 14 characters
// chosen as base62[randomByte % 62]. OpenCode's own clients generate message
// IDs the same way, so the server keeps its lexicographic ordering.
func newAscendingMessageID() (string, error) {
	random := make([]byte, 14)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate OpenCode message ID: %w", err)
	}
	now := time.Now().UnixMilli()
	webAscendingIDLock.Lock()
	if now != webAscendingLastMS {
		webAscendingLastMS = now
		webAscendingCount = 0
	}
	webAscendingCount++
	value := uint64(now)*4096 + uint64(webAscendingCount) // #nosec G115 -- Unix milliseconds are positive.
	webAscendingIDLock.Unlock()
	var timeBytes [6]byte
	for index := range timeBytes {
		timeBytes[index] = byte(value >> (40 - 8*index))
	}
	for index, b := range random {
		random[index] = webBase62Alphabet[int(b)%len(webBase62Alphabet)]
	}
	return "msg_" + hex.EncodeToString(timeBytes[:]) + string(random), nil
}

type webStatusError struct {
	status int
	err    error
}

func (e *webStatusError) Error() string { return e.err.Error() }

func (e *webStatusError) Unwrap() error {
	if e.status == http.StatusNotFound {
		return errWebNotFound
	}
	return nil
}

// webJSON performs one bounded API call. Non-2xx responses become
// *webStatusError (404 matches errWebNotFound); destination may be nil.
func (c *apiClient) webJSON(ctx context.Context, method, path string, query url.Values, payload, destination any) (http.Header, error) {
	resp, err := c.doQuery(ctx, method, path, "", query, payload, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if !successfulStatus(resp.StatusCode) {
		return nil, &webStatusError{status: resp.StatusCode, err: c.statusError("API request", resp)}
	}
	if destination == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		return resp.Header, nil
	}
	if err := requireContentType(resp, "application/json"); err != nil {
		return nil, c.safeError("OpenCode API response", err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, webMaxResponseBytes+1))
	if err != nil {
		return nil, c.safeError("read OpenCode API response", err)
	}
	if len(data) > webMaxResponseBytes {
		return nil, fmt.Errorf("OpenCode API response body is too large")
	}
	if err := decodeStrictJSON(data, destination); err != nil {
		return nil, c.safeError("decode OpenCode API response", err)
	}
	return resp.Header, nil
}

type webProviderError struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

func (e *webProviderError) message() string {
	var data struct {
		Message any `json:"message"`
	}
	_ = json.Unmarshal(e.Data, &data)
	message, _ := data.Message.(string)
	return message
}

type webMessageInfo struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Role      string `json:"role"`
	Time      struct {
		Created float64 `json:"created"`
	} `json:"time"`
	Error *webProviderError `json:"error"`
}

type webMessage struct {
	Info  webMessageInfo    `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

type webPartTime struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type webToolState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output string          `json:"output"`
	Title  string          `json:"title"`
	Error  string          `json:"error"`
	Time   *webPartTime    `json:"time"`
}

type webPart struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionID"`
	MessageID string        `json:"messageID"`
	Type      string        `json:"type"`
	Text      string        `json:"text"`
	Synthetic bool          `json:"synthetic"`
	Ignored   bool          `json:"ignored"`
	Time      *webPartTime  `json:"time"`
	Tool      string        `json:"tool"`
	State     *webToolState `json:"state"`
}

type webSessionStatus struct {
	Type    string `json:"type"`
	Attempt int    `json:"attempt"`
	Message string `json:"message"`
}

type webPermissionRequest struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionID"`
	Permission string         `json:"permission"`
	Patterns   []string       `json:"patterns"`
	Metadata   map[string]any `json:"metadata"`
}

type webQuestionOption struct {
	Label string `json:"label"`
}

type webQuestionInfo struct {
	Question string              `json:"question"`
	Header   string              `json:"header"`
	Options  []webQuestionOption `json:"options"`
	Multiple *bool               `json:"multiple"`
	Custom   *bool               `json:"custom"`
}

type webQuestionRequest struct {
	ID        string            `json:"id"`
	SessionID string            `json:"sessionID"`
	Questions []webQuestionInfo `json:"questions"`
}

type webFileDiff struct {
	File      string  `json:"file"`
	Patch     string  `json:"patch"`
	Additions float64 `json:"additions"`
	Deletions float64 `json:"deletions"`
	Status    string  `json:"status"`
}

func webSessionPath(id, suffix string) (string, error) {
	if !validOpenCodeSessionID(id) {
		return "", fmt.Errorf("invalid OpenCode session ID")
	}
	return sessionPathPrefix + id + suffix, nil
}

func (c *apiClient) webStatuses(ctx context.Context) (map[string]webSessionStatus, error) {
	statuses := map[string]webSessionStatus{}
	_, err := c.webJSON(ctx, http.MethodGet, "/session/status", nil, nil, &statuses)
	return statuses, err
}

func (c *apiClient) webPermissions(ctx context.Context) ([]webPermissionRequest, error) {
	var requests []webPermissionRequest
	_, err := c.webJSON(ctx, http.MethodGet, "/permission", nil, nil, &requests)
	return requests, err
}

func (c *apiClient) webQuestions(ctx context.Context) ([]webQuestionRequest, error) {
	var requests []webQuestionRequest
	_, err := c.webJSON(ctx, http.MethodGet, "/question", nil, nil, &requests)
	return requests, err
}

// webMessagePage returns one page of messages, oldest first, and the cursor
// for the next older page ("" when this page reaches the beginning).
func (c *apiClient) webMessagePage(ctx context.Context, sessionID string, limit int, before string) ([]webMessage, string, error) {
	path, err := webSessionPath(sessionID, "/message")
	if err != nil {
		return nil, "", err
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		query.Set("before", before)
	}
	var page []webMessage
	header, err := c.webJSON(ctx, http.MethodGet, path, query, nil, &page)
	if err != nil {
		return nil, "", err
	}
	return page, header.Get("X-Next-Cursor"), nil
}

// webUserMessageExists reports whether sessionID holds a user message with
// exactly messageID.
func (c *apiClient) webUserMessageExists(ctx context.Context, sessionID, messageID string) (bool, error) {
	if !webMessageIDRE.MatchString(messageID) {
		return false, fmt.Errorf("invalid OpenCode message ID")
	}
	path, err := webSessionPath(sessionID, "/message/"+messageID)
	if err != nil {
		return false, err
	}
	var message webMessage
	if _, err := c.webJSON(ctx, http.MethodGet, path, nil, nil, &message); err != nil {
		if errors.Is(err, errWebNotFound) {
			return false, nil
		}
		return false, err
	}
	return message.Info.ID == messageID && message.Info.Role == "user", nil
}

// webPrompt posts one prompt and returns the HTTP status. The body is never
// read into errors because OpenCode may quote the prompt.
func (c *apiClient) webPrompt(ctx context.Context, sessionID, messageID, prompt string) (int, error) {
	path, err := webSessionPath(sessionID, "/prompt_async")
	if err != nil {
		return 0, err
	}
	payload := map[string]any{
		"messageID": messageID,
		"parts":     []map[string]string{{"type": "text", "text": prompt}},
	}
	resp, err := c.doQuery(ctx, http.MethodPost, path, "", nil, payload, "")
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (c *apiClient) webAbort(ctx context.Context, sessionID string) error {
	path, err := webSessionPath(sessionID, "/abort")
	if err != nil {
		return err
	}
	var aborted bool
	_, err = c.webJSON(ctx, http.MethodPost, path, nil, nil, &aborted)
	return err
}

func (c *apiClient) webDiff(ctx context.Context, sessionID string) ([]webFileDiff, error) {
	path, err := webSessionPath(sessionID, "/diff")
	if err != nil {
		return nil, err
	}
	var diffs []webFileDiff
	_, err = c.webJSON(ctx, http.MethodGet, path, nil, nil, &diffs)
	return diffs, err
}

func (c *apiClient) webReplyPermission(ctx context.Context, requestID, reply string) error {
	if !webPermissionIDRE.MatchString(requestID) {
		return fmt.Errorf("invalid OpenCode permission ID")
	}
	var ok bool
	_, err := c.webJSON(ctx, http.MethodPost, "/permission/"+requestID+"/reply", nil, map[string]string{"reply": reply}, &ok)
	return err
}

func (c *apiClient) webReplyQuestion(ctx context.Context, requestID string, answers [][]string) error {
	if !webQuestionIDRE.MatchString(requestID) {
		return fmt.Errorf("invalid OpenCode question ID")
	}
	var ok bool
	_, err := c.webJSON(ctx, http.MethodPost, "/question/"+requestID+"/reply", nil, map[string]any{"answers": answers}, &ok)
	return err
}

func (c *apiClient) webRejectQuestion(ctx context.Context, requestID string) error {
	if !webQuestionIDRE.MatchString(requestID) {
		return fmt.Errorf("invalid OpenCode question ID")
	}
	var ok bool
	_, err := c.webJSON(ctx, http.MethodPost, "/question/"+requestID+"/reject", nil, nil, &ok)
	return err
}
