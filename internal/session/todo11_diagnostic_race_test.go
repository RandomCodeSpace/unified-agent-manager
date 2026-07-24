package session

import (
	"runtime"
	"sync"
	"testing"
)

func TestTodo11AttachDiagnosticSnapshotsMutableClientState(t *testing.T) {
	// Given: client state changing under the host mutex during attach diagnostics.
	for range 100 {
		h := &host{name: "uam-fake-abcdef12", registry: newClientRegistry()}
		client := &attachClient{
			version: protocolV2,
			done:    make(chan struct{}),
			out:     make(chan serverMessage, 8),
		}
		stop := make(chan struct{})
		started := make(chan struct{})
		var mutator sync.WaitGroup
		mutator.Add(1)
		go func() {
			defer mutator.Done()
			close(started)
			for {
				select {
				case <-stop:
					return
				default:
				}
				h.mu.Lock()
				client.assignedRole = roleStandby
				client.hello.TermHint = "screen-256color"
				h.mu.Unlock()
				runtime.Gosched()
			}
		}()
		<-started

		// When: registration snapshots fields and emits attach diagnostics.
		_, err := h.registerAttachClient(client, clientRegistration{
			requestedRole: roleController,
			hello:         validTestHello(),
		})
		close(stop)
		mutator.Wait()

		// Then: registration succeeds without an unsynchronized diagnostic read.
		if err != nil {
			t.Fatal(err)
		}
	}
}
