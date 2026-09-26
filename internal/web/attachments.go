package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Upload limits, checked before anything reaches a provider.
const (
	maxAttachments = 5
	maxImageBytes  = 3 << 20
	maxPDFBytes    = 10 << 20
	maxTextBytes   = maxPromptBytes
	// maxUploadBytes caps the upload route's body; the other routes keep
	// maxBodyBytes.
	maxUploadBytes = maxPDFBytes
	// uploadExpiry is how long an upload no prompt used is kept.
	uploadExpiry  = 24 * time.Hour
	sweepInterval = time.Hour
	uploadsDir    = agentapi.UploadsDir
	mimePDF       = "application/pdf"
	mimeText      = "text/plain"
	// Limits for the images tools return, kept beside the uploads.
	maxToolImageBytes = 5 << 20
	maxToolImages     = 50
)

var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// upload is one stored attachment of a Task: <id> holds the bytes and
// <id>.json this record, in the Task's directory under uploadsDir.
type upload struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
	// UsedAt is set once a prompt carrying it was accepted, or may have
	// been; a used upload lives as long as its Task.
	UsedAt time.Time `json:"used_at,omitzero"`
	// Tool marks an image a tool's result returned rather than an upload.
	// It lives as long as its Task and is never a prompt attachment.
	Tool bool `json:"tool,omitempty"`
}

func (u *upload) info() agentapi.Attachment {
	return agentapi.Attachment{ID: u.ID, Name: u.Name, MIME: u.MIME, Size: u.Size}
}

func isImage(mime string) bool { return slices.Contains(imageTypes, mime) }

// uploadRoot is where uploads live: next to sessions.json, never in a
// project directory.
func (m *Manager) uploadRoot() string {
	return filepath.Join(filepath.Dir(m.store.Path()), uploadsDir)
}

func (m *Manager) taskUploadDir(taskID string) string {
	return filepath.Join(m.uploadRoot(), taskID)
}

// sniff returns the MIME type UAM stores and sends for data, from the bytes
// alone: png, jpeg, gif, webp, PDF, or UTF-8 text without a NUL byte, sent
// as text/plain. Everything else, SVG included, is refused.
func sniff(data []byte) (string, error) {
	if len(data) == 0 {
		return "", newError(http.StatusBadRequest, "the file is empty")
	}
	detected := http.DetectContentType(data)
	switch {
	case isImage(detected):
		if len(data) > maxImageBytes {
			return "", newError(http.StatusRequestEntityTooLarge, "images can be at most 3 MiB")
		}
		return detected, nil
	case detected == mimePDF && bytes.HasPrefix(data, []byte("%PDF-")):
		if len(data) > maxPDFBytes {
			return "", newError(http.StatusRequestEntityTooLarge, "PDF files can be at most 10 MiB")
		}
		return mimePDF, nil
	// Any other UTF-8 without a NUL is text, whatever DetectContentType makes
	// of its first bytes (a note starting "BM" or "ID3", or one with an escape).
	case bytes.IndexByte(data, 0) < 0 && utf8.Valid(data):
		if svgRoot(data) {
			return "", newError(http.StatusUnsupportedMediaType, "SVG images cannot be attached")
		}
		if len(data) > maxTextBytes {
			return "", newError(http.StatusRequestEntityTooLarge, "text files can be at most 256 KiB")
		}
		return mimeText, nil
	}
	return "", newError(http.StatusUnsupportedMediaType, "only png, jpeg, gif and webp images, PDF files and UTF-8 text files can be attached")
}

// svgRoot reports whether the document's first element is <svg>, after an
// optional byte order mark, XML declaration, doctype and comments.
func svgRoot(data []byte) bool {
	rest := bytes.TrimPrefix(data[:min(len(data), 64<<10)], []byte("\xef\xbb\xbf"))
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		var end []byte
		switch {
		case bytes.HasPrefix(rest, []byte("<?")):
			end = []byte("?>")
		case bytes.HasPrefix(rest, []byte("<!--")):
			end = []byte("-->")
		case bytes.HasPrefix(rest, []byte("<!")):
			end = []byte(">")
		default:
			return len(rest) >= 4 && bytes.EqualFold(rest[:4], []byte("<svg"))
		}
		i := bytes.Index(rest, end)
		if i < 0 {
			return false
		}
		rest = rest[i+len(end):]
	}
}

// cleanUploadName keeps a file's base name, made safe to show.
func cleanUploadName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = clipRunes(strings.TrimSpace(displaytext.Sanitize(name)), maxNameRunes)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	return name
}

// mediaAllowed applies the model's gate to one upload. A model without a
// reported gate accepts every type UAM accepts.
func mediaAllowed(model string, media *agentapi.Media, mime string) error {
	if media == nil || mime == mimeText {
		return nil
	}
	label := cmpName(model)
	switch {
	case mime == mimePDF && !media.PDF:
		return newError(http.StatusBadRequest, "%s does not accept PDF files", label)
	case isImage(mime) && !media.Images:
		return newError(http.StatusBadRequest, "%s does not accept images", label)
	case len(media.Types) > 0 && !slices.Contains(media.Types, mime):
		return newError(http.StatusBadRequest, "%s does not accept %s files", label, mime)
	}
	return nil
}

func cmpName(model string) string {
	if model == "" {
		return "the default model"
	}
	return "model " + model
}

// mediaLocked is the gate of the Task's selected model.
func (m *Manager) mediaLocked(s *webSession) *agentapi.Media {
	return m.modelLocked(s.provider, s.model).Media
}

// Upload stores one file for the Task and returns its record. The type is
// sniffed from the bytes, and images and PDFs must pass the Task's model
// gate.
func (m *Manager) Upload(id, name string, data []byte) (agentapi.Attachment, error) {
	return m.upload(id, name, data, nil)
}

// A draft may target a queued model without changing the running turn.
func (m *Manager) upload(id, name string, data []byte, selectedModel *string) (agentapi.Attachment, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.Attachment{}, err
	}
	mime, err := sniff(data)
	if err != nil {
		return agentapi.Attachment{}, err
	}
	m.mu.Lock()
	model := s.model
	if selectedModel != nil {
		model = *selectedModel
	}
	switch {
	case s.removed:
		err = newError(http.StatusNotFound, "session not found")
	case m.closed:
		err = errShuttingDown
	case selectedModel != nil && (model == "" || m.modelLocked(s.provider, model).ID == ""):
		err = newError(http.StatusBadRequest, "model must be an offered model ID")
	default:
		if err = s.readOnlyLocked(); err == nil {
			err = mediaAllowed(model, m.modelLocked(s.provider, model).Media, mime)
		}
	}
	m.mu.Unlock()
	if err != nil {
		return agentapi.Attachment{}, err
	}
	uid, err := newUUID()
	if err != nil {
		return agentapi.Attachment{}, fmt.Errorf("generate attachment id: %w", err)
	}
	sum := sha256.Sum256(data)
	u := &upload{ID: uid, Name: cleanUploadName(name), MIME: mime, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), CreatedAt: m.now()}
	if err := m.storeUpload(s, u, data); err != nil {
		if errors.Is(err, errTaskRemoved) {
			return agentapi.Attachment{}, newError(http.StatusNotFound, "session not found")
		}
		return agentapi.Attachment{}, fmt.Errorf("store attachment: %w", err)
	}
	m.sweepUploads()
	return u.info(), nil
}

// errTaskRemoved reports a Task deleted while one of its files was stored.
var errTaskRemoved = errors.New("task removed")

// storeUpload writes an upload or tool image and records it on the Task. A
// Task deleted meanwhile keeps nothing: the file goes with it, and the error
// is errTaskRemoved.
func (m *Manager) storeUpload(s *webSession, u *upload, data []byte) error {
	dir := m.taskUploadDir(s.id)
	if err := writeUpload(m.uploadRoot(), dir, u, data); err != nil {
		return err
	}
	m.mu.Lock()
	removed := s.removed
	if !removed {
		if s.uploads == nil {
			s.uploads = map[string]*upload{}
		}
		s.uploads[u.ID] = u
	}
	m.mu.Unlock()
	if removed {
		removeUploads(dir)
		return errTaskRemoved
	}
	return nil
}

// writeUpload stores data and its record owner-only: 0700 directories and
// 0600 files.
func writeUpload(root, dir string, u *upload, data []byte) error {
	for _, d := range []string{root, dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if info, err := os.Lstat(d); err != nil || !info.IsDir() {
			return fmt.Errorf("%s is not a directory", d)
		}
		if err := os.Chmod(d, 0o700); err != nil { // #nosec G302 -- owner-only directory; needs the execute bit.
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, u.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- dir is UAM's; the ID is a generated UUID.
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return writeUploadRecord(dir, u)
}

// writeUploadRecord replaces <id>.json atomically.
func writeUploadRecord(dir string, u *upload) error {
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, u.ID+".json"))
}

func removeUploads(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		log.Warn("remove web attachments failed", "dir", dir, "error", err)
	}
}

// loadUploads reads every Task's uploads when the service starts, and
// removes the directories of Tasks that no longer exist.
func (m *Manager) loadUploads() {
	entries, err := os.ReadDir(m.uploadRoot())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("read web attachments failed", "error", err)
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !validRequestID(e.Name()) {
			continue
		}
		dir := m.taskUploadDir(e.Name())
		m.mu.Lock()
		s := m.sessions[e.Name()]
		m.mu.Unlock()
		if s == nil {
			removeUploads(dir)
			continue
		}
		uploads := readUploads(dir)
		m.mu.Lock()
		s.uploads = uploads
		m.mu.Unlock()
	}
}

func readUploads(dir string) map[string]*upload {
	out := map[string]*upload{}
	files, err := os.ReadDir(dir)
	if err != nil {
		log.Warn("read web attachments failed", "dir", dir, "error", err)
		return out
	}
	for _, f := range files {
		id, ok := strings.CutSuffix(f.Name(), ".json")
		if !ok || !validRequestID(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name())) // #nosec G304 -- UAM's own directory; the name matched a UUID.
		var u upload
		if err != nil || json.Unmarshal(data, &u) != nil || u.ID != id {
			continue
		}
		if info, err := os.Lstat(filepath.Join(dir, id)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		out[id] = &u
	}
	return out
}

// sweepUploads deletes uploads that no prompt used within uploadExpiry. An
// upload a queued prompt carries is in use.
func (m *Manager) sweepUploads() {
	cutoff := m.now().Add(-uploadExpiry)
	type expired struct{ dir, id string }
	var gone []expired
	m.mu.Lock()
	for _, s := range m.sessions {
		for id, u := range s.uploads {
			if u.Tool || !u.UsedAt.IsZero() || !u.CreatedAt.Before(cutoff) || s.queuedUpload(id) {
				continue
			}
			delete(s.uploads, id)
			gone = append(gone, expired{m.taskUploadDir(s.id), id})
		}
	}
	m.mu.Unlock()
	for _, e := range gone {
		for _, name := range []string{e.id, e.id + ".json", e.id + ".d"} {
			if err := os.RemoveAll(filepath.Join(e.dir, name)); err != nil {
				log.Warn("remove expired web attachment failed", "error", err)
			}
		}
	}
}

func (s *webSession) queuedUpload(id string) bool {
	return slices.ContainsFunc(s.queue, func(q QueuedPrompt) bool {
		return slices.ContainsFunc(q.Attachments, func(a agentapi.Attachment) bool { return a.ID == id })
	})
}

// sweepLoop expires unused uploads, drops idle read-only transcripts and
// closes idle conversations while the service runs.
func (m *Manager) sweepLoop() {
	defer m.wg.Done()
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	h := time.NewTicker(historySweep)
	defer h.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.sweepUploads()
		case <-h.C:
			m.evictHistories()
			m.closeIdleConversations()
		}
	}
}

// checkUploadsLocked resolves a prompt's upload IDs for the Task: at most
// maxAttachments, each stored for this Task, within the model's gate and
// image count.
func (m *Manager) checkUploadsLocked(s *webSession, ids []string) ([]*upload, error) {
	return m.checkUploadsForModelLocked(s, ids, s.model)
}

func (m *Manager) checkUploadsForModelLocked(s *webSession, ids []string, model string) ([]*upload, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*upload
	for _, id := range ids {
		if slices.ContainsFunc(out, func(u *upload) bool { return u.ID == id }) {
			continue
		}
		u := s.uploads[id]
		if u == nil || u.Tool {
			return nil, newError(http.StatusBadRequest, "attachment %q is not an upload of this task", clipRunes(displaytext.Sanitize(id), maxDetailRunes))
		}
		out = append(out, u)
	}
	if len(out) > maxAttachments {
		return nil, newError(http.StatusBadRequest, "a prompt can carry at most %d attachments", maxAttachments)
	}
	media := m.modelLocked(s.provider, model).Media
	images := 0
	for _, u := range out {
		if err := mediaAllowed(model, media, u.MIME); err != nil {
			return nil, err
		}
		if isImage(u.MIME) {
			images++
		}
	}
	if media != nil && media.MaxImages > 0 && images > media.MaxImages {
		noun := "images"
		if media.MaxImages == 1 {
			noun = "image"
		}
		return nil, newError(http.StatusBadRequest, "%s accepts at most %d %s per prompt", cmpName(model), media.MaxImages, noun)
	}
	return out, nil
}

func uploadInfos(uploads []*upload) []agentapi.Attachment {
	if len(uploads) == 0 {
		return nil
	}
	out := make([]agentapi.Attachment, 0, len(uploads))
	for _, u := range uploads {
		out = append(out, u.info())
	}
	return out
}

// readBlobs reads the uploads' bytes for sending.
func (m *Manager) readBlobs(taskID string, uploads []*upload) ([]agentapi.Blob, error) {
	var out []agentapi.Blob
	for _, u := range uploads {
		data, err := os.ReadFile(filepath.Join(m.taskUploadDir(taskID), u.ID)) // #nosec G304 -- UAM's own directory; the ID is a stored UUID.
		if err != nil || int64(len(data)) != u.Size {
			return nil, newError(http.StatusConflict, "attachment %s is no longer stored", u.Name)
		}
		b := agentapi.Blob{Name: u.Name, MIME: u.MIME, Data: data}
		if u.MIME == mimePDF {
			if b.Path, err = namedCopy(m.taskUploadDir(taskID), u, data); err != nil {
				log.Warn("name web attachment failed", "session", taskID, "error", err)
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// namedCopy links an upload to <id>.d/<name> beside it, the name ending in
// .pdf, and returns that absolute path: Copilot passes a PDF to the model
// only as a file it reads, recognised by its name, or failing that gives the
// agent the path, which inline bytes do not have.
func namedCopy(dir string, u *upload, data []byte) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	name := u.Name
	if !strings.EqualFold(filepath.Ext(name), ".pdf") {
		name += ".pdf"
	}
	sub := u.ID + ".d"
	if err := root.MkdirAll(sub, 0o700); err != nil {
		return "", err
	}
	rel := filepath.Join(sub, name)
	if info, err := root.Lstat(rel); err == nil && info.Mode().IsRegular() && info.Size() == u.Size {
		return filepath.Join(dir, rel), nil
	}
	_ = root.Remove(rel)
	if err := root.Link(u.ID, rel); err != nil {
		if err := root.WriteFile(rel, data, 0o600); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, rel), nil
}

// markUsed keeps uploads a prompt carried for as long as the Task exists.
func (m *Manager) markUsed(s *webSession, uploads []*upload) {
	now := m.now()
	var changed []upload
	m.mu.Lock()
	for _, u := range uploads {
		if cur := s.uploads[u.ID]; cur != nil && cur.UsedAt.IsZero() {
			cur.UsedAt = now
			changed = append(changed, *cur)
		}
	}
	m.mu.Unlock()
	dir := m.taskUploadDir(s.id)
	for i := range changed {
		if err := writeUploadRecord(dir, &changed[i]); err != nil {
			log.Warn("record web attachment use failed", "session", s.id, "error", err)
		}
	}
}

// linkUploadsLocked gives a user item's attachments the ID of the stored
// copy with the same content, so browsers can show it after a reload.
func (s *webSession) linkUploadsLocked(it *agentapi.Item) {
	for i := range it.Attachments {
		a := &it.Attachments[i]
		if a.ID != "" || a.SHA256 == "" {
			continue
		}
		for _, u := range s.uploads {
			if u.SHA256 == a.SHA256 {
				a.ID, a.Size = u.ID, u.Size
				break
			}
		}
	}
}

// keepImages stores a tool item's images with its Task and replaces each
// with its stored copy: ID, sniffed type, size and name, without the bytes.
// An image this Task already keeps, by SHA-256, is not stored again, and an
// image without bytes is found that way only. The rest are dropped, and
// ImagesNote says why. It writes files, so it runs without Manager.mu.
func (m *Manager) keepImages(s *webSession, it *agentapi.Item) {
	if len(it.Images) == 0 {
		return
	}
	s.imageMu.Lock()
	defer s.imageMu.Unlock()
	var kept []agentapi.Image
	var reasons []string
	dropped := 0
	for _, img := range it.Images {
		u, reason := m.keepImage(s, img)
		if u == nil {
			dropped++
			if !slices.Contains(reasons, reason) {
				reasons = append(reasons, reason)
			}
			continue
		}
		if !slices.ContainsFunc(kept, func(k agentapi.Image) bool { return k.ID == u.ID }) {
			kept = append(kept, agentapi.Image{ID: u.ID, MIME: u.MIME, Size: u.Size, Name: u.Name, SHA256: u.SHA256})
		}
	}
	it.Images, it.ImagesNote = kept, ""
	if dropped > 0 {
		it.ImagesNote = imagesNote(dropped, strings.Join(reasons, "; "))
	}
}

// imagesNote says how many of a tool item's images were left out, and why.
func imagesNote(dropped int, why string) string {
	noun := "images"
	if dropped == 1 {
		noun = "image"
	}
	return fmt.Sprintf("%d %s not kept: %s", dropped, noun, why)
}

// keepImage returns the stored copy of one tool image, storing it when the
// Task has none, or the reason it is not kept.
func (m *Manager) keepImage(s *webSession, img agentapi.Image) (*upload, string) {
	digest, mime := strings.ToLower(img.SHA256), ""
	if len(img.Data) > 0 {
		if len(img.Data) > maxToolImageBytes {
			return nil, "over 5 MiB"
		}
		if mime = http.DetectContentType(img.Data); !isImage(mime) {
			return nil, "not a png, jpeg, gif or webp image"
		}
		sum := sha256.Sum256(img.Data)
		digest = hex.EncodeToString(sum[:])
	}
	m.mu.Lock()
	stored, count := s.toolImageLocked(digest)
	removed := s.removed
	m.mu.Unlock()
	switch {
	case removed:
		return nil, "the task was deleted"
	case stored != nil:
		return stored, ""
	case len(img.Data) == 0:
		return nil, "the provider did not record its bytes"
	case count >= maxToolImages:
		return nil, fmt.Sprintf("a task keeps at most %d images", maxToolImages)
	}
	id, err := newUUID()
	if err != nil {
		return nil, "could not be stored"
	}
	u := &upload{ID: id, MIME: mime, Size: int64(len(img.Data)), SHA256: digest, CreatedAt: m.now(), Tool: true}
	if img.Name != "" {
		u.Name = cleanUploadName(img.Name)
	}
	if err := m.storeUpload(s, u, img.Data); err != nil {
		if errors.Is(err, errTaskRemoved) {
			return nil, "the task was deleted"
		}
		log.Warn("store tool image failed", "session", s.id, "error", err)
		return nil, "could not be stored"
	}
	return u, ""
}

// toolImageLocked returns the Task's stored tool image with this digest, if
// any, and how many tool images it keeps.
func (s *webSession) toolImageLocked(digest string) (*upload, int) {
	var match *upload
	count := 0
	for _, u := range s.uploads {
		if !u.Tool {
			continue
		}
		count++
		if digest != "" && u.SHA256 == digest {
			match = u
		}
	}
	return match, count
}

// Attachment returns one stored upload of a Task and its bytes.
func (m *Manager) Attachment(id, attachmentID string) (agentapi.Attachment, []byte, time.Time, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.Attachment{}, nil, time.Time{}, err
	}
	m.mu.Lock()
	var u upload
	cur := s.uploads[attachmentID]
	if cur != nil {
		u = *cur
	}
	m.mu.Unlock()
	if cur == nil {
		return agentapi.Attachment{}, nil, time.Time{}, newError(http.StatusNotFound, "attachment not found")
	}
	blobs, err := m.readBlobs(s.id, []*upload{&u})
	if err != nil {
		return agentapi.Attachment{}, nil, time.Time{}, newError(http.StatusNotFound, "attachment not found")
	}
	return u.info(), blobs[0].Data, u.CreatedAt, nil
}
