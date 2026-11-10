package web

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// cleanTitle is a provider title made safe and short enough to show.
func cleanTitle(title string) string {
	return clipRunes(strings.TrimSpace(displaytext.Sanitize(title)), maxNameRunes)
}

// cleanTaskName sanitizes an optional Task name; "" means the provider title
// is shown.
func cleanTaskName(name string) (string, error) {
	clean := strings.TrimSpace(displaytext.Sanitize(name))
	if utf8.RuneCountInString(clean) > maxNameRunes {
		return "", newError(http.StatusBadRequest, "name is longer than %d characters", maxNameRunes)
	}
	return clean, nil
}

func cleanName(name, fallback string) (string, error) {
	clean := strings.TrimSpace(displaytext.Sanitize(name))
	if clean == "" {
		clean = clipRunes(strings.TrimSpace(displaytext.Sanitize(fallback)), maxNameRunes)
	}
	if clean == "" {
		return "", newError(http.StatusBadRequest, "name is required")
	}
	if utf8.RuneCountInString(clean) > maxNameRunes {
		return "", newError(http.StatusBadRequest, "name is longer than %d characters", maxNameRunes)
	}
	return clean, nil
}

// shortError is provider error text made safe and short enough to show.
func shortError(err error) string {
	return clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
