package web

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const gitGuardLead = "uam git guard: "

// gitGuardToolUse refuses a shell call whose git command loses work or skips
// checks with no undo, and, with scope (a Thread's own worktree), one whose
// -C, --git-dir or --work-tree points outside it. It is a seatbelt for
// mistakes, not a sandbox: scripts, aliases, eval and non-literal words are
// not read.
func gitGuardToolUse(use agentapi.ToolUse, scope string) string {
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
			if deny == "" && scope != "" {
				deny = gitScopeCall(words, c.Assigns, use.Workdir, scope)
			}
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
		if gitGlobalTakesValue(words[i]) {
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

// gitGlobalTakesValue reports a git option before the command whose value
// is the next word.
func gitGlobalTakesValue(word string) bool {
	switch word {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--exec-path", "--super-prefix":
		return true
	}
	return false
}

// gitScopeEnv are the environment variables that point git at another
// repository or working tree.
var gitScopeEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"}

// gitScopeCall refuses a git command whose -C, --git-dir, --work-tree,
// -c core.worktree or GIT_DIR-like assignment names a folder outside scope,
// the Thread's worktree; relative values are read from dir (scope when
// unknown), after any earlier -C. A value starting with ~ is taken as
// outside: the shell expands it to the home folder.
func gitScopeCall(words []string, assigns []*syntax.Assign, dir, scope string) string {
	if len(words) == 0 || path.Base(words[0]) != "git" {
		return ""
	}
	scope = resolvedPath(scope)
	dir = cmp.Or(dir, scope)
	outside := func(flag, value string) string {
		if value == "" {
			return ""
		}
		target := value
		if !strings.HasPrefix(value, "~") {
			if target = resolvedPath(rawJoin(dir, target)); withinDir(scope, target) {
				return ""
			}
		}
		return fmt.Sprintf("uam scope: git %s %s is outside this thread's worktree %s. Run git in your own worktree; to change another folder, start a thread there with %s.", flag, value, scope, createTaskToolName)
	}
	for _, a := range assigns {
		if a.Name != nil && slices.Contains(gitScopeEnv, a.Name.Value) && a.Value != nil {
			if deny := outside(a.Name.Value+"=", glabLiteral(a.Value.Parts)); deny != "" {
				return deny
			}
		}
	}
	for i := 1; i < len(words) && strings.HasPrefix(words[i], "-"); i++ {
		flag, value, inline := strings.Cut(words[i], "=")
		if flag == "-c" && i+1 < len(words) {
			i++
			if key, v, ok := strings.Cut(words[i], "="); ok && strings.EqualFold(key, "core.worktree") {
				if deny := outside("-c core.worktree=", v); deny != "" {
					return deny
				}
			}
			continue
		}
		if flag != "-C" && flag != "--git-dir" && flag != "--work-tree" {
			if gitGlobalTakesValue(words[i]) {
				i++
			}
			continue
		}
		if !inline {
			if i+1 >= len(words) {
				return ""
			}
			i++
			value = words[i]
		}
		if deny := outside(flag, value); deny != "" {
			return deny
		}
		if flag == "-C" && value != "" {
			dir = resolvedPath(rawJoin(dir, value))
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
