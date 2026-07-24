package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type monitoredAttach struct {
	conn       net.Conn
	reader     *bufio.Reader
	mu         sync.Mutex
	roles      []roleEvent
	generation uint64
	record     map[string]bool
	carry      []byte
	done       chan struct{}
}

func openMonitoredAttach(t *testing.T, client *Client, name string, requested clientRole, monitor bool) (*monitoredAttach, response) {
	t.Helper()
	conn, err := net.Dial("unix", SocketPath(client.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	hello := validTestHello()
	if err := writeJSONLine(conn, request{
		Op: opAttach, Version: protocolV2, Cols: 80, Rows: 24, RequestedRole: requested, Hello: &hello,
	}); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	var response response
	if err := readBoundedJSONLine(reader, &response); err != nil || !response.OK {
		t.Fatalf("attach response = %+v, %v", response, err)
	}
	attached := &monitoredAttach{conn: conn, reader: reader, generation: response.Generation, record: make(map[string]bool), done: make(chan struct{})}
	for range 2 {
		kind, payload, err := readFrame(reader)
		if err != nil {
			t.Fatal(err)
		}
		attached.consume(kind, payload)
	}
	if monitor {
		go attached.readFrames(reader)
	}
	return attached, response
}

func (attached *monitoredAttach) readFrames(reader *bufio.Reader) {
	defer close(attached.done)
	for {
		kind, payload, err := readFrame(reader)
		if err != nil {
			return
		}
		attached.consume(kind, payload)
	}
}

func (attached *monitoredAttach) consume(kind byte, payload []byte) {
	attached.mu.Lock()
	defer attached.mu.Unlock()
	if kind == serverFrameControl {
		var event roleEvent
		if json.Unmarshal(payload, &event) == nil && event.Type == "role" {
			attached.roles = append(attached.roles, event)
			attached.generation = event.Generation
		}
		return
	}
	data := append(attached.carry, payload...)
	for _, marker := range []string{
		"REC:65:81:25", "REC:66:101:31", "REC:67:111:33", "REC:68:121:35",
		"REC:72:", "REC:79:", "REC:83:", "REC:90:",
	} {
		if bytes.Contains(data, []byte(marker)) {
			attached.record[marker] = true
		}
	}
	if len(data) > 128 {
		data = data[len(data)-128:]
	}
	attached.carry = append(attached.carry[:0], data...)
}

func (attached *monitoredAttach) writeControlFrame(kind byte, payload []byte) error {
	attached.mu.Lock()
	generation := attached.generation
	attached.mu.Unlock()
	framed, err := ownedFramePayload(generation, payload)
	if err != nil {
		return err
	}
	return writeFrame(attached.conn, kind, framed)
}

func (attached *monitoredAttach) setGeneration(generation uint64) {
	attached.mu.Lock()
	attached.generation = generation
	attached.mu.Unlock()
}

func (attached *monitoredAttach) sawRole(role clientRole) bool {
	attached.mu.Lock()
	defer attached.mu.Unlock()
	for _, event := range attached.roles {
		if event.Role == role {
			return true
		}
	}
	return false
}

func (attached *monitoredAttach) sawRecord(marker string) bool {
	attached.mu.Lock()
	defer attached.mu.Unlock()
	return attached.record[marker]
}

func (attached *monitoredAttach) roleEvents() []roleEvent {
	attached.mu.Lock()
	defer attached.mu.Unlock()
	return append([]roleEvent(nil), attached.roles...)
}

func (attached *monitoredAttach) detach(t *testing.T) {
	t.Helper()
	if err := writeFrame(attached.conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attached.done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not close after detach")
	}
}

func TestControllerRegistryRealPTYFixture(t *testing.T) {
	evidenceDir := os.Getenv("UAM_TASK4_EVIDENCE_DIR")
	if evidenceDir == "" {
		t.Skip("manual Todo 4 PTY fixture")
	}
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "uam-fake-a4b4c4d4"
	command := `stty raw -echo; printf READY; while :; do n=$(dd bs=1 count=1 2>/dev/null | od -An -tu1 | tr -d ' \n'); [ -n "$n" ] || exit; set -- $(stty size); printf '\r\nREC:%s:%s:%s\r\n' "$n" "$2" "$1"; if [ "$n" = 72 ]; then yes X | head -c 8388608; fi; done`
	if err := client.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "manual provider readiness", func() bool {
		output, err := client.Capture(ctx, name, 20)
		return err == nil && bytes.Contains([]byte(output), []byte("READY"))
	})

	controller, controllerResponse := openMonitoredAttach(t, client, name, roleController, false)
	standby, standbyResponse := openMonitoredAttach(t, client, name, roleController, true)
	observer, observerResponse := openMonitoredAttach(t, client, name, roleObserver, false)
	if controllerResponse.AssignedRole != roleController || standbyResponse.AssignedRole != roleStandby || observerResponse.AssignedRole != roleObserver {
		t.Fatalf("initial roles = %q/%q/%q", controllerResponse.AssignedRole, standbyResponse.AssignedRole, observerResponse.AssignedRole)
	}
	_ = observer.writeControlFrame(frameResize, resizePayload(120, 40))
	_ = observer.writeControlFrame(frameStdin, []byte("O"))
	_ = standby.writeControlFrame(frameResize, resizePayload(100, 30))
	_ = standby.writeControlFrame(frameStdin, []byte("S"))
	_ = controller.writeControlFrame(frameResize, resizePayload(81, 25))
	_ = controller.writeControlFrame(frameStdin, []byte("A"))
	waitFor(t, "first controller input", func() bool { return standby.sawRecord("REC:65:81:25") })

	transfer, err := json.Marshal(roleCommand{Action: actionTransferControl})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(controller.conn, frameRole, transfer); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "standby transfer", func() bool { return standby.sawRole(roleController) })
	_ = controller.writeControlFrame(frameResize, resizePayload(82, 26))
	_ = controller.writeControlFrame(frameStdin, []byte("Z"))
	_ = standby.writeControlFrame(frameResize, resizePayload(101, 31))
	_ = standby.writeControlFrame(frameStdin, []byte("B"))
	waitFor(t, "transferred controller input", func() bool { return standby.sawRecord("REC:66:101:31") })

	standby.detach(t)
	failover, failoverResponse := openMonitoredAttach(t, client, name, roleController, true)
	if failoverResponse.AssignedRole != roleStandby {
		t.Fatalf("failover role = %q", failoverResponse.AssignedRole)
	}
	controller.setGeneration(failoverResponse.Generation)
	if err := controller.writeControlFrame(frameStdin, []byte("H")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "slow controller eviction", func() bool { return failover.sawRole(roleController) })
	waitFor(t, "slow controller provider input", func() bool { return failover.sawRecord("REC:72:") })
	_ = failover.writeControlFrame(frameResize, resizePayload(111, 33))
	_ = failover.writeControlFrame(frameStdin, []byte("C"))
	waitFor(t, "post-backpressure controller input", func() bool { return failover.sawRecord("REC:67:111:33") })

	failover.detach(t)
	_ = observer.writeControlFrame(frameStdin, []byte("O"))
	_ = writeFrame(observer.conn, frameDetach, nil)
	if err := observer.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.reader.WriteTo(bytes.NewBuffer(nil)); err != nil {
		t.Fatalf("wait for observer detach: %v", err)
	}
	output, err := client.Capture(ctx, name, 50)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(output), []byte("REC:79:")) {
		t.Fatal("observer input reached provider in no-controller state")
	}
	replacement, replacementResponse := openMonitoredAttach(t, client, name, roleController, true)
	if replacementResponse.AssignedRole != roleController {
		t.Fatalf("replacement after vacancy = %q", replacementResponse.AssignedRole)
	}
	_ = replacement.writeControlFrame(frameResize, resizePayload(121, 35))
	_ = replacement.writeControlFrame(frameStdin, []byte("D"))
	waitFor(t, "replacement controller input", func() bool { return replacement.sawRecord("REC:68:121:35") })
	for _, rejected := range []string{"REC:79:", "REC:83:", "REC:90:"} {
		if standby.sawRecord(rejected) || failover.sawRecord(rejected) || replacement.sawRecord(rejected) {
			t.Fatalf("non-owner provider record observed: %s", rejected)
		}
	}

	eventEvidence := struct {
		Initial        []response  `json:"initial"`
		StandbyEvents  []roleEvent `json:"standby_events"`
		FailoverEvents []roleEvent `json:"failover_events"`
	}{
		Initial:        []response{controllerResponse, standbyResponse, observerResponse, failoverResponse, replacementResponse},
		StandbyEvents:  standby.roleEvents(),
		FailoverEvents: failover.roleEvents(),
	}
	events, err := json.MarshalIndent(eventEvidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "task-4-events.json"), append(events, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	trace := fmt.Appendf(nil, "accepted=A@81x25,B@101x31,H,C@111x33,D@121x35\nrejected=O,S,Z\nslow_controller_promoted=%t\n", failover.sawRole(roleController))
	if err := os.WriteFile(filepath.Join(evidenceDir, "task-4-pty-trace.bin"), trace, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = replacement.conn.Close()
	if err := client.Kill(ctx, name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "manual host cleanup", func() bool {
		entries, err := os.ReadDir(client.Dir)
		return err == nil && len(entries) == 0
	})
	cleanup := []byte("{\"runtime_entries\":0,\"socket_removed\":true}\n")
	if err := os.WriteFile(filepath.Join(evidenceDir, "task-4-cleanup.json"), cleanup, 0o600); err != nil {
		t.Fatal(err)
	}
}
