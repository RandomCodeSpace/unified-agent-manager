package web

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Title job bounds. A job waits for one of maxTitleJobs slots before its
// titleTimeout starts.
const (
	titleTimeout       = 20 * time.Second
	maxTitleJobs       = 2
	maxTitleRunes      = 60
	maxTitleInputRunes = 2000
)

// titlerLocked returns the provider's Titler when it is available and has
// the titles capability, else nil.
func (m *Manager) titlerLocked(name string) agentapi.Titler {
	if info := m.infos[name]; !info.Available || !info.Capabilities.Titles {
		return nil
	}
	t, _ := m.providers[name].(agentapi.Titler)
	return t
}

// titleModelLocked returns the model that titles s from in, or "" when none
// does. Only a Task's first message is titled: a prompt, not a command, with
// text, sent while the Task has no name, no title, no user item and no
// submission the provider may have taken, and while the settings name a
// model for its provider.
func (m *Manager) titleModelLocked(s *webSession, in turnInput) string {
	model := m.settings.TitleModel[s.provider]
	if model == "" || in.command != "" || strings.TrimSpace(in.text) == "" || s.name != "" || s.title != "" || s.truncated || m.titlerLocked(s.provider) == nil {
		return ""
	}
	if slices.ContainsFunc(s.items, func(it agentapi.Item) bool { return it.Kind == agentapi.ItemUser && it.AgentID == "" }) ||
		slices.ContainsFunc(s.submissions, func(sub Submission) bool {
			return sub.Status == SubmissionAccepted || sub.Status == SubmissionUncertain
		}) {
		return ""
	}
	return model
}

// startTitle titles s from text with model in the background. It never
// delays the turn.
func (m *Manager) startTitle(s *webSession, model, text string) {
	m.mu.Lock()
	titler := m.titlerLocked(s.provider)
	if m.closed || titler == nil {
		m.mu.Unlock()
		return
	}
	input := strings.TrimSpace(displaytext.Sanitize(text))
	if runes := []rune(input); len(runes) > maxTitleInputRunes {
		input = string(runes[:maxTitleInputRunes])
	}
	req := agentapi.TitleRequest{Model: model, Workdir: s.workdir, Text: input}
	renames := s.renames
	m.titles.Add(1)
	m.mu.Unlock()
	go m.title(s, titler, req, renames)
}

// title runs one title job. A failure, a timeout or an empty title leaves the
// provider's title; nothing is retried. A rename made meanwhile wins.
func (m *Manager) title(s *webSession, titler agentapi.Titler, req agentapi.TitleRequest, renames uint64) {
	defer m.titles.Done()
	select {
	case m.titleSlots <- struct{}{}:
		defer func() { <-m.titleSlots }()
	case <-m.ctx.Done():
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, titleTimeout)
	reply, err := titler.Title(ctx, req)
	cancel()
	title := cleanGeneratedTitle(reply)
	if err == nil && title == "" {
		err = errors.New("the reply held no title")
	}
	if err != nil {
		log.Info("task title not generated; the provider's title stays", "session", s.id, "model", req.Model, "error", err)
		return
	}
	m.mu.Lock()
	if s.removed || s.renames != renames {
		m.mu.Unlock()
		log.Info("task renamed while its title was generated; the name stays", "session", s.id)
		return
	}
	before := m.summaryLocked(s)
	s.title = title
	conv := s.conv
	m.changedLocked(s, before)
	m.mu.Unlock()
	if conv == nil {
		log.Info("task titled; its conversation closed before the provider could take the title", "session", s.id, "model", req.Model)
		return
	}
	ctx, cancel = context.WithTimeout(m.ctx, controlTimeout)
	err = conv.SetTitle(ctx, title)
	cancel()
	if err != nil {
		log.Info("task titled; the provider kept its own title", "session", s.id, "model", req.Model, "error", err)
		return
	}
	log.Info("task titled", "session", s.id, "model", req.Model)
}

var (
	thinkRE       = regexp.MustCompile(`(?is)<think>.*?</think>`)
	titleTagRE    = regexp.MustCompile(`(?is)<(title|session-title)>(.*?)</(?:title|session-title)>`)
	titlePrefixRE = regexp.MustCompile(`(?i)^title\s*:\s*`)
	spacesRE      = regexp.MustCompile(`\s+`)
)

// titleWrappers are the pairs stripped from both ends of a title.
var titleWrappers = [][2]string{{`"`, `"`}, {`'`, `'`}, {"`", "`"}, {"*", "*"}, {"_", "_"}, {"“", "”"}, {"‘", "’"}, {"«", "»"}}

// cleanGeneratedTitle turns a model's reply into a title: without reasoning
// blocks, the content of a title element when there is one, its first line,
// sanitized, without a "Title:" label, wrapping quotes or emphasis, or
// trailing punctuation, and cut to maxTitleRunes at a word boundary. It
// returns "" when nothing is left.
func cleanGeneratedTitle(reply string) string {
	reply = thinkRE.ReplaceAllString(reply, "")
	if m := titleTagRE.FindStringSubmatch(reply); m != nil {
		reply = m[2]
	}
	line := ""
	for l := range strings.Lines(reply) {
		if line = strings.TrimSpace(l); line != "" {
			break
		}
	}
	title := spacesRE.ReplaceAllString(displaytext.Sanitize(line), " ")
	for {
		before := title
		title = strings.TrimSpace(titlePrefixRE.ReplaceAllString(title, ""))
		for _, w := range titleWrappers {
			if len(title) >= len(w[0])+len(w[1]) && strings.HasPrefix(title, w[0]) && strings.HasSuffix(title, w[1]) {
				title = strings.TrimSpace(title[len(w[0]) : len(title)-len(w[1])])
			}
		}
		title = strings.TrimRight(title, ".,:; ")
		if title == before {
			break
		}
	}
	if utf8.RuneCountInString(title) > maxTitleRunes {
		// The rune after the limit is kept to see whether the cut ends a word.
		runes := []rune(title)[:maxTitleRunes+1]
		cut := maxTitleRunes
		for i := maxTitleRunes; i > 0; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		title = strings.TrimRight(string(runes[:cut]), ".,:; ")
	}
	return title
}
