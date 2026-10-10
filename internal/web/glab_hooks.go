package web

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"mvdan.cc/sh/v3/syntax"
)

type glabEdit struct {
	start, end int
	text       string
}
type glabShell struct {
	edits []glabEdit
	deny  string
	push  bool
}

func (m *Manager) glabPre(ctx context.Context, use agentapi.ToolUse) agentapi.ToolVerdict {
	if use.Tool != "bash" && use.Tool != "shell" {
		return agentapi.ToolVerdict{}
	}
	command, _ := use.Args["command"].(string)
	if command == "" {
		return agentapi.ToolVerdict{}
	}
	gate, ok := m.glab.gate(ctx, use.Workdir)
	if !ok {
		return agentapi.ToolVerdict{}
	}
	return glabCommand(ctx, gate, command, use.Args)
}

func glabCommand(ctx context.Context, gate glabGate, command string, args map[string]any) agentapi.ToolVerdict {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return agentapi.ToolVerdict{}
	}
	w := glabShell{}
	for _, st := range f.Stmts {
		w.stmt(st)
	}
	out := agentapi.ToolVerdict{Deny: w.deny}
	if out.Deny != "" {
		return out
	}
	if w.push {
		out = glabLintPush(ctx, gate)
		if out.Deny != "" {
			return out
		}
	}
	if len(w.edits) != 0 {
		slices.SortFunc(w.edits, func(a, b glabEdit) int { return b.start - a.start })
		for _, e := range w.edits {
			command = command[:e.start] + e.text + command[e.end:]
		}
		out.Args = maps.Clone(args)
		out.Args["command"] = command
		out.Context = strings.TrimSpace(out.Context + "\nuam capped this glab list: default 10, maximum 20; use -p 2 for the next page.")
	}
	return out
}

func (w *glabShell) stmt(st *syntax.Stmt) {
	if st == nil {
		return
	}
	switch c := st.Cmd.(type) {
	case *syntax.CallExpr:
		w.call(c, true)
	case *syntax.BinaryCmd:
		w.stmt(c.X)
		w.stmt(c.Y)
	case *syntax.Block:
		for _, child := range c.Stmts {
			w.stmt(child)
		}
	default:
		// Denials still apply inside loops/subshells, but their commands and
		// execution count are not rewritten or used to trigger remote lint.
		syntax.Walk(st, func(n syntax.Node) bool {
			if c, ok := n.(*syntax.CallExpr); ok {
				w.call(c, false)
			}
			return true
		})
	}
}

func (w *glabShell) call(c *syntax.CallExpr, rewrite bool) {
	words := make([]string, len(c.Args))
	for i, word := range c.Args {
		words[i] = glabLiteral(word.Parts)
	}
	start := 0
	if len(words) > 0 && words[0] == "rtk" {
		start++
		if len(words) > start && words[start] == "proxy" {
			start++
		}
	}
	if len(words) <= start+1 {
		return
	}
	if words[start] == "git" && words[start+1] == "push" && rewrite {
		w.push = true
	}
	if words[start] != "glab" {
		return
	}
	base := start + 1
	// Cobra accepts the inherited repository flag before the subcommand.
	// Keep word indexes aligned so a later page-size edit touches only its value.
	for base < len(words) {
		if words[base] == "-R" || words[base] == "--repo" {
			base += 2
		} else if strings.HasPrefix(words[base], "--repo=") || strings.HasPrefix(words[base], "-R") {
			base++
		} else {
			break
		}
	}
	if base >= len(words) {
		return
	}
	a := words[base:]
	if deny := glabDeny(a); deny != "" && w.deny == "" {
		w.deny = "uam glab: " + deny
	}
	if !rewrite || !glabList(a) {
		return
	}
	found := false
	for i := 2; i < len(a); i++ {
		flag := a[i]
		if flag == "-P" || flag == "--per-page" {
			found = true
			if i+1 < len(a) {
				w.capWord(c.Args[base+i+1], a[i+1], "")
				i++
			}
			continue
		}
		if strings.HasPrefix(flag, "--per-page=") {
			found = true
			w.capWord(c.Args[base+i], strings.TrimPrefix(flag, "--per-page="), "--per-page=")
			continue
		}
		if strings.HasPrefix(flag, "-P") {
			found = true
			w.capWord(c.Args[base+i], strings.TrimPrefix(flag, "-P"), "-P")
		}
	}
	if found {
		return
	}
	pos := int(c.Args[len(c.Args)-1].End().Offset())
	w.edits = append(w.edits, glabEdit{pos, pos, " -P 10"})
}

// Only static shell words are interpreted; expansions stay with the shell.
func glabLiteral(parts []syntax.WordPart) string {
	var b strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return ""
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			value := glabLiteral(p.Parts)
			if value == "" {
				return ""
			}
			b.WriteString(value)
		default:
			return ""
		}
	}
	return b.String()
}

func (w *glabShell) capWord(word *syntax.Word, value, prefix string) {
	if n, err := strconv.Atoi(value); err == nil && n > 20 {
		w.edits = append(w.edits, glabEdit{int(word.Pos().Offset()), int(word.End().Offset()), prefix + "20"})
	}
}

func glabList(a []string) bool {
	return len(a) >= 2 && (a[1] == "list" || a[1] == "ls") && (a[0] == "issue" || a[0] == "incident" || a[0] == "mr" || a[0] == "ci")
}

func glabDeny(a []string) string {
	for _, arg := range a {
		if arg == "--paginate" || strings.HasPrefix(arg, "--paginate=") {
			return "use one page with -P 10 and -p <page> instead of --paginate."
		}
		if glabList(a) && (arg == "-A" || arg == "--all" || strings.HasPrefix(arg, "--all=")) {
			return "use a filtered list with -P 10 and -p <page> instead of -A/--all."
		}
	}
	if len(a) == 0 {
		return ""
	}
	if a[0] == "api" || a[0] == "search" {
		return "use glab issue list, glab mr list, or glab ci get instead of glab api/search."
	}
	if len(a) < 2 {
		return ""
	}
	switch a[0] + " " + a[1] {
	case "auth login":
		return "use an already authenticated GitLab account; sign in outside the Task instead of glab auth login."
	case "ci view":
		return "use glab ci get instead of the interactive glab ci view."
	case "ci status":
		if slices.Contains(a[2:], "--live") || slices.Contains(a[2:], "--live=true") {
			return "use glab ci status without --live for one status snapshot."
		}
	case "ci trace", "ci retry":
		job := false
		for i := 2; i < len(a); i++ {
			if a[i] == "-p" || a[i] == "--pipeline-id" || a[i] == "-R" || a[i] == "--repo" || a[i] == "-b" || a[i] == "--branch" {
				i++
				continue
			}
			if a[i] != "" && !strings.HasPrefix(a[i], "-") {
				job = true
			}
		}
		if !job {
			return "use glab " + a[0] + " " + a[1] + " <job-id> -p <pipeline-id> instead of choosing a job interactively."
		}
	case "mr create":
		if !slices.ContainsFunc(a[2:], func(s string) bool { return s == "-y" || s == "--yes" || s == "-f" || s == "--fill" }) {
			return "use glab mr create -y or -f to supply a noninteractive choice."
		}
	}
	return ""
}

var glabCIFile = regexp.MustCompile(`(^|/)\.gitlab-ci\.ya?ml$|(^|/)\.gitlab/ci/[^\x00\r\n]*\.ya?ml$`)

func glabLintPush(ctx context.Context, gate glabGate) agentapi.ToolVerdict {
	ctx, cancel := context.WithTimeout(ctx, glabTimeout)
	defer cancel()
	changed, err := glabGit(ctx, gate.Dir, "diff", "--name-only", "-z", "@{push}..HEAD", "--")
	if err != nil {
		base, baseErr := glabGit(ctx, gate.Dir, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
		if baseErr != nil {
			return agentapi.ToolVerdict{}
		}
		changed, err = glabGit(ctx, gate.Dir, "diff", "--name-only", "-z", strings.TrimSpace(base)+"...HEAD", "--")
	}
	if err != nil {
		return agentapi.ToolVerdict{}
	}
	var files []string
	for _, file := range strings.Split(changed, "\x00") {
		if glabCIFile.MatchString(file) {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return agentapi.ToolVerdict{}
	}
	branch, err := glabGit(ctx, gate.Dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return agentapi.ToolVerdict{}
	}
	branch = strings.TrimSpace(branch)
	_, remoteErr := glabGit(ctx, gate.Dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	out := agentapi.ToolVerdict{}
	if remoteErr != nil {
		out.Context = "uam CI lint: static only; this branch has no remote tracking ref."
	}
	for _, file := range files {
		// Prefix and CI-only matching keep git's filenames positional. This
		// runner never invokes a shell or accepts arbitrary glab verbs.
		args := []string{"./" + file}
		if remoteErr == nil {
			args = append(args, "--dry-run", "--ref", branch, "--include-jobs")
		}
		output, runErr := glabRun(ctx, gate, glabTimeout, "ci lint", args...)
		lower := strings.ToLower(output)
		if strings.Contains(lower, "include") && (strings.Contains(lower, "not found") || strings.Contains(lower, "access")) {
			continue
		}
		var exit *exec.ExitError
		// glab 1.116 prints this exact marker only for a successful lint API
		// response whose Valid field is false; API/transport errors allow.
		marker := "./" + file + " is invalid."
		if errors.As(runErr, &exit) && strings.Contains(output, marker) {
			_, detail, _ := strings.Cut(output, marker)
			lines := strings.Split(strings.TrimSpace(detail), "\n")
			out.Deny = fmt.Sprintf("uam CI config invalid (%s):\n%s", file, strings.Join(lines[:min(20, len(lines))], "\n"))
			return out
		}
	}
	return out
}
