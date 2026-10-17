package web

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Title job bounds. A job waits for one of maxTitleJobs slots before its
// titleTimeout starts; every model it asks shares that budget. A model's
// title has at most maxTitleWords words.
const (
	titleTimeout       = 45 * time.Second
	maxTitleJobs       = 2
	maxTitleRunes      = 60
	maxTitleWords      = 7
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

// utilityModelLocked returns the provider's Utility model, the model UAM
// uses for its own small AI jobs: the one Settings name, else the cheapest
// priced model; "" when Settings opt the provider out or none is priced.
func (m *Manager) utilityModelLocked(provider string) string {
	switch model := m.settings.TitleModel[provider]; model {
	case store.WebTitleModelNone:
		return ""
	case "":
		return m.cheapestModelLocked(provider)
	default:
		return model
	}
}

// cheapestModelLocked returns the provider's cheapest priced model, or ""
// when none is priced. A model is priced when it reports input and output
// prices; auto and models hidden in Settings are skipped. The lowest
// input+output price per token wins, then the lower input price, then the
// lower ID.
func (m *Manager) cheapestModelLocked(provider string) string {
	type candidate struct {
		id           string
		total, input float64
	}
	var best *candidate
	for _, mo := range m.infos[provider].Models {
		p := mo.Prices
		if mo.ID == "auto" || p == nil || p.Input == nil || p.Output == nil || slices.Contains(m.settings.HiddenModels[provider], mo.ID) {
			continue
		}
		batch := float64(cmp.Or(p.BatchSize, defaultPriceBatch))
		c := candidate{id: mo.ID, total: (*p.Input + *p.Output) / batch, input: *p.Input / batch}
		if best == nil || c.total < best.total || c.total == best.total && (c.input < best.input || c.input == best.input && c.id < best.id) {
			best = &c
		}
	}
	if best == nil {
		return ""
	}
	return best.id
}

// defaultPriceBatch is the token count a price is for when the provider
// reports no batch size, as the browser assumes too.
const defaultPriceBatch = 1_000_000

// titleCandidate is a model a title job may ask. images is set when the
// first message's images go with the request; session when it is the
// Task's own model, asked because no Utility model is set.
type titleCandidate struct {
	model   string
	images  bool
	session bool
}

// titlePlan is how a Task's first message gets its title: the models to
// ask, in order, each after the one before failed, and whether the job waits
// for the first turn's reply. renames is the Task's rename count when the
// message was sent.
type titlePlan struct {
	models     []titleCandidate
	afterReply bool
	text       string
	renames    uint64
}

// maxTitleImages bounds the images a title request carries.
const maxTitleImages = 3

// titlePlanLocked returns how s is titled from in, or nil when it is not.
// Only a Task's first message is titled: a prompt, not a command, with
// text, uploads or files, sent while the Task has no name, no title other
// than a fallback, no user item and no submission the provider may have
// taken, and while its provider titles Tasks.
//
// A Utility model set in Settings titles it. Unset, the cheapest priced
// model does, then the Task's own model when that one fails (not auto,
// which names no model; the provider asks it at its lowest effort). A message
// with no text goes with its images to a model that accepts them; otherwise
// the job waits for the first turn to end and titles from the agent's reply.
func (m *Manager) titlePlanLocked(s *webSession, in turnInput, uploads []*upload) *titlePlan {
	text := strings.TrimSpace(in.text)
	if in.command != "" || text == "" && len(uploads) == 0 && len(in.files) == 0 || s.title != "" && !s.titleFallback || !s.untitledFirstLocked() || m.titlerLocked(s.provider) == nil {
		return nil
	}
	var models []titleCandidate
	switch setting := m.settings.TitleModel[s.provider]; setting {
	case store.WebTitleModelNone:
		return nil
	case "":
		cheapest := m.cheapestModelLocked(s.provider)
		if cheapest != "" {
			models = append(models, titleCandidate{model: cheapest})
		}
		if s.model != "" && s.model != "auto" && s.model != cheapest && m.modelLocked(s.provider, s.model).ID != "" {
			models = append(models, titleCandidate{model: s.model, session: true})
		}
	default:
		models = append(models, titleCandidate{model: setting})
	}
	if len(models) == 0 {
		return nil
	}
	plan := &titlePlan{text: in.text, renames: s.renames}
	if text != "" {
		plan.models = models
		return plan
	}
	for i := range models {
		models[i].images = m.seesImagesLocked(s.provider, models[i].model, uploads)
	}
	if !models[0].images {
		plan.afterReply = true
		plan.models = models
		return plan
	}
	// Without text, only a model that sees the images titles at once.
	plan.models = slices.DeleteFunc(models, func(c titleCandidate) bool { return !c.images })
	return plan
}

// untitledFirstLocked reports whether the next message s takes is its
// first and s has no name: a full history, no user item and no submission
// the provider may have taken. The caller holds mu.
func (s *webSession) untitledFirstLocked() bool {
	return s.name == "" && !s.truncated &&
		!slices.ContainsFunc(s.items, func(it agentapi.Item) bool { return it.Kind == agentapi.ItemUser && it.AgentID == "" }) &&
		!slices.ContainsFunc(s.submissions, func(sub Submission) bool {
			return sub.Status == SubmissionAccepted || sub.Status == SubmissionUncertain
		})
}

// sendFallbackLocked titles s from in as it is sent, when s has neither a
// name nor a title, or only the fallback of a first message the provider
// did not take: in's text (a command with its name), else its first
// upload's or file's name. A model's title replaces it when one comes. It
// reports whether it set one. The caller holds mu.
func (s *webSession) sendFallbackLocked(in turnInput, uploads []*upload) bool {
	if s.name != "" || s.title != "" && (!s.titleFallback || !s.untitledFirstLocked()) {
		return false
	}
	text := in.text
	if in.command != "" {
		text = "/" + strings.TrimPrefix(in.command, "/") + " " + text
	}
	names := []string{text}
	for _, u := range uploads {
		names = append(names, u.Name)
	}
	for _, f := range in.files {
		names = append(names, filepath.Base(f))
	}
	for _, name := range names {
		if title := fallbackTitle(name); title != "" {
			s.title, s.titleFallback = title, true
			return true
		}
	}
	return false
}

// replyFallbackLocked titles s once a turn ended when it still has neither
// a name nor a title: from its first message, else from the first line of
// the agent's last reply. The caller holds mu.
func (m *Manager) replyFallbackLocked(s *webSession) {
	if s.name != "" || s.title != "" {
		return
	}
	title := ""
	for _, it := range s.items {
		if ownerMessage(it) {
			title = fallbackTitle(it.Text)
			break
		}
	}
	for i := len(s.items) - 1; title == "" && i >= 0; i-- {
		if it := s.items[i]; it.AgentID == "" && it.Kind == agentapi.ItemAssistant && strings.TrimSpace(it.Text) != "" {
			for line := range strings.Lines(it.Text) {
				if title = fallbackTitle(strings.TrimLeft(line, "#>*-` \t")); title != "" {
					break
				}
			}
			break
		}
	}
	if title == "" {
		return
	}
	s.title, s.titleFallback = title, true
	m.kickPlaceNameLocked(s)
}

// fallbackTitle is text as a title until a model gives one: its words on
// one line, sanitized, and when longer than maxTitleRunes cut at a word
// boundary with an ellipsis, which counts toward the limit.
func fallbackTitle(text string) string {
	title := strings.TrimSpace(spacesRE.ReplaceAllString(displaytext.Sanitize(spacesRE.ReplaceAllString(text, " ")), " "))
	runes := []rune(title)
	if len(runes) <= maxTitleRunes {
		return title
	}
	cut := maxTitleRunes - 1
	for i := cut; i > 0; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(runes[:cut]), " .,:;-") + "…"
}

// seesImagesLocked reports whether model accepts one of the uploads'
// images. A model that reports no media gate is not assumed to.
func (m *Manager) seesImagesLocked(provider, model string, uploads []*upload) bool {
	media := m.modelLocked(provider, model).Media
	return media != nil && media.Images && slices.ContainsFunc(uploads, func(u *upload) bool {
		return isImage(u.MIME) && mediaAllowed(model, media, u.MIME) == nil
	})
}

// titleImages returns the blobs a title request to model carries: the
// images it accepts, at most maxTitleImages or its own limit.
func (m *Manager) titleImages(provider, model string, blobs []agentapi.Blob) []agentapi.Blob {
	m.mu.Lock()
	media := m.modelLocked(provider, model).Media
	m.mu.Unlock()
	if media == nil || !media.Images {
		return nil
	}
	limit := maxTitleImages
	if media.MaxImages > 0 {
		limit = min(limit, media.MaxImages)
	}
	var out []agentapi.Blob
	for _, b := range blobs {
		if len(out) < limit && isImage(b.MIME) && len(b.Data) > 0 && mediaAllowed(model, media, b.MIME) == nil {
			out = append(out, agentapi.Blob{Name: b.Name, MIME: b.MIME, Data: b.Data})
		}
	}
	return out
}

// startTitleLocked titles s by plan in the background, from the first
// message's text, its images and the agent's reply. It never delays the
// turn. The caller holds mu.
func (m *Manager) startTitleLocked(s *webSession, plan *titlePlan, blobs []agentapi.Blob, reply string) {
	titler := m.titlerLocked(s.provider)
	if m.closed || titler == nil {
		return
	}
	clip := func(text string) string {
		text = strings.TrimSpace(displaytext.Sanitize(text))
		if runes := []rune(text); len(runes) > maxTitleInputRunes {
			text = string(runes[:maxTitleInputRunes])
		}
		return text
	}
	req := agentapi.TitleRequest{Workdir: s.workdir, Text: clip(plan.text), Reply: clip(reply)}
	if req.Text == "" && req.Reply == "" && !slices.ContainsFunc(plan.models, func(c titleCandidate) bool { return c.images }) {
		log.Info("task title not generated; the first message and reply had no text", "session", s.id)
		return
	}
	call := UtilityCall{Purpose: purposeTitle, Provider: s.provider, TaskID: s.id, ProjectID: s.projectID}
	s.titling = true
	m.titles.Add(1)
	go m.title(s, titler, plan.models, blobs, req, call, plan.renames)
}

// titleAfterReplyLocked starts the title s waits for once a turn ended
// with a reply, from the agent's last reply. A turn that ends without one,
// such as a stale end the open conversation reports, leaves it waiting. A
// name given meanwhile ends the wait. The caller holds mu.
func (m *Manager) titleAfterReplyLocked(s *webSession) {
	plan := s.titleWait
	if s.name != "" || s.renames != plan.renames {
		s.titleWait = nil
		return
	}
	for i := len(s.items) - 1; i >= 0; i-- {
		if it := s.items[i]; it.AgentID == "" && it.Kind == agentapi.ItemAssistant && strings.TrimSpace(it.Text) != "" {
			s.titleWait = nil
			m.startTitleLocked(s, plan, nil, it.Text)
			return
		}
	}
}

// title runs one title job and gives s the title it makes, over the one s
// has. A rename made meanwhile wins; without a title the one s has stays.
func (m *Manager) title(s *webSession, titler agentapi.Titler, models []titleCandidate, blobs []agentapi.Blob, req agentapi.TitleRequest, call UtilityCall, renames uint64) {
	defer m.titles.Done()
	title, model := m.generateTitle(s.id, s.provider, titler, models, blobs, req, call)
	req.Model = model
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return
	}
	before := m.summaryLocked(s)
	s.titling = false
	switch {
	case title == "":
		log.Info("the thread keeps its title", "session", s.id)
	case s.renames != renames:
		log.Info("task renamed while its title was generated; the name stays", "session", s.id)
		title = ""
	default:
		s.title, s.titleFallback = title, false
	}
	conv := s.conv
	m.changedLocked(s, before)
	m.kickPlaceNameLocked(s)
	m.mu.Unlock()
	if title == "" {
		return
	}
	if conv == nil {
		log.Info("task titled; its conversation closed before the provider could take the title", "session", s.id, "model", req.Model)
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err := conv.SetTitle(ctx, title)
	cancel()
	if err != nil {
		log.Info("task titled; the provider kept its own title", "session", s.id, "model", req.Model, "error", err)
		return
	}
	log.Info("task titled", "session", s.id, "model", req.Model)
}

// generateTitle asks each model in turn, as Utility calls within one
// titleTimeout, until one gives a title, and returns it with that model: a
// failure, a timeout or a reply that is no title moves on to the next,
// today's Utility limit or the spent budget ends the job. Each model gets
// an equal share of the time left, so one that does not answer leaves time
// for the next. It returns "" when no model gives one.
func (m *Manager) generateTitle(id, provider string, titler agentapi.Titler, models []titleCandidate, blobs []agentapi.Blob, req agentapi.TitleRequest, call UtilityCall) (string, string) {
	select {
	case m.titleSlots <- struct{}{}:
		defer func() { <-m.titleSlots }()
	case <-m.ctx.Done():
		return "", ""
	}
	ctx, cancel := context.WithTimeout(m.ctx, titleTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	for i, c := range models {
		req.Model, req.Images = c.model, nil
		if c.images {
			req.Images = m.titleImages(provider, c.model, blobs)
		}
		call.Model, call.SessionModel = c.model, c.session
		var title string
		var invalid error
		attempt, stop := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(models)-i))
		_, err := m.runUtilityChecked(attempt, call, req.Text+req.Reply, func(ctx context.Context, onUsage func(agentapi.UtilityUsage)) (string, error) {
			req.OnUsage = onUsage
			return titler.Title(ctx, req)
		}, func(call *UtilityCall, reply string, err error) {
			if err != nil {
				return
			}
			if title, invalid = generatedTitle(reply); invalid != nil {
				call.Reason = "invalid_answer"
			}
		})
		stop()
		if err == nil {
			err = invalid
		}
		if err == nil {
			return title, c.model
		}
		log.Info("task title not generated", "session", id, "model", req.Model, "error", err)
		var e *Error
		if errors.As(err, &e) && e.Code == codeUtilityPaused || ctx.Err() != nil {
			break
		}
	}
	return "", ""
}

var (
	thinkRE       = regexp.MustCompile(`(?is)<think>.*?</think>`)
	titleTagRE    = regexp.MustCompile(`(?is)<(title|session-title)>(.*?)</(?:title|session-title)>`)
	titlePrefixRE = regexp.MustCompile(`(?i)^title\s*:\s*`)
	spacesRE      = regexp.MustCompile(`\s+`)
	// Inline Markdown: a link keeps its text; strong emphasis, code marks
	// and a leading heading mark go.
	mdLinkRE = regexp.MustCompile(`\[([^\]]*)\]\([^)\s]*\)`)
	mdMarkRE = regexp.MustCompile("\\*\\*|`|^#{1,6}\\s+")
)

// titleWrappers are the pairs stripped from both ends of a title.
var titleWrappers = [][2]string{{`"`, `"`}, {`'`, `'`}, {"`", "`"}, {"*", "*"}, {"_", "_"}, {"“", "”"}, {"‘", "’"}, {"«", "»"}}

// cleanGeneratedTitle turns a model's reply into a title: without reasoning
// blocks, the content of a title element when there is one, its first line,
// sanitized, without inline Markdown, a "Title:" label, wrapping quotes or
// emphasis, or trailing punctuation, and cut to maxTitleRunes at a word
// boundary. It returns "" when nothing is left.
func cleanGeneratedTitle(reply string) string {
	title := titleLine(reply)
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

// answerRE matches a reply that answers or carries out the message rather
// than naming it: one that opens like an answer, gives a reason, or holds
// more than one sentence.
var answerRE = regexp.MustCompile(`(?i)^(?:sure|yes|here(?:'s|’s| is| are)|i(?:'m|’m| am| will|'ll|’ll)?\s|it(?:'s|’s| is)\s)|\bbecause\b|[.!?;]\s+\S`)

// generatedTitle returns the title in a model's reply, as
// cleanGeneratedTitle cleans it, or why the reply holds none: nothing is
// left, it is longer than a title (cutting it would leave half a phrase),
// or it answers the message instead of naming it.
func generatedTitle(reply string) (string, error) {
	title := titleLine(reply)
	switch {
	case title == "":
		return "", errors.New("the reply held no title")
	case utf8.RuneCountInString(title) > maxTitleRunes || len(strings.Fields(title)) > maxTitleWords:
		return "", errors.New("the reply was longer than a title")
	case answerRE.MatchString(title):
		return "", errors.New("the reply answered the message instead of naming it")
	}
	return title, nil
}

// titleLine is a model's reply as a title, uncut: see cleanGeneratedTitle.
func titleLine(reply string) string {
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
	title = strings.TrimSpace(mdMarkRE.ReplaceAllString(mdLinkRE.ReplaceAllString(title, "$1"), ""))
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
	return title
}
