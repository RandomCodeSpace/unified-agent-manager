package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// parseAnswer accepts a prose preamble or a fenced object, but never a second
// answer. The JSON decoder handles balanced objects and escaped string braces.
func parseAnswer(reply string, v any) error {
	if len(reply) > 2048 {
		return errors.New("answer is too long")
	}
	reply = strings.TrimSpace(thinkRE.ReplaceAllString(reply, ""))
	lower := strings.ToLower(reply)
	if strings.Contains(lower, "i cannot") || strings.Contains(lower, "as an ai") {
		return errors.New("refusal is not an answer")
	}
	start := strings.IndexByte(reply, '{')
	if start < 0 {
		return errors.New("answer must contain one JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(reply[start:]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	tail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reply[start+int(dec.InputOffset()):]), "```"))
	if tail != "" {
		return errors.New("answer must contain only one object")
	}
	return nil
}

type utilityJob struct {
	purpose   string
	system    string
	validate  func(string, string) (string, error)
	retryHint string
	fallback  string
	// wanted, when set, says why the answer is no longer wanted, checked
	// before each attempt; nil while it is.
	wanted func() error
}

// runJob retries only an invalid answer, in a fresh provider request. Errors,
// pauses and a second invalid answer leave the caller's deterministic fallback.
// No attempt is made once the job's answer is no longer wanted or its
// working folder is gone.
func (m *Manager) runJob(ctx context.Context, job utilityJob, call UtilityCall, runner agentapi.UtilityRunner, req agentapi.UtilityRequest) (string, error) {
	call.Purpose, call.Model = job.purpose, req.Model
	req.Purpose, req.System = job.purpose, job.system
	for attempt := 0; attempt < 2; attempt++ {
		if job.wanted != nil {
			if err := job.wanted(); err != nil {
				return job.fallback, err
			}
		}
		if req.Workdir != "" {
			if info, err := os.Stat(req.Workdir); err != nil || !info.IsDir() {
				return job.fallback, errors.New("the thread's folder is gone")
			}
		}
		call.Retry = attempt > 0
		if call.Retry {
			req.System += "\nYour previous answer was not valid. " + job.retryHint
			req.Timeout = 20 * time.Second
		}
		var answer string
		var invalid error
		_, err := m.runUtilityChecked(ctx, call, req.System+req.Prompt, func(ctx context.Context, usage func(agentapi.UtilityUsage)) (string, error) {
			req.OnUsage = usage
			return runner.RunUtility(ctx, req)
		}, func(c *UtilityCall, reply string, err error) {
			if err != nil {
				c.Fallback = true
				return
			}
			answer, invalid = job.validate(reply, req.Prompt)
			c.Valid = new(invalid == nil)
			if invalid == nil {
				c.Answer = answer
			} else {
				c.Reason = "invalid_answer"
				c.Fallback = attempt == 1
			}
		})
		if err != nil {
			return job.fallback, err
		}
		if invalid == nil {
			return answer, nil
		}
	}
	return job.fallback, nil
}

// recordAnswerLocked counts the job's answer toward the purpose's pause: a
// valid one, or an invalid one that ended the job. An invalid first answer
// is left to the retry, so a turn counts once.
func (l *utilityLog) recordAnswerLocked(call UtilityCall) {
	if call.Valid == nil || !*call.Valid && !call.Fallback || dayOf(call.At) != l.day {
		return
	}
	answers := append(l.answers[call.Purpose], *call.Valid)
	if len(answers) > 10 {
		answers = answers[len(answers)-10:]
	}
	l.answers[call.Purpose] = answers
	invalid := 0
	for _, valid := range answers {
		if !valid {
			invalid++
		}
	}
	if invalid >= 4 {
		l.paused[call.Purpose] = true
	}
}

// suggestionURL matches a link: a scheme with //, one a browser follows
// without (mailto:, javascript:, data:, file:), www., or a host under a
// common web domain. File names such as README.md or main.go do not match.
var suggestionURL = regexp.MustCompile(`(?i)(?:\b[a-z][a-z0-9+.-]*://|\b(?:mailto|javascript|data|file):|\bwww\.|\b(?:[a-z0-9-]+\.)+(?:com|org|net|io|dev|app|ai|co|info|gov|edu)\b)`)

// sentenceBreak is the end of one sentence followed by another;
// sentenceEnds splits text into its sentences and lines.
var (
	sentenceBreak = regexp.MustCompile(`[.!?]+\s+\S`)
	sentenceEnds  = regexp.MustCompile(`[.!?]+(?:\s+|$)|\n`)
)

func validateSuggestion(reply, input string) (string, error) {
	if utf8.RuneCountInString(reply) > 320 {
		return "", errors.New("suggestion is too long")
	}
	if _, source, ok := strings.Cut(input, "<agent_reply>\n"); ok {
		input, _, _ = strings.Cut(source, "\n</agent_reply>")
	}
	reply = strings.TrimSpace(thinkRE.ReplaceAllString(reply, ""))
	lower := strings.ToLower(reply)
	if reply == "" || utf8.RuneCountInString(reply) > 80 || strings.ContainsAny(reply, "\r\n|`") || suggestionURL.MatchString(reply) || strings.Contains(lower, "i cannot") || strings.Contains(lower, "as an ai") || displaytext.Sanitize(reply) != reply || repeatsSentence(input, reply) || sentenceBreak.MatchString(reply) {
		return "", errors.New("suggestion must be one new sentence of at most 80 characters without a URL or pipe")
	}
	return reply, nil
}

// repeatsSentence reports whether reply is one of text's sentences or
// lines, ignoring case and ending punctuation. Words the reply only shares,
// such as "Commit this" after "Want me to commit this?", do not count.
func repeatsSentence(text, reply string) bool {
	want := strings.ToLower(strings.TrimRight(reply, ".!? "))
	for _, sentence := range sentenceEnds.Split(text, -1) {
		if strings.ToLower(strings.TrimSpace(sentence)) == want {
			return true
		}
	}
	return false
}

var suggestionJob = utilityJob{purpose: purposeSuggestReplies, system: suggestRepliesSystem, validate: validateSuggestion, retryHint: "Reply with exactly one short user message, such as: Commit this"}
