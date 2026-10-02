package web

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// notices drains sub until it has been quiet for a moment and returns the
// notify frames it saw.
func notices(t *testing.T, sub *Subscriber) []noticeEvent {
	t.Helper()
	var out []noticeEvent
	for {
		select {
		case raw := <-sub.Frames():
			if f := parseFrame(t, raw); f.event == "notify" {
				var n noticeEvent
				decodeField(t, f, "session_id", &n.SessionID)
				decodeField(t, f, "kind", &n.Kind)
				decodeField(t, f, "title", &n.Title)
				out = append(out, n)
			}
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

func TestNoticeKind(t *testing.T) {
	for _, tc := range []struct {
		s    SessionSummary
		want string
	}{
		{SessionSummary{State: StateAwaitingAnswer}, noticeQuestion},
		{SessionSummary{State: StateAwaitingPermission}, noticePermission},
		{SessionSummary{State: StateFailed}, noticeFailed},
		{SessionSummary{State: StateCompleted}, noticeFinished},
		{SessionSummary{State: StateCompleted, SubagentsRunning: 1}, ""},
		{SessionSummary{State: StateCompleted, BackgroundTasksRunning: 1}, ""},
		{SessionSummary{State: StateWorking}, ""},
		{SessionSummary{State: StateIdle}, ""},
		{SessionSummary{State: StateCancelled}, ""},
		{SessionSummary{State: StateAwaitingAnswer, Stage: StageSettled}, ""},
	} {
		if got := noticeKind(tc.s); got != tc.want {
			t.Errorf("noticeKind(%s, stage %q) = %q, want %q", tc.s.State, tc.s.Stage, got, tc.want)
		}
	}
}

// A Task that comes to need the owner, or finishes, is announced once per
// transition; staying in that state, or other changes, announce nothing.
func TestNoticesFireOncePerTransition(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(agentapi.Interaction{
		ID: "q1", Kind: agentapi.InteractionQuestion, Title: "Question from Copilot", State: agentapi.InteractionPending,
		Questions: []agentapi.Question{{Text: "Which   colour should it be?\nPick one.", Choices: []string{"red", "blue"}}},
	})
	conv.EmitTitle("a new title") // no transition
	got := notices(t, sub)
	if len(got) != 1 || got[0] != (noticeEvent{SessionID: sum.ID, Kind: noticeQuestion, Title: "task needs you: Which colour should it be?"}) {
		t.Fatalf("after a question: %+v", got)
	}
	if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Answers: [][]string{{"red"}}}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitTitle("another title")
	got = notices(t, sub)
	if len(got) != 1 || got[0] != (noticeEvent{SessionID: sum.ID, Kind: noticeFinished, Title: "task finished"}) {
		t.Fatalf("after finishing: %+v", got)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitTurn(agentapi.TurnFailed, "boom")
	if got = notices(t, sub); len(got) != 1 || got[0].Kind != noticeFailed || got[0].Title != "task failed" {
		t.Fatalf("after failing: %+v", got)
	}
}

// The pushed badge is the Task list's Needs you count: Tasks waiting on a
// request, and failed ones no page has opened since they failed.
func TestNeedsYouCountMatchesTheNeedsYouGroup(t *testing.T) {
	m, prov, _ := newTestManager(t)
	count := func() int {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.needsYouLocked()
	}
	asking, askConv := createSession(t, m, prov)
	askConv.EmitInteraction(agentapi.Interaction{ID: "p1", Kind: agentapi.InteractionPermission, Title: "Run", State: agentapi.InteractionPending})
	failed, failConv := createSession(t, m, prov)
	failConv.EmitTurn(agentapi.TurnWorking, "")
	failConv.EmitTurn(agentapi.TurnFailed, "boom")
	if n := count(); n != 2 {
		t.Fatalf("needs you = %d, want 2 (%s asking, %s failed unread)", n, asking.ID, failed.ID)
	}
	sub, _, err := m.Subscribe(failed.ID) // a page opens the failed Task
	if err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("needs you after opening the failed Task = %d, want 1", n)
	}
	// Failing again while that page shows it leaves it read.
	failConv.EmitTurn(agentapi.TurnWorking, "")
	failConv.EmitTurn(agentapi.TurnFailed, "again")
	if n := count(); n != 1 {
		t.Fatalf("needs you after failing on screen = %d, want 1", n)
	}
	m.Unsubscribe(sub)
}

func TestClipNotice(t *testing.T) {
	long := strings.Repeat("é", 150)
	got := clipNotice(long)
	if r := []rune(got); len(r) != maxNoticeText || !strings.HasSuffix(got, "…") {
		t.Fatalf("clipNotice = %q (%d runes)", got, len(r))
	}
	if got := clipNotice("  a\n\tb  "); got != "a b" {
		t.Fatalf("clipNotice = %q", got)
	}
}

// pushReceiver is a browser's side of a subscription: its keys, and the
// requests its push service received.
type pushReceiver struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newPushReceiver(t *testing.T) *pushReceiver {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return &pushReceiver{key: key, auth: auth}
}

func (r *pushReceiver) subscription(endpoint string) webpush.Subscription {
	return webpush.Subscription{Endpoint: endpoint, Keys: webpush.Keys{
		P256dh: base64.RawURLEncoding.EncodeToString(r.key.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(r.auth),
	}}
}

// decrypt opens an aes128gcm Web Push body (RFC 8291, RFC 8188).
func (r *pushReceiver) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("push body of %d bytes", len(body))
	}
	salt, idLen := body[:16], int(body[20])
	_ = binary.BigEndian.Uint32(body[16:20])
	serverKey, ciphertext := body[21:21+idLen], body[21+idLen:]
	pub, err := ecdh.P256().NewPublicKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := r.key.ECDH(pub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), r.key.PublicKey().Bytes()...), serverKey...)
	ikm, err := hkdf.Key(sha256.New, secret, r.auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt push: %v", err)
	}
	end := bytes.LastIndexByte(plain, 2)
	if end < 0 || len(bytes.Trim(plain[end+1:], "\x00")) != 0 {
		t.Fatal("push record padding is malformed")
	}
	return plain[:end]
}

type pushRequest struct {
	path   string
	header http.Header
	body   []byte
}

// A transition is pushed, encrypted, to every subscribed browser with a short
// TTL and a per-Task topic; a subscription its push service reports gone is
// forgotten. The key file is owner-only.
func TestPushSendsEncryptedNoticeAndForgetsExpired(t *testing.T) {
	m, prov, _ := newTestManager(t)
	var mu sync.Mutex
	var got []pushRequest
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, pushRequest{path: r.URL.Path, header: r.Header.Clone(), body: body})
		mu.Unlock()
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m.push.client = srv.Client()
	public, err := m.push.publicKey()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.push.publicKey(); again != public {
		t.Fatal("the VAPID key changed on the second call")
	}
	info, err := os.Stat(m.push.path)
	if err != nil || info.Mode().Perm() != 0o600 || filepath.Base(m.push.path) != pushFileName {
		t.Fatalf("push file %s: %v %v", m.push.path, info, err)
	}
	live, gone := newPushReceiver(t), newPushReceiver(t)
	for _, sub := range []pushSubscription{
		{Subscription: live.subscription(srv.URL + "/live"), Subscriber: "https://uam.example"},
		{Subscription: gone.subscription(srv.URL + "/gone")},
	} {
		if err := m.push.subscribe(sub); err != nil {
			t.Fatal(err)
		}
	}
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(agentapi.Interaction{ID: "p1", Kind: agentapi.InteractionPermission, Title: "Run a shell command", Detail: "rm -rf secret", State: agentapi.InteractionPending})
	waitUntil(t, "the expired subscription to be forgotten", func() bool {
		m.push.mu.Lock()
		defer m.push.mu.Unlock()
		return len(m.push.file.Subscriptions) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("push requests = %d, want 2", len(got))
	}
	req := got[0]
	if req.path != "/live" {
		req = got[1]
	}
	h := req.header
	if h.Get("TTL") != "1800" || h.Get("Urgency") != "high" || h.Get("Content-Encoding") != "aes128gcm" || len(h.Get("Topic")) != 32 ||
		!strings.HasPrefix(h.Get("Authorization"), "vapid t=") || !strings.HasSuffix(h.Get("Authorization"), "k="+public) {
		t.Fatalf("push headers: %v", h)
	}
	var payload map[string]any
	if err := json.Unmarshal(live.decrypt(t, req.body), &payload); err != nil {
		t.Fatal(err)
	}
	key, _ := payload["key"].(string)
	if !strings.HasPrefix(key, sum.ID+":permission:") {
		t.Fatalf("payload key = %q", key)
	}
	want := map[string]any{"title": "task needs you: Run a shell command", "task": sum.ID, "kind": noticePermission, "key": key, "badge": float64(1)}
	if len(payload) != len(want) {
		t.Fatalf("payload = %v, want %v", payload, want)
	}
	for k, v := range want {
		if payload[k] != v {
			t.Fatalf("payload = %v, want %v", payload, want)
		}
	}
	data, _ := os.ReadFile(m.push.path)
	if strings.Contains(string(data), "/gone") || !strings.Contains(string(data), "/live") {
		t.Fatalf("stored subscriptions: %s", data)
	}
}

func TestPushRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	if w := ts.do(http.MethodGet, "/api/push", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out GET /api/push = %d", w.Code)
	}
	w := ts.do(http.MethodGet, "/api/push", "", auth)
	var key struct {
		PublicKey string `json:"public_key"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &key) != nil || len(decodePushKey(key.PublicKey)) != 65 {
		t.Fatalf("GET /api/push = %d %s", w.Code, w.Body)
	}
	r := newPushReceiver(t)
	good, _ := json.Marshal(r.subscription("https://push.example/send/abc"))
	for _, bad := range []webpush.Subscription{
		r.subscription("http://push.example/send/abc"),
		r.subscription("https://user@push.example/x"),
		{Endpoint: "https://push.example/x", Keys: webpush.Keys{P256dh: "AAAA", Auth: "AAAA"}},
	} {
		body, _ := json.Marshal(bad)
		if w := ts.do(http.MethodPost, "/api/push/subscribe", string(body), auth); w.Code != http.StatusBadRequest {
			t.Fatalf("subscribe %s = %d", body, w.Code)
		}
	}
	if w := ts.do(http.MethodPost, "/api/push/subscribe", string(good), auth, withHeader("Origin", "http://127.0.0.1:8260")); w.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d %s", w.Code, w.Body)
	}
	ts.m.push.mu.Lock()
	n := len(ts.m.push.file.Subscriptions)
	ts.m.push.mu.Unlock()
	if n != 1 {
		t.Fatalf("subscriptions = %d", n)
	}
	if w := ts.do(http.MethodPost, "/api/push/unsubscribe", `{"endpoint":"https://push.example/send/abc"}`, auth); w.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d %s", w.Code, w.Body)
	}
	ts.m.push.mu.Lock()
	n = len(ts.m.push.file.Subscriptions)
	ts.m.push.mu.Unlock()
	if n != 0 {
		t.Fatalf("subscriptions after unsubscribe = %d", n)
	}
}

func TestServiceWorkerIsRevalidated(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>app</title>")},
		"sw.js":      {Data: []byte("self.addEventListener('push', () => {})")},
	}})
	w := ts.do(http.MethodGet, "/sw.js", "")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != noCache || !strings.Contains(w.Header().Get("Content-Type"), "javascript") ||
		w.Header().Get("Content-Security-Policy") != contentSecurity {
		t.Fatalf("GET /sw.js = %d %v", w.Code, w.Header())
	}
}

// A Task a visible page shows is not pushed (the service worker shows every
// push, so the decision is the service's); the notify event still reaches
// the pages, with the same key a push for it carries.
func TestNoPushForTheTaskOnScreen(t *testing.T) {
	m, prov, _ := newTestManager(t)
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m.push.client = srv.Client()
	if _, err := m.push.publicKey(); err != nil {
		t.Fatal(err)
	}
	browser := newPushReceiver(t)
	if err := m.push.subscribe(pushSubscription{Subscription: browser.subscription(srv.URL + "/live")}); err != nil {
		t.Fatal(err)
	}
	pushes := func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(bodies)
	}
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	m.notePage(sub, "page-1")
	m.SetViewing("page-1", sum.ID)
	frameKey := func() string {
		t.Helper()
		for {
			select {
			case raw := <-sub.Frames():
				if f := parseFrame(t, raw); f.event == "notify" {
					var key string
					decodeField(t, f, "key", &key)
					return key
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no notify event")
			}
		}
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(agentapi.Interaction{ID: "q1", Kind: agentapi.InteractionQuestion, Title: "Which?", State: agentapi.InteractionPending, Questions: []agentapi.Question{{Text: "Which?", Choices: []string{"x", "y"}}}})
	if key := frameKey(); !strings.HasPrefix(key, sum.ID+":question:") {
		t.Fatalf("notify key = %q", key)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(pushes()); n != 0 {
		t.Fatalf("pushes while the Task is on screen = %d, want 0", n)
	}
	// The page went to the background: the next notice is pushed.
	m.SetViewing("page-1", "")
	if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Answers: [][]string{{"x"}}}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	key := frameKey()
	waitUntil(t, "a push", func() bool { return len(pushes()) == 1 })
	var payload pushPayload
	if err := json.Unmarshal(browser.decrypt(t, pushes()[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Key != key || payload.Kind != noticeFinished {
		t.Fatalf("push key %q kind %q, notify key %q", payload.Key, payload.Kind, key)
	}
}

func TestViewingRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sub, _, err := ts.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer ts.m.Unsubscribe(sub)
	ts.m.notePage(sub, "page-1")
	for _, bad := range []string{`{"page":"","task":"x"}`, `{"page":"` + strings.Repeat("a", 65) + `","task":"x"}`, `{"page":"a b","task":"x"}`} {
		if w := ts.do(http.MethodPost, "/api/viewing", bad, auth); w.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/viewing %s = %d", bad, w.Code)
		}
	}
	if w := ts.do(http.MethodPost, "/api/viewing", `{"page":"page-1","task":"t1"}`, auth); w.Code != http.StatusNoContent {
		t.Fatalf("POST /api/viewing = %d %s", w.Code, w.Body)
	}
	ts.m.mu.Lock()
	on := ts.m.onScreenLocked("t1")
	ts.m.mu.Unlock()
	if !on {
		t.Fatal("t1 is not on screen after the page said so")
	}
	ts.m.Unsubscribe(sub)
	ts.m.mu.Lock()
	on = ts.m.onScreenLocked("t1")
	ts.m.mu.Unlock()
	if on {
		t.Fatal("t1 still on screen after its page's stream closed")
	}
}
