package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestResolveFilesEnvelopeAndCaps(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _, _ := rawImageTask(t, ts)
	path := "/api/sessions/" + sum.ID + "/files/resolve"
	body := func(paths []string) string {
		raw, _ := json.Marshal(map[string]any{"paths": paths})
		return string(raw)
	}
	paths := make([]string, 64)
	for i := range paths {
		paths[i] = fmt.Sprintf("missing-%d", i)
	}
	valid := body(paths)
	for _, tc := range []struct {
		input  string
		status int
	}{
		{`{"paths":["notes.txt","missing","notes.txt"]}`, 200},
		{valid, 200}, {body(append(paths, "missing-0")), 200}, {body(append(paths, "extra")), 400},
		{body([]string{strings.Repeat("x", 4096)}), 200}, {body([]string{strings.Repeat("x", 4097)}), 400},
		{`{}`, 400}, {`null`, 400}, {`[]`, 400}, {`{"paths":[]}`, 400}, {`{"paths":null}`, 400},
		{`{"paths":[null]}`, 400}, {`{"paths":[1]}`, 400}, {`{"paths":[""]}`, 400},
		{`{"paths":["bad\u0000path"]}`, 400}, {"{\"paths\":[\"" + string([]byte{0xff}) + "\"]}", 400},
		{valid + `{}`, 400}, {valid + strings.Repeat(" ", maxResolveBodyBytes-len(valid)), 200},
		{valid + strings.Repeat(" ", maxResolveBodyBytes-len(valid)+1), 413},
	} {
		if got := ts.do(http.MethodPost, path, tc.input, withCookie(ts)); got.Code != tc.status {
			t.Errorf("body length %d: status %d, want %d", len(tc.input), got.Code, tc.status)
		}
	}
	w := ts.do(http.MethodPost, path, `{"paths":["notes.txt","missing","notes.txt"]}`, withCookie(ts))
	var response struct {
		Files []resolvedFile `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := []resolvedFile{{"notes.txt", true, "file"}, {"missing", false, "unavailable"}}
	if !reflect.DeepEqual(response.Files, want) || w.Header().Get("Location") != "" {
		t.Fatalf("unexpected resolver results: %+v", response.Files)
	}
	for _, tc := range []struct {
		target string
		opts   []reqOpt
		status int
	}{
		{path, nil, 401}, {path, []reqOpt{withHeader("Cookie", cookieName+"=invalid")}, 401},
		{"/api/sessions/missing/files/resolve", []reqOpt{withCookie(ts)}, 404},
		{path, []reqOpt{withCookie(ts), withHeader("Origin", "https://foreign.example")}, 403},
		{path, []reqOpt{withCookie(ts), withHeader("Content-Type", "text/plain")}, 415},
	} {
		if w := ts.do(http.MethodPost, tc.target, valid, tc.opts...); w.Code != tc.status {
			t.Errorf("middleware status %d, want %d", w.Code, tc.status)
		}
	}
}

func TestResolveFilesConfinedReadableAndCanceled(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, real, outside := rawImageTask(t, ts)
	literal := "文%#.bin"
	if err := os.WriteFile(filepath.Join(real, literal), []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(real, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "unreadable"), nil, 0000); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path      string
		available bool
	}{
		{literal, true}, {"./" + literal, true}, {"shots/../" + literal, true},
		{filepath.Join(sum.Workdir, literal), true}, {filepath.Join(real, literal), true},
		{"inside-link.png", true}, {"empty.png", true}, {"fake.png", true},
		{"outside-link.png", false}, {"outside-dir/secret.png", false},
		{filepath.Join(outside, "secret.png"), false}, {"../outside/secret.png", false},
		{"missing", false}, {"shots", false}, {"pipe", false},
	} {
		got, err := ts.m.resolveFiles(context.Background(), sum.ID, []string{tc.path})
		if err != nil || len(got) != 1 || got[0].Exists != tc.available || got[0].Path != tc.path {
			t.Fatalf("path %q: %+v, %v", tc.path, got, err)
		}
	}
	if os.Geteuid() != 0 && readableTaskFile(sum.Workdir, "unreadable") {
		t.Fatal("unreadable file resolved available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := ts.m.resolveFiles(ctx, sum.ID, []string{literal}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled lookup returned %+v, %v", got, err)
	}
}
