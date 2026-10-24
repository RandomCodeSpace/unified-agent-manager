package store

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var providerNameRE = regexp.MustCompile(`^[a-z0-9]+$`)

var prohibitedProfileFields = map[string]struct{}{
	"allow_heuristic_resume": {},
	"allow_latest":           {},
	"allow_unsafe_resume":    {},
	"argv":                   {},
	"command":                {},
	"env":                    {},
	"environment":            {},
	"heuristic_resume":       {},
	"provider_session_id":    {},
	"resume":                 {},
	"resume_args":            {},
	"resume_kind":            {},
	"resume_safety":          {},
	"term":                   {},
}

var knownSessionOverrideFields = map[string]struct{}{
	"mode":                        {},
	"command_alias":               {},
	"mouse":                       {},
	"control_prefix":              {},
	"back_detach":                 {},
	"scrollback_lines":            {},
	"client_id":                   {},
	"controller_id":               {},
	"requested_role":              {},
	"assigned_role":               {},
	"terminal_width":              {},
	"terminal_height":             {},
	"capabilities":                {},
	"negotiated_protocol_version": {},
}

type sessionProfileOverridesAlias SessionProfileOverrides

func (o SessionProfileOverrides) MarshalJSON() ([]byte, error) {
	return marshalUnknownJSON(sessionProfileOverridesAlias(o), o.unknown, knownSessionOverrideFields)
}

func (o *SessionProfileOverrides) UnmarshalJSON(data []byte) error {
	var alias sessionProfileOverridesAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*o = SessionProfileOverrides(alias)
	unknown, err := decodeUnknownJSON(data, knownSessionOverrideFields)
	if err != nil {
		return err
	}
	o.unknown = unknown
	return nil
}

func ValidateProfile(profile Profile) error {
	for field := range profile.unknown {
		if _, prohibited := prohibitedProfileFields[strings.ToLower(field)]; prohibited {
			return fmt.Errorf("profile field %q is prohibited", field)
		}
	}
	if profile.Provider != nil && !providerNameRE.MatchString(*profile.Provider) {
		return fmt.Errorf("invalid profile provider %q", *profile.Provider)
	}
	if profile.Mode != nil && *profile.Mode != ModeYolo && *profile.Mode != ModeSafe {
		return fmt.Errorf("invalid profile mode %q", *profile.Mode)
	}
	if profile.CommandAlias != nil && !isSafeCommandAlias(*profile.CommandAlias) {
		return fmt.Errorf("invalid profile command alias %q", *profile.CommandAlias)
	}
	if profile.Mouse != nil && !validMousePolicy(*profile.Mouse) {
		return fmt.Errorf("invalid profile mouse policy %q", *profile.Mouse)
	}
	if profile.ControlPrefix != nil && !validControlPrefix(*profile.ControlPrefix) {
		return fmt.Errorf("invalid profile control prefix %q", *profile.ControlPrefix)
	}
	if profile.ScrollbackLines != nil && !validScrollbackLines(*profile.ScrollbackLines) {
		return fmt.Errorf("invalid profile scrollback lines %d", *profile.ScrollbackLines)
	}
	return nil
}

func validMousePolicy(policy MousePolicy) bool {
	return policy == MousePolicyAuto || policy == MousePolicyOn || policy == MousePolicyOff
}

func validControlPrefix(prefix string) bool {
	return len(prefix) == 3 && prefix[0] == 'C' && prefix[1] == '-' && prefix[2] >= 'a' && prefix[2] <= 'z'
}

func validScrollbackLines(lines int) bool { return lines >= 100 && lines <= 100000 }
