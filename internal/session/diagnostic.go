package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type RuntimeDiagnostic struct {
	Protocols  []int `json:"protocols"`
	Controller int   `json:"controller"`
	Standby    int   `json:"standby"`
	Observer   int   `json:"observer"`
}

func (h *host) runtimeDiagnostic() RuntimeDiagnostic {
	h.mu.Lock()
	defer h.mu.Unlock()
	report := RuntimeDiagnostic{Protocols: []int{int(protocolV1), int(protocolV2)}}
	for client := range h.registry.clients {
		switch client.assignedRole {
		case roleController:
			report.Controller++
		case roleStandby:
			report.Standby++
		case roleObserver:
			report.Observer++
		}
	}
	return report
}

func (c *Client) Doctor(ctx context.Context, name string) (RuntimeDiagnostic, error) {
	resp, err := c.roundTrip(ctx, name, request{Op: opDoctor})
	if err != nil {
		return RuntimeDiagnostic{}, err
	}
	if resp.Diagnostic == nil {
		return RuntimeDiagnostic{}, fmt.Errorf("session %s: missing diagnostic response", name)
	}
	return *resp.Diagnostic, nil
}

func (c *Client) RuntimeCount(_ context.Context) (int, error) {
	if err := VerifyDir(c.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		name, stateFile := strings.CutSuffix(entry.Name(), ".json")
		if !stateFile || ValidateName(name) != nil {
			continue
		}
		state, readErr := readState(c.Dir, name)
		if readErr == nil && (state.hostAlive() || state.childAlive()) {
			count++
		}
	}
	return count, nil
}
