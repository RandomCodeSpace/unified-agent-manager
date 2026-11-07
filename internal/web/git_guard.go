package web

import (
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const gitGuardLead = "uam git guard: "

// gitGuardToolUse refuses a shell call whose git command loses work or skips
// checks with no undo. It is a seatbelt for mistakes, not a sandbox: scripts,
// aliases, eval and non-literal words are not read.
func gitGuardToolUse(use agentapi.ToolUse) string {
	command, _ := use.Args["command"].(string)
	if permissionToolKind(use.Tool) != "shell" || command == "" {
		return ""
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return ""
	}
	deny := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok && deny == "" {
			words := make([]string, len(c.Args))
			for i, w := range c.Args {
				words[i] = glabLiteral(w.Parts)
			}
			deny = gitGuardCall(words)
		}
		return deny == ""
	})
	return deny
}

// gitGuardCall checks one parsed command; quoted text such as a commit
// message is a single word, so it never matches.
func gitGuardCall(words []string) string {
	if len(words) == 0 || path.Base(words[0]) != "git" {
		return ""
	}
	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") {
		if words[i] == "-C" || words[i] == "-c" {
			i++
		}
		i++
	}
	if i >= len(words) {
		return ""
	}
	sub, args := words[i], words[i+1:]
	short := func(flag rune) bool {
		return slices.ContainsFunc(args, func(a string) bool {
			return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], flag)
		})
	}
	if slices.Contains(args, "--no-verify") || sub == "commit" && slices.Contains(args, "-n") {
		return gitGuardLead + "--no-verify skips the checks the hooks run; fix what they report instead."
	}
	switch sub {
	case "add":
		if short('A') || slices.ContainsFunc(args, func(a string) bool { return a == "--all" || a == "." || a == "./" || a == "*" || a == ":/" }) {
			return gitGuardLead + "stage explicit paths (git add <file>...), not everything: blanket staging sweeps in unrelated and secret files."
		}
	case "reset":
		if slices.Contains(args, "--hard") {
			return gitGuardLead + "git reset --hard discards uncommitted work, including other Threads' in a shared folder; use git restore <file> for your own files, or commit first."
		}
	case "push":
		if short('f') || slices.ContainsFunc(args, func(a string) bool { return a == "--force" || strings.HasPrefix(a, "+") }) {
			return gitGuardLead + "use git push --force-with-lease, which refuses to overwrite commits you have not seen, not --force, -f or a +refspec."
		}
	case "stash":
		if gitGuardStash(args) {
			return gitGuardLead + "the stash stack is shared by every worktree of the repository: use git stash push -m <unique tag>, then git stash apply and drop by its ref, or make a WIP commit."
		}
	}
	return ""
}

// gitGuardStash reports a stash call that adds an unnamed entry or acts on
// the top entry, which may belong to another worktree.
func gitGuardStash(args []string) bool {
	named := slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-m") || strings.HasPrefix(a, "--message") })
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return !named
	}
	refs := slices.ContainsFunc(args[1:], func(a string) bool { return !strings.HasPrefix(a, "-") })
	switch args[0] {
	case "push":
		return !named
	case "pop", "clear":
		return true
	case "apply", "drop":
		return !refs
	}
	return false
}
