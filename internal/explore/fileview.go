package explore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// maxPreviewBytes caps the file size the viewer will load so a stray multi-MB
// file can't stall the render loop or blow memory. 256 KiB comfortably covers
// source files.
const maxPreviewBytes = 256 * 1024

var (
	errPreviewTooLarge = errors.New("file too large to preview")
	errPreviewBinary   = errors.New("binary file")
)

// readFilePreview reads path for read-only display. It rejects oversized files
// and files that look binary (a NUL byte in the first 8 KiB), returning a
// sentinel error the caller renders as guard text instead of dumping bytes.
// On success it returns the (optionally highlighted) content.
func readFilePreview(path string, colorEnabled bool) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- path is confined to the user's project tree (jailed to the explore root); read-only preview
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	// LimitReader caps the read in one syscall sequence — there's no TOCTOU window
	// where an agent writing this file concurrently could grow it past the cap
	// between a stat and a read. The +1 byte lets us detect "over the limit".
	data, err := io.ReadAll(io.LimitReader(f, maxPreviewBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxPreviewBytes {
		return "", errPreviewTooLarge
	}
	sniff := data
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "", errPreviewBinary
	}
	return highlight(string(data), path, colorEnabled), nil
}

// highlight returns src syntax-highlighted for a 256-color terminal, keyed by
// the filename for lexer detection. When colorEnabled is false (NO_COLOR or a
// non-color terminal) it returns src unchanged so no-color terminals stay clean.
// Any chroma error falls back to the plain source — the viewer never blanks.
func highlight(src, filename string, colorEnabled bool) string {
	if !colorEnabled {
		return src
	}
	var b strings.Builder
	if err := quick.Highlight(&b, src, lexerName(filename), "terminal256", "monokai"); err != nil {
		return src
	}
	return b.String()
}

// lexerName maps a filename to a chroma lexer hint. Passing the base name
// improves chroma's detection for common extensions.
func lexerName(filename string) string {
	return filepath.Base(filename)
}

// colorEnabled reports whether the default renderer supports colour output.
// Returns false on dumb/ASCII terminals and when NO_COLOR is set.
func colorEnabled() bool {
	return lipgloss.DefaultRenderer().ColorProfile() != termenv.Ascii
}

// withLineNumbers prefixes every line in content with a right-aligned line
// number and a dim " │ " separator. It is safe to apply after chroma
// highlighting because ANSI escape sequences are per-line.
func withLineNumbers(content string) string {
	lines := strings.Split(content, "\n")
	// Trim a trailing empty element produced by a trailing newline so the last
	// displayed line number matches the actual line count.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return content
	}
	width := len(fmt.Sprintf("%d", len(lines)))
	dimStyle := lipgloss.NewStyle().Faint(true)
	var b strings.Builder
	for i, line := range lines {
		gutter := fmt.Sprintf("%*d │ ", width, i+1)
		b.WriteString(dimStyle.Render(gutter))
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
