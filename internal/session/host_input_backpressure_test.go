package session

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func nonReadingPTYHost(t *testing.T) *host {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "stty raw -echo; printf ready; exec sleep 30")
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		_ = master.Close()
	})
	ready := make(chan error, 1)
	go func() {
		var data [5]byte
		_, err := io.ReadFull(master, data[:])
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider did not enter raw mode")
	}
	return &host{registry: newClientRegistry(), term: vterm.New(80, 24, historyLines), ptmx: master, providerOutputSeen: true}
}

func TestInputBackpressureDoesNotBlockControls(t *testing.T) {
	h := nonReadingPTYHost(t)
	server := todo9StartServer(t, h)
	controller, _ := openMonitoredAttach(t, server.client, todo9SessionName, roleController, false)
	standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	if err := controller.writeControlFrame(frameStdin, bytes.Repeat([]byte("x"), maxFrameLen-ownershipEpochLen)); err != nil {
		t.Fatal(err)
	}
	// The provider never reads: this frame is much larger than the raw tty queue.
	time.Sleep(50 * time.Millisecond)
	resized := make(chan struct{})
	go func() {
		h.resizeClient(standby, standby.generation, terminalSize{cols: 100, rows: 30})
		close(resized)
	}()
	select {
	case <-resized:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("backpressured input blocked resize")
	}
	if err := writeFrame(controller.conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	// Even the input sender's own reader must finish, without another client
	// having to force a transfer or stop the provider.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		promoted := h.registry.controller == standby
		h.mu.Unlock()
		if promoted {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("controller could not detach while its provider ignored input")
}

func TestInputBackpressureDoesNotBlockFocus(t *testing.T) {
	h := nonReadingPTYHost(t)
	if err := h.writeOutOfBandInput(bytes.Repeat([]byte("x"), maxFrameLen)); err == nil {
		t.Fatal("full provider input queue did not report backpressure")
	}
	done := make(chan struct{})
	go func() {
		h.writeFocusEvent(focusIn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("synthetic focus blocked on provider input")
	}
}

func TestPendingInputCannotCrossControlTransfer(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	if _, err := term.MakeRaw(slave.Fd()); err != nil {
		t.Fatal(err)
	}
	slave, err = makePTYNonblocking(slave)
	if err != nil {
		t.Fatal(err)
	}
	h := &host{registry: newClientRegistry(), term: vterm.New(80, 24, historyLines), ptmx: master}
	controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	written := make(chan error, 1)
	go func() {
		written <- h.writeControllerInput(controller, controller.generation, bytes.Repeat([]byte("x"), maxFrameLen))
	}()
	time.Sleep(50 * time.Millisecond)
	command, err := json.Marshal(roleCommand{Action: actionTransferControl})
	if err != nil || !h.handleRoleCommand(controller, command) {
		t.Fatalf("transfer: %v", err)
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("stale input was not discarded: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("pending input blocked control transfer")
	}
	if err := slave.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	queued, _ := io.ReadAll(slave)
	if len(queued) == 0 || len(queued) >= maxFrameLen {
		t.Fatalf("queued input = %d bytes, expected a partial write", len(queued))
	}
	if err := h.writeControllerInput(standby, standby.generation, []byte("new-controller")); err != nil {
		t.Fatal(err)
	}
	if got := todo9ReadExact(t, slave, len("new-controller")); string(got) != "new-controller" {
		t.Fatalf("stale bytes crossed transfer: %q", got)
	}
}

func TestNonblockingPTYCloseInterruptsReadAfterResize(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close() })
	master, err = makePTYNonblocking(master)
	if err != nil {
		t.Fatal(err)
	}
	h := &host{ptmx: master}
	h.applyPTYSize(terminalSize{cols: 100, rows: 30})
	done := make(chan error, 1)
	go func() {
		var data [1]byte
		_, err := master.Read(data[:])
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read succeeded after closing master")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PTY close did not interrupt read")
	}
}

func TestInputSurvivesTransientBackpressure(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	if _, err := term.MakeRaw(slave.Fd()); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("normal-paste"), 16*1024)
	got := make([]byte, len(want))
	read := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, err := io.ReadFull(slave, got)
		read <- err
	}()
	h := &host{registry: newClientRegistry(), ptmx: master}
	if err := h.writeOutOfBandInput(want); err != nil {
		t.Fatalf("transient backpressure lost input: %v", err)
	}
	select {
	case err := <-read:
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("provider received different paste bytes: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider did not receive the complete paste")
	}
}
