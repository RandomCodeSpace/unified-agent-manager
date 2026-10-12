package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"mvdan.cc/sh/v3/syntax"
)

const (
	gitLabReceiptPrefix = "uam-commit:"
	gitLabReceiptLimit  = 128
	gitLabReceiptBytes  = 256 << 10
	gitLabReceiptTime   = 3 * time.Second
)

type gitLabCommitReceipt struct{ taskID, sha, branch, origin string }

func gitLabOriginFingerprint(gate glabGate) string {
	origin := sha256.Sum256([]byte(gate.Host + "\n" + gate.Path))
	return hex.EncodeToString(origin[:])
}

// gitLabCommitAction is safe as an unquoted shell assignment: the Task UUID
// and hex-encoded branch contain no shell syntax. It changes only the reflog,
// never the commit object or the repository's hooks/configuration.
func gitLabCommitAction(taskID, branch string, gate glabGate) string {
	if !validRequestID(taskID) || branch == "" || len(branch) > 1024 || gate.Host == "" || gate.Path == "" {
		return ""
	}
	// glabOrigin has already removed credentials and normalized the remote.
	// Binding that identity prevents replay after origin points elsewhere.
	return gitLabReceiptPrefix + taskID + ":" + hex.EncodeToString([]byte(branch)) + ":" + gitLabOriginFingerprint(gate)
}

func gitLabProvenanceAction(ctx context.Context, dir, taskID string) string {
	ctx, cancel := context.WithTimeout(ctx, gitLabReceiptTime)
	defer cancel()
	gate, ok := glabOrigin(ctx, dir)
	if !ok {
		return ""
	}
	return gitLabCommitAction(taskID, gitLabReceiptBranch(ctx, dir), gate)
}

// markGitLabCommits marks direct, literal git commit calls, including their
// rtk spelling. Scripts, aliases and git global options are deliberately not
// interpreted. Environment applies to this call only, not a later pull/reset.
func markGitLabCommits(command, action string) string {
	if action == "" || command == "" {
		return command
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return command
	}
	var positions []int
	unsupported := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.FuncDecl); ok {
			// A function can shadow git/rtk or run in a different repository.
			unsupported = true
			return false
		}
		c, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		words := make([]string, len(c.Args))
		for i, word := range c.Args {
			words[i] = glabLiteral(word.Parts)
		}
		if len(words) > 0 && (words[0] == "source" || words[0] == "." || words[0] == "eval" || words[0] == "alias") {
			unsupported = true
		}
		start := 0
		if len(words) > 0 && words[0] == "rtk" {
			start++
			if len(words) > start && words[start] == "proxy" {
				start++
			}
		}
		if len(words) <= start+1 || words[start] != "git" || words[start+1] != "commit" {
			return true
		}
		for _, a := range c.Assigns {
			if a.Name != nil && strings.HasPrefix(a.Name.Value, "GIT_") {
				return true
			}
		}
		positions = append(positions, int(c.Pos().Offset()))
		return true
	})
	if unsupported {
		return command
	}
	slices.Sort(positions)
	for i := len(positions) - 1; i >= 0; i-- {
		p := positions[i]
		command = command[:p] + "GIT_REFLOG_ACTION=" + action + " " + command[p:]
	}
	return command
}

func (m *Manager) stampGitLabCommit(ctx context.Context, s *webSession, use agentapi.ToolUse) map[string]any {
	if use.Tool != "bash" && use.Tool != "shell" {
		return nil
	}
	command, _ := use.Args["command"].(string)
	// Avoid a git process for ordinary shell calls. Parsing is still required;
	// this inexpensive prefilter is not a provenance decision.
	if !strings.Contains(command, "commit") {
		return nil
	}
	m.mu.Lock()
	dir, id := s.workdir, s.id
	m.mu.Unlock()
	if use.Workdir != "" && use.Workdir != dir {
		return nil
	}
	marked := markGitLabCommits(command, gitLabProvenanceAction(ctx, dir, id))
	if marked == command {
		return nil
	}
	args := maps.Clone(use.Args)
	args["command"] = marked
	return args
}

func gitLabReceiptBranch(ctx context.Context, dir string) string {
	git, err := lookGit()
	if err != nil {
		return ""
	}
	out, code, _, err := runGit(ctx, git, dir, 2048, "symbolic-ref", "--quiet", "--short", "HEAD")
	branch := strings.TrimSpace(string(out))
	if err != nil || code != 0 || len(branch) > 1024 {
		return ""
	}
	return branch
}

// readGitLabCommitReceipts reads only the current branch's bounded reflog.
// Checking the branch encoded before the call against the actual ref excludes
// a command that changed branch before committing. HEAD movement alone never
// establishes provenance. Oldest first lets the latest receipt win downstream.
func readGitLabCommitReceipts(ctx context.Context, dir string) []gitLabCommitReceipt {
	ctx, cancel := context.WithTimeout(ctx, gitLabReceiptTime)
	defer cancel()
	gate, ok := glabOrigin(ctx, dir)
	if !ok {
		return nil
	}
	branch := gitLabReceiptBranch(ctx, dir)
	if branch == "" {
		return nil
	}
	git, err := lookGit()
	if err != nil {
		return nil
	}
	out, code, _, err := runGit(ctx, git, dir, gitLabReceiptBytes, "reflog", "show", "-n128", "--format=%H%x00%gs", "refs/heads/"+branch)
	if err != nil || code != 0 {
		return nil
	}
	return parseGitLabCommitReceipts(string(out), branch, gate)
}

func parseGitLabCommitReceipts(output, branch string, gate glabGate) []gitLabCommitReceipt {
	var found []gitLabCommitReceipt
	for _, line := range strings.Split(output, "\n") {
		sha, subject, ok := strings.Cut(line, "\x00")
		if !ok || !gitLabReceiptSHA(sha) || !strings.HasPrefix(subject, gitLabReceiptPrefix) {
			continue
		}
		action, _, ok := strings.Cut(subject, ": ")
		if !ok {
			continue
		}
		taskID, _, ok := strings.Cut(strings.TrimPrefix(action, gitLabReceiptPrefix), ":")
		if !ok || action != gitLabCommitAction(taskID, branch, gate) {
			continue
		}
		found = append(found, gitLabCommitReceipt{taskID: taskID, sha: sha, branch: branch, origin: gitLabOriginFingerprint(gate)})
		if len(found) == gitLabReceiptLimit {
			break
		}
	}
	slices.Reverse(found)
	return found
}

func gitLabReceiptSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func (m *Manager) succeededToolUse(ctx context.Context, s *webSession, use agentapi.ToolUse) {
	if use.Tool != "bash" && use.Tool != "shell" && use.Tool != "read_bash" {
		return
	}
	m.mrFiles.done(use)
	m.mu.Lock()
	dir, id := s.workdir, s.id
	active := !m.closed && !s.removed && m.sessions[id] == s
	m.mu.Unlock()
	if !active {
		return
	}
	receipts := readGitLabCommitReceipts(ctx, dir)
	for i := len(receipts) - 1; i >= 0; i-- {
		if receipts[i].taskID == id {
			m.saveGitLabCommitReceipt(ctx, receipts[i])
			return
		}
	}
}

func (m *Manager) saveGitLabCommitReceipt(ctx context.Context, receipt gitLabCommitReceipt) {
	if err := m.recordGitLabCommit(ctx, receipt.taskID, receipt.sha, receipt.branch, receipt.origin); err != nil {
		log.Warn("record GitLab commit failed", "session", receipt.taskID, "error", err)
	}
}

// reconcileGitLabCommits recovers receipts after a restart, a store failure,
// or a shell tool returning before its background process has committed. Only
// extant Tasks in this exact workdir can own a receipt. The monitor calls it
// once per cycle; its context bounds the whole pass and each repo is read once.
func (m *Manager) reconcileGitLabCommits(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	m.mu.Lock()
	dirs := make(map[string]map[string]bool)
	for id, s := range m.sessions {
		if s.removed || m.projects[s.projectID] == nil {
			continue
		}
		if dirs[s.workdir] == nil {
			dirs[s.workdir] = make(map[string]bool)
		}
		dirs[s.workdir][id] = true
	}
	m.mu.Unlock()
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		if ctx.Err() != nil {
			return
		}
		receipts := readGitLabCommitReceipts(ctx, dir)
		for i := len(receipts) - 1; i >= 0; i-- {
			receipt := receipts[i]
			if dirs[dir][receipt.taskID] {
				m.saveGitLabCommitReceipt(ctx, receipt)
				delete(dirs[dir], receipt.taskID)
			}
		}
	}
}
