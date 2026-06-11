package adapter

import "strings"

// ShellJoin renders argv as a single /bin/sh command string with every value
// POSIX single-quote escaped. The native backend execs agent argv directly —
// no shell anywhere on the dispatch path — so this survives only for the
// command-alias fallback, where the user's interactive shell must resolve an
// alias/function that LookPath cannot see (shellAliasCommand).
func ShellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !isShellSafeRune(r)
	}) == -1 {
		return s
	}
	// POSIX single-quote escaping: wrap in single quotes and rewrite any
	// embedded single quote as the close-reopen idiom '\''. Inside single
	// quotes /bin/sh performs no expansion, so $(), ``, $VAR, and newlines
	// all reach the command literally.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafeRune(r rune) bool {
	if r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '+' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	return r >= 'a' && r <= 'z'
}
