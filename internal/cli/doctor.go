package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

func runDoctor(ctx context.Context, svc *app.Service, args []string) error {
	sessionID, asJSON, err := parseDoctorArgs(args)
	if err != nil {
		return err
	}
	if sessionID == "" {
		report := svc.DoctorGlobal(ctx)
		if asJSON {
			return writeJSON(report)
		}
		fmt.Printf("store\t%s\n", report.Store.Status)
		fmt.Printf("runtime\t%s\tsessions=%d\n", report.Runtime.Status, report.Runtime.Count)
		for _, provider := range report.Providers {
			fmt.Printf("provider\t%s\t%s\t%s\t%s\n",
				displaytext.Sanitize(provider.Name), provider.Status, provider.OuterScreen, provider.KeyProtocol)
		}
		for _, profile := range report.Profiles {
			fmt.Printf("profile\t%s\t%s\n", displaytext.Sanitize(profile.Name), profile.Status)
		}
		return nil
	}
	report, err := svc.DoctorSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(report)
	}
	fmt.Printf("session\t%s\t%s\n", displaytext.Sanitize(report.SessionID), displaytext.Sanitize(report.SessionName))
	fmt.Printf("runtime\t%s\tprotocols=%s\tcontroller=%d\tstandby=%d\tobserver=%d\n",
		report.RuntimeStatus, protocolList(report.Runtime.Protocols),
		report.Runtime.Controller, report.Runtime.Standby, report.Runtime.Observer)
	fmt.Printf("profile\tselected=%s\teffective=%s\n",
		displaytext.Sanitize(report.SelectedProfile), displaytext.Sanitize(report.EffectiveProfile))
	fmt.Printf("provider\t%s\touter_screen=%s\tkey_protocol=%s\n",
		displaytext.Sanitize(report.Provider), report.ProviderPolicy.OuterScreen, report.ProviderPolicy.KeyProtocol)
	fmt.Printf("fallback\t%s\n", strings.Join(report.FallbackReasons, ","))
	return nil
}

func parseDoctorArgs(args []string) (string, bool, error) {
	sessionID := ""
	asJSON := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			if asJSON {
				return "", false, errors.New("doctor accepts --json once")
			}
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("doctor: unknown flag %q", arg)
		case sessionID == "":
			sessionID = arg
		default:
			return "", false, errors.New("doctor accepts at most one session ID")
		}
	}
	return sessionID, asJSON, nil
}

func protocolList(protocols []int) string {
	values := make([]string, len(protocols))
	for index, protocol := range protocols {
		values[index] = fmt.Sprintf("%d", protocol)
	}
	return strings.Join(values, ",")
}
