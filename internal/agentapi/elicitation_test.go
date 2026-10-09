package agentapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestFieldValue(t *testing.T) {
	one, three, five := 1, 3, 5.0
	text := Question{Custom: true, Field: &Field{Name: "s", Type: FieldString, Required: true, MinLength: &one, MaxLength: &three}}
	email := Question{Custom: true, Field: &Field{Name: "e", Type: FieldString, Format: FormatEmail}}
	date := Question{Custom: true, Field: &Field{Name: "d", Type: FieldString, Format: FormatDate}}
	integer := Question{Custom: true, Field: &Field{Name: "i", Type: FieldInteger, Maximum: &five}}
	number := Question{Custom: true, Field: &Field{Name: "n", Type: FieldNumber}}
	boolean := Question{Choices: []string{"Yes", "No"}, Field: &Field{Name: "b", Type: FieldBoolean, Values: []string{"true", "false"}}}
	choice := Question{Choices: []string{"Stable", "Beta"}, Field: &Field{Name: "c", Type: FieldString, Values: []string{"stable", "beta"}}}
	many := Question{Multiple: true, Choices: []string{"a", "b", "c"}, Field: &Field{Name: "m", Type: FieldArray, MinItems: &one, MaxItems: &one}}
	cases := []struct {
		name   string
		q      Question
		values []string
		want   any
		ok     bool
		err    string
	}{
		{"required missing", text, nil, nil, false, "is required"},
		{"optional missing", email, nil, nil, false, ""},
		{"blank", text, []string{" "}, nil, false, "empty"},
		{"short enough", text, []string{"héé"}, "héé", true, ""},
		{"too long in characters", text, []string{"abcd"}, nil, false, "at most 3 characters"},
		{"two answers", text, []string{"a", "b"}, nil, false, "one answer"},
		{"email", email, []string{"a@example.com"}, "a@example.com", true, ""},
		{"email with a name", email, []string{"A <a@example.com>"}, nil, false, "valid email"},
		{"date", date, []string{"2026-10-09"}, "2026-10-09", true, ""},
		{"not a date", date, []string{"10/09/2026"}, nil, false, "valid date"},
		{"integer", integer, []string{" 4 "}, 4.0, true, ""},
		{"fraction for integer", integer, []string{"4.5"}, nil, false, "whole"},
		{"above maximum", integer, []string{"6"}, nil, false, "at most 5"},
		{"not finite", number, []string{"NaN"}, nil, false, "not a number"},
		{"number", number, []string{"-1.5"}, -1.5, true, ""},
		{"yes", boolean, []string{"Yes"}, true, true, ""},
		{"no", boolean, []string{"No"}, false, true, ""},
		{"boolean text", boolean, []string{"true"}, nil, false, "listed choices"},
		{"choice sends its value", choice, []string{"Beta"}, "beta", true, ""},
		{"value is not a label", choice, []string{"beta"}, nil, false, "listed choices"},
		{"several", many, []string{"b"}, []string{"b"}, true, ""},
		{"too many", many, []string{"a", "b"}, nil, false, "at most 1"},
		{"repeated", many, []string{"a", "a"}, nil, false, "once"},
	}
	for _, tc := range cases {
		got, ok, err := tc.q.FieldValue(tc.values)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err = %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: = %#v %v %v, want %#v %v", tc.name, got, ok, err, tc.want, tc.ok)
		}
	}
}

func TestFieldCloneSharesNothing(t *testing.T) {
	n, f := 1, 2.0
	a := &Field{Values: []string{"x"}, MinLength: &n, Minimum: &f}
	b := a.Clone()
	b.Values[0], *b.MinLength, *b.Minimum = "y", 9, 9
	if a.Values[0] != "x" || *a.MinLength != 1 || *a.Minimum != 2 {
		t.Fatalf("clone shares state: %+v", a)
	}
}
