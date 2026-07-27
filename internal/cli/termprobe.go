package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
)

// ─── terminal probe ───────────────────────────────────────────────────────────
//
// uam runs on the host; the terminal drawing it is whichever client the user
// attached from, and every first-class client (Windows Terminal, Termius,
// VS Code, JetBrains) advertises the same TERM while rendering the dashboard's
// East-Asian-Ambiguous glyphs (● ○ ◆ …) at different widths. TERM cannot
// distinguish them, so the width is measured: print one ambiguous glyph at
// column 1, ask the terminal where the cursor landed (CPR, ESC[6n), and read
// the answer. A glyph the font draws two cells wide lands the cursor at column
// 3 instead of 2 — that terminal gets the ASCII glyph set.
//
// The probe runs once, before Bubble Tea takes the screen, on the same
// stdin/stdout pair Bubble Tea will use. It erases its own output, so the shell
// line stays clean, and it fails toward the full Unicode set: a terminal that
// never answers is far more likely to be a healthy one behind a slow link than
// a broken one.

// cprTimeout bounds how long startup waits for the terminal to answer. Termius
// over mobile data answers in tens of milliseconds; a terminal that stays
// silent this long is not going to answer at all.
const cprTimeout = 200 * time.Millisecond

// probeTermCaps resolves the terminal capabilities for this attach. Overrides
// and locale can decide without touching the terminal; only the genuinely
// ambiguous case pays for a round trip.
func probeTermCaps() app.TermCaps {
	unmeasured := app.CapsFromEnvironment(os.Getenv, 0, false)
	if unmeasured.Glyphs == app.GlyphsASCII || os.Getenv("UAM_WIDE") == "0" {
		return unmeasured
	}
	width, ok := measureAmbiguousWidth(os.Stdin, os.Stdout)
	if !ok {
		return unmeasured
	}
	return app.CapsFromEnvironment(os.Getenv, width, true)
}

// measureAmbiguousWidth prints ProbeGlyph at column 1 and reads back the
// cursor column. Returns (width, true) on a parsed answer, (0, false) on any
// failure — no TTY, raw mode refused, timeout, or garbled response.
func measureAmbiguousWidth(in, out *os.File) (int, bool) {
	if !term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) {
		return 0, false
	}
	// Raw mode keeps the CPR answer out of the line editor: no echo of the
	// escape bytes, no waiting for a newline that never comes.
	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return 0, false
	}
	defer func() { _ = term.Restore(in.Fd(), state) }()
	if _, err := out.WriteString("\r" + app.ProbeGlyph + "\x1b[6n"); err != nil {
		return 0, false
	}
	// Erase the probe glyph whether or not the read succeeds.
	defer func() { _, _ = out.WriteString("\r\x1b[K") }()
	response, ok := readCPR(in, cprTimeout)
	if !ok {
		return 0, false
	}
	col, ok := parseCPRColumn(response)
	if !ok || col < 2 {
		return 0, false
	}
	return col - 1, true
}

// readCPR accumulates bytes until the CPR terminator 'R' or the deadline.
// os.Stdin usually has no deadline support (it is a blocking fd outside the
// runtime poller), so readiness is established with poll(2) before each read —
// the same idiom the attach loop uses in internal/session.
func readCPR(in *os.File, timeout time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, 16)
	one := make([]byte, 1)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false
		}
		pollFD := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(pollFD, int(remaining.Milliseconds())+1)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, false
		}
		if ready == 0 {
			return nil, false
		}
		n, err := in.Read(one)
		if err != nil {
			return nil, false
		}
		if n == 0 {
			continue
		}
		buf = append(buf, one[0])
		if one[0] == 'R' {
			return buf, true
		}
		// A CPR answer is at most ESC [ rrrr ; cccc R. Anything longer means
		// the stream is not answering our question.
		if len(buf) > 32 {
			return nil, false
		}
	}
}

// parseCPRColumn extracts the column from a cursor position report,
// ESC[<row>;<col>R. Bytes queued before the answer (a keypress racing the
// probe) are skipped by parsing from the final CSI in the buffer.
func parseCPRColumn(response []byte) (int, bool) {
	s := string(response)
	start := strings.LastIndex(s, "\x1b[")
	if start < 0 {
		return 0, false
	}
	var row, col int
	if _, err := fmt.Sscanf(s[start:], "\x1b[%d;%dR", &row, &col); err != nil {
		return 0, false
	}
	if col < 1 {
		return 0, false
	}
	return col, true
}
