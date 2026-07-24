package log

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

const (
	diagnosticUnavailable = "unavailable"
	diagnosticRedacted    = "redacted"
)

var diagnosticIdentifierRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

var diagnosticEvents = allowedDiagnostics(
	"attach.lifecycle", "attach.negotiation", "controller.failover", "profile.resolution",
	"provider.exception", "resize.accepted", "resize.ignored", "role.assignment",
	"role.promotion", "role.transfer", "role.vacancy", "slow_client.eviction",
)

var diagnosticReasons = allowedDiagnostics(
	"accepted", "assigned", "attached", "connection_drop", "connection_write",
	"deadline_reset", "default_profile", "detached", "dropped", "fallback",
	"handshake_write", "host_shutdown", "invalid_size", "legacy", "malformed_frame",
	"no_controller", "not_controller", "observer", "output_backpressure",
	"profile_fallback", "promoted", "provider_primary_screen", "rejected",
	"selected", "session_profile", "slow_client", "stale_generation", "timeout",
	"transferred", "unknown_client",
)

var diagnosticRoles = allowedDiagnostics("controller", "observer", "standby")
var diagnosticPolicies = allowedDiagnostics(
	"default", "global", "global,session", "global,session,profile",
	"global,session,profile,session_override", "primary", "uam",
	"session",
)
var diagnosticFallbacks = allowedDiagnostics("legacy")
var diagnosticTermHints = allowedDiagnostics(
	"alacritty", "ghostty", "screen-256color", "tmux-256color",
	"wezterm", "xterm-256color", "xterm-kitty",
)

type DiagnosticEvent struct {
	Event    string
	Session  string
	ClientID string
	Protocol int
	Role     string
	Reason   string
	Provider string
	Profile  string
	Policy   string
	Fallback string
	TermHint string
}

func Diagnostic(event DiagnosticEvent) {
	protocol := event.Protocol
	if protocol != 1 && protocol != 2 {
		protocol = 0
	}
	attributes := []slog.Attr{
		slog.String("event", allowedDiagnosticValue(event.Event, diagnosticEvents)),
		slog.String("session", diagnosticIdentifier(event.Session)),
		slog.String("client_id", diagnosticClientID(event.ClientID)),
		slog.Int("protocol", protocol),
		slog.String("role", allowedDiagnosticValue(event.Role, diagnosticRoles)),
		slog.String("reason", allowedDiagnosticValue(event.Reason, diagnosticReasons)),
		slog.String("provider", diagnosticIdentifier(event.Provider)),
		slog.String("profile", diagnosticIdentifier(event.Profile)),
		slog.String("policy", allowedDiagnosticValue(event.Policy, diagnosticPolicies)),
		slog.String("fallback", allowedDiagnosticValue(event.Fallback, diagnosticFallbacks)),
		slog.String("term_hint", allowedDiagnosticValue(event.TermHint, diagnosticTermHints)),
	}
	current.LogAttrs(context.Background(), slog.LevelInfo, "diagnostic event", attributes...)
}

func ProfileFallback(scope, profile string) {
	Diagnostic(DiagnosticEvent{
		Event:    "profile.resolution",
		Reason:   "profile_fallback",
		Profile:  profile,
		Policy:   scope,
		Fallback: "legacy",
	})
}

func allowedDiagnostics(values ...string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return allowed
}

func allowedDiagnosticValue(value string, allowed map[string]struct{}) string {
	if value == "" {
		return diagnosticUnavailable
	}
	if _, ok := allowed[value]; !ok {
		return diagnosticRedacted
	}
	return value
}

func diagnosticIdentifier(value string) string {
	if value == "" {
		return diagnosticUnavailable
	}
	if !diagnosticIdentifierRE.MatchString(value) || diagnosticSensitive(value) {
		return diagnosticRedacted
	}
	return value
}

func diagnosticClientID(value string) string {
	if value == "" {
		return diagnosticUnavailable
	}
	if !strings.HasPrefix(value, "client-") || !diagnosticIdentifierRE.MatchString(value) {
		return diagnosticRedacted
	}
	suffix := strings.TrimPrefix(value, "client-")
	if suffix == "" {
		return diagnosticRedacted
	}
	for _, character := range suffix {
		if character < '0' || character > '9' {
			return diagnosticRedacted
		}
	}
	return value
}

func diagnosticSensitive(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"auth", "credential", "input", "output", "password", "private", "secret", "token"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
