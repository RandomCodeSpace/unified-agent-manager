package web

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Notices tell the owner a Task needs them or finished: a "notify" event to
// every open page, which shows a browser notification when that page is
// hidden or shows another Task, and a Web Push message to every subscribed
// browser, whose service worker shows it even with no page open.
const (
	noticeQuestion   = "question"
	noticePermission = "permission"
	noticeFailed     = "failed"
	noticeFinished   = "finished"

	pushFileName = "web-push.json"
	// maxPushSubscriptions bounds the stored browsers; the oldest goes first.
	maxPushSubscriptions = 20
	// pushTTL is how long a push service keeps an undelivered notice: a
	// browser offline for longer gets nothing stale.
	pushTTL = 30 * 60
	// pushSendWait bounds one push service request.
	pushSendWait = 15 * time.Second
	// maxNoticeText caps the Task name and the question title in a notice.
	maxNoticeText     = 100
	maxPushEndpoint   = 2048
	defaultSubscriber = "uam@localhost"
)

// noticeEvent is the "notify" stream event: one per transition, with the
// same seq in every page, so pages can show it once between them.
type noticeEvent struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	// Key names the notice in every page and in its push (noticeKey).
	Key string `json:"key"`
}

// noticeKey is the one name of a notice: pages claim it so it shows once,
// and the service worker reports the pushes it showed by it.
func noticeKey(id, kind string, seq uint64) string {
	return fmt.Sprintf("%s:%s:%d", id, kind, seq)
}

// pushPayload is what the service worker receives: the Task's name and
// question title only, never transcript text or a credential.
type pushPayload struct {
	Title string `json:"title"`
	Task  string `json:"task"`
	Kind  string `json:"kind"`
	// Key names this notice as pages know it (noticeKey), so a page that
	// saw it shown skips its own.
	Key string `json:"key"`
	// Badge is how many Tasks need the owner, for the app icon.
	Badge int `json:"badge"`
}

// noticeKind is what a Task's summary calls for: needs you (a question, a
// permission, a failure), finished (the turn completed and nothing it
// started still runs), or nothing. A settled or archived Task calls for
// nothing.
func noticeKind(s SessionSummary) string {
	if s.Stage != StageActive {
		return ""
	}
	switch s.State {
	case StateAwaitingPermission:
		return noticePermission
	case StateAwaitingAnswer:
		return noticeQuestion
	case StateFailed:
		return noticeFailed
	case StateCompleted:
		if s.SubagentsRunning == 0 && s.BackgroundTasksRunning == 0 {
			return noticeFinished
		}
	}
	return ""
}

// noticeLocked announces s when its summary moved into a notice kind it was
// not in before. It runs from changedLocked with the published summary.
func (m *Manager) noticeLocked(s *webSession, before, after SessionSummary) {
	if failedOrInterrupted(after.State) && !failedOrInterrupted(before.State) {
		// Unread, as the Task list's Needs you group counts it, unless a
		// page shows the Task as it ends.
		s.unseenEnd = !m.watchedLocked(s.id)
	}
	kind := noticeKind(after)
	if kind == "" || kind == noticeKind(before) || m.closed {
		return
	}
	title := noticeTitle(s, kind, after.Ask)
	m.broadcastLocked("notify", "", func(seq uint64) any {
		return noticeEvent{Seq: seq, SessionID: s.id, Kind: kind, Title: title, Key: noticeKey(s.id, kind, seq)}
	})
	// The owner is looking at the Task: no push. A service worker shows
	// every push it gets (browsers penalise or replace silent ones), so the
	// decision is made here.
	if m.onScreenLocked(s.id) {
		return
	}
	// broadcastLocked took the next seq whether or not a page listens.
	payload := pushPayload{Title: title, Task: s.id, Kind: kind, Key: noticeKey(s.id, kind, m.seq), Badge: m.needsYouLocked()}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.push.send(m.ctx, payload)
	}()
}

// maxPageID bounds the id a page names its streams with.
const maxPageID = 64

// validPageID accepts the ids pages make: letters, digits, '-' and '_'.
func validPageID(page string) bool {
	if page == "" || len(page) > maxPageID {
		return false
	}
	for _, r := range page {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// notePage names the browser tab sub's stream belongs to.
func (m *Manager) notePage(sub *Subscriber, page string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub.page = page
}

// SetViewing records the Task page shows while it is visible, "" when it
// shows none or is hidden. It holds for the page's open streams; a stream
// that ends takes it along.
func (m *Manager) SetViewing(page, task string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sub := range m.subs {
		if sub.page == page {
			sub.viewing = task
		}
	}
}

// onScreenLocked reports whether a visible page shows Task id.
func (m *Manager) onScreenLocked(id string) bool {
	for sub := range m.subs {
		if sub.page != "" && sub.viewing == id {
			return true
		}
	}
	return false
}

// handleViewing is POST /api/viewing with {"page","task"}: the Task a
// visible page shows ("" for none), so its notices are not pushed.
func (s *Server) handleViewing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Page string `json:"page"`
		Task string `json:"task"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !validPageID(body.Page) || len(body.Task) > maxPageID*2 {
		writeError(w, http.StatusBadRequest, "invalid page or task")
		return
	}
	s.m.SetViewing(body.Page, body.Task)
	w.WriteHeader(http.StatusNoContent)
}

func failedOrInterrupted(state string) bool { return state == StateFailed || state == StateInterrupted }

// watchedLocked reports whether a page has Task id open.
func (m *Manager) watchedLocked(id string) bool {
	for sub := range m.subs {
		if sub.session == id {
			return true
		}
	}
	return false
}

// needsYouLocked is the Task list's Needs you count, which the app badge
// carries (without the planner's requests): active Tasks waiting on a
// request, and failed or interrupted ones no page has opened since they
// ended (the service's stand-in for each browser's unread mark).
func (m *Manager) needsYouLocked() int {
	n := 0
	for _, s := range m.sessions {
		if s.stage != StageActive {
			continue
		}
		switch st := s.state(); {
		case st == StateAwaitingPermission || st == StateAwaitingAnswer:
			n++
		case failedOrInterrupted(st) && s.unseenEnd:
			n++
		}
	}
	return n
}

// noticeTitle is the notice's one line: "<Task> needs you: <request>",
// "<Task> failed" or "<Task> finished". The request is the summary's Ask,
// as the Task list shows it.
func noticeTitle(s *webSession, kind string, ask *Ask) string {
	name := clipNotice(cmp.Or(s.name, s.title, "New task"))
	switch kind {
	case noticeFailed:
		return name + " failed"
	case noticeFinished:
		return name + " finished"
	}
	var asked string
	if ask != nil {
		asked = clipNotice(ask.Title)
	}
	if asked == "" {
		return name + " needs you"
	}
	return name + " needs you: " + asked
}

// clipNotice folds whitespace and keeps at most maxNoticeText characters.
func clipNotice(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > maxNoticeText {
		return strings.TrimSpace(string(r[:maxNoticeText-1])) + "…"
	}
	return text
}

// pushSubscription is one browser's Web Push subscription. Subscriber is the
// page's origin when it is https (Apple's push service wants one), used as
// the VAPID contact.
type pushSubscription struct {
	webpush.Subscription
	Subscriber string    `json:"subscriber,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// pushFile is web-push.json in the config directory, owner-only: the VAPID
// key pair, generated once, and the subscribed browsers.
type pushFile struct {
	PublicKey     string             `json:"public_key"`
	PrivateKey    string             `json:"private_key"`
	Subscriptions []pushSubscription `json:"subscriptions,omitempty"`
}

// pushStore keeps web-push.json; it is read on first use.
type pushStore struct {
	mu     sync.Mutex
	path   string
	loaded bool
	file   pushFile
	// client sends to push services; tests replace it.
	client webpush.HTTPClient
}

func (p *pushStore) loadLocked() error {
	if p.loaded {
		return nil
	}
	if p.path == "" {
		return errors.New("push settings have no location")
	}
	data, err := os.ReadFile(p.path) // #nosec G304 -- UAM's own file next to its config.
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read push settings: %w", err)
	default:
		if err := json.Unmarshal(data, &p.file); err != nil {
			return fmt.Errorf("read push settings %s: %w", p.path, err)
		}
	}
	p.loaded = true
	return nil
}

func (p *pushStore) saveLocked() error {
	data, err := json.MarshalIndent(p.file, "", "  ") // #nosec G117 -- web-push.json holds the VAPID private key on purpose; it is written owner-only (0600).
	if err != nil {
		return err
	}
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create push settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, pushFileName+".tmp.*") // owner-only (0600)
	if err != nil {
		return fmt.Errorf("write push settings: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write push settings: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write push settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write push settings: %w", err)
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		return fmt.Errorf("write push settings: %w", err)
	}
	return nil
}

// publicKey returns the VAPID public key, generating and storing the key
// pair the first time.
func (p *pushStore) publicKey() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.loadLocked(); err != nil {
		return "", err
	}
	if p.file.PublicKey != "" && p.file.PrivateKey != "" {
		return p.file.PublicKey, nil
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", fmt.Errorf("generate push keys: %w", err)
	}
	p.file.PrivateKey, p.file.PublicKey = private, public
	// Subscriptions belong to the old key; they cannot be sent to.
	p.file.Subscriptions = nil
	if err := p.saveLocked(); err != nil {
		p.file = pushFile{}
		return "", err
	}
	return public, nil
}

// subscribe stores sub, replacing the browser's earlier subscription.
func (p *pushStore) subscribe(sub pushSubscription) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.loadLocked(); err != nil {
		return err
	}
	if p.file.PrivateKey == "" {
		return &Error{Status: http.StatusConflict, Message: "push keys are missing; reload the page"}
	}
	subs := slices.DeleteFunc(p.file.Subscriptions, func(s pushSubscription) bool { return s.Endpoint == sub.Endpoint })
	subs = append(subs, sub)
	if extra := len(subs) - maxPushSubscriptions; extra > 0 {
		subs = subs[extra:]
	}
	p.file.Subscriptions = subs
	return p.saveLocked()
}

// unsubscribe forgets the subscription at endpoint, if stored.
func (p *pushStore) unsubscribe(endpoint string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.loadLocked(); err != nil {
		return err
	}
	n := len(p.file.Subscriptions)
	p.file.Subscriptions = slices.DeleteFunc(p.file.Subscriptions, func(s pushSubscription) bool { return s.Endpoint == endpoint })
	if len(p.file.Subscriptions) == n {
		return nil
	}
	return p.saveLocked()
}

// send pushes payload to every subscription, one at a time, and forgets
// the ones the push service says are gone (404, 410). Failures are logged
// without the endpoint, which is a capability URL.
func (p *pushStore) send(ctx context.Context, payload pushPayload) {
	p.mu.Lock()
	if err := p.loadLocked(); err != nil {
		p.mu.Unlock()
		log.Warn("web push skipped", "error", err)
		return
	}
	subs := slices.Clone(p.file.Subscriptions)
	public, private, client := p.file.PublicKey, p.file.PrivateKey, p.client
	p.mu.Unlock()
	if len(subs) == 0 || private == "" {
		return
	}
	if client == nil {
		client = &http.Client{Timeout: pushSendWait}
	}
	urgency := webpush.UrgencyHigh
	if payload.Kind == noticeFinished {
		urgency = webpush.UrgencyNormal
	}
	// A Topic replaces a still-undelivered notice of the same Task.
	sum := sha256.Sum256([]byte(payload.Task))
	topic := hex.EncodeToString(sum[:16])
	var gone []string
	for _, sub := range subs {
		// A fresh message each time: the library pads it in place.
		message, err := json.Marshal(payload)
		if err != nil {
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, pushSendWait)
		resp, err := webpush.SendNotificationWithContext(sendCtx, message, &sub.Subscription, &webpush.Options{
			HTTPClient: client, Subscriber: cmp.Or(sub.Subscriber, defaultSubscriber), Topic: topic, TTL: pushTTL,
			Urgency: urgency, VAPIDPublicKey: public, VAPIDPrivateKey: private,
		})
		if err != nil {
			cancel()
			log.Warn("web push failed", "error", redactPushError(err, sub.Endpoint))
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			gone = append(gone, sub.Endpoint)
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			log.Warn("web push refused", "status", resp.StatusCode, "service", pushService(sub.Endpoint))
		}
	}
	for _, endpoint := range gone {
		if err := p.unsubscribe(endpoint); err != nil {
			log.Warn("forget an expired web push subscription failed", "error", err)
		}
	}
}

// pushService is an endpoint's host, safe to log.
func pushService(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return ""
}

// redactPushError keeps the endpoint's path out of a logged error.
func redactPushError(err error, endpoint string) string {
	return strings.ReplaceAll(err.Error(), endpoint, pushService(endpoint))
}

// validPushSubscription checks what a browser sent: an https endpoint and
// the P-256 public key and 16-byte secret Web Push encryption needs.
func validPushSubscription(sub webpush.Subscription) bool {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || len(sub.Endpoint) > maxPushEndpoint || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	key, auth := decodePushKey(sub.Keys.P256dh), decodePushKey(sub.Keys.Auth)
	return len(key) == 65 && key[0] == 4 && len(auth) == 16
}

func decodePushKey(s string) []byte {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b
		}
	}
	return nil
}

// handlePushKey is GET /api/push: the VAPID public key a browser subscribes with.
func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request) {
	key, err := s.m.push.publicKey()
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": key})
}

// handlePushSubscribe is POST /api/push/subscribe: store this browser's
// subscription (the PushSubscription's JSON).
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var body webpush.Subscription
	if !decodeBody(w, r, &body) {
		return
	}
	if !validPushSubscription(body) {
		writeError(w, http.StatusBadRequest, "invalid push subscription")
		return
	}
	sub := pushSubscription{Subscription: body, CreatedAt: time.Now().UTC()}
	if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "https://") {
		sub.Subscriber = origin
	}
	if err := s.m.push.subscribe(sub); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePushUnsubscribe is POST /api/push/unsubscribe with {"endpoint"}.
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.m.push.unsubscribe(body.Endpoint); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
