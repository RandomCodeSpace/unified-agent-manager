package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func TestTodo9OwnershipRealPTYFixture(t *testing.T) {
	evidenceDir := os.Getenv("UAM_TASK9_EVIDENCE_DIR")
	if evidenceDir == "" {
		t.Skip("UAM_TASK9_EVIDENCE_DIR is required for artifact collection")
	}
	if !filepath.IsAbs(evidenceDir) {
		t.Fatalf("UAM_TASK9_EVIDENCE_DIR must be absolute: %q", evidenceDir)
	}
	if filepath.Base(evidenceDir) != "task-9-ownership" {
		t.Fatalf("refusing unexpected evidence directory: %q", evidenceDir)
	}
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})
	oldState, err := term.MakeRaw(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Restore(tty.Fd(), oldState) })
	h := &host{registry: newClientRegistry(), term: vterm.New(80, 24, historyLines), ptmx: ptmx}
	server := todo9StartServer(t, h)

	detachedErr := server.client.SendLine(context.Background(), todo9SessionName, "detached")
	if detachedErr != nil {
		t.Fatal(detachedErr)
	}
	controller, controllerResponse := openMonitoredAttach(t, server.client, todo9SessionName, roleController, false)
	standby, standbyResponse := openMonitoredAttach(t, server.client, todo9SessionName, roleController, false)
	if controllerResponse.AssignedRole != roleController || standbyResponse.AssignedRole != roleStandby {
		t.Fatalf("initial roles = %q/%q", controllerResponse.AssignedRole, standbyResponse.AssignedRole)
	}
	attachedErr := server.client.SendLine(context.Background(), todo9SessionName, "busy")
	attachedBusy := errors.Is(attachedErr, ErrSessionBusy)
	if !attachedBusy {
		t.Fatalf("attached reply = %v", attachedErr)
	}
	if err := controller.writeControlFrame(frameStdin, []byte("controller")); err != nil {
		t.Fatal(err)
	}
	if err := standby.writeControlFrame(frameResize, resizePayload(100, 35)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "standby resize observation", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		for client := range h.registry.clients {
			if client.id == standbyResponse.ClientID {
				return client.latestSize == (terminalSize{cols: 100, rows: 35})
			}
		}
		return false
	})
	command, err := json.Marshal(roleCommand{Action: actionTransferControl})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(controller.conn, frameRole, command); err != nil {
		t.Fatal(err)
	}
	controllerTransfer := todo9ReadRoleEvent(t, controller)
	standbyTransfer := todo9ReadRoleEvent(t, standby)
	transferRepaintBytes := todo9ReadRepaint(t, standby)
	todo9AssertNoAdditionalFrame(t, standby)
	if controllerTransfer.Role != roleStandby || standbyTransfer.Role != roleController || standbyTransfer.Reason != "transferred" {
		t.Fatalf("transfer events = %+v / %+v", controllerTransfer, standbyTransfer)
	}
	if err := standby.writeControlFrame(frameStdin, []byte("promoted")); err != nil {
		t.Fatal(err)
	}
	want := []byte("detached\rcontrollerpromoted")
	got := todo9ReadExact(t, tty, len(want))
	byteOrder := bytes.Equal(got, want)
	if !byteOrder {
		t.Fatalf("PTY byte order = %q, want %q", got, want)
	}
	size, err := pty.GetsizeFull(ptmx)
	if err != nil {
		t.Fatal(err)
	}
	if int(size.Cols) != 100 || int(size.Rows) != 35 {
		t.Fatalf("PTY size = %dx%d", size.Cols, size.Rows)
	}

	if err := writeFrame(controller.conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	todo9WaitForClientCount(t, h, 1)
	failover, failoverResponse := openMonitoredAttach(t, server.client, todo9SessionName, roleController, false)
	if failoverResponse.AssignedRole != roleStandby {
		t.Fatalf("failover role = %q", failoverResponse.AssignedRole)
	}
	if err := writeFrame(standby.conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	failoverPromotion := todo9ReadRoleEvent(t, failover)
	promotionRepaintBytes := todo9ReadRepaint(t, failover)
	todo9AssertNoAdditionalFrame(t, failover)
	if failoverPromotion.Role != roleController || failoverPromotion.Reason != "promoted" {
		t.Fatalf("failover event = %+v", failoverPromotion)
	}

	events := []todo9ObservedEvent{
		{Source: "round_trip", Event: "reply_detached", Accepted: detachedErr == nil},
		{Source: "round_trip", Event: "reply_attached", Busy: attachedBusy},
		{Source: "server_control_frame", Event: "role", ClientID: controllerTransfer.ClientID, Role: controllerTransfer.Role, Generation: controllerTransfer.Generation, Reason: controllerTransfer.Reason},
		{Source: "server_control_frame", Event: "role", ClientID: standbyTransfer.ClientID, Role: standbyTransfer.Role, Generation: standbyTransfer.Generation, Reason: standbyTransfer.Reason},
		{Source: "server_pty_frame", Event: "transfer_repaint", Bytes: transferRepaintBytes},
		{Source: "server_control_frame", Event: "role", ClientID: failoverPromotion.ClientID, Role: failoverPromotion.Role, Generation: failoverPromotion.Generation, Reason: failoverPromotion.Reason},
		{Source: "server_pty_frame", Event: "promotion_repaint", Bytes: promotionRepaintBytes},
	}
	assertions := todo9Assertions{
		DetachedReply:         detachedErr == nil,
		AttachedBusy:          attachedBusy,
		ByteOrder:             byteOrder,
		MixedWriterBytes:      !byteOrder,
		TransferRoleEvents:    2,
		TransferRepaints:      1,
		DropPromotionEvents:   1,
		DropPromotionRepaints: 1,
		PTYCols:               int(size.Cols),
		PTYRows:               int(size.Rows),
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "pty-byte-order.bin"), got, 0o644); err != nil {
		t.Fatal(err)
	}
	todo9WriteObservedArtifacts(t, evidenceDir, events, assertions)

	if err := writeFrame(failover.conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	todo9WaitForClientCount(t, h, 0)
	_ = controller.conn.Close()
	_ = standby.conn.Close()
	_ = failover.conn.Close()
	if err := server.listener.Close(); err != nil {
		t.Fatal(err)
	}
	_, socketErr := os.Stat(SocketPath(server.client.Dir, todo9SessionName))
	socketRemoved := errors.Is(socketErr, os.ErrNotExist)
	ptyClosed := ptmx.Close() == nil && tty.Close() == nil
	if err := os.RemoveAll(server.client.Dir); err != nil {
		t.Fatal(err)
	}
	_, runtimeErr := os.Stat(server.client.Dir)
	runtimeRemoved := errors.Is(runtimeErr, os.ErrNotExist)
	nestedOMOFound := false
	if err := filepath.WalkDir(evidenceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".omo" {
			nestedOMOFound = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleanup := todo9CleanupObservation{
		AttachedClients:   0,
		SocketRemoved:     socketRemoved,
		RuntimeRemoved:    runtimeRemoved,
		PTYClosed:         ptyClosed,
		NestedOMOFound:    nestedOMOFound,
		InProcessHostOnly: true,
	}
	if !cleanup.SocketRemoved || !cleanup.RuntimeRemoved || !cleanup.PTYClosed || cleanup.NestedOMOFound {
		t.Fatalf("cleanup observation = %+v", cleanup)
	}
	cleanupData, err := json.Marshal(cleanup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "cleanup-receipt.json"), append(cleanupData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
