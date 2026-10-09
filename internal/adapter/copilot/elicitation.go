package copilot

import (
	"cmp"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Elicitation bounds: fields in one form, choices in one field, the bytes of
// one label (a title, a choice), and of a link.
const (
	maxFormFields   = 16
	maxFieldChoices = 64
	maxFormLabel    = 1 << 10
	maxElicitURL    = 2 << 10
)

// fieldKeywords are the schema keywords each field type may carry: what the
// form shows or checks, and annotations that change no answer. Any other
// keyword could make a valid answer invalid (a pattern, a multipleOf), so a
// form that uses one is declined rather than shown without it.
var fieldKeywords = map[string][]string{
	agentapi.FieldString:  {"type", "title", "description", "default", "minLength", "maxLength", "format", "enum", "enumNames", "oneOf", "anyOf"},
	agentapi.FieldNumber:  {"type", "title", "description", "default", "minimum", "maximum"},
	agentapi.FieldInteger: {"type", "title", "description", "default", "minimum", "maximum"},
	agentapi.FieldBoolean: {"type", "title", "description", "default"},
	agentapi.FieldArray:   {"type", "title", "description", "default", "items", "minItems", "maxItems", "uniqueItems"},
}

var stringFormats = []string{agentapi.FormatEmail, agentapi.FormatURI, agentapi.FormatDate, agentapi.FormatDateTime}

// elicit is the SDK's elicitation callback: an MCP server's (or the agent's)
// form or link. Like askUser it runs on its own goroutine and blocks until
// the user accepts, declines or cancels, or the conversation ends (an error,
// which the SDK sends as cancel). A request UAM cannot show whole is declined
// at once and shown as declined, never shown in part.
func (c *conversation) elicit(req copilot.ElicitationContext) (copilot.ElicitationResult, error) {
	now := time.Now()
	ix, err := elicitationOf(req, now)
	in := &interaction{Interaction: ix, elicitKey: elicitKey(req.Message, req.URL, req.ElicitationSource)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return copilot.ElicitationResult{}, errNoUser
	}
	c.elicitAskedLocked(in, now)
	if err != nil {
		in.State, in.Resolution = agentapi.InteractionRejected, "declined: "+errText(err)
		c.elicits.settled(in)
		c.emitInteractionLocked(in)
		c.mu.Unlock()
		return copilot.ElicitationResult{Action: copilot.ElicitationActionDecline}, nil
	}
	in.reply = make(chan userReply, 1)
	c.pending[in.ID] = in
	c.emitInteractionLocked(in)
	c.mu.Unlock()
	r := <-in.reply
	return r.elicit, r.err
}

// answerElicitationLocked releases a blocked elicitation handler with the
// action the answer names: Cancel, Reject (decline) or accept with the
// form's values, each checked against its field.
func (c *conversation) answerElicitationLocked(in *interaction, ans agentapi.Answer) error {
	var r copilot.ElicitationResult
	switch {
	case ans.Cancel && (ans.Reject || len(ans.Answers) > 0):
		return errors.New("copilot: a cancel takes no answers")
	case ans.Cancel:
		r.Action = copilot.ElicitationActionCancel
		in.State, in.Resolution = agentapi.InteractionRejected, "cancelled"
	case ans.Reject:
		r.Action = copilot.ElicitationActionDecline
		in.State, in.Resolution = agentapi.InteractionRejected, "declined"
	default:
		content, err := formContent(in.Questions, ans.Answers)
		if err != nil {
			return err
		}
		r = copilot.ElicitationResult{Action: copilot.ElicitationActionAccept, Content: content}
		in.State, in.Resolution = agentapi.InteractionAnswered, elicitAccepted(in.Interaction)
	}
	delete(c.pending, in.ID)
	c.elicits.settled(in)
	c.emitInteractionLocked(in)
	in.reply <- userReply{elicit: r}
	return nil
}

// elicitAccepted is the resolution of an accepted elicitation: a form is
// answered, a link only accepted (the user says they opened it).
func elicitAccepted(ix agentapi.Interaction) string {
	if ix.Elicitation != nil && ix.Elicitation.Mode == agentapi.ElicitationURL {
		return "accepted"
	}
	return "answered"
}

// formContent is the accepted form's content: one entry per answered field.
func formContent(questions []agentapi.Question, answers [][]string) (map[string]copilot.ElicitationFieldValue, error) {
	if len(answers) != len(questions) {
		return nil, fmt.Errorf("copilot: a form needs one answer per field (%d)", len(questions))
	}
	var content map[string]copilot.ElicitationFieldValue
	for i, q := range questions {
		v, ok, err := q.FieldValue(answers[i])
		if err != nil {
			return nil, fmt.Errorf("copilot: field %d %v", i+1, err)
		}
		if !ok {
			continue
		}
		if content == nil {
			content = map[string]copilot.ElicitationFieldValue{}
		}
		content[q.Field.Name] = v
	}
	return content, nil
}

// elicitationOf is the interaction for one elicitation request, or why UAM
// cannot show it.
func elicitationOf(req copilot.ElicitationContext, now time.Time) (agentapi.Interaction, error) {
	source := clip(strings.TrimSpace(deref(req.ElicitationSource)), maxFormLabel)
	from := cmp.Or(source, "Copilot")
	ix := agentapi.Interaction{
		ID:          "elicitation-" + rand.Text(),
		Kind:        agentapi.InteractionQuestion,
		Title:       "Form from " + from,
		Detail:      clip(req.Message, maxToolText),
		State:       agentapi.InteractionPending,
		Time:        now,
		Elicitation: &agentapi.Elicitation{Mode: agentapi.ElicitationForm, Source: source},
	}
	mode := agentapi.ElicitationForm
	if req.Mode != nil {
		mode = string(*req.Mode)
	}
	switch mode {
	case agentapi.ElicitationURL:
		ix.Title, ix.Elicitation.Mode = "Link from "+from, agentapi.ElicitationURL
		link, err := linkOf(deref(req.URL))
		if err != nil {
			return ix, err
		}
		ix.Elicitation.URL = link
		return ix, nil
	case agentapi.ElicitationForm:
		if req.RequestedSchema == nil {
			return ix, errors.New("the form has no fields")
		}
		qs, err := formQuestions(req.RequestedSchema)
		ix.Questions = qs
		return ix, err
	default:
		return ix, fmt.Errorf("unknown request mode %q", clip(mode, 64))
	}
}

// linkOf checks a link the user may open: an https URL of at most
// maxElicitURL bytes, with a host and no credentials, as sent.
func linkOf(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("the link is missing")
	}
	if len(raw) > maxElicitURL || !utf8.ValidString(raw) || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", errors.New("the link is not a plain URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", errors.New("only an https link without credentials can be shown")
	}
	return raw, nil
}

// formQuestions are a form's fields as questions, ordered by name: the
// SDK hands the schema's properties over as a map, so their order is lost.
func formQuestions(schema *copilot.ElicitationSchema) ([]agentapi.Question, error) {
	if len(schema.Properties) > maxFormFields {
		return nil, fmt.Errorf("the form has more than %d fields", maxFormFields)
	}
	for _, name := range schema.Required {
		if _, ok := schema.Properties[name]; !ok {
			return nil, fmt.Errorf("required field %q is not in the form", clip(name, 64))
		}
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for name := range schema.Properties {
			if !yield(name) {
				return
			}
		}
	})
	qs := make([]agentapi.Question, 0, len(names))
	for _, name := range names {
		q, err := fieldOf(name, schema.Properties[name], slices.Contains(schema.Required, name))
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", clip(name, 64), err)
		}
		qs = append(qs, q)
	}
	return qs, nil
}

// fieldOf is one schema property as a question.
func fieldOf(name string, raw any, required bool) (agentapi.Question, error) {
	if name == "" || len(name) > maxFormLabel || !utf8.ValidString(name) {
		return agentapi.Question{}, errors.New("unfit name")
	}
	prop, ok := raw.(map[string]any)
	if !ok {
		return agentapi.Question{}, errors.New("not a schema")
	}
	typ, _ := prop["type"].(string)
	allowed, ok := fieldKeywords[typ]
	if !ok {
		return agentapi.Question{}, errors.New("unsupported type")
	}
	for k := range prop {
		if !slices.Contains(allowed, k) {
			return agentapi.Question{}, fmt.Errorf("unsupported keyword %q", clip(k, 64))
		}
	}
	title, err := label(prop, "title")
	if err != nil {
		return agentapi.Question{}, err
	}
	description, err := text(prop, "description")
	if err != nil {
		return agentapi.Question{}, err
	}
	f := &agentapi.Field{Name: name, Type: typ, Required: required}
	q := agentapi.Question{Header: cmp.Or(title, name), Text: description, Field: f}
	if q.Text == "" {
		q.Header, q.Text = "", cmp.Or(title, name)
	}
	switch typ {
	case agentapi.FieldString:
		if q.Choices, f.Values, err = choicesOf(prop); err != nil {
			return q, err
		}
		if q.Choices != nil {
			if _, ok := prop["minLength"]; ok {
				return q, errors.New("length bounds on a choice")
			}
			if _, ok := prop["maxLength"]; ok {
				return q, errors.New("length bounds on a choice")
			}
			if _, ok := prop["format"]; ok {
				return q, errors.New("a format on a choice")
			}
			return q, nil
		}
		q.Custom = true
		if f.MinLength, err = count(prop, "minLength"); err != nil {
			return q, err
		}
		if f.MaxLength, err = count(prop, "maxLength"); err != nil {
			return q, err
		}
		if v, ok := prop["format"]; ok {
			s, _ := v.(string)
			if !slices.Contains(stringFormats, s) {
				return q, errors.New("unsupported format")
			}
			f.Format = s
		}
		return q, bounded(f.MinLength, f.MaxLength)
	case agentapi.FieldNumber, agentapi.FieldInteger:
		q.Custom = true
		if f.Minimum, err = number(prop, "minimum"); err != nil {
			return q, err
		}
		if f.Maximum, err = number(prop, "maximum"); err != nil {
			return q, err
		}
		if f.Minimum != nil && f.Maximum != nil && *f.Minimum > *f.Maximum {
			return q, errors.New("minimum above maximum")
		}
		return q, nil
	case agentapi.FieldBoolean:
		q.Choices, f.Values = []string{"Yes", "No"}, []string{"true", "false"}
		return q, nil
	default: // agentapi.FieldArray
		q.Multiple = true
		items, ok := prop["items"].(map[string]any)
		if !ok {
			return q, errors.New("array without items")
		}
		for k := range items {
			if !slices.Contains([]string{"type", "enum", "enumNames", "oneOf", "anyOf"}, k) {
				return q, fmt.Errorf("unsupported items keyword %q", clip(k, 64))
			}
		}
		if t, ok := items["type"]; ok && t != agentapi.FieldString {
			return q, errors.New("items are not strings")
		}
		if q.Choices, f.Values, err = choicesOf(items); err != nil {
			return q, err
		}
		if q.Choices == nil {
			return q, errors.New("array items without choices")
		}
		if u, ok := prop["uniqueItems"]; ok && u != true {
			return q, errors.New("repeated choices")
		}
		if f.MinItems, err = count(prop, "minItems"); err != nil {
			return q, err
		}
		if f.MaxItems, err = count(prop, "maxItems"); err != nil {
			return q, err
		}
		return q, bounded(f.MinItems, f.MaxItems)
	}
}

// choicesOf reads an enumeration: enum with optional enumNames, or oneOf or
// anyOf of {const, title}. Labels show, values send; both are unique. Nil
// when the schema enumerates nothing.
func choicesOf(prop map[string]any) (labels, values []string, err error) {
	if raw, ok := prop["enum"]; ok {
		if values, err = textList(raw); err != nil {
			return nil, nil, fmt.Errorf("enum: %w", err)
		}
		labels = values
		if raw, ok := prop["enumNames"]; ok {
			if labels, err = textList(raw); err != nil || len(labels) != len(values) {
				return nil, nil, errors.New("enumNames do not match enum")
			}
		}
	} else if _, ok := prop["enumNames"]; ok {
		return nil, nil, errors.New("enumNames without enum")
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		raw, ok := prop[key]
		if !ok {
			continue
		}
		if values != nil {
			return nil, nil, errors.New("more than one enumeration")
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s is not a list", key)
		}
		for _, entry := range list {
			opt, ok := entry.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s entry is not a schema", key)
			}
			for k := range opt {
				if k != "const" && k != "title" {
					return nil, nil, fmt.Errorf("unsupported %s keyword %q", key, clip(k, 64))
				}
			}
			v, ok := opt["const"].(string)
			if !ok {
				return nil, nil, fmt.Errorf("%s entry without a text const", key)
			}
			t, err := label(opt, "title")
			if err != nil {
				return nil, nil, err
			}
			values, labels = append(values, v), append(labels, cmp.Or(t, v))
		}
		if values == nil {
			return nil, nil, fmt.Errorf("empty %s", key)
		}
	}
	if values == nil {
		return nil, nil, nil
	}
	if len(values) > maxFieldChoices {
		return nil, nil, fmt.Errorf("more than %d choices", maxFieldChoices)
	}
	for i := range values {
		if strings.TrimSpace(labels[i]) == "" || len(labels[i]) > maxFormLabel || len(values[i]) > maxFormLabel ||
			slices.Index(labels, labels[i]) != i || slices.Index(values, values[i]) != i {
			return nil, nil, errors.New("empty, long or repeated choices")
		}
	}
	return labels, values, nil
}

func textList(raw any) ([]string, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("not a list")
	}
	out := make([]string, len(list))
	for i, v := range list {
		if out[i], ok = v.(string); !ok {
			return nil, errors.New("not text")
		}
	}
	return out, nil
}

// label is an optional short text keyword; text an optional long one.
func label(prop map[string]any, key string) (string, error) {
	s, err := text(prop, key)
	if len(s) > maxFormLabel {
		return "", fmt.Errorf("%s is too long", key)
	}
	return s, err
}

func text(prop map[string]any, key string) (string, error) {
	raw, ok := prop[key]
	if !ok {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok || !utf8.ValidString(s) {
		return "", fmt.Errorf("%s is not text", key)
	}
	return strings.TrimSpace(clip(s, maxToolText)), nil
}

// number reads an optional finite number keyword; count a whole one, not negative.
func number(prop map[string]any, key string) (*float64, error) {
	raw, ok := prop[key]
	if !ok {
		return nil, nil
	}
	n, ok := raw.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, fmt.Errorf("%s is not a number", key)
	}
	return &n, nil
}

func count(prop map[string]any, key string) (*int, error) {
	n, err := number(prop, key)
	if n == nil || err != nil {
		return nil, err
	}
	if *n < 0 || *n != math.Trunc(*n) || *n > math.MaxInt32 {
		return nil, fmt.Errorf("%s is not a count", key)
	}
	v := int(*n)
	return &v, nil
}

func bounded(lo, hi *int) error {
	if lo != nil && hi != nil && *lo > *hi {
		return errors.New("lower bound above upper bound")
	}
	return nil
}

// elicitLinks pairs each elicitation.requested event with the elicit
// callback for the same request, as questionLinks does for ask_user: the
// callback lacks the request ID, tool call and agent the event carries.
// They pair by message, link and source in arrival order, whichever side
// comes first. Each side is bounded; an unpaired event expires.
type elicitLinks struct {
	events  []elicitEvent  // events without a callback yet, oldest first
	waiting []*interaction // pending elicitations without an event yet, oldest first
}

type elicitEvent struct {
	key, requestID, toolCallID, agentID string
	at                                  time.Time
}

func elicitKey(message string, link, source *string) string {
	return message + "\x00" + deref(link) + "\x00" + strings.TrimSpace(deref(source))
}

func (l *elicitLinks) expire(now time.Time) {
	l.events = slices.DeleteFunc(l.events, func(e elicitEvent) bool { return now.Sub(e.at) > questionLinkTTL })
}

// settled forgets an elicitation that ended.
func (l *elicitLinks) settled(in *interaction) {
	l.waiting = slices.DeleteFunc(l.waiting, func(w *interaction) bool { return w == in })
}

// elicitAskedLocked gives a new elicitation its event's request, tool call
// and agent when the event came first; otherwise it waits for the event.
func (c *conversation) elicitAskedLocked(in *interaction, now time.Time) {
	l := &c.elicits
	l.expire(now)
	if i := slices.IndexFunc(l.events, func(e elicitEvent) bool { return e.key == in.elicitKey }); i >= 0 {
		e := l.events[i]
		l.events = slices.Delete(l.events, i, i+1)
		in.requestID, in.ToolCallID, in.AgentID = e.requestID, e.toolCallID, e.agentID
		return
	}
	if len(l.waiting) >= maxQuestionLinks {
		l.waiting = l.waiting[1:]
	}
	l.waiting = append(l.waiting, in)
}

// elicitRequestedLocked handles an elicitation.requested event: it links the
// oldest waiting elicitation with its key, which is emitted again with the
// link, or keeps the event for its callback.
func (c *conversation) elicitRequestedLocked(d *rpc.ElicitationRequestedData, agentID string, now time.Time) {
	if d.RequestID == "" {
		return
	}
	l := &c.elicits
	l.expire(now)
	key := elicitKey(d.Message, d.URL, d.ElicitationSource)
	toolCallID := strings.TrimSpace(deref(d.ToolCallID))
	if i := slices.IndexFunc(l.waiting, func(in *interaction) bool { return in.elicitKey == key }); i >= 0 {
		in := l.waiting[i]
		l.waiting = slices.Delete(l.waiting, i, i+1)
		in.requestID, in.ToolCallID, in.AgentID = d.RequestID, toolCallID, agentID
		c.emitInteractionLocked(in)
		return
	}
	if len(l.events) >= maxQuestionLinks {
		l.events = l.events[1:]
	}
	l.events = append(l.events, elicitEvent{key: key, requestID: d.RequestID, toolCallID: toolCallID, agentID: agentID, at: now})
}

// elicitCompletedLocked handles an elicitation.completed event. One this
// client answered is already settled; one still pending was resolved
// without it (the CLI or the server withdrew it), so it ends here too and
// its handler is released without an answer.
func (c *conversation) elicitCompletedLocked(d *rpc.ElicitationCompletedData) {
	l := &c.elicits
	l.events = slices.DeleteFunc(l.events, func(e elicitEvent) bool { return e.requestID == d.RequestID })
	for id, in := range c.pending {
		if in.Elicitation == nil || in.requestID == "" || in.requestID != d.RequestID {
			continue
		}
		delete(c.pending, id)
		l.settled(in)
		in.State, in.Resolution = agentapi.InteractionExpired, "resolved elsewhere"
		if d.Action != nil {
			switch *d.Action {
			case rpc.ElicitationCompletedActionAccept:
				in.State, in.Resolution = agentapi.InteractionAnswered, "answered elsewhere"
			case rpc.ElicitationCompletedActionDecline:
				in.State, in.Resolution = agentapi.InteractionRejected, "declined elsewhere"
			case rpc.ElicitationCompletedActionCancel:
				in.Resolution = "cancelled"
			}
		}
		c.emitInteractionLocked(in)
		in.reply <- userReply{err: errNoUser}
		return
	}
}
