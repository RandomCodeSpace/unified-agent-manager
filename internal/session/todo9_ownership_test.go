package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
)

const todo9SessionName = "uam-fake-abcdef12"

type todo9Server struct {
	client   *Client
	listener net.Listener
}

func todo9StartServer(t *testing.T, h *host) *todo9Server {
	t.Helper()
	dir := socketTestDir(t)
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", SocketPath(dir, todo9SessionName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go h.handleConn(conn)
		}
	}()
	return &todo9Server{client: &Client{Dir: dir}, listener: listener}
}

func todo9ControlClient(t *testing.T, h *host) *Client {
	t.Helper()
	return todo9StartServer(t, h).client
}

func todo9PipeHost(t *testing.T) (*host, *os.File) {
	t.Helper()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})
	return &host{registry: newClientRegistry(), term: vterm.New(80, 24, historyLines), ptmx: writeEnd}, readEnd
}

func todo9ReadExact(t *testing.T, reader *os.File, size int) []byte {
	t.Helper()
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, size)
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	return got
}

func todo9AssertNoPTYBytes(t *testing.T, reader *os.File) {
	t.Helper()
	if err := reader.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if n, err := reader.Read(one[:]); n != 0 || err == nil {
		t.Fatalf("unexpected PTY bytes: %q, err=%v", one[:n], err)
	}
}

func TestReplyWithoutControllerSucceeds(t *testing.T) {
	h, provider := todo9PipeHost(t)
	client := todo9ControlClient(t, h)

	if err := client.SendLine(t.Context(), todo9SessionName, "detached"); err != nil {
		t.Fatalf("detached reply: %v", err)
	}

	if got := todo9ReadExact(t, provider, len("detached\r")); !bytes.Equal(got, []byte("detached\r")) {
		t.Fatalf("provider bytes = %q", got)
	}
}

func TestReplyWithControllerReturnsBusy(t *testing.T) {
	h, provider := todo9PipeHost(t)
	registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	client := todo9ControlClient(t, h)

	err := client.SendLine(t.Context(), todo9SessionName, "must-not-mix")

	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("reply error = %v, want errors.Is ErrSessionBusy", err)
	}
	var busy *SessionBusyError
	if !errors.As(err, &busy) || busy.Operation != opSend {
		t.Fatalf("reply error = %#v, want typed send busy error", err)
	}
	todo9AssertNoPTYBytes(t, provider)
}

func TestOutOfBandResizeRejected(t *testing.T) {
	h, _ := todo9PipeHost(t)
	registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	client := todo9ControlClient(t, h)

	_, err := client.roundTrip(t.Context(), todo9SessionName, request{Op: opResize, Cols: 120, Rows: 40})

	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("resize error = %v, want errors.Is ErrSessionBusy", err)
	}
	cols, rows := h.term.Size()
	if cols != 80 || rows != 24 {
		t.Fatalf("terminal resized out of band to %dx%d", cols, rows)
	}
}

func TestEveryDropPathPromotesOnce(t *testing.T) {
	tests := []struct {
		name string
		drop func(*host, *attachClient)
	}{
		{name: "detach", drop: func(h *host, client *attachClient) {
			var frame bytes.Buffer
			if err := writeFrame(&frame, frameDetach, nil); err != nil {
				t.Fatal(err)
			}
			h.attachReader(client, bufio.NewReader(&frame))
		}},
		{name: "socket failure", drop: func(h *host, client *attachClient) {
			h.attachReader(client, bufio.NewReader(bytes.NewReader(nil)))
		}},
		{name: "malformed frame", drop: func(h *host, client *attachClient) {
			var frame bytes.Buffer
			if err := writeFrame(&frame, 0xff, nil); err != nil {
				t.Fatal(err)
			}
			h.attachReader(client, bufio.NewReader(&frame))
		}},
		{name: "slow client eviction", drop: func(h *host, client *attachClient) {
			client.out <- serverMessage{kind: serverFramePTY, payload: []byte("blocked")}
			h.enqueueClient(client, serverMessage{kind: serverFramePTY, payload: []byte("overflow")})
		}},
		{name: "concurrent repeated drop", drop: func(h *host, client *attachClient) {
			var drops sync.WaitGroup
			for range 32 {
				drops.Add(1)
				go func() {
					defer drops.Done()
					h.dropClient(client)
				}()
			}
			drops.Wait()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h, _ := todo9PipeHost(t)
			controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
			standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 90, rows: 30})

			test.drop(h, controller)

			if h.registry.controller != standby {
				t.Fatal("oldest standby was not promoted")
			}
			promotions := 0
			for len(standby.out) > 0 {
				message := <-standby.out
				var event roleEvent
				if message.kind == serverFrameControl && json.Unmarshal(message.payload, &event) == nil && event.Reason == "promoted" {
					promotions++
				}
			}
			if promotions != 1 {
				t.Fatalf("promotion events = %d, want 1", promotions)
			}
		})
	}

	t.Run("terminal host shutdown does not promote", func(t *testing.T) {
		registry := newClientRegistry()
		controller := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
		standby := registerTestClient(t, registry, roleController, terminalSize{cols: 90, rows: 30})
		clients := registry.drain()
		if len(clients) != 2 || registry.controller != nil {
			t.Fatal("shutdown did not drain registry")
		}
		if controller.assignedRole != roleController || standby.assignedRole != roleStandby || len(standby.out) != 0 {
			t.Fatal("terminal shutdown promoted a standby")
		}
	})
}

func TestPromotionAppliesLatestValidSizeOnce(t *testing.T) {
	h, _ := todo9PipeHost(t)
	controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 90, rows: 30})
	standby.ready = true
	standby.out = make(chan serverMessage, 4)
	if h.registry.updateSize(standby, standby.generation, terminalSize{cols: 100, rows: 35}) {
		t.Fatal("standby resize reached PTY before promotion")
	}

	h.dropClient(controller)
	h.dropClient(controller)

	cols, rows := h.term.Size()
	if cols != 100 || rows != 35 {
		t.Fatalf("promoted size = %dx%d, want latest 100x35", cols, rows)
	}
	promotions := 0
	repaints := 0
	for len(standby.out) > 0 {
		message := <-standby.out
		var event roleEvent
		if message.kind == serverFrameControl && json.Unmarshal(message.payload, &event) == nil && event.Reason == "promoted" {
			promotions++
		}
		if message.kind == serverFramePTY {
			repaints++
		}
	}
	if promotions != 1 {
		t.Fatalf("promotion applications = %d, want 1", promotions)
	}
	if repaints != 1 {
		t.Fatalf("promotion repaints = %d, want 1", repaints)
	}
}

func TestControllerTransferRepaintsOnce(t *testing.T) {
	h, _ := todo9PipeHost(t)
	controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 100, rows: 35})
	standby.ready = true
	standby.out = make(chan serverMessage, 4)
	command, err := json.Marshal(roleCommand{Action: actionTransferControl})
	if err != nil {
		t.Fatal(err)
	}

	if !h.handleRoleCommand(controller, command) {
		t.Fatal("valid transfer command was rejected")
	}
	if !h.handleRoleCommand(controller, command) {
		t.Fatal("repeated transfer command was rejected")
	}

	repaints := 0
	for len(standby.out) > 0 {
		if message := <-standby.out; message.kind == serverFramePTY {
			repaints++
		}
	}
	if repaints != 1 {
		t.Fatalf("transfer repaints = %d, want 1", repaints)
	}
	cols, rows := h.term.Size()
	if cols != 100 || rows != 35 {
		t.Fatalf("transferred size = %dx%d, want 100x35", cols, rows)
	}
}
