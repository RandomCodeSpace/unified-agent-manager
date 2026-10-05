package web

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"time"
)

const noticesUnavailable = "connected notifications unavailable"

func (s *Server) initConnectedNotifications() {
	if s.connections == nil {
		return
	}
	s.notices = newConnectedNotices(s.m, s.connections.InstanceID(), filepath.Join(filepath.Dir(s.m.store.Path()), connectedNoticeFile))
	h := s.notices
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			changed := s.connections.Changed()
			sources := []noticeSource{}
			for _, saved := range s.connections.List() {
				if !saved.Enabled || !slices.Contains(saved.Capabilities, "notices-v1") {
					continue
				}
				target, lifetime, err := s.connections.Acquire(saved.ID)
				if err != nil {
					continue
				}
				sources = append(sources, noticeSource{ID: target.ID, InstanceID: target.InstanceID, Label: target.Label, Generation: target.Generation, Context: lifetime,
					Open: func(ctx context.Context, cursor string) (*http.Response, error) {
						transport, err := s.connectionTransport(target)
						if err != nil {
							return nil, err
						}
						request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.BaseURL+"/api/federation/notices?cursor="+url.QueryEscape(cursor), nil)
						if err != nil {
							return nil, err
						}
						request.Header.Set("Authorization", "Bearer "+target.Credential)
						request.Header.Set(headerUAMInstance, target.InstanceID)
						request.Header.Set("Accept", "text/event-stream")
						request.Close = true
						// RoundTrip never follows a redirect with the saved grant.
						return transport.RoundTrip(request)
					}})
			}
			h.reconcile(sources)
			select {
			case <-h.ctx.Done():
				return
			case <-changed:
			}
		}
	}()
}

func (s *Server) connectedNotificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/federation/notices", s.handleFederationNotices)
	mux.HandleFunc("GET /api/connected-notifications/events", s.handleConnectedNoticeEvents)
	mux.HandleFunc("POST /api/connected-notifications/viewing", s.handleConnectedNoticeViewing)
}

func noticeStreamWriter(w http.ResponseWriter) func([]byte) bool {
	rc := http.NewResponseController(w)
	w.Header().Set(headerContentType, "text/event-stream")
	w.Header().Set(headerCacheControl, "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return func(frame []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteWait))
		if _, err := w.Write(frame); err != nil {
			return false
		} // #nosec G705 -- fixed SSE names and JSON-encoded values.
		return rc.Flush() == nil
	}
}

func (s *Server) handleFederationNotices(w http.ResponseWriter, r *http.Request) {
	if s.connections == nil {
		writeError(w, http.StatusServiceUnavailable, noticesUnavailable)
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" {
		cursor = r.Header.Get("Last-Event-ID")
	}
	if len(cursor) > 160 {
		writeError(w, http.StatusBadRequest, "invalid notice cursor")
		return
	}
	reader, baseline, replay := s.m.subscribeNotices(cursor)
	defer s.m.unsubscribeNotices(reader)
	write := noticeStreamWriter(w)
	instance := s.connections.InstanceID()
	send := func(name string, event sourceNotice) bool {
		event.InstanceID = instance
		frame, _ := encodeFrame(name, event)
		return write(frame)
	}
	if !send("snapshot", baseline) {
		return
	}
	for _, event := range replay {
		name := "attention"
		if event.Kind != "" {
			name = "notice"
		}
		if !send(name, event) {
			return
		}
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.m.ctx.Done():
			return
		case <-reader.gone:
			return
		case event := <-reader.ch:
			name := "attention"
			if event.Kind != "" {
				name = "notice"
			}
			if !send(name, event) {
				return
			}
		case <-ticker.C:
			if !write([]byte(": keep-alive\n\n")) {
				return
			}
		}
	}
}

func (s *Server) handleConnectedNoticeEvents(w http.ResponseWriter, r *http.Request) {
	h := s.notices
	if h == nil {
		writeError(w, http.StatusServiceUnavailable, noticesUnavailable)
		return
	}
	page := r.URL.Query().Get("page")
	if !validPageID(page) {
		writeError(w, http.StatusBadRequest, "invalid page")
		return
	}
	reader := &connectedNoticeReader{page: page, ch: make(chan []byte, subscriberQueue), gone: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, noticesUnavailable)
		return
	}
	h.readers[reader] = struct{}{}
	initial, _ := encodeFrame("attention", h.attentionLocked())
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.readers, reader); h.mu.Unlock() }()
	write := noticeStreamWriter(w)
	if !write(initial) {
		return
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.ctx.Done():
			return
		case <-reader.gone:
			return
		case frame := <-reader.ch:
			if !write(frame) {
				return
			}
		case <-ticker.C:
			if !write([]byte(": keep-alive\n\n")) {
				return
			}
		}
	}
}

func (s *Server) handleConnectedNoticeViewing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Page         string `json:"page"`
		ConnectionID string `json:"connection_id"`
		Generation   uint64 `json:"generation"`
		Task         string `json:"task"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !validPageID(body.Page) || len(body.Task) > 128 {
		writeError(w, http.StatusBadRequest, "invalid page or task")
		return
	}
	h := s.notices
	if h == nil {
		writeError(w, http.StatusServiceUnavailable, noticesUnavailable)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if body.Task != "" {
		source := h.sources[body.ConnectionID]
		if source == nil || source.source.Generation != body.Generation || source.ctx.Err() != nil {
			writeError(w, http.StatusConflict, "notification source is no longer enabled")
			return
		}
	}
	for reader := range h.readers {
		if reader.page == body.Page {
			reader.connection, reader.task = body.ConnectionID, body.Task
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
