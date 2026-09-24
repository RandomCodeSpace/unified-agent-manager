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
	uploadsDir    = "web-attachments"
	mimePDF       = "application/pdf"
	mimeText      = "text/plain"
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
	case strings.HasPrefix(detected, "text/") && bytes.IndexByte(data, 0) < 0 && utf8.Valid(data):
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
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.Attachment{}, err
	}
	mime, err := sniff(data)
	if err != nil {
		return agentapi.Attachment{}, err
	}
	m.mu.Lock()
	switch {
	case s.removed:
		err = newError(http.StatusNotFound, "session not found")
	case m.closed:
		err = errShuttingDown
	default:
		if err = s.readOnlyLocked(); err == nil {
			err = mediaAllowed(s.model, m.mediaLocked(s), mime)
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
	dir := m.taskUploadDir(s.id)
	if err := writeUpload(m.uploadRoot(), dir, u, data); err != nil {
		return agentapi.Attachment{}, fmt.Errorf("store attachment: %w", err)
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
		// Deleted while storing: nothing of the Task may stay behind.
		removeUploads(dir)
		return agentapi.Attachment{}, newError(http.StatusNotFound, "session not found")
	}
	m.sweepUploads()
	return u.info(), nil
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
			if !u.UsedAt.IsZero() || !u.CreatedAt.Before(cutoff) || s.queuedUpload(id) {
				continue
			}
			delete(s.uploads, id)
			gone = append(gone, expired{m.taskUploadDir(s.id), id})
		}
	}
	m.mu.Unlock()
	for _, e := range gone {
		for _, name := range []string{e.id, e.id + ".json"} {
			if err := os.Remove(filepath.Join(e.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
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

// sweepLoop expires unused uploads while the service runs.
func (m *Manager) sweepLoop() {
	defer m.wg.Done()
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.sweepUploads()
		}
	}
}

// checkUploadsLocked resolves a prompt's upload IDs for the Task: at most
// maxAttachments, each stored for this Task, within the model's gate and
// image count.
func (m *Manager) checkUploadsLocked(s *webSession, ids []string) ([]*upload, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*upload
	for _, id := range ids {
		if slices.ContainsFunc(out, func(u *upload) bool { return u.ID == id }) {
			continue
		}
		u := s.uploads[id]
		if u == nil {
			return nil, newError(http.StatusBadRequest, "attachment %q is not an upload of this task", clipRunes(displaytext.Sanitize(id), maxDetailRunes))
		}
		out = append(out, u)
	}
	if len(out) > maxAttachments {
		return nil, newError(http.StatusBadRequest, "a prompt can carry at most %d attachments", maxAttachments)
	}
	media := m.mediaLocked(s)
	images := 0
	for _, u := range out {
		if err := mediaAllowed(s.model, media, u.MIME); err != nil {
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
		return nil, newError(http.StatusBadRequest, "%s accepts at most %d %s per prompt", cmpName(s.model), media.MaxImages, noun)
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
		out = append(out, agentapi.Blob{Name: u.Name, MIME: u.MIME, Data: data})
	}
	return out, nil
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
