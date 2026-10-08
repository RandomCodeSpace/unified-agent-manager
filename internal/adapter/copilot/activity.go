package copilot

import (
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Bounds of the activity texts, in runes.
const (
	maxIntentRunes      = 160
	maxRetryReasonRunes = 64
)

// activity is the running turn's live state and what fills it: the abort
// reason the turn's end reports, and each agent's last failed model call,
// which the retry that follows it describes. Intent and retries are never
// recorded by the CLI, so they exist only live.
type activity struct {
	now         agentapi.Activity
	abortReason string
	// failures holds, by agent ID ("" for the main agent), the failed model
	// call no retry or success followed yet.
	failures map[string]callFailure
}

type callFailure struct {
	status  int
	network bool
}

// startActivityLocked starts the activity afresh for a new turn. The CLI
// starts the main agent's turn again for each model call; those keep it.
func (c *conversation) startActivityLocked() {
	if !c.turnRunning {
		c.act = activity{}
	}
}

// observeActivityLocked folds the events behind the turn's activity into it
// and reports whether ev needs nothing more. It logs the stop, retry and
// notice events it sees, so a live run records what the CLI sends.
func (c *conversation) observeActivityLocked(ev copilot.SessionEvent, agentID string) bool {
	switch d := ev.Data.(type) {
	case *rpc.AssistantIntentData:
		// The CLI derives it from the todo list and never tags it with an
		// agent, so a subagent's row can show as the main agent's intent.
		if agentID != "" {
			return true
		}
		intent := clipRuneCount(strings.TrimSpace(displaytext.Sanitize(d.Intent)), maxIntentRunes)
		if intent != c.act.now.Intent {
			c.act.now.Intent = intent
			c.emitActivityLocked()
		}
		return true
	case *rpc.AbortData:
		log.Debug("copilot abort", "session", c.id, "agent", agentID, "reason", string(d.Reason))
		if agentID == "" {
			c.act.abortReason = string(d.Reason)
		}
		return true
	case *rpc.ModelCallFailureData:
		f := callFailure{network: d.FailureKind != nil && *d.FailureKind == rpc.ModelCallFailureKindTransport}
		if d.StatusCode != nil {
			f.status = int(*d.StatusCode)
		}
		log.Debug("copilot model call failure", "session", c.id, "agent", agentID, "status", f.status, "network", f.network, "source", string(d.Source))
		if c.act.failures == nil {
			c.act.failures = map[string]callFailure{}
		}
		c.act.failures[agentID] = f
		return true
	case *rpc.AssistantTurnRetryData:
		log.Debug("copilot turn retry", "session", c.id, "agent", agentID, "reason", orEmpty(d.Reason))
		c.retryLocked(agentID, d.Reason, ev.Timestamp)
		return true
	case *rpc.AssistantUsageData:
		// The call went through: its retries are over. The usage itself is
		// handled as before, which also reports a subagent's record.
		delete(c.act.failures, agentID)
		if agentID != "" {
			if sa := c.subs.byID[agentID]; sa != nil {
				sa.Retry = nil
			}
		} else if c.act.now.Retry != nil {
			c.act.now.Retry = nil
			c.emitActivityLocked()
		}
	case *rpc.SessionInfoData:
		log.Debug("copilot session info", "session", c.id, "agent", agentID, "type", d.InfoType, "shown", shownInfo[d.InfoType])
	case *rpc.SessionWarningData:
		log.Debug("copilot session warning", "session", c.id, "agent", agentID, "type", d.WarningType, "remediation", orEmpty((*string)(d.Remediation)))
	}
	return false
}

// retryLocked counts a retry of the agent's current model call, described
// by the failure before it, on the turn's activity or on the subagent's
// record.
func (c *conversation) retryLocked(agentID string, reason *string, at time.Time) {
	f := c.act.failures[agentID]
	delete(c.act.failures, agentID)
	next := func(prev *agentapi.Retry) *agentapi.Retry {
		r := agentapi.Retry{Count: 1, Status: f.status, Network: f.network, At: at}
		if prev != nil {
			r.Count = prev.Count + 1
		}
		if reason != nil {
			r.Reason = clipRuneCount(strings.TrimSpace(displaytext.Sanitize(*reason)), maxRetryReasonRunes)
		}
		return &r
	}
	if agentID == "" {
		c.act.now.Retry = next(c.act.now.Retry)
		c.emitActivityLocked()
		return
	}
	sa := c.subs.byID[agentID]
	if sa == nil || sa.Status != agentapi.SubagentRunning {
		return
	}
	sa.Retry = next(sa.Retry)
	snapshot := sa.Snapshot()
	c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &snapshot})
}

func (c *conversation) emitActivityLocked() {
	a := c.act.now
	c.emitLocked(agentapi.Event{Kind: agentapi.EventActivity, Activity: &a})
}

// shownInfo lists the session.info types shown as notices. The others
// describe the CLI's own terminal (tips, slash commands) or what uam shows
// itself (timing, context window, snapshots, configuration).
var shownInfo = map[string]bool{"authentication": true, "model": true, "mcp": true, "notification": true}

// remedies says, per remediation a warning names, what to do in uam.
var remedies = map[rpc.RemediationAction]string{
	rpc.RemediationActionSignIn:               "Sign in to GitHub Copilot again in Settings.",
	rpc.RemediationActionSwitchAccount:        "Sign in to GitHub Copilot with another account in Settings.",
	rpc.RemediationActionShowAccount:          "Settings shows which GitHub Copilot account is signed in.",
	rpc.RemediationActionAllowSandboxOutbound: "Allow outbound network access in the Copilot sandbox policy.",
	rpc.RemediationActionReviewSandboxPolicy:  "Review the Copilot sandbox policy.",
}

// noticeText words a session.info or session.warning event as a notice, or
// reports false when it is not shown. Both are recorded, so live events and
// history share this.
func noticeText(data rpc.SessionEventData) (string, bool) {
	switch d := data.(type) {
	case *rpc.SessionInfoData:
		text := displaytext.Sanitize(d.Message)
		if !shownInfo[d.InfoType] || text == "" {
			return "", false
		}
		if d.Tip != nil && displaytext.Sanitize(*d.Tip) != "" {
			text = sentence(text) + " " + displaytext.Sanitize(*d.Tip)
		}
		return text + noticeLink(d.URL), true
	case *rpc.SessionWarningData:
		text := "Warning: " + displaytext.Sanitize(d.Message)
		if d.Remediation != nil && remedies[*d.Remediation] != "" {
			text = sentence(text) + " " + remedies[*d.Remediation]
		}
		return text + noticeLink(d.URL), true
	}
	return "", false
}

// sentence ends s with a full stop unless it already ends a sentence.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

var linkEscaper = strings.NewReplacer("(", "%28", ")", "%29", " ", "%20", "<", "%3C", ">", "%3E", `\`, "%5C")

// noticeLink is a Markdown link to u, or "" unless u is an absolute https
// URL without credentials.
func noticeLink(u *string) string {
	if u == nil {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(*u))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return " [Learn more](" + linkEscaper.Replace(parsed.String()) + ")"
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// clipRuneCount cuts s to at most n runes.
func clipRuneCount(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
