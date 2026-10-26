package copilot

import "cmp"

// noAttribution is the git rule until the owner's rules arrive.
const noAttribution = "Write commit messages and pull or merge request titles and descriptions as the owner's own work: no Co-authored-by trailer and no line crediting an AI, agent or tool, unless the owner asks for one."

// SetGitRules gives sessions opened or resumed afterwards, and the subagents
// they start, rules, which follow taskSystem and subagentSystem.
// providerCoAuthor turns on the CLI's own co-author tool and commit-trailer
// instructions; off, they are dropped.
func (p *webProvider) SetGitRules(rules string, providerCoAuthor bool) {
	p.customMu.Lock()
	defer p.customMu.Unlock()
	p.gitRules, p.coauthor = rules, providerCoAuthor
}

// git is the git rules and the CLI co-author switch for a session opened now.
func (p *webProvider) git() (string, bool) {
	p.customMu.Lock()
	defer p.customMu.Unlock()
	return cmp.Or(p.gitRules, noAttribution), p.coauthor
}
