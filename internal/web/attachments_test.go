package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func encoded(t *testing.T, enc func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := enc(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngBytes(t *testing.T) []byte {
	return encoded(t, func(b *bytes.Buffer, img image.Image) error { return png.Encode(b, img) })
}

var (
	webpBytes = []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00\x30\x01\x00\x9d\x01\x2a\x01\x00\x01\x00\x02\x00\x34\x25\xa4\x00\x03\x70\x00\xfe\xfb\x94\x00\x00")
	pdfBytes  = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")
)

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var mediaModels = []agentapi.Model{
	{ID: "auto", Name: "Auto"},
	{ID: "vision", Name: "Vision", Media: &agentapi.Media{Images: true, MaxImages: 1, Types: []string{"image/png", "image/jpeg"}}},
	{ID: "docs", Name: "Docs", Media: &agentapi.Media{Images: true, PDF: true, MaxImages: 5}},
	{ID: "blind", Name: "Blind", Media: &agentapi.Media{}},
}

func uploadTask(t *testing.T, model string) (*Manager, *agenttest.Provider, SessionSummary, *agenttest.Conversation, *storeDir) {
	t.Helper()
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels(mediaModels, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir()), Model: model})
	if err != nil {
		t.Fatal(err)
	}
	return m, prov, sum, prov.Last(), &storeDir{filepath.Dir(st.Path())}
}

type storeDir struct{ dir string }

func (d *storeDir) task(id string) string { return filepath.Join(d.dir, uploadsDir, id) }

func mustUpload(t *testing.T, m *Manager, id, name string, data []byte) agentapi.Attachment {
	t.Helper()
	att, err := m.Upload(id, name, data)
	if err != nil {
		t.Fatalf("Upload(%s): %v", name, err)
	}
	return att
}

func TestUploadSniffsTheBytesAndAppliesLimits(t *testing.T) {
	m, _, sum, _, dirs := uploadTask(t, "docs")
	jpg := encoded(t, func(b *bytes.Buffer, img image.Image) error { return jpeg.Encode(b, img, nil) })
	gifData := encoded(t, func(b *bytes.Buffer, img image.Image) error { return gif.Encode(b, img, nil) })
	for _, tc := range []struct {
		name string
		data []byte
		mime string
	}{
		{"shot.png", pngBytes(t), "image/png"},
		{"photo.txt", jpg, "image/jpeg"},
		{"anim.gif", gifData, "image/gif"},
		{"pic.webp", webpBytes, "image/webp"},
		{"spec.pdf", pdfBytes, "application/pdf"},
		{"../../notes.md", []byte("# notes\nsecond line\n"), "text/plain"},
		{"page.html", []byte("<!doctype html><html><script>alert(1)</script></html>"), "text/plain"},
		{"big.txt", bytes.Repeat([]byte("a"), maxTextBytes), "text/plain"},
		{"bm.txt", []byte("BM notes: bitmap looks start like this"), "text/plain"},
		{"tags.txt", []byte("ID3 tags are read first"), "text/plain"},
		{"print.txt", []byte("%!PS-Adobe-3.0 is a PostScript header"), "text/plain"},
		{"term.log", []byte("\x1b[31mred\x1b[0m\n"), "text/plain"},
	} {
		att, err := m.Upload(sum.ID, tc.name, tc.data)
		if err != nil || att.MIME != tc.mime || att.Size != int64(len(tc.data)) || !validRequestID(att.ID) {
			t.Fatalf("%s = %+v, %v; want %s", tc.name, att, err, tc.mime)
		}
		if tc.name == "../../notes.md" && att.Name != "notes.md" {
			t.Fatalf("name = %q, want the base name", att.Name)
		}
	}
	for _, tc := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"empty.txt", nil, http.StatusBadRequest},
		{"logo.png", []byte(`<?xml version="1.0"?><!-- x --><svg xmlns="http://www.w3.org/2000/svg"></svg>`), http.StatusUnsupportedMediaType},
		{"logo.svg", []byte("\xef\xbb\xbf <SVG></SVG>"), http.StatusUnsupportedMediaType},
		{"archive.png", []byte("PK\x03\x04\x14\x00\x00\x00\x08\x00fake zip"), http.StatusUnsupportedMediaType},
		{"photo.heic", []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), http.StatusUnsupportedMediaType},
		{"song.txt", []byte("ID3\x03\x00\x00\x00\x00\x00\x00mp3"), http.StatusUnsupportedMediaType},
		{"prog.png", []byte("MZ\x90\x00\x03\x00\x00\x00"), http.StatusUnsupportedMediaType},
		{"nul.txt", []byte("text\x00with a NUL"), http.StatusUnsupportedMediaType},
		{"utf16.txt", []byte("\xff\xfeh\x00i\x00"), http.StatusUnsupportedMediaType},
		{"latin1.txt", []byte("caf\xe9"), http.StatusUnsupportedMediaType},
		{"huge.png", append(pngBytes(t), make([]byte, maxImageBytes)...), http.StatusRequestEntityTooLarge},
		{"huge.pdf", append(append([]byte{}, pdfBytes...), make([]byte, maxPDFBytes)...), http.StatusRequestEntityTooLarge},
		{"huge.txt", bytes.Repeat([]byte("a"), maxTextBytes+1), http.StatusRequestEntityTooLarge},
	} {
		if _, err := m.Upload(sum.ID, tc.name, tc.data); statusOf(err) != tc.status {
			t.Fatalf("%s = %v, want %d", tc.name, err, tc.status)
		}
	}

	root := filepath.Join(dirs.dir, uploadsDir)
	for dir, mode := range map[string]os.FileMode{root: 0o700, dirs.task(sum.ID): 0o700} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %v, %v", dir, info.Mode(), err)
		}
	}
	files, _ := os.ReadDir(dirs.task(sum.ID))
	if len(files) != 24 {
		t.Fatalf("stored %d files, want 12 uploads and 12 records", len(files))
	}
	for _, f := range files {
		if info, _ := f.Info(); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", f.Name(), info.Mode())
		}
	}
	if strings.HasPrefix(dirs.task(sum.ID), sum.Workdir) {
		t.Fatal("uploads must live outside the project directory")
	}
}

func TestUploadsFollowTheModelGate(t *testing.T) {
	m, _, sum, _, _ := uploadTask(t, "blind")
	png1 := pngBytes(t)
	if _, err := m.Upload(sum.ID, "a.png", png1); statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "model blind does not accept images") {
		t.Fatalf("image on a blind model = %v", err)
	}
	if _, err := m.Upload(sum.ID, "a.pdf", pdfBytes); statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "PDF") {
		t.Fatalf("PDF on a blind model = %v", err)
	}
	text := mustUpload(t, m, sum.ID, "a.txt", []byte("hello"))

	model := "vision"
	if _, err := m.SetModel(sum.ID, &model, nil, nil); err != nil {
		t.Fatal(err)
	}
	gifData := encoded(t, func(b *bytes.Buffer, img image.Image) error { return gif.Encode(b, img, nil) })
	if _, err := m.Upload(sum.ID, "a.gif", gifData); statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "image/gif") {
		t.Fatalf("unlisted image type = %v", err)
	}
	if _, err := m.Upload(sum.ID, "a.pdf", pdfBytes); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("PDF without application/pdf = %v", err)
	}
	a := mustUpload(t, m, sum.ID, "a.png", png1)
	b := mustUpload(t, m, sum.ID, "b.png", append(append([]byte{}, png1...), 0))
	send := func(ids ...string) error {
		_, err := m.Submit(sum.ID, PromptRequest{Text: "look", RequestID: mustUUID(t), Attachments: ids})
		return err
	}
	if err := send(a.ID, b.ID); statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "accepts at most 1 image per prompt") {
		t.Fatalf("two images on a one-image model = %v", err)
	}

	// The gate applies when the prompt is sent, not only at upload.
	blind := "blind"
	if _, err := m.SetModel(sum.ID, &blind, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := send(a.ID); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("image after switching to a blind model = %v", err)
	}
	auto := "auto"
	if _, err := m.SetModel(sum.ID, &auto, nil, nil); err != nil {
		t.Fatal(err)
	}
	mustUpload(t, m, sum.ID, "c.pdf", pdfBytes)
	if err := send(a.ID, b.ID, text.ID); err != nil {
		t.Fatalf("auto reports no gate and accepts images: %v", err)
	}

	// The browser gets the gate with the model list.
	raw, _ := json.Marshal(m.Providers()[0].Models)
	if !strings.Contains(string(raw), `"media":{"images":true,"pdf":false,"max_images":1,"types":["image/png","image/jpeg"]}`) ||
		!strings.Contains(string(raw), `"media":{"images":false,"pdf":false}`) || strings.Count(string(raw), `"media"`) != 3 {
		t.Fatalf("model list = %s", raw)
	}
}

func TestPromptAttachmentsSendOnceAndStay(t *testing.T) {
	m, _, sum, conv, dirs := uploadTask(t, "docs")
	img, doc := pngBytes(t), []byte("codeword: PLUM")
	a := mustUpload(t, m, sum.ID, "shot.png", img)
	b := mustUpload(t, m, sum.ID, "note.txt", doc)
	for _, ids := range [][]string{{"nope"}, {a.ID, a.ID, b.ID, "0f3e6a0e-4a7b-4c7e-9d1a-0c2b3a4d5e6f"}} {
		if _, err := m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Attachments: ids}); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("attachments %v = %v", ids, err)
		}
	}
	var six []string
	for i := range 6 {
		six = append(six, mustUpload(t, m, sum.ID, "n.txt", []byte{'a' + byte(i)}).ID)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Attachments: six}); statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "at most 5") {
		t.Fatalf("six attachments = %v", err)
	}

	rid := mustUUID(t)
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "what is this?", RequestID: rid, Attachments: []string{a.ID, b.ID}})
	if err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("send = %+v, %v", sub, err)
	}
	want := []agentapi.Blob{{Name: "shot.png", MIME: "image/png", Data: img}, {Name: "note.txt", MIME: "text/plain", Data: doc}}
	if p := conv.Prompts(); len(p) != 1 || len(p[0].Attachments) != 2 || p[0].Attachments[0].Name != want[0].Name ||
		!bytes.Equal(p[0].Attachments[0].Data, img) || p[0].Attachments[1].MIME != "text/plain" || !bytes.Equal(p[0].Attachments[1].Data, doc) {
		t.Fatalf("sent %+v", conv.Prompts())
	}
	// A retried request ID returns the recorded outcome; nothing is sent or
	// uploaded again.
	if again, err := m.Submit(sum.ID, PromptRequest{Text: "what is this?", RequestID: rid, Attachments: []string{a.ID, b.ID}}); err != nil || again != sub || len(conv.Prompts()) != 1 {
		t.Fatalf("retry = %+v, %v, sends %d", again, err, len(conv.Prompts()))
	}
	files, _ := os.ReadDir(dirs.task(sum.ID))
	if len(files) != 16 {
		t.Fatalf("stored %d files after a retry, want 8 uploads and 8 records", len(files))
	}
	var record upload
	data, _ := os.ReadFile(filepath.Join(dirs.task(sum.ID), a.ID+".json"))
	if json.Unmarshal(data, &record) != nil || record.UsedAt.IsZero() || record.SHA256 != sha(img) {
		t.Fatalf("record after send = %s", data)
	}

	// A steer and a queued prompt keep their attachments.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "steer", RequestID: mustUUID(t), Mode: ModeSteer, Attachments: []string{b.ID}}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("steer with an attachment = %+v, %v", sub, err)
	}
	if p := conv.SteerPrompts(); len(p) != 1 || p[0].Text != "steer" || len(p[0].Attachments) != 1 || !bytes.Equal(p[0].Attachments[0].Data, doc) {
		t.Fatalf("steered %+v", p)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "later", RequestID: mustUUID(t), Mode: ModeQueue, Attachments: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}
	if q := detail(t, m, sum.ID).Queue; len(q) != 1 || len(q[0].Attachments) != 1 || q[0].Attachments[0] != b {
		t.Fatalf("queue = %+v, want %+v", q, b)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "queued prompt sent", func() bool { return len(conv.Prompts()) == 2 })
	if p := conv.Prompts()[1]; len(p.Attachments) != 1 || !bytes.Equal(p.Attachments[0].Data, doc) {
		t.Fatalf("drained %+v", p)
	}

	conv.EmitTurn(agentapi.TurnCompleted, "")
	m.prov().SetCommands([]agentapi.Command{{Name: "review"}}, nil)
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review", Attachments: []string{a.ID}}); err != nil {
		t.Fatal(err)
	}
	if runs := conv.CommandRuns(); len(runs) != 1 || len(runs[0].Args.Attachments) != 1 || !bytes.Equal(runs[0].Args.Attachments[0].Data, img) {
		t.Fatalf("command runs = %+v", runs)
	}
}

// prov returns the only provider of a test manager.
func (m *Manager) prov() *agenttest.Provider {
	return m.providers[m.order[0]].(*agenttest.Provider)
}

func TestTranscriptAttachmentsLinkToTheStoredCopy(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels(mediaModels, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir()), Model: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	img := pngBytes(t)
	a := mustUpload(t, m, sum.ID, "shot.png", img)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "see", RequestID: mustUUID(t), Attachments: []string{a.ID}}); err != nil {
		t.Fatal(err)
	}
	user := agentapi.Item{ID: "u1", Kind: agentapi.ItemUser, Text: "see", Attachments: []agentapi.Attachment{
		{Name: "shot.png", MIME: "image/png", SHA256: sha(img)},
		{Name: "gone.pdf", MIME: "application/pdf", SHA256: sha([]byte("other"))},
	}}
	prov.Last().EmitItem(user)
	items := detail(t, m, sum.ID).Items
	wantLinked := []agentapi.Attachment{{ID: a.ID, Name: "shot.png", MIME: "image/png", Size: int64(len(img)), SHA256: sha(img)}, {Name: "gone.pdf", MIME: "application/pdf", SHA256: sha([]byte("other"))}}
	if len(items) != 1 || len(items[0].Attachments) != 2 || items[0].Attachments[0] != wantLinked[0] || items[0].Attachments[1] != wantLinked[1] {
		t.Fatalf("items = %+v", items)
	}
	raw, _ := json.Marshal(items[0])
	if !strings.Contains(string(raw), `"attachments":[{"id":"`+a.ID+`","name":"shot.png","mime":"image/png","size":`) ||
		strings.Contains(string(raw), "sha256") || strings.Contains(string(raw), sha(img)) || !strings.Contains(string(raw), `{"name":"gone.pdf","mime":"application/pdf"}`) {
		t.Fatalf("item JSON = %s", raw)
	}

	// After a restart the stored copies load from disk, and the reopened
	// history links to them.
	// A directory whose Task is gone is removed then.
	orphan := filepath.Join(filepath.Dir(st.Path()), uploadsDir, mustUUID(t))
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	convID := sum.ConversationID
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.SetModels(mediaModels, nil)
	prov2.AddConversation(convID, []agentapi.Item{user})
	m2 := startManager(t, st, prov2)
	if err := m2.View(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	if items := detail(t, m2, sum.ID).Items; len(items) != 1 || items[0].Attachments[0].ID != a.ID {
		t.Fatalf("history items = %+v", items)
	}
	if got, data, _, err := m2.Attachment(sum.ID, a.ID); err != nil || got.MIME != "image/png" || !bytes.Equal(data, img) {
		t.Fatalf("stored copy after restart = %+v, %v", got, err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("an unknown task's upload directory survived a start: %v", err)
	}
}

func TestUploadsGoWithTheTaskAndExpireUnused(t *testing.T) {
	m, _, sum, conv, dirs := uploadTask(t, "docs")
	used := mustUpload(t, m, sum.ID, "used.txt", []byte("used"))
	queued := mustUpload(t, m, sum.ID, "queued.txt", []byte("queued"))
	stale := mustUpload(t, m, sum.ID, "stale.txt", []byte("stale"))
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Attachments: []string{used.ID}}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "y", RequestID: mustUUID(t), Mode: ModeQueue, Attachments: []string{queued.ID}}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(uploadExpiry + time.Minute)
	m.now = func() time.Time { return later }
	m.sweepUploads()
	for id, kept := range map[string]bool{used.ID: true, queued.ID: true, stale.ID: false} {
		_, _, _, err := m.Attachment(sum.ID, id)
		_, statErr := os.Stat(filepath.Join(dirs.task(sum.ID), id))
		if (err == nil) != kept || (statErr == nil) != kept {
			t.Fatalf("upload %s: kept=%v, lookup %v, file %v", id, kept, err, statErr)
		}
	}

	conv.EmitTurn(agentapi.TurnCompleted, "")
	// The queue empties before the drained prompt is sent; end that turn only
	// once the provider has it, so the send cannot mark the Task busy after.
	waitUntil(t, "queued prompt sent", func() bool { return len(conv.Prompts()) == 2 })
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "turn ended", func() bool { return !busy(detail(t, m, sum.ID).State) })
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs.task(sum.ID)); !os.IsNotExist(err) {
		t.Fatalf("uploads survived deleting the task: %v", err)
	}

	other, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	mustUpload(t, m, other.ID, "a.txt", []byte("a"))
	if _, err := m.Archive(other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Upload(other.ID, "b.txt", []byte("b")); statusOf(err) != http.StatusConflict {
		t.Fatalf("upload to an archived task = %v", err)
	}
	if err := m.RemoveProject(other.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs.task(other.ID)); !os.IsNotExist(err) {
		t.Fatalf("uploads survived removing the project: %v", err)
	}
}

func TestAttachmentRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	ts.prov.SetModels(mediaModels, nil)
	ts.m.RefreshModels()
	auth := withCookie(ts)
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, ts.m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/sessions/" + sum.ID + "/attachments"
	octet := withHeader("Content-Type", "application/octet-stream")
	img := pngBytes(t)

	w := ts.do(http.MethodPost, base+"?name=shot.png", string(img), auth, octet)
	var att agentapi.Attachment
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &att) != nil || att.MIME != "image/png" || att.Name != "shot.png" || att.Size != int64(len(img)) {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(w.Body.String(), `{"id":"`+att.ID+`","name":"shot.png","mime":"image/png","size":`) {
		t.Fatalf("upload JSON = %s", w.Body)
	}
	for _, tc := range []struct {
		what string
		opts []reqOpt
		code int
	}{
		{"JSON type", []reqOpt{auth}, http.StatusUnsupportedMediaType},
		{"browser type", []reqOpt{auth, withHeader("Content-Type", "image/png")}, http.StatusUnsupportedMediaType},
		{"form type", []reqOpt{auth, withHeader("Content-Type", "multipart/form-data; boundary=x")}, http.StatusUnsupportedMediaType},
		{"cross-site", []reqOpt{auth, octet, withHeader("Sec-Fetch-Site", "cross-site")}, http.StatusForbidden},
		{"foreign origin", []reqOpt{auth, octet, withHeader("Origin", "https://evil.example")}, http.StatusForbidden},
		{"no cookie", []reqOpt{octet}, http.StatusUnauthorized},
		{"foreign host", []reqOpt{auth, octet, withHost("evil.example")}, http.StatusForbidden},
	} {
		if w := ts.do(http.MethodPost, base+"?name=x.png", string(img), tc.opts...); w.Code != tc.code {
			t.Fatalf("%s = %d %s, want %d", tc.what, w.Code, w.Body, tc.code)
		}
	}
	if w := ts.do(http.MethodPost, base, strings.Repeat("a", maxUploadBytes+1), auth, octet); w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "at most 10 MiB") {
		t.Fatalf("oversized upload = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, base, `<svg xmlns="http://www.w3.org/2000/svg"/>`, auth, octet); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("SVG upload = %d %s", w.Code, w.Body)
	}
	// Only the upload route takes a non-JSON body, and the others keep the
	// 1 MiB cap.
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/prompt", `{"text":"x"}`, auth, octet); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("octet-stream prompt = %d", w.Code)
	}
	if w := ts.do(http.MethodPost, base+"/extra", string(img), auth, octet); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("octet-stream to another path = %d", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/prompt", `{"text":"`+strings.Repeat("x", maxBodyBytes)+`"}`, auth); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large JSON prompt = %d", w.Code)
	}

	w = ts.do(http.MethodGet, base+"/"+att.ID, "", auth)
	h := w.Header()
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), img) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != `inline; filename=shot.png` || h.Get("Content-Security-Policy") != contentSecurity {
		t.Fatalf("serve image = %d %v", w.Code, h)
	}
	w = ts.do(http.MethodPost, base+"?name="+`r%C3%A9sum%C3%A9%22.html`, "<html><body>hi</body></html>", auth, octet)
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &att) != nil {
		t.Fatalf("html as text = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodGet, base+"/"+att.ID, "", auth)
	if h := w.Header(); h.Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment; filename*=utf-8''r%C3%A9sum%C3%A9%22.html") {
		t.Fatalf("serve text = %v", h)
	}
	if w := ts.do(http.MethodGet, base+"/"+mustUUID(t), "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown attachment = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, base+"/"+att.ID, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("serve without cookie = %d", w.Code)
	}
	other, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, ts.m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+other.ID+"/attachments/"+att.ID, "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("another task's attachment = %d", w.Code)
	}
}

// toolItem is a completed tool call that returned images.
func toolItem(id, agentID string, images ...agentapi.Image) agentapi.Item {
	return agentapi.Item{ID: id, Kind: agentapi.ItemTool, AgentID: agentID, Tool: &agentapi.ToolCall{Name: "view", Status: agentapi.ToolCompleted}, Images: images}
}

func toolImage(name string, data []byte) agentapi.Image {
	return agentapi.Image{Name: name, MIME: "image/png", SHA256: sha(data), Data: data}
}

func TestToolImagesAreSniffedDedupedAndKeptWithTheTask(t *testing.T) {
	m, prov, sum, conv, dirs := uploadTask(t, "docs")
	img := pngBytes(t)
	gifData := encoded(t, func(b *bytes.Buffer, img image.Image) error { return gif.Encode(b, img, nil) })
	conv.EmitItem(toolItem("t1", "",
		toolImage("../shots/shot.png", img),
		toolImage("again.png", img),
		agentapi.Image{MIME: "image/jpeg", Data: gifData}, // the type comes from the bytes
		agentapi.Image{MIME: "image/webp", Data: webpBytes},
		toolImage("logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)),
		toolImage("doc.png", pdfBytes),
		toolImage("huge.png", append(append([]byte{}, img...), make([]byte, maxToolImageBytes)...)),
		agentapi.Image{MIME: "image/png", SHA256: sha([]byte("never stored"))},
	))
	m.imageWG.Wait()
	items := detail(t, m, sum.ID).Items
	if len(items) != 1 || len(items[0].Images) != 3 {
		t.Fatalf("items = %+v", items)
	}
	got := items[0].Images
	for i, want := range []struct{ name, mime string }{{"shot.png", "image/png"}, {"", "image/gif"}, {"", "image/webp"}} {
		if !validRequestID(got[i].ID) || got[i].Name != want.name || got[i].MIME != want.mime || got[i].Data != nil {
			t.Fatalf("image %d = %+v, want %+v", i, got[i], want)
		}
	}
	if got[0].Size != int64(len(img)) {
		t.Fatalf("size = %d", got[0].Size)
	}
	wantNote := "4 images not kept: not a png, jpeg, gif or webp image; over 5 MiB; the provider did not record its bytes"
	if items[0].ImagesNote != wantNote {
		t.Fatalf("note = %q, want %q", items[0].ImagesNote, wantNote)
	}
	raw, _ := json.Marshal(items[0])
	if !strings.Contains(string(raw), `"images":[{"id":"`+got[0].ID+`","mime":"image/png","size":`+strconv.Itoa(len(img))+`,"name":"shot.png"},{"id":"`+got[1].ID+`","mime":"image/gif","size":`) ||
		!strings.Contains(string(raw), `"images_note":"`+wantNote+`"`) || strings.Contains(string(raw), sha(img)) || strings.Contains(string(raw), "data") {
		t.Fatalf("item JSON = %s", raw)
	}

	// The same bytes from another call, or from a subagent, name the same
	// stored copy.
	prov.Last().EmitSubagent(agentapi.Subagent{ID: "agent-1", Name: "helper", Status: agentapi.SubagentRunning})
	conv.EmitItem(toolItem("t2", "", toolImage("other.png", img)))
	conv.EmitItem(toolItem("t1", "agent-1", toolImage("sub.png", img)))
	m.imageWG.Wait()
	if items := detail(t, m, sum.ID).Items; len(items) != 2 || len(items[1].Images) != 1 || items[1].Images[0].ID != got[0].ID || items[1].ImagesNote != "" {
		t.Fatalf("items after a repeat = %+v", items)
	}
	sub, err := m.Subagent(sum.ID, "agent-1")
	if err != nil || len(sub.Items) != 1 || len(sub.Items[0].Images) != 1 || sub.Items[0].Images[0].ID != got[0].ID {
		t.Fatalf("subagent detail = %+v, %v", sub, err)
	}

	// Stored beside the uploads, owner-only, with a record marked tool.
	files, _ := os.ReadDir(dirs.task(sum.ID))
	if len(files) != 6 {
		t.Fatalf("stored %d files, want 3 images and 3 records", len(files))
	}
	for _, f := range files {
		if info, _ := f.Info(); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", f.Name(), info.Mode())
		}
	}
	var record upload
	data, _ := os.ReadFile(filepath.Join(dirs.task(sum.ID), got[0].ID+".json"))
	if json.Unmarshal(data, &record) != nil || !record.Tool || record.SHA256 != sha(img) || record.Name != "shot.png" {
		t.Fatalf("record = %s", data)
	}
	if att, stored, _, err := m.Attachment(sum.ID, got[1].ID); err != nil || att.MIME != "image/gif" || !bytes.Equal(stored, gifData) {
		t.Fatalf("stored gif = %+v, %v", att, err)
	}

	// A tool image never expires and is not a prompt attachment.
	later := time.Now().Add(uploadExpiry + time.Hour)
	m.now = func() time.Time { return later }
	m.sweepUploads()
	if _, _, _, err := m.Attachment(sum.ID, got[0].ID); err != nil {
		t.Fatalf("tool image expired: %v", err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Attachments: []string{got[0].ID}}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("tool image as an attachment = %v", err)
	}

	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs.task(sum.ID)); !os.IsNotExist(err) {
		t.Fatalf("tool images survived deleting the task: %v", err)
	}
}

// Emit must not block: images are stored on their own goroutine, the item
// is published at once without them and again once they are stored.
func TestEmitReturnsWhileToolImagesStore(t *testing.T) {
	m, _, sum, conv, _ := uploadTask(t, "docs")
	img := pngBytes(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	m.storeImageHook = func() {
		entered <- struct{}{}
		<-release
	}
	returned := make(chan struct{})
	go func() {
		conv.EmitItem(toolItem("t1", "", toolImage("shot.png", img)))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("Emit waited for image storage")
	}
	<-entered // storage is under way and held
	items := detail(t, m, sum.ID).Items
	if len(items) != 1 || items[0].Tool == nil || len(items[0].Images) != 0 || items[0].ImagesNote != "" {
		t.Fatalf("item while its images store = %+v", items)
	}
	// Nothing else waits on it either.
	conv.EmitItem(toolItem("t2", ""))
	if items := detail(t, m, sum.ID).Items; len(items) != 2 {
		t.Fatalf("items while images store = %+v", items)
	}
	close(release)
	m.imageWG.Wait()
	items = detail(t, m, sum.ID).Items
	if len(items[0].Images) != 1 || !validRequestID(items[0].Images[0].ID) || items[0].Images[0].Name != "shot.png" {
		t.Fatalf("item after its images stored = %+v", items[0])
	}
	if _, data, _, err := m.Attachment(sum.ID, items[0].Images[0].ID); err != nil || !bytes.Equal(data, img) {
		t.Fatalf("stored image = %v", err)
	}
	// Images on anything but a tool item are dropped.
	conv.EmitItem(agentapi.Item{ID: "u1", Kind: agentapi.ItemAssistant, Text: "hi", Images: []agentapi.Image{toolImage("x.png", img)}, ImagesNote: "n"})
	if it := detail(t, m, sum.ID).Items[2]; it.Images != nil || it.ImagesNote != "" {
		t.Fatalf("non-tool item kept images: %+v", it)
	}
}

func TestToolImagesStopAtTheTaskCap(t *testing.T) {
	m, _, sum, conv, dirs := uploadTask(t, "docs")
	img := pngBytes(t)
	var first []agentapi.Image
	for i := range maxToolImages {
		first = append(first, toolImage("", append(append([]byte{}, img...), byte(i))))
	}
	conv.EmitItem(toolItem("t1", "", first...))
	extra := append(append([]byte{}, img...), 0xff, 0xff)
	conv.EmitItem(toolItem("t2", "", toolImage("", extra), first[7]))
	m.imageWG.Wait()
	items := detail(t, m, sum.ID).Items
	if len(items[0].Images) != maxToolImages || items[0].ImagesNote != "" {
		t.Fatalf("first item kept %d images, note %q", len(items[0].Images), items[0].ImagesNote)
	}
	if got := items[1]; len(got.Images) != 1 || got.Images[0].ID != items[0].Images[7].ID || got.ImagesNote != "1 image not kept: a task keeps at most 50 images" {
		t.Fatalf("item past the cap = %+v", got)
	}
	if files, _ := os.ReadDir(dirs.task(sum.ID)); len(files) != 2*maxToolImages {
		t.Fatalf("stored %d files, want %d", len(files), 2*maxToolImages)
	}
	// Uploads do not count against the cap, and the cap does not stop them.
	mustUpload(t, m, sum.ID, "note.txt", []byte("note"))
}

func TestToolImagesFromStaleConversationAreNotStored(t *testing.T) {
	m, _, sum, conv, dirs := uploadTask(t, "docs")
	s, err := m.lookup(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The old provider may deliver an event after UAM has replaced its
	// conversation. It must not create either a row or an image file.
	m.mu.Lock()
	s.gen++
	m.mu.Unlock()
	conv.EmitItem(toolItem("stale", "", toolImage("shot.png", pngBytes(t))))
	m.imageWG.Wait()
	if items := detail(t, m, sum.ID).Items; len(items) != 0 {
		t.Fatalf("stale conversation added items: %+v", items)
	}
	if files, err := os.ReadDir(dirs.task(sum.ID)); !os.IsNotExist(err) && (err != nil || len(files) != 0) {
		t.Fatalf("stale conversation stored files: %v, %v", files, err)
	}
}

func TestToolImagesSurviveARestart(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	img, later := pngBytes(t), append(pngBytes(t), 1)
	prov.Last().EmitItem(toolItem("t1", "", toolImage("shot.png", img)))
	m.imageWG.Wait()
	stored := detail(t, m, sum.ID).Items[0].Images[0]

	// A directory whose Task is gone is removed at start.
	orphan := filepath.Join(filepath.Dir(st.Path()), uploadsDir, mustUUID(t))
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, mustUUID(t)+".json"), []byte(`{"tool":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The provider's record keeps the first image by digest only, and the
	// second with its bytes, which UAM had not stored.
	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(sum.ConversationID, []agentapi.Item{
		toolItem("t1", "", agentapi.Image{MIME: "image/png", SHA256: strings.ToUpper(sha(img))}),
		toolItem("t2", "", toolImage("", later)),
	})
	m2 := startManager(t, st, prov2)
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("an unknown task's image directory survived a start: %v", err)
	}
	if err := m2.View(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	items := detail(t, m2, sum.ID).Items
	if len(items) != 2 || len(items[0].Images) != 1 || !reflect.DeepEqual(items[0].Images[0], stored) || len(items[1].Images) != 1 || !validRequestID(items[1].Images[0].ID) {
		t.Fatalf("history items = %+v, want %+v first", items, stored)
	}
	if _, data, _, err := m2.Attachment(sum.ID, items[1].Images[0].ID); err != nil || !bytes.Equal(data, later) {
		t.Fatalf("image stored from history = %v", err)
	}
}

func TestToolImageRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	sub, _, err := ts.m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.m.Unsubscribe(sub)
	img := pngBytes(t)
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", Name: "helper", Status: agentapi.SubagentRunning})
	conv.EmitItem(toolItem("t1", "", toolImage("", img)))
	conv.EmitItem(toolItem("t1", "agent-1", toolImage("shot.png", img)))

	// The item frame comes at once without its images, then again with them.
	var f frame
	var it agentapi.Item
	for it.Images == nil {
		f = frameOf(t, sub, "item")
		decodeField(t, f, "item", &it)
	}
	if len(it.Images) != 1 || !validRequestID(it.Images[0].ID) || !strings.Contains(string(f.data["item"]), `"images":[{"id":"`+it.Images[0].ID+`","mime":"image/png","size":`+strconv.Itoa(len(img))+`}]`) {
		t.Fatalf("item frame = %s", f.data["item"])
	}
	id := it.Images[0].ID
	for _, path := range []string{"/api/sessions/" + sum.ID, "/api/sessions/" + sum.ID + "/subagents/agent-1"} {
		w := ts.do(http.MethodGet, path, "", auth)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"images":[{"id":"`+id+`"`) {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body)
		}
	}

	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/attachments/"+id, "", auth)
	h := w.Header()
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), img) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != "inline" || h.Get("Content-Security-Policy") != contentSecurity || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("serve tool image = %d %v", w.Code, h)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/attachments/"+id, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("serve without cookie = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/attachments/"+id, "", auth, withHost("evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("serve to a foreign host = %d", w.Code)
	}
	other, _ := createSession(t, ts.m, ts.prov)
	if w := ts.do(http.MethodGet, "/api/sessions/"+other.ID+"/attachments/"+id, "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("another task's image = %d", w.Code)
	}
}

// History loading and importing bypass conversation.Open, but must still keep tool images.
func TestToolImagesInReadOnlyHistoryAndImport(t *testing.T) {
	for _, imported := range []bool{false, true} {
		t.Run(fmt.Sprint("import=", imported), func(t *testing.T) {
			img := pngBytes(t)
			h := agentapi.History{Items: []agentapi.Item{toolItem("image", "", toolImage("shot.png", img))}}
			var m *Manager
			var id string
			if imported {
				manager, prov, _, project := importManager(t)
				m = manager
				prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
				prov.SetHistory(prevA, h)
				sum, err := m.Import(context.Background(), project.ID, prevA)
				if err != nil {
					t.Fatal(err)
				}
				id = sum.ID
			} else {
				manager, prov, _, tasks := closedTasks(t, StageSettled)
				m, id = manager, tasks[0].ID
				prov.SetHistory(tasks[0].ConversationID, h)
				if err := m.View(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			d := waitHistory(t, m, id, HistoryLoaded)
			if len(d.Items) != 1 || len(d.Items[0].Images) != 1 {
				t.Fatalf("images lost from history: %+v", d.Items)
			}
			stored := d.Items[0].Images[0]
			if stored.ID == "" || len(stored.Data) != 0 {
				t.Fatalf("image is not a stored reference: %+v", stored)
			}
			if _, data, _, err := m.Attachment(id, stored.ID); err != nil || !bytes.Equal(data, img) {
				t.Fatalf("stored image = %v", err)
			}
		})
	}
}
