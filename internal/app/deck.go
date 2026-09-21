package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/charmbracelet/x/ansi"
)

// ─── deck geometry ───────────────────────────────────────────────────────────
//
// The deck lays sessions out as fixed-height two-line stamps in as many
// columns as the width allows. Geometry is a function of width and roster
// height only. It never depends on session state, so a stamp keeps its door
// (its position on the page) while the status underneath it changes.

const (
	// stampMinWidth is the narrowest stamp that still carries name, provider,
	// status, directory and creation time. 40 columns is the supported minimum
	// terminal width, so one column always fits.
	stampMinWidth = 39
	stampHeight   = 2
	// deckDoors is how many stamps on a page answer a digit key.
	deckDoors = 9
)

type deckGeometry struct {
	cols, cell, gap, rows int
}

func deckGeometryFor(width, height int) deckGeometry {
	cols := max(1, (width+1)/(stampMinWidth+1))
	cell := (width - (cols - 1)) / cols
	gap := 0
	if cols > 1 {
		gap = 1
	}
	rows := max(1, (height+gap)/(stampHeight+gap))
	return deckGeometry{cols: cols, cell: cell, gap: gap, rows: rows}
}

func (g deckGeometry) perPage() int { return g.cols * g.rows }

// slotOrigin returns the top-left cell of the slot-th stamp on a page,
// relative to the roster's origin.
func (g deckGeometry) slotOrigin(slot int) (x, y int) {
	return (slot % g.cols) * (g.cell + 1), (slot / g.cols) * (stampHeight + g.gap)
}

// pageBounds returns the half-open range of visible positions on the page
// that holds position.
func (g deckGeometry) pageBounds(total, position int) (int, int) {
	per := g.perPage()
	if total <= 0 || per <= 0 {
		return 0, 0
	}
	position = max(0, min(position, total-1))
	start := (position / per) * per
	return start, min(total, start+per)
}

// ─── stamps ──────────────────────────────────────────────────────────────────

type stamp struct {
	sess adapter.Session
	// door is the digit that opens this stamp, 1..deckDoors, or 0 when the
	// stamp is past the ninth slot of its page.
	door     int
	selected bool
}

// stampStatus is the literal lifecycle word, with the exit detail folded in
// for failures so the word alone says what went wrong.
func stampStatus(sess adapter.Session) string {
	switch {
	case sess.ProcAlive == adapter.Alive:
		return "Running"
	case failureExitDetail(sess) != "":
		return "Failed " + strings.TrimPrefix(failureExitDetail(sess), "exit ")
	default:
		return "Stopped"
	}
}

// stampLines renders one stamp at the given cell width. Line one carries the
// rail, door digit, status glyph and word, name and provider; line two carries
// the directory and the creation stamp. Both lines are padded to width.
func (m Model) stampLines(s stamp, width int) (string, string) {
	now := m.dashboardNow()
	t := toneForSession(s.sess)
	rail := " "
	if s.selected {
		rail = toneOf(toneSelected).mark()
	}
	door := " "
	if s.door > 0 {
		door = brandStyle.Render(strconv.Itoa(s.door))
	}
	status := stampStatus(s.sess)
	statusCell := padRightANSI(t.render(status), max(8, ansi.StringWidth(status)))
	provider := ansi.Truncate(providerBadge(s.sess), 8, truncTail())
	pin := ""
	if s.sess.Pinned {
		pin = toneOf(tonePinned).mark() + " "
	}
	prefix := rail + door + " " + t.mark() + " " + statusCell + "  " + pin
	nameBudget := max(1, width-ansi.StringWidth(prefix)-ansi.StringWidth(provider)-2)
	name := ansi.Truncate(displayNameOf(s.sess), nameBudget, truncTail())
	nameRender := titleStyle.Render(name)
	if s.selected {
		nameRender = selectedStyle.Render(name)
	}
	line1 := prefix + padRightANSI(nameRender, nameBudget) + "  " + hintStyle.Render(provider)

	created := createdStamp(s.sess.CreatedAt, now)
	pathBudget := max(1, width-3-ansi.StringWidth(created)-2)
	path := elidePathLeft(m.displayPath(s.sess.Cwd), pathBudget)
	line2 := rail + "  " + padRightANSI(hintStyle.Render(path), pathBudget) + "  " + ageClassFor(s.sess, now).tone().render(created)
	return padRightANSI(line1, width), padRightANSI(line2, width)
}

func displayNameOf(sess adapter.Session) string {
	return displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID, "unnamed"))
}

// createdStamp spells a creation time in the dashboard's zone: the clock when
// it is today, the day when it is this year, the month and year otherwise. It
// changes only when the day rolls over, never on a refresh tick.
func createdStamp(created, now time.Time) string {
	if created.IsZero() {
		return ""
	}
	local := created.In(now.Location())
	y, mo, d := local.Date()
	ny, nmo, nd := now.Date()
	switch {
	case y == ny && mo == nmo && d == nd:
		return local.Format("15:04")
	case y == ny:
		return local.Format("02 Jan")
	default:
		return local.Format("Jan 2006")
	}
}

// elidePathLeft keeps the tail of a path within width, cutting at a segment
// boundary when one fits so the visible part is whole directory names.
func elidePathLeft(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(path) <= width {
		return path
	}
	tail := truncTail()
	for i := 0; i < len(path); i++ {
		if path[i] != '/' {
			continue
		}
		if candidate := tail + path[i:]; ansi.StringWidth(candidate) <= width {
			return candidate
		}
	}
	runes := []rune(path)
	budget := width - ansi.StringWidth(tail)
	start := len(runes)
	for start > 0 && ansi.StringWidth(string(runes[start-1:])) <= budget {
		start--
	}
	return tail + string(runes[start:])
}

// tildePath shortens a path under the home directory to the ~ spelling.
func tildePath(path, home string) string {
	home = strings.TrimRight(home, "/")
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

func (m Model) displayPath(cwd string) string {
	return tildePath(absCwd(cwd), m.homeDir)
}
