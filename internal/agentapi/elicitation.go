package agentapi

import (
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Elicitation modes.
const (
	ElicitationForm = "form"
	ElicitationURL  = "url"
)

// Elicitation marks a question interaction that stands for a provider's
// structured request, such as an MCP server's elicitation. A form's
// questions each carry a Field; a link has no questions and its URL is only
// shown: the user opens it, the service never does. Its answer may Cancel
// (dismiss) as well as Reject (decline) or answer (accept).
type Elicitation struct {
	Mode string `json:"mode"`
	// Source names who asked, such as an MCP server; empty when unknown.
	Source string `json:"source,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Field types.
const (
	FieldString  = "string"
	FieldNumber  = "number"
	FieldInteger = "integer"
	FieldBoolean = "boolean"
	// FieldArray takes several of its question's Choices.
	FieldArray = "array"
)

// Field formats a string field may require.
const (
	FormatEmail    = "email"
	FormatURI      = "uri"
	FormatDate     = "date"
	FormatDateTime = "date-time"
)

// Field is one typed form field behind a question. An enumerated field (and
// a boolean, as "Yes" and "No") offers its question's Choices; Values holds
// what each choice sends, in the same order. Bounds left nil do not apply.
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Values   []string `json:"values,omitempty"`
	Format   string   `json:"format,omitempty"`
	Minimum  *float64 `json:"minimum,omitempty"`
	Maximum  *float64 `json:"maximum,omitempty"`
	// MinLength and MaxLength bound a string in characters.
	MinLength *int `json:"min_length,omitempty"`
	MaxLength *int `json:"max_length,omitempty"`
	// MinItems and MaxItems bound how many choices an array field takes.
	MinItems *int `json:"min_items,omitempty"`
	MaxItems *int `json:"max_items,omitempty"`
}

// Clone returns a copy of f that shares nothing with it.
func (f *Field) Clone() *Field {
	if f == nil {
		return nil
	}
	c := *f
	c.Values = slices.Clone(f.Values)
	c.Minimum, c.Maximum = clonePtr(f.Minimum), clonePtr(f.Maximum)
	c.MinLength, c.MaxLength = clonePtr(f.MinLength), clonePtr(f.MaxLength)
	c.MinItems, c.MaxItems = clonePtr(f.MinItems), clonePtr(f.MaxItems)
	return &c
}

// Clone returns a copy of e that shares nothing with it.
func (e *Elicitation) Clone() *Elicitation {
	if e == nil {
		return nil
	}
	c := *e
	return &c
}

func clonePtr[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// FieldValue is what values, the answer to q, send for q's Field: a string,
// float64, bool or []string, and whether the field is answered at all (an
// optional field may be left empty). Every bound the field carries is
// checked, so an invalid answer never reaches the provider.
func (q Question) FieldValue(values []string) (any, bool, error) {
	f := q.Field
	if f == nil {
		return nil, false, errors.New("is not a form field")
	}
	if len(values) == 0 {
		if f.Required {
			return nil, false, errors.New("is required")
		}
		return nil, false, nil
	}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return nil, false, errors.New("has an empty answer")
		}
	}
	if f.Type == FieldArray {
		return q.arrayValue(values)
	}
	if len(values) > 1 {
		return nil, false, errors.New("accepts one answer")
	}
	v := values[0]
	if len(q.Choices) > 0 {
		i := slices.Index(q.Choices, v)
		if i < 0 {
			return nil, false, errors.New("only accepts the listed choices")
		}
		if i < len(f.Values) {
			v = f.Values[i]
		}
		if f.Type == FieldBoolean {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, false, errors.New("is not yes or no")
			}
			return b, true, nil
		}
		return v, true, nil
	}
	switch f.Type {
	case FieldString:
		return f.stringValue(v)
	case FieldNumber, FieldInteger:
		return f.numberValue(strings.TrimSpace(v))
	default:
		return nil, false, fmt.Errorf("has an unknown type %q", f.Type)
	}
}

func (q Question) arrayValue(values []string) (any, bool, error) {
	f := q.Field
	out := make([]string, 0, len(values))
	for _, v := range values {
		i := slices.Index(q.Choices, v)
		if i < 0 {
			return nil, false, errors.New("only accepts the listed choices")
		}
		if i < len(f.Values) {
			v = f.Values[i]
		}
		if slices.Contains(out, v) {
			return nil, false, errors.New("takes each choice once")
		}
		out = append(out, v)
	}
	if f.MinItems != nil && len(out) < *f.MinItems {
		return nil, false, fmt.Errorf("needs at least %d choices", *f.MinItems)
	}
	if f.MaxItems != nil && len(out) > *f.MaxItems {
		return nil, false, fmt.Errorf("takes at most %d choices", *f.MaxItems)
	}
	return out, true, nil
}

func (f *Field) stringValue(v string) (any, bool, error) {
	n := utf8.RuneCountInString(v)
	if f.MinLength != nil && n < *f.MinLength {
		return nil, false, fmt.Errorf("needs at least %d characters", *f.MinLength)
	}
	if f.MaxLength != nil && n > *f.MaxLength {
		return nil, false, fmt.Errorf("takes at most %d characters", *f.MaxLength)
	}
	var ok bool
	switch f.Format {
	case "":
		ok = true
	case FormatEmail:
		a, err := mail.ParseAddress(v)
		ok = err == nil && a.Address == v
	case FormatURI:
		u, err := url.Parse(v)
		ok = err == nil && u.Scheme != ""
	case FormatDate:
		_, err := time.Parse(time.DateOnly, v)
		ok = err == nil
	case FormatDateTime:
		_, err := time.Parse(time.RFC3339, v)
		ok = err == nil
	default:
		return nil, false, fmt.Errorf("has an unknown format %q", f.Format)
	}
	if !ok {
		return nil, false, fmt.Errorf("is not a valid %s", f.Format)
	}
	return v, true, nil
}

func (f *Field) numberValue(v string) (any, bool, error) {
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, false, errors.New("is not a number")
	}
	if f.Type == FieldInteger && n != math.Trunc(n) {
		return nil, false, errors.New("is not a whole number")
	}
	if f.Minimum != nil && n < *f.Minimum {
		return nil, false, fmt.Errorf("must be at least %s", strconv.FormatFloat(*f.Minimum, 'g', -1, 64))
	}
	if f.Maximum != nil && n > *f.Maximum {
		return nil, false, fmt.Errorf("must be at most %s", strconv.FormatFloat(*f.Maximum, 'g', -1, 64))
	}
	return n, true, nil
}
