package session

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

type clientCapability string

const (
	capabilityFramedOutput     clientCapability = "framed_output"
	capabilityRoleEvents       clientCapability = "role_events"
	capabilityLocalMouseFilter clientCapability = "local_mouse_filter"
	capabilityOwnedScreen      clientCapability = "owned_screen"
)

const maxClientHintLen = 128

type clientHello struct {
	TTY          bool               `json:"tty"`
	TermHint     string             `json:"term_hint,omitempty"`
	ColorHint    string             `json:"color_hint,omitempty"`
	Capabilities []clientCapability `json:"capabilities"`
}

func validateClientHello(hello clientHello) error {
	if len(hello.TermHint) > maxClientHintLen || len(hello.ColorHint) > maxClientHintLen {
		return fmt.Errorf("client hello hint exceeds %d bytes", maxClientHintLen)
	}
	seen := make(map[clientCapability]struct{}, len(hello.Capabilities))
	for _, capability := range hello.Capabilities {
		switch capability {
		case capabilityFramedOutput, capabilityRoleEvents, capabilityLocalMouseFilter, capabilityOwnedScreen:
		default:
			return fmt.Errorf("unsupported client capability %q", capability)
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("duplicate client capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

type clientRole string

const (
	roleController clientRole = "controller"
	roleStandby    clientRole = "standby"
	roleObserver   clientRole = "observer"
)

func validateRequestedRole(role clientRole) error {
	switch role {
	case roleController, roleStandby, roleObserver:
		return nil
	default:
		return fmt.Errorf("unsupported requested role %q", role)
	}
}

type roleAction string

const (
	actionRequestControl  roleAction = "request_control"
	actionTransferControl roleAction = "transfer_control"
)

type roleCommand struct {
	Action roleAction `json:"action"`
}

func (command roleCommand) validate() error {
	switch command.Action {
	case actionRequestControl, actionTransferControl:
		return nil
	default:
		return errors.New("invalid role action")
	}
}

type roleEvent struct {
	Type       string     `json:"type"`
	ClientID   string     `json:"client_id"`
	Role       clientRole `json:"role"`
	Generation uint64     `json:"generation"`
	Reason     string     `json:"reason"`
}

func defaultClientHello(tty bool, termHint, colorHint string) clientHello {
	return clientHello{
		TTY:       tty,
		TermHint:  boundedClientHint(termHint),
		ColorHint: boundedClientHint(colorHint),
		Capabilities: []clientCapability{
			capabilityFramedOutput,
			capabilityRoleEvents,
			capabilityLocalMouseFilter,
			capabilityOwnedScreen,
		},
	}
}

func boundedClientHint(hint string) string {
	if len(hint) <= maxClientHintLen {
		return hint
	}
	hint = hint[:maxClientHintLen]
	for !utf8.ValidString(hint) {
		hint = hint[:len(hint)-1]
	}
	return hint
}
