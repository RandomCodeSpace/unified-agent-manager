package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// Each profile field is checked, and fields that could steer a provider's
// command line are refused even when this version does not model them.
func TestValidateProfileRejectsEachInvalidField(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"valid", `{"provider":"copilot","mode":"yolo","command_alias":"gh.copilot","mouse":"off","control_prefix":"C-b","back_detach":true,"scrollback_lines":5000}`, ""},
		{"prohibited field", `{"argv":["sh"]}`, `profile field "argv" is prohibited`},
		{"prohibited field any case", `{"Resume_Args":"--x"}`, `profile field "Resume_Args" is prohibited`},
		{"provider", `{"provider":"Copilot"}`, "invalid profile provider"},
		{"command alias flag", `{"command_alias":"-x"}`, "invalid profile command alias"},
		{"command alias empty", `{"command_alias":""}`, "invalid profile command alias"},
		{"command alias shell", `{"command_alias":"a;b"}`, "invalid profile command alias"},
		{"mouse", `{"mouse":"sometimes"}`, "invalid profile mouse policy"},
		{"control prefix", `{"control_prefix":"C-1"}`, "invalid profile control prefix"},
		{"scrollback", `{"scrollback_lines":99}`, "invalid profile scrollback lines 99"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p Profile
			if err := json.Unmarshal([]byte(tc.raw), &p); err != nil {
				t.Fatal(err)
			}
			err := ValidateProfile(p)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("ValidateProfile(%s) = %v, want %q", tc.raw, err, tc.want)
			}
		})
	}
}
