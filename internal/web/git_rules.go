package web

import "cmp"

// maxCoAuthorRunes bounds the co-author trailer's value in Settings.
const maxCoAuthorRunes = 200

// defaultCommitPrompt is the commit message instructions when Settings
// leaves them empty.
const defaultCommitPrompt = `Follow Conventional Commits: a subject line "type(scope): summary", where type is feat, fix, docs, style, refactor, perf, test, build, ci, chore or revert and the scope is optional. Write the summary in the imperative, in plain words, at most 72 characters, with no trailing period. Add a body only when the reason is not obvious: a blank line, then a few short lines on why. Mark a breaking change with "!" after the type and a "BREAKING CHANGE:" footer.`

// gitRules are the instructions agents get for commits and pull or merge
// requests (agentapi.GitRulesUser), and whether the provider adds its own
// co-author trailer: only when the co-author is on without a name.
func (s Settings) gitRules() (string, bool) {
	rules := "Commit messages:\n" + cmp.Or(s.CommitPrompt, defaultCommitPrompt) + "\n\nPull and merge requests:\n" + s.mrPrompt() + "\n\n"
	switch {
	case !s.CoAuthor.Enabled:
		return rules + "Write commit messages and pull or merge request titles and descriptions as the owner's own work: no Co-authored-by trailer and no line crediting an AI, agent or tool, unless the owner asks for one.", false
	case s.CoAuthor.Name != "":
		return rules + `End every commit message with the trailer "Co-authored-by: ` + s.CoAuthor.Name + `" after a blank line. Add no other co-author trailer and no line crediting an AI, agent or tool.`, false
	default:
		return rules + "Add no line crediting an AI, agent or tool other than the co-author trailer.", true
	}
}
