package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type todo9ObservedEvent struct {
	Source     string     `json:"source"`
	Event      string     `json:"event"`
	ClientID   string     `json:"client_id,omitempty"`
	Role       clientRole `json:"role,omitempty"`
	Generation uint64     `json:"generation,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Bytes      int        `json:"bytes,omitempty"`
	Accepted   bool       `json:"accepted,omitempty"`
	Busy       bool       `json:"busy,omitempty"`
}

type todo9Assertions struct {
	DetachedReply         bool `json:"detached_reply"`
	AttachedBusy          bool `json:"attached_busy"`
	ByteOrder             bool `json:"byte_order"`
	MixedWriterBytes      bool `json:"mixed_writer_bytes"`
	TransferRoleEvents    int  `json:"transfer_role_events"`
	TransferRepaints      int  `json:"transfer_repaints"`
	DropPromotionEvents   int  `json:"drop_promotion_events"`
	DropPromotionRepaints int  `json:"drop_promotion_repaints"`
	PTYCols               int  `json:"pty_cols"`
	PTYRows               int  `json:"pty_rows"`
}

type todo9CleanupObservation struct {
	AttachedClients   int  `json:"attached_clients"`
	SocketRemoved     bool `json:"socket_removed"`
	RuntimeRemoved    bool `json:"runtime_removed"`
	PTYClosed         bool `json:"pty_closed"`
	NestedOMOFound    bool `json:"nested_omo_found"`
	InProcessHostOnly bool `json:"in_process_host_only"`
}

func todo9ReadServerFrame(t *testing.T, attached *monitoredAttach) (byte, []byte) {
	t.Helper()
	if err := attached.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	kind, payload, err := readFrame(attached.reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := attached.conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return kind, payload
}

func todo9ReadRoleEvent(t *testing.T, attached *monitoredAttach) roleEvent {
	t.Helper()
	kind, payload := todo9ReadServerFrame(t, attached)
	if kind != serverFrameControl {
		t.Fatalf("frame kind = %d, want control", kind)
	}
	var event roleEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	attached.setGeneration(event.Generation)
	return event
}

func todo9ReadRepaint(t *testing.T, attached *monitoredAttach) int {
	t.Helper()
	kind, payload := todo9ReadServerFrame(t, attached)
	if kind != serverFramePTY || len(payload) == 0 {
		t.Fatalf("repaint frame = kind %d bytes %d", kind, len(payload))
	}
	return len(payload)
}

func todo9AssertNoAdditionalFrame(t *testing.T, attached *monitoredAttach) {
	t.Helper()
	if err := attached.conn.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, err := readFrame(attached.reader)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("additional server frame observed: %v", err)
	}
	if err := attached.conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func todo9WaitForClientCount(t *testing.T, h *host, want int) {
	t.Helper()
	waitFor(t, "attached client count", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.registry.clients) == want
	})
}

func todo9WriteObservedArtifacts(t *testing.T, evidenceDir string, events []todo9ObservedEvent, assertions todo9Assertions) {
	t.Helper()
	var eventData bytes.Buffer
	encoder := json.NewEncoder(&eventData)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "ownership-events.jsonl"), eventData.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	assertionData, err := json.Marshal(assertions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "assertions.json"), append(assertionData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
