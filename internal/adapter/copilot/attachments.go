package copilot

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// attachments maps a prompt's references to Copilot attachments. Uploads go
// inline as blobs, except one with a named copy on the host (a PDF, or a
// large text file), which goes as that file: the CLI passes a document to the
// model natively only where the model client supports it, and otherwise gives
// the agent the file's path, which a blob does not have. A text file it lists
// by path and line count only, without its content.
func attachments(p agentapi.Prompt) []copilot.Attachment {
	var out []copilot.Attachment
	for _, f := range p.Files {
		if f.Dir {
			out = append(out, &rpc.AttachmentDirectory{Path: f.Path, DisplayName: f.Rel})
		} else {
			out = append(out, &rpc.AttachmentFile{Path: f.Path, DisplayName: f.Rel})
		}
	}
	for _, b := range p.Attachments {
		if b.Path != "" {
			out = append(out, &rpc.AttachmentFile{Path: b.Path, DisplayName: b.Name})
			continue
		}
		data, name := base64.StdEncoding.EncodeToString(b.Data), b.Name
		out = append(out, &rpc.AttachmentBlob{Data: &data, MIMEType: b.MIME, DisplayName: &name})
	}
	return out
}

// blobAttachments describes the uploads a user message carried, without
// their bytes. A live blob has the data; a recorded one has the asset ID, the
// SHA-256 of the bytes, instead. An upload sent as its named copy is a file
// under the web service's upload store, hashed from disk; it is NotNative
// unless the message lists its type among those the CLI sent natively and
// its path is not among those that fell back to the path flow.
func blobAttachments(atts []copilot.Attachment, native, fallback []string) []agentapi.Attachment {
	var out []agentapi.Attachment
	for _, a := range atts {
		if f, ok := a.(*rpc.AttachmentFile); ok {
			if att, ok := uploadFile(f); ok {
				att.NotNative = att.MIME == "application/pdf" && (!slices.Contains(native, att.MIME) || slices.Contains(fallback, f.Path))
				out = append(out, att)
			}
			continue
		}
		b, ok := a.(*rpc.AttachmentBlob)
		if !ok {
			continue
		}
		att := agentapi.Attachment{MIME: b.MIMEType}
		if b.DisplayName != nil {
			att.Name = *b.DisplayName
		}
		if b.ByteLength != nil {
			att.Size = *b.ByteLength
		}
		switch {
		case b.Data != nil:
			if data, err := base64.StdEncoding.DecodeString(*b.Data); err == nil {
				sum := sha256.Sum256(data)
				att.SHA256, att.Size = hex.EncodeToString(sum[:]), int64(len(data))
			}
		case b.AssetID != nil:
			if digest, ok := strings.CutPrefix(*b.AssetID, "sha256:"); ok {
				att.SHA256 = strings.ToLower(digest)
			}
		}
		out = append(out, att)
	}
	return out
}

// uploadFile describes a file attachment that is an upload's named copy,
// <task>/<id>.d/<name> in the web service's upload store, UploadsDir beside
// the uam store; any other file is a reference, not an upload. The copy of a
// PDF ends in .pdf and that of a text file does not. A copy no longer on disk
// keeps its name and type, without a hash.
func uploadFile(f *rpc.AttachmentFile) (agentapi.Attachment, bool) {
	root, err := filepath.Abs(filepath.Join(filepath.Dir(store.DefaultPath()), agentapi.UploadsDir))
	if err != nil {
		return agentapi.Attachment{}, false
	}
	// Keep opened uploads inside the upload store.
	rel, err := filepath.Rel(root, filepath.Clean(f.Path))
	parts := strings.Split(rel, string(filepath.Separator))
	if err != nil || !filepath.IsLocal(rel) || len(parts) != 3 || !strings.HasSuffix(parts[1], ".d") {
		return agentapi.Attachment{}, false
	}
	att := agentapi.Attachment{Name: f.DisplayName, MIME: "text/plain"}
	if strings.EqualFold(filepath.Ext(rel), ".pdf") {
		att.MIME = "application/pdf"
	}
	file, err := os.OpenInRoot(root, rel)
	if err != nil {
		return att, true
	}
	defer func() { _ = file.Close() }()
	h := sha256.New()
	if n, err := io.Copy(h, file); err == nil {
		att.SHA256, att.Size = hex.EncodeToString(h.Sum(nil)), n
	}
	return att, true
}
