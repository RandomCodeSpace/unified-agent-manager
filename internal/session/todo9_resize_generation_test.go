package session

import (
	"fmt"
	"testing"
)

func TestResizeGenerationRejectsStaleFrames(t *testing.T) {
	h, _ := todo9PipeHost(t)
	controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	stale := controller.generation
	standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 90, rows: 30})
	h.registry.transfer(controller)

	frames := [][]byte{
		resizePayload(120, 40),
		ownedFramePayload(stale, resizePayload(121, 41)),
		ownedFramePayload(standby.generation+1, resizePayload(122, 42)),
		ownedFramePayload(standby.generation, resizePayload(0, 0)),
		ownedFramePayload(standby.generation, resizePayload(1001, 24)),
	}
	for _, frame := range frames {
		h.handleResizeFrame(standby, frame)
	}

	cols, rows := h.term.Size()
	if cols != 80 || rows != 24 {
		t.Fatalf("rejected resize reached terminal: %dx%d", cols, rows)
	}
}

func TestInvalidPromotedSizeRetainsPrevious(t *testing.T) {
	for _, invalid := range []terminalSize{{}, {cols: 1001, rows: 24}, {cols: 80, rows: 1001}} {
		t.Run(fmt.Sprintf("%dx%d", invalid.cols, invalid.rows), func(t *testing.T) {
			h, _ := todo9PipeHost(t)
			controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
			standby := registerTestClient(t, h.registry, roleController, terminalSize{})
			h.registry.updateSize(standby, standby.generation, invalid)

			h.dropClient(controller)

			cols, rows := h.term.Size()
			if cols != 80 || rows != 24 {
				t.Fatalf("invalid promoted size changed terminal to %dx%d", cols, rows)
			}
		})
	}
}
