package copilot

import copilot "github.com/github/copilot-sdk/go"

// systemMessage appends system to the CLI's prompt. An output style replaces
// the CLI's tone section rather than adding to it, so it costs only its own
// length.
func systemMessage(system, style string) *copilot.SystemMessageConfig {
	if style == "" {
		return &copilot.SystemMessageConfig{Mode: "append", Content: system}
	}
	return &copilot.SystemMessageConfig{Mode: "customize", Content: system, Sections: map[string]copilot.SectionOverride{
		copilot.SectionTone: {Action: copilot.SectionActionReplace, Content: style},
	}}
}
