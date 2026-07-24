package session

import "fmt"

const (
	AttachPrefixEnv     = "UAM_ATTACH_PREFIX"
	AttachBackDetachEnv = "UAM_ATTACH_BACK_DETACH"

	AttachPolicyMouseEnv      = "UAM_ATTACH_POLICY_MOUSE"
	AttachPolicyPrefixEnv     = "UAM_ATTACH_POLICY_PREFIX"
	AttachPolicyBackDetachEnv = "UAM_ATTACH_POLICY_BACK_DETACH"
)

type attachPolicySnapshot struct {
	mouse         string
	controlPrefix string
	backDetach    bool
	backDetachSet bool
}

type resolvedAttachPolicy struct {
	mouseEnabled  bool
	controlPrefix byte
	backDetach    bool
}

func attachPolicyFromEnv(getenv func(string) string) attachPolicySnapshot {
	backDetach := getenv(AttachPolicyBackDetachEnv)
	return attachPolicySnapshot{
		mouse:         getenv(AttachPolicyMouseEnv),
		controlPrefix: getenv(AttachPolicyPrefixEnv),
		backDetach:    backDetach != "0",
		backDetachSet: backDetach != "",
	}
}

func resolveAttachPolicy(snapshot attachPolicySnapshot, getenv func(string) string) (resolvedAttachPolicy, error) {
	mouse, err := parseProfileMousePolicy(snapshot.mouse)
	if err != nil {
		return resolvedAttachPolicy{}, err
	}
	prefix, err := parseControlPrefix(snapshot.controlPrefix)
	if err != nil {
		return resolvedAttachPolicy{}, err
	}
	policy := resolvedAttachPolicy{mouseEnabled: mouse, controlPrefix: prefix, backDetach: true}
	if snapshot.backDetachSet {
		policy.backDetach = snapshot.backDetach
	}
	if value := getenv(AttachMouseEnv); value != "" {
		policy.mouseEnabled = value != "off"
	}
	if value := getenv(AttachPrefixEnv); value != "" {
		policy.controlPrefix, err = parseControlPrefix(value)
		if err != nil {
			return resolvedAttachPolicy{}, fmt.Errorf("%s: %w", AttachPrefixEnv, err)
		}
	}
	if value := getenv(AttachBackDetachEnv); value != "" {
		policy.backDetach = value != "0"
	}
	return policy, nil
}

func parseProfileMousePolicy(value string) (bool, error) {
	switch value {
	case "", "auto", "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid attach mouse policy %q", value)
	}
}

func parseControlPrefix(value string) (byte, error) {
	if value == "" {
		value = "C-b"
	}
	if len(value) != 3 || value[0] != 'C' || value[1] != '-' || value[2] < 'a' || value[2] > 'z' {
		return 0, fmt.Errorf("invalid attach control prefix %q", value)
	}
	return value[2] - 'a' + 1, nil
}

func controlPrefixName(prefix byte) string {
	if prefix < 1 || prefix > 26 {
		prefix = detachPrefix
	}
	return "Ctrl+" + string(rune('A'+prefix-1))
}
