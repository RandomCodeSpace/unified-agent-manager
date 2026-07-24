package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type SessionDiagnosticSource interface {
	Doctor(context.Context, string) (session.RuntimeDiagnostic, error)
}

type DoctorCheck struct {
	Status  string `json:"status"`
	Count   int    `json:"count,omitempty"`
	Version int    `json:"version,omitempty"`
}

type ProviderDoctor struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	OuterScreen string `json:"outer_screen,omitempty"`
	KeyProtocol string `json:"key_protocol,omitempty"`
}

type ProfileDoctor struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type GlobalDoctorReport struct {
	Store     DoctorCheck      `json:"store"`
	Runtime   DoctorCheck      `json:"runtime"`
	Providers []ProviderDoctor `json:"providers"`
	Profiles  []ProfileDoctor  `json:"profiles"`
}

type ProviderPolicyDoctor struct {
	OuterScreen string `json:"outer_screen"`
	KeyProtocol string `json:"key_protocol"`
}

type SessionDoctorReport struct {
	SessionID        string                    `json:"session_id"`
	SessionName      string                    `json:"session_name"`
	Provider         string                    `json:"provider"`
	RuntimeStatus    string                    `json:"runtime_status"`
	Runtime          session.RuntimeDiagnostic `json:"runtime"`
	SelectedProfile  string                    `json:"selected_profile"`
	EffectiveProfile string                    `json:"effective_profile"`
	ProviderPolicy   ProviderPolicyDoctor      `json:"provider_policy"`
	FallbackReasons  []string                  `json:"fallback_reasons"`
}

var doctorIdentifierRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var doctorProviderRE = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

func (s *Service) DoctorGlobal(ctx context.Context) GlobalDoctorReport {
	report := GlobalDoctorReport{
		Store:     DoctorCheck{Status: "ok"},
		Runtime:   DoctorCheck{Status: "ok"},
		Providers: make([]ProviderDoctor, 0),
		Profiles:  make([]ProfileDoctor, 0),
	}
	cfg, err := s.loadDoctorConfig()
	if err != nil {
		report.Store.Status = "invalid"
	} else {
		report.Store.Version = cfg.SchemaVersion
		if cfg.ReadOnly {
			report.Store.Status = "read_only"
		}
		for name, profile := range cfg.Profiles {
			status := "ok"
			safeName := safeDoctorProfile(name)
			if safeName == "redacted" || store.ValidateProfile(profile) != nil {
				status = "invalid"
			}
			report.Profiles = append(report.Profiles, ProfileDoctor{Name: safeName, Status: status})
		}
		sort.Slice(report.Profiles, func(i, j int) bool { return report.Profiles[i].Name < report.Profiles[j].Name })
	}
	client := session.NewClient()
	runtimeCount, runtimeErr := client.RuntimeCount(ctx)
	if runtimeErr != nil {
		report.Runtime.Status = "unavailable"
	} else {
		report.Runtime.Count = runtimeCount
	}
	for _, provider := range s.Registry.Enabled() {
		name := safeDoctorProvider(provider.Name())
		entry := ProviderDoctor{Name: name, Status: "available"}
		if name == "redacted" {
			entry.Status = "invalid"
		}
		if policyProvider, ok := provider.(adapter.TerminalPolicyAdapter); ok {
			policy := policyProvider.TerminalPolicy()
			if policy.Validate() == nil {
				entry.OuterScreen = string(policy.OuterScreen)
				entry.KeyProtocol = string(policy.KeyProtocol)
			}
		}
		report.Providers = append(report.Providers, entry)
	}
	for name := range s.Registry.DisabledReasons() {
		report.Providers = append(report.Providers, ProviderDoctor{Name: safeDoctorProvider(name), Status: "unavailable"})
	}
	sort.Slice(report.Providers, func(i, j int) bool { return report.Providers[i].Name < report.Providers[j].Name })
	return report
}

func (s *Service) DoctorSession(ctx context.Context, id string) (SessionDoctorReport, error) {
	cfg, err := s.loadDoctorConfig()
	if err != nil {
		return SessionDoctorReport{}, errors.New("diagnostic store is invalid")
	}
	_, record, err := exactSessionRecord(&cfg, id)
	if err != nil {
		return SessionDoctorReport{}, err
	}
	report := SessionDoctorReport{
		SessionID: safeDoctorIdentifier(record.ID), SessionName: safeDoctorSessionName(record.SessionName),
		Provider: safeDoctorProvider(record.Agent), RuntimeStatus: "ok",
		SelectedProfile: safeDoctorProfile(record.Profile), EffectiveProfile: "none",
		FallbackReasons: make([]string, 0),
		Runtime:         session.RuntimeDiagnostic{Protocols: []int{1, 2}},
	}
	if record.Profile == "" {
		report.SelectedProfile = "default"
	} else if _, exists := cfg.Profiles[record.Profile]; !exists {
		report.SelectedProfile = "redacted"
	}
	if report.Provider == "redacted" {
		report.RuntimeStatus = "provider_invalid"
		return report, nil
	}
	provider, ok := s.Registry.Get(record.Agent)
	if !ok {
		report.Provider = "unavailable"
		report.RuntimeStatus = "provider_unavailable"
		return report, nil
	}
	policyProvider, ok := provider.(adapter.TerminalPolicyAdapter)
	if !ok {
		report.RuntimeStatus = "policy_unavailable"
		return report, nil
	}
	policy := policyProvider.TerminalPolicy()
	report.ProviderPolicy = ProviderPolicyDoctor{
		OuterScreen: string(policy.OuterScreen), KeyProtocol: string(policy.KeyProtocol),
	}
	effective, resolveErr := ResolveProfilePolicy(ResolutionInput{
		Config: cfg, Session: record, ProviderPolicy: policy,
	})
	if resolveErr != nil {
		report.RuntimeStatus = "profile_invalid"
		return report, nil
	}
	if effective.SelectedProfile() != "" {
		report.EffectiveProfile = safeDoctorProfile(effective.SelectedProfile())
	}
	for _, diagnostic := range effective.Diagnostics() {
		report.FallbackReasons = append(report.FallbackReasons, diagnostic.Code)
	}
	source := s.SessionDiagnostics
	if source == nil {
		source = session.NewClient()
	}
	runtime, runtimeErr := source.Doctor(ctx, record.SessionName)
	if runtimeErr != nil {
		report.RuntimeStatus = "stale"
	} else {
		report.Runtime = runtime
	}
	return report, nil
}

func (s *Service) loadDoctorConfig() (store.Config, error) {
	if s.Store == nil {
		return store.Config{}, errors.New("diagnostic store is unavailable")
	}
	data, err := os.ReadFile(s.Store.Path())
	if errors.Is(err, os.ErrNotExist) {
		return store.DefaultConfig(), nil
	}
	if err != nil {
		return store.Config{}, fmt.Errorf("read diagnostic store: %w", err)
	}
	var cfg store.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return store.Config{}, errors.New("parse diagnostic store")
	}
	return cfg, nil
}

func safeDoctorIdentifier(value string) string {
	if value == "" {
		return "unavailable"
	}
	if !doctorIdentifierRE.MatchString(value) || doctorSensitive(value) {
		return "redacted"
	}
	return value
}

func safeDoctorSessionName(value string) string {
	if value == "" {
		return "unavailable"
	}
	if session.ValidateName(value) != nil || doctorSensitive(value) {
		return "redacted"
	}
	return value
}

func safeDoctorProvider(value string) string {
	if value == "" {
		return "unavailable"
	}
	if !doctorProviderRE.MatchString(value) || doctorSensitive(value) {
		return "redacted"
	}
	return value
}

func safeDoctorProfile(value string) string {
	if value == "" {
		return "unavailable"
	}
	if store.ValidateProfileName(value) != nil || doctorSensitive(value) {
		return "redacted"
	}
	return value
}

func doctorSensitive(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"auth", "credential", "input", "output", "password", "private", "secret", "token"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
