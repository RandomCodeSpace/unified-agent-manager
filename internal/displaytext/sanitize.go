// Package displaytext prepares untrusted metadata for display in a terminal.
package displaytext

import (
	"strings"
	"unicode/utf8"
)

type parseState uint8

const (
	ground parseState = iota
	escape
	csi
	controlString
	controlStringEscape
)

// Sanitize returns valid UTF-8 terminal display text. It removes ANSI CSI,
// OSC, DCS, SOS, PM, and APC control sequences; drops unsafe control runes;
// and turns horizontal tabs and line breaks into spaces.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	state := ground
	for i := 0; i < len(s); {
		// Accept the legacy single-byte C1 representation as well as valid
		// UTF-8 C1 runes. DecodeRuneInString otherwise treats it as invalid.
		if s[i] >= 0x80 && s[i] <= 0x9f {
			state = step(state, rune(s[i]), &b)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++ // discard invalid bytes; output is always valid UTF-8
			continue
		}
		state = step(state, r, &b)
		i += size
	}
	return b.String()
}

func step(state parseState, r rune, b *strings.Builder) parseState {
	switch state {
	case escape:
		switch r {
		case '[':
			return csi
		case ']', 'P', 'X', '^', '_':
			return controlString
		case 0x1b:
			return escape
		default:
			return appendGround(r, b)
		}
	case csi:
		if r == 0x1b {
			return escape
		}
		if r == 0x9c || r >= 0x40 && r <= 0x7e {
			return ground
		}
		return csi
	case controlString:
		switch r {
		case 0x07, 0x9c:
			return ground
		case 0x1b:
			return controlStringEscape
		default:
			return controlString
		}
	case controlStringEscape:
		if r == '\\' || r == 0x9c || r == 0x07 {
			return ground
		}
		if r == 0x1b {
			return controlStringEscape
		}
		return controlString
	default:
		return appendGround(r, b)
	}
}

func appendGround(r rune, b *strings.Builder) parseState {
	switch r {
	case 0x1b:
		return escape
	case 0x9b:
		return csi
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
		return controlString
	case '\t', '\n', '\r':
		b.WriteByte(' ')
		return ground
	}
	if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f {
		return ground
	}
	b.WriteRune(r)
	return ground
}
