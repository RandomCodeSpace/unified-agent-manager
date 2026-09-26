package web

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

var responseGzipPool = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return writer
}}

func serveCompressed(w http.ResponseWriter, r *http.Request, next http.Handler) {
	writer := &compressedResponse{
		ResponseWriter: w,
		acceptsGzip:    acceptsGzip(r.Header.Values("Accept-Encoding")),
		head:           r.Method == http.MethodHead,
		ranged:         r.Header.Get("Range") != "",
	}
	defer writer.close()
	next.ServeHTTP(writer, r)
}

// An explicit gzip preference takes precedence over a wildcard, including a
// refusal or invalid quality. Tokens must match in full, never by substring.
func acceptsGzip(values []string) bool {
	explicit, wildcard := false, false
	seenGzip := false
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			coding, _, _ := strings.Cut(entry, ";")
			coding = strings.ToLower(strings.TrimSpace(coding))
			if coding != "gzip" && coding != "*" {
				continue
			}
			_, params, err := mime.ParseMediaType(entry)
			allowed := err == nil && len(params) <= 1 && !strings.Contains(entry, `"`)
			if quality, exists := params["q"]; exists {
				allowed = allowed && positiveQuality(quality)
			} else {
				allowed = allowed && len(params) == 0
			}
			if coding == "gzip" {
				// A refusal also wins over a duplicate positive gzip entry.
				explicit = allowed && (!seenGzip || explicit)
				seenGzip = true
			} else {
				wildcard = allowed
			}
		}
	}
	return explicit || (!seenGzip && wildcard)
}

func positiveQuality(q string) bool {
	if q == "" || (q[0] != '0' && q[0] != '1') {
		return false
	}
	positive := q[0] == '1'
	if len(q) == 1 {
		return positive
	}
	if q[1] != '.' || len(q) > 5 {
		return false
	}
	for _, digit := range q[2:] {
		if digit < '0' || digit > '9' || (q[0] == '1' && digit != '0') {
			return false
		}
		positive = positive || digit != '0'
	}
	return positive
}

func compressibleType(value string) bool {
	kind, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return strings.HasPrefix(kind, "text/") || kind == "application/json" ||
		strings.HasSuffix(kind, "+json") || kind == "application/javascript" ||
		kind == "application/x-javascript" || kind == "image/svg+xml"
}

func varyAcceptEncoding(h http.Header) {
	for _, value := range h.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if field = strings.TrimSpace(field); field == "*" || strings.EqualFold(field, "Accept-Encoding") {
				return
			}
		}
	}
	h.Add("Vary", "Accept-Encoding")
}

type compressedResponse struct {
	http.ResponseWriter
	gzip        *gzip.Writer
	acceptsGzip bool
	head        bool
	ranged      bool
	wroteHeader bool
}

// ResponseController unwraps this writer to preserve deadlines and cancellation.
func (w *compressedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *compressedResponse) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	h := w.Header()
	varyAcceptEncoding(h)
	if w.acceptsGzip && !w.ranged && status >= 200 && status != http.StatusNoContent &&
		status != http.StatusResetContent && status != http.StatusNotModified && status != http.StatusPartialContent &&
		h.Get("Content-Encoding") == "" && h.Get("Content-Disposition") == "" && compressibleType(h.Get("Content-Type")) {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		// HEAD describes the GET representation without writing a gzip member.
		if !w.head {
			w.gzip = responseGzipPool.Get().(*gzip.Writer)
			w.gzip.Reset(w.ResponseWriter)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *compressedResponse) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		if _, set := w.Header()["Content-Type"]; !set && w.Header().Get("Content-Encoding") == "" && len(p) != 0 {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.head {
		return len(p), nil
	}
	if w.gzip != nil {
		return w.gzip.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// FlushError commits even tiny SSE frames before flushing the HTTP transport,
// and reports either failure so the handler can release its subscription.
func (w *compressedResponse) FlushError() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzip != nil {
		if err := w.gzip.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *compressedResponse) close() {
	if w.gzip != nil {
		_ = w.gzip.Close()
		w.gzip.Reset(io.Discard) // The pool must not retain the connection.
		responseGzipPool.Put(w.gzip)
		w.gzip = nil
	}
}
