package journal

import (
	"bytes"
	"regexp"
)

// ansiRE matches CSI ("\x1b["), OSC ("\x1b]...BEL or ST"), and charset
// switch sequences. Covers the vast majority of escape sequences emitted
// by terminal-UI agents.
var ansiRE = regexp.MustCompile("\x1b\\[[?0-9;]*[a-zA-Z]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[()][AB012]|\x1b[=>]|\x1b\\\\")

// ExtractLines turns a raw PTY byte tail into logical lines suitable for
// detect.ClassifyPane:
//  1. Strip ANSI escape sequences.
//  2. Split on '\n'.
//  3. For each \n-line, trim a single trailing '\r' (CRLF line ending),
//     then treat any remaining embedded '\r' as in-place overwrite: keep
//     only the bytes after the last '\r' in that line.
//
// Order matters: PTY drivers translate '\n' to '\r\n' on output, so plain
// terminal lines arrive as `text\r\n` and split-on-'\n' yields `text\r`.
// Trimming the trailing '\r' first preserves real content; only internal
// '\r' (as in spinner frames `\rframe1\rframe2`) keeps overwrite semantics.
func ExtractLines(raw []byte) []string {
	clean := ansiRE.ReplaceAll(raw, nil)
	parts := bytes.Split(clean, []byte{'\n'})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = bytes.TrimSuffix(p, []byte{'\r'})
		if idx := bytes.LastIndexByte(p, '\r'); idx >= 0 {
			p = p[idx+1:]
		}
		out = append(out, string(p))
	}
	return out
}

// TailLines returns the last n lines from `lines`. If len(lines) <= n,
// returns the slice as-is.
func TailLines(lines []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// Tail returns the last n lines from raw bytes (combined extract+tail).
func Tail(raw []byte, n int) []string {
	return TailLines(ExtractLines(raw), n)
}
