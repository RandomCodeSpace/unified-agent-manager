package web

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const eventSocketPath = "/api/events/socket"

// handleEventSocket carries one event stream over a WebSocket. Over plain
// HTTP/1.1 a browser allows six connections per host, and each tab holds an
// event stream or more, so a few tabs would hold them all and stall every
// request; WebSockets have a pool of their own. The stream is the same
// request, cookie and all, served by ServeHTTP, so its authentication and
// handler are the ones a direct request gets.
//
// The first message is the stream's HTTP status as text; on 200 the stream's
// bytes follow as binary messages, and the socket closes when the stream ends.
// Like the terminal, the handshake is refused cross-site and from an Origin
// other than this Host or a public origin.
func (s *Server) handleEventSocket(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	target, ok := eventSocketTarget(r.URL.Query().Get("path"))
	if !ok {
		writeError(w, http.StatusBadRequest, "path must be an event stream")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.terminalOrigins, CompressionMode: websocket.CompressionNoContextTakeover})
	if err != nil {
		// Accept has answered.
		return
	}
	ctx := conn.CloseRead(r.Context())
	stream := r.Clone(ctx)
	stream.URL = target
	stream.RequestURI = target.RequestURI()
	stream.Body = http.NoBody
	for _, name := range []string{"Upgrade", "Connection", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Extensions", "Sec-Websocket-Protocol", headerAcceptEncoding} {
		stream.Header.Del(name)
	}
	stream.Header.Set("Accept", "text/event-stream")
	out := &socketStream{conn: conn, ctx: ctx, header: make(http.Header)}
	s.ServeHTTP(out, stream)
	out.WriteHeader(http.StatusOK) // a handler that wrote nothing
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// eventSocketTarget is raw as a request target when it is one of the event
// streams, this instance's or a connected instance's, and nothing else.
func eventSocketTarget(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/") || path.Clean(u.Path) != u.Path {
		return nil, false
	}
	stream := u.Path
	if rest, ok := strings.CutPrefix(stream, "/api/connected/"); ok {
		id, inner, found := strings.Cut(rest, "/")
		if !found || id == "" || (inner != "api/events" && inner != "api/events/detail") {
			return nil, false
		}
		return u, true
	}
	switch stream {
	case "/api/events", "/api/events/detail", "/api/connected-notifications/events":
		return u, true
	}
	return nil, false
}

// socketStream is the ResponseWriter of a stream carried by an event socket.
type socketStream struct {
	conn     *websocket.Conn
	ctx      context.Context
	header   http.Header
	status   int
	deadline time.Time
	err      error
}

func (s *socketStream) Header() http.Header { return s.header }

// WriteHeader sends the status once. An answer the browser's EventSource
// would refuse, a 200 that is not an event stream, is sent as a failure.
func (s *socketStream) WriteHeader(status int) {
	if s.status != 0 || status < http.StatusOK {
		return
	}
	s.status = status
	if kind, _, _ := mime.ParseMediaType(s.header.Get(headerContentType)); status == http.StatusOK && kind != "text/event-stream" {
		s.status = http.StatusBadGateway
	}
	_ = s.send(websocket.MessageText, []byte(strconv.Itoa(s.status)))
}

// Write sends the stream's bytes. The body of a failure stays here: an
// EventSource does not read it either.
func (s *socketStream) Write(p []byte) (int, error) {
	s.WriteHeader(http.StatusOK)
	if s.status != http.StatusOK {
		return len(p), nil
	}
	if err := s.send(websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *socketStream) send(kind websocket.MessageType, p []byte) error {
	if s.err != nil {
		return s.err
	}
	ctx := s.ctx
	if !s.deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, s.deadline)
		defer cancel()
	}
	s.err = s.conn.Write(ctx, kind, p)
	return s.err
}

// FlushError reports a failed send: each Write is sent as it comes.
func (s *socketStream) FlushError() error { return s.err }

// SetWriteDeadline bounds the sends, as it bounds writes to a connection.
func (s *socketStream) SetWriteDeadline(deadline time.Time) error {
	s.deadline = deadline
	return nil
}
