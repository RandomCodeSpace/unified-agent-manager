package session

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type todo11Fixture struct {
	Input []struct {
		Name string `json:"name"`
		Hex  string `json:"hex"`
	} `json:"input"`
	ProviderOutputHex  string `json:"provider_output_hex"`
	MalformedEscapeHex string `json:"malformed_escape_hex"`
}

type todo11Normalized struct {
	TermHint             string            `json:"term_hint"`
	NegotiatedTermHint   string            `json:"negotiated_term_hint"`
	ProviderTERM         string            `json:"provider_term"`
	Protocol             int               `json:"protocol"`
	ControllerRole       string            `json:"controller_role"`
	ReconnectRole        string            `json:"reconnect_role"`
	InputHex             map[string]string `json:"input_hex"`
	ProviderModes        []string          `json:"provider_modes"`
	InitialSize          [2]int            `json:"initial_size"`
	FinalSize            [2]int            `json:"final_size"`
	WINCHObserved        bool              `json:"winch_observed"`
	ReplayObserved       bool              `json:"replay_observed"`
	ReplayModesObserved  bool              `json:"replay_modes_observed"`
	ObserverSuppressed   bool              `json:"observer_suppressed"`
	DisconnectReattached bool              `json:"disconnect_reattached"`
	MalformedDropped     bool              `json:"malformed_dropped"`
	TruncatedDropped     bool              `json:"truncated_dropped"`
	LargePasteBytes      int               `json:"large_paste_bytes"`
	CapabilityInferred   bool              `json:"capability_inferred"`
	SocketRemoved        bool              `json:"socket_removed"`
	RuntimeEntries       int               `json:"runtime_entries"`
}

func todo11LoadFixture(t *testing.T) todo11Fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "todo11", "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture todo11Fixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func todo11Decode(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func todo11SafeName(termName string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(termName)
}

func todo11WriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
