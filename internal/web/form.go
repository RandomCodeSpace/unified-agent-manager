package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// uam_form is the host tool through which a Thread asks the owner several
// things at once (docs/web.md "Forms"). It shows one form, waits for the
// owner, and returns the chosen action and every field's value in one
// result. The form is a question uam itself asks (owner_confirm.go), so it
// blocks the turn, is answered from its card and is withdrawn like any
// question. Field kinds are named after A2UI v0.9 components.

const (
	formToolName = "uam_form"
	// formSource marks the form's question as uam's own (Elicitation.Source).
	formSource     = formToolName
	maxFormFields  = 8
	maxFormActions = 4
	maxFormOptions = 12
	// Text limits, in runes; names are in bytes.
	maxFormTitle       = 120
	maxFormBody        = 2000
	maxFormLabel       = 120
	maxFormHelp        = 240
	maxFormOption      = 80
	maxFormName        = 40
	maxFormActionLabel = 40
	maxFormShortText   = 500
	maxFormLongText    = 4000
)

// Field kinds and variants, as A2UI v0.9 names them.
const (
	kindTextField    = "TextField"
	kindChoicePicker = "ChoicePicker"
	kindCheckBox     = "CheckBox"
	variantShort     = "shortText"
	variantLong      = "longText"
	variantOne       = "mutuallyExclusive"
	variantMany      = "multipleSelection"
)

// A CheckBox is a boolean field answered with one of these.
var checkChoices, checkValues = []string{"Yes", "No"}, []string{"true", "false"}

var formTool = agentapi.HostTool{
	Name: formToolName,
	Description: "Ask the owner several things at once: uam shows one form in this conversation, waits until they answer, and returns their answer as JSON. " +
		"Use it instead of a row of separate questions when you need two or more answers, or as a button card (no fields, two or more actions) when the owner should pick what happens next. " +
		"For a single question with options, ask_user is enough. " +
		fmt.Sprintf("A form has up to %d fields and 1 to %d actions (buttons, the first is the default). ", maxFormFields, maxFormActions) +
		"Field kinds: TextField (variant shortText or longText), ChoicePicker (options; variant mutuallyExclusive for one or multipleSelection for several) and CheckBox (a yes/no; it always has a value, so required does not apply). " +
		"Every string is shown as plain text: no Markdown, HTML, links or images. " +
		`The result is {"status":"submitted","action":<action name>,"values":{<field name>: string, list of options, true/false, or null when left empty}}, ` +
		`or {"status":"dismissed"} when the owner closed the form without answering; then go on without the answer or ask in your reply. A refused form says why; fix it and call again.`,
	Parameters: toolSchema([]string{"title", "actions"}, map[string]any{
		"title": stringProp(fmt.Sprintf("What the form is for, up to %d characters, e.g. \"Release settings\".", maxFormTitle)),
		"body":  stringProp(fmt.Sprintf("Optional plain text under the title, up to %d characters.", maxFormBody)),
		"fields": listProp(fmt.Sprintf("The fields, in the order shown, at most %d.", maxFormFields), toolSchema([]string{"kind", "name", "label"}, map[string]any{
			"kind":     enumProp("The field's kind.", kindTextField, kindChoicePicker, kindCheckBox),
			"name":     stringProp(fmt.Sprintf("The key of this field's value in the result: letters, digits, _ or -, starting with a letter, up to %d characters, unique in the form.", maxFormName)),
			"label":    stringProp(fmt.Sprintf("What the field asks, up to %d characters.", maxFormLabel)),
			"help":     stringProp(fmt.Sprintf("Optional line under the field, up to %d characters.", maxFormHelp)),
			"required": map[string]any{"type": "boolean", "description": "The owner must fill it in. Not for CheckBox."},
			"variant": enumProp(fmt.Sprintf("TextField: shortText (one line, up to %d characters, the default) or longText (a text box, up to %d). ChoicePicker: mutuallyExclusive (one option, the default) or multipleSelection (any number).", maxFormShortText, maxFormLongText),
				variantShort, variantLong, variantOne, variantMany),
			"options": listProp(fmt.Sprintf("ChoicePicker only: 2 to %d distinct options, each up to %d characters; the chosen ones are returned as written.", maxFormOptions, maxFormOption), map[string]any{"type": "string"}),
			"default": map[string]any{"description": "Optional starting value: text for a TextField, one option (mutuallyExclusive) or a list of options (multipleSelection) for a ChoicePicker, true or false for a CheckBox."},
		})),
		"actions": listProp(fmt.Sprintf("The buttons, 1 to %d, in the order shown; the first is the default.", maxFormActions), toolSchema([]string{"name", "label"}, map[string]any{
			"name":  stringProp(fmt.Sprintf("Returned as action: letters, digits, _ or -, starting with a letter, up to %d characters, unique in the form.", maxFormName)),
			"label": stringProp(fmt.Sprintf("The button's text, up to %d characters, e.g. \"Deploy\".", maxFormActionLabel)),
		})),
	}),
}

type formArgs struct {
	Title   string       `json:"title"`
	Body    string       `json:"body"`
	Fields  []formField  `json:"fields"`
	Actions []formAction `json:"actions"`
}

type formField struct {
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Label    string          `json:"label"`
	Help     string          `json:"help"`
	Required bool            `json:"required"`
	Variant  string          `json:"variant"`
	Options  []string        `json:"options"`
	Default  json.RawMessage `json:"default"`
}

type formAction struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// formText refuses a string the form cannot show as given: missing when
// required, too long, or with a control character (newlines and tabs only
// where lines allows them).
func formText(what, v string, limit int, required, lines bool) error {
	switch {
	case required && strings.TrimSpace(v) == "":
		return fmt.Errorf("%s is required", what)
	case !utf8.ValidString(v):
		return fmt.Errorf("%s is not valid UTF-8", what)
	case utf8.RuneCountInString(v) > limit:
		return fmt.Errorf("%s is longer than %d characters", what, limit)
	case strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) && (!lines || (r != '\n' && r != '\t')) }):
		return fmt.Errorf("%s has a control character", what)
	}
	return nil
}

// formName refuses a name that is not a short identifier.
func formName(what, v string) error {
	if v == "" || len(v) > maxFormName || !unicode.IsLetter(rune(v[0])) ||
		strings.ContainsFunc(v, func(r rune) bool {
			return r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-')
		}) {
		return fmt.Errorf("%s %q must be letters, digits, _ or -, starting with a letter, up to %d characters", what, clipLine(v), maxFormName)
	}
	return nil
}

// formFromArgs checks a uam_form call's arguments and returns the question
// that shows the form. Every count, length and option is bounded here: the
// arguments come from a model.
func formFromArgs(raw json.RawMessage) (agentapi.Interaction, error) {
	var a formArgs
	if err := decodeToolArgs(raw, &a); err != nil {
		return agentapi.Interaction{}, err
	}
	if err := errors.Join(formText("title", a.Title, maxFormTitle, true, false), formText("body", a.Body, maxFormBody, false, true)); err != nil {
		return agentapi.Interaction{}, err
	}
	if len(a.Fields) > maxFormFields {
		return agentapi.Interaction{}, fmt.Errorf("the form has %d fields; it takes at most %d", len(a.Fields), maxFormFields)
	}
	if len(a.Actions) == 0 || len(a.Actions) > maxFormActions {
		return agentapi.Interaction{}, fmt.Errorf("the form needs 1 to %d actions; it has %d", maxFormActions, len(a.Actions))
	}
	ix := agentapi.Interaction{
		Title: a.Title, Detail: a.Body,
		Elicitation: &agentapi.Elicitation{Mode: agentapi.ElicitationForm, Source: formSource},
	}
	for i, act := range a.Actions {
		what := fmt.Sprintf("action %d", i+1)
		if err := errors.Join(formName(what+" name", act.Name), formText(what+" label", act.Label, maxFormActionLabel, true, false)); err != nil {
			return agentapi.Interaction{}, err
		}
		if slices.ContainsFunc(ix.Options, func(o agentapi.Option) bool { return o.ID == act.Name }) {
			return agentapi.Interaction{}, fmt.Errorf("action name %q is used twice", act.Name)
		}
		ix.Options = append(ix.Options, agentapi.Option{ID: act.Name, Label: act.Label})
	}
	names := map[string]bool{}
	for i, f := range a.Fields {
		what := fmt.Sprintf("field %d", i+1)
		if f.Name != "" {
			what = fmt.Sprintf("field %q", clipLine(f.Name))
		}
		if err := errors.Join(formName("field name", f.Name), formText(what+" label", f.Label, maxFormLabel, true, false), formText(what+" help", f.Help, maxFormHelp, false, false)); err != nil {
			return agentapi.Interaction{}, err
		}
		if names[f.Name] {
			return agentapi.Interaction{}, fmt.Errorf("field name %q is used twice", f.Name)
		}
		names[f.Name] = true
		q, err := formQuestion(f)
		if err != nil {
			return agentapi.Interaction{}, fmt.Errorf("%s: %w", what, err)
		}
		ix.Questions = append(ix.Questions, q)
	}
	return ix, nil
}

// formQuestion is one checked field as a question with a typed Field, so the
// answer is checked as any form's is (agentapi.Question.FieldValue).
func formQuestion(f formField) (agentapi.Question, error) {
	field := &agentapi.Field{Name: f.Name, Required: f.Required, Help: f.Help}
	q := agentapi.Question{Text: f.Label, Field: field}
	hasDefault := len(f.Default) > 0 && string(f.Default) != "null"
	switch f.Kind {
	case kindTextField:
		limit := maxFormShortText
		switch f.Variant {
		case "", variantShort:
		case variantLong:
			limit, field.Multiline = maxFormLongText, true
		default:
			return q, fmt.Errorf("a TextField's variant is %s or %s", variantShort, variantLong)
		}
		if f.Options != nil {
			return q, errors.New("a TextField takes no options")
		}
		field.Type, field.MaxLength, q.Custom = agentapi.FieldString, &limit, true
		if hasDefault {
			var v string
			if json.Unmarshal(f.Default, &v) != nil {
				return q, errors.New("a TextField's default is text")
			}
			if err := formText("its default", v, limit, false, field.Multiline); err != nil {
				return q, err
			}
			if strings.TrimSpace(v) != "" {
				field.Default = []string{v}
			}
		}
	case kindChoicePicker:
		if len(f.Options) < 2 || len(f.Options) > maxFormOptions {
			return q, fmt.Errorf("a ChoicePicker needs 2 to %d options; it has %d", maxFormOptions, len(f.Options))
		}
		for _, o := range f.Options {
			if err := formText("an option", o, maxFormOption, true, false); err != nil {
				return q, err
			}
			if strings.TrimSpace(o) != o {
				return q, fmt.Errorf("option %q starts or ends with a space", o)
			}
		}
		for i, o := range f.Options {
			if slices.Contains(f.Options[:i], o) {
				return q, fmt.Errorf("option %q is listed twice", o)
			}
		}
		q.Choices = slices.Clone(f.Options)
		switch f.Variant {
		case "", variantOne:
			field.Type = agentapi.FieldString
		case variantMany:
			field.Type, q.Multiple = agentapi.FieldArray, true
		default:
			return q, fmt.Errorf("a ChoicePicker's variant is %s or %s", variantOne, variantMany)
		}
		if hasDefault {
			var one string
			var many []string
			switch {
			case !q.Multiple && json.Unmarshal(f.Default, &one) == nil:
				many = []string{one}
			case q.Multiple && json.Unmarshal(f.Default, &many) == nil:
			default:
				return q, errors.New("a ChoicePicker's default is one option, or a list of options for multipleSelection")
			}
			for i, v := range many {
				if !slices.Contains(q.Choices, v) || slices.Contains(many[:i], v) {
					return q, fmt.Errorf("default %q is not one of the options, or is listed twice", clipLine(v))
				}
			}
			field.Default = many
		}
	case kindCheckBox:
		if f.Required {
			return q, errors.New("a CheckBox always has a value, so it cannot be required")
		}
		if f.Variant != "" || f.Options != nil {
			return q, errors.New("a CheckBox takes no variant or options")
		}
		field.Type, q.Choices, field.Values, field.Default = agentapi.FieldBoolean, slices.Clone(checkChoices), slices.Clone(checkValues), []string{"No"}
		if hasDefault {
			var on bool
			if json.Unmarshal(f.Default, &on) != nil {
				return q, errors.New("a CheckBox's default is true or false")
			}
			if on {
				field.Default = []string{"Yes"}
			}
		}
	default:
		return q, fmt.Errorf("kind %q is not one of %s, %s or %s", clipLine(f.Kind), kindTextField, kindChoicePicker, kindCheckBox)
	}
	return q, nil
}

// formCall runs a uam_form call of the Task taskID.
func (m *Manager) formCall(ctx context.Context, taskID string, call agentapi.HostToolCall) agentapi.HostToolResult {
	text, err := m.showForm(ctx, taskID, call)
	if err != nil {
		return agentapi.HostToolResult{Text: err.Error(), Failed: true}
	}
	return agentapi.HostToolResult{Text: text}
}

func (m *Manager) showForm(ctx context.Context, taskID string, call agentapi.HostToolCall) (string, error) {
	if call.TaskID != taskID {
		return "", errors.New("the call does not belong to this thread")
	}
	ix, err := formFromArgs(call.Arguments)
	if err != nil {
		return "", err
	}
	ctx, end, err := m.startCall(ctx, taskID)
	if err != nil {
		return "", err
	}
	defer end()
	ix.ToolCallID, ix.AgentID = call.CallID, call.AgentID
	answer, err := m.ask(ctx, taskID, "uam-form-", ix)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("the owner did not answer: the form was withdrawn")
		}
		return "", err
	}
	res := map[string]any{"status": "dismissed"}
	if !answer.Reject && !answer.Cancel {
		// The answer was checked against every field before it got here.
		values := make(map[string]any, len(ix.Questions))
		for i, q := range ix.Questions {
			v, ok, err := q.FieldValue(answer.Answers[i])
			if err != nil {
				return "", fmt.Errorf("field %q: %w", q.Field.Name, err)
			}
			if !ok {
				v = nil
			}
			values[q.Field.Name] = v
		}
		res = map[string]any{"status": "submitted", "action": answer.Decision, "values": values}
	}
	out, err := json.Marshal(res)
	return string(out), err
}
