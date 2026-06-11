package vterm

import (
	"strconv"
	"strings"
)

// col is a packed terminal color. The zero value is the default color;
// otherwise the top byte is the kind and the low bytes hold the value.
type col uint32

const (
	colKindIdx col = 1 << 24 // low byte: palette index 0–255
	colKindRGB col = 2 << 24 // low three bytes: r, g, b
)

func idxColor(i int) col { return colKindIdx | col(clamp(i, 0, 255)) } // #nosec G115 -- clamped

func rgbColor(r, g, b int) col {
	return colKindRGB | col(clamp(r, 0, 255))<<16 | col(clamp(g, 0, 255))<<8 | col(clamp(b, 0, 255)) // #nosec G115 -- clamped
}

// attr flag bits.
const (
	aBold uint8 = 1 << iota
	aDim
	aItalic
	aUnderline
	aBlink
	aReverse
	aStrike
)

// attr is the SGR state of one cell. The zero value is "no attributes".
type attr struct {
	fg, bg col
	flags  uint8
}

// cell is one grid position: a rune (0 = blank or wide-rune continuation)
// and the SGR attributes it was written or erased with.
type cell struct {
	r rune
	a attr
}

// visible reports whether the cell contributes to a repaint even without a
// glyph: a colored background, or reverse/underline/strike, paint on blanks.
func (c cell) visible() bool {
	if c.r != 0 && c.r != ' ' {
		return true
	}
	return c.a.bg != 0 || c.a.flags&(aReverse|aUnderline|aStrike) != 0
}

func rowBlank(row []cell) bool {
	for _, c := range row {
		if c.visible() {
			return false
		}
	}
	return true
}

// applySGR folds one SGR parameter string into the terminal's current
// attributes. Both classic semicolon parameters ("38;5;196") and colon
// sub-parameter forms ("38:5:196", "38:2::r:g:b", "4:0") are handled.
func (t *Terminal) applySGR(params string) {
	if params == "" {
		t.cur = attr{}
		return
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		sub := strings.Split(fields[i], ":")
		switch code := atoiSGR(sub[0]); code {
		case 0:
			t.cur = attr{}
		case 1:
			t.cur.flags |= aBold
		case 2:
			t.cur.flags |= aDim
		case 3:
			t.cur.flags |= aItalic
		case 4:
			// 4:0 is "underline off"; bare 4 and the 4:n styles all underline.
			if len(sub) > 1 && atoiSGR(sub[1]) == 0 {
				t.cur.flags &^= aUnderline
			} else {
				t.cur.flags |= aUnderline
			}
		case 5, 6:
			t.cur.flags |= aBlink
		case 7:
			t.cur.flags |= aReverse
		case 9:
			t.cur.flags |= aStrike
		case 21:
			t.cur.flags |= aUnderline // double underline: render as underline
		case 22:
			t.cur.flags &^= aBold | aDim
		case 23:
			t.cur.flags &^= aItalic
		case 24:
			t.cur.flags &^= aUnderline
		case 25:
			t.cur.flags &^= aBlink
		case 27:
			t.cur.flags &^= aReverse
		case 29:
			t.cur.flags &^= aStrike
		case 30, 31, 32, 33, 34, 35, 36, 37:
			t.cur.fg = idxColor(code - 30)
		case 38:
			t.cur.fg = parseExtColor(sub, fields, &i, t.cur.fg)
		case 39:
			t.cur.fg = 0
		case 40, 41, 42, 43, 44, 45, 46, 47:
			t.cur.bg = idxColor(code - 40)
		case 48:
			t.cur.bg = parseExtColor(sub, fields, &i, t.cur.bg)
		case 49:
			t.cur.bg = 0
		case 58:
			// Underline color: parse and discard so its arguments are not
			// misread as standalone codes.
			parseExtColor(sub, fields, &i, 0)
		case 90, 91, 92, 93, 94, 95, 96, 97:
			t.cur.fg = idxColor(code - 90 + 8)
		case 100, 101, 102, 103, 104, 105, 106, 107:
			t.cur.bg = idxColor(code - 100 + 8)
		}
	}
}

// parseExtColor decodes the 38/48/58 extended-color forms. In the semicolon
// form the arguments are the following fields, which it consumes via i; in
// the colon form everything lives in sub. Unknown forms return prev.
func parseExtColor(sub, fields []string, i *int, prev col) col {
	if len(sub) > 1 {
		switch atoiSGR(sub[1]) {
		case 5:
			if len(sub) >= 3 {
				return idxColor(atoiSGR(sub[2]))
			}
		case 2:
			// 38:2:r:g:b, or 38:2:<colorspace>:r:g:b — take the last three.
			if len(sub) >= 5 {
				return rgbColor(atoiSGR(sub[len(sub)-3]), atoiSGR(sub[len(sub)-2]), atoiSGR(sub[len(sub)-1]))
			}
		}
		return prev
	}
	if *i+1 >= len(fields) {
		return prev
	}
	switch atoiSGR(fields[*i+1]) {
	case 5:
		if *i+2 < len(fields) {
			v := idxColor(atoiSGR(fields[*i+2]))
			*i += 2
			return v
		}
		*i = len(fields)
	case 2:
		if *i+4 < len(fields) {
			v := rgbColor(atoiSGR(fields[*i+2]), atoiSGR(fields[*i+3]), atoiSGR(fields[*i+4]))
			*i += 4
			return v
		}
		*i = len(fields)
	default:
		*i++
	}
	return prev
}

// atoiSGR parses the leading digits of one SGR parameter; anything malformed
// truncates rather than failing, mirroring csiParams.
func atoiSGR(s string) int {
	v := 0
	for _, c := range s {
		if c < '0' || c > '9' || v > 1<<20 {
			return v
		}
		v = v*10 + int(c-'0')
	}
	return v
}

// sgr renders the attribute set as one escape sequence, always starting from
// a reset so the previous run's state cannot bleed through.
func (a attr) sgr() string {
	var b strings.Builder
	b.WriteString("\x1b[0")
	for _, f := range []struct {
		bit  uint8
		code string
	}{
		{aBold, "1"}, {aDim, "2"}, {aItalic, "3"}, {aUnderline, "4"},
		{aBlink, "5"}, {aReverse, "7"}, {aStrike, "9"},
	} {
		if a.flags&f.bit != 0 {
			b.WriteByte(';')
			b.WriteString(f.code)
		}
	}
	writeColor(&b, a.fg, 30, 90, "38")
	writeColor(&b, a.bg, 40, 100, "48")
	b.WriteByte('m')
	return b.String()
}

func writeColor(b *strings.Builder, c col, base, brightBase int, ext string) {
	switch c >> 24 {
	case 1:
		i := int(c & 0xff)
		switch {
		case i < 8:
			b.WriteString(";" + strconv.Itoa(base+i))
		case i < 16:
			b.WriteString(";" + strconv.Itoa(brightBase+i-8))
		default:
			b.WriteString(";" + ext + ";5;" + strconv.Itoa(i))
		}
	case 2:
		b.WriteString(";" + ext + ";2;" +
			strconv.Itoa(int(c>>16&0xff)) + ";" +
			strconv.Itoa(int(c>>8&0xff)) + ";" +
			strconv.Itoa(int(c&0xff)))
	}
}
