package copilot

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// exitText reports why the CLI died without the stderr tail the SDK appends:
// after an abrupt exit that tail comes from the npm wrapper and misleads
// (for example "no platform package found" after the native CLI was killed).
func exitText(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\nstderr:")
	return clip(displaytext.Sanitize(msg), maxErrorText)
}

func errText(err error) string {
	return clip(displaytext.Sanitize(err.Error()), maxErrorText)
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// seconds is d for a reader: tenths below 10 s, whole seconds above.
func seconds(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
}
