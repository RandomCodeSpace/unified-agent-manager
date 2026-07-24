package app

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	fixedTERM                 = "xterm-256color"
	defaultControlPrefix      = "C-b"
	defaultScrollbackLines    = 4000
	DiagnosticProfileFallback = "profile_fallback"
)

type ResolutionInput struct {
	Config         store.Config
	Session        store.SessionRecord
	ProviderPolicy adapter.ProviderTerminalPolicy
}

type ClientTemporaryOverride struct {
	Mouse         *store.MousePolicy `json:"-"`
	ControlPrefix *string            `json:"-"`
	BackDetach    *bool              `json:"-"`
}

type ClientCapabilities struct {
	FramedOutput     bool `json:"-"`
	RoleEvents       bool `json:"-"`
	LocalMouseFilter bool `json:"-"`
	OwnedScreen      bool `json:"-"`
}

type PolicyDiagnostic struct {
	Code    string
	Profile string
	Scope   string
}

type EffectivePolicy struct {
	launch      LaunchPolicySnapshot
	attachment  attachmentDefaults
	diagnostics []PolicyDiagnostic
	profile     string
}

type LaunchPolicySnapshot struct {
	provider        string
	mode            store.Mode
	commandAlias    string
	scrollbackLines int
	term            string
	providerPolicy  adapter.ProviderTerminalPolicy
}

type AttachmentPolicySnapshot struct {
	mouse           store.MousePolicy
	controlPrefix   string
	backDetach      bool
	mouseFiltered   bool
	ownsOuterScreen bool
	capabilities    ClientCapabilities
}

type attachmentDefaults struct {
	mouse         store.MousePolicy
	controlPrefix string
	backDetach    bool
	outerScreen   adapter.OuterScreenPolicy
}

func (p EffectivePolicy) LaunchSnapshot() LaunchPolicySnapshot { return p.launch }

func (p EffectivePolicy) SelectedProfile() string { return p.profile }

func (p EffectivePolicy) Diagnostics() []PolicyDiagnostic {
	return append([]PolicyDiagnostic(nil), p.diagnostics...)
}

func (p EffectivePolicy) NewAttachment(temporary ClientTemporaryOverride, capabilities ClientCapabilities) (AttachmentPolicySnapshot, error) {
	overrides := store.SessionProfileOverrides{
		Mouse: temporary.Mouse, ControlPrefix: temporary.ControlPrefix, BackDetach: temporary.BackDetach,
	}
	if err := store.ValidateSessionProfileOverrides(overrides); err != nil {
		return AttachmentPolicySnapshot{}, err
	}
	mouse := p.attachment.mouse
	controlPrefix := p.attachment.controlPrefix
	backDetach := p.attachment.backDetach
	if temporary.Mouse != nil {
		mouse = *temporary.Mouse
	}
	if temporary.ControlPrefix != nil {
		controlPrefix = *temporary.ControlPrefix
	}
	if temporary.BackDetach != nil {
		backDetach = *temporary.BackDetach
	}
	return AttachmentPolicySnapshot{
		mouse: mouse, controlPrefix: controlPrefix, backDetach: backDetach,
		mouseFiltered:   mouse == store.MousePolicyOff && capabilities.LocalMouseFilter,
		ownsOuterScreen: p.attachment.outerScreen == adapter.OuterScreenUAM && capabilities.OwnedScreen,
		capabilities:    capabilities,
	}, nil
}

func (p LaunchPolicySnapshot) Provider() string { return p.provider }

func (p LaunchPolicySnapshot) Mode() store.Mode { return p.mode }

func (p LaunchPolicySnapshot) CommandAlias() string { return p.commandAlias }

func (p LaunchPolicySnapshot) ScrollbackLines() int { return p.scrollbackLines }

func (p LaunchPolicySnapshot) TERM() string { return p.term }

func (p LaunchPolicySnapshot) ProviderPolicy() adapter.ProviderTerminalPolicy {
	return p.providerPolicy
}

func (p AttachmentPolicySnapshot) Mouse() store.MousePolicy { return p.mouse }

func (p AttachmentPolicySnapshot) ControlPrefix() string { return p.controlPrefix }

func (p AttachmentPolicySnapshot) BackDetach() bool { return p.backDetach }

func (p AttachmentPolicySnapshot) MouseFiltered() bool { return p.mouseFiltered }

func (p AttachmentPolicySnapshot) OwnsOuterScreen() bool { return p.ownsOuterScreen }

func (p AttachmentPolicySnapshot) Capabilities() ClientCapabilities { return p.capabilities }
