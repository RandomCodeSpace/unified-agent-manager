package session

import (
	"bytes"
	"testing"
)

func todo11ExerciseHost(
	t *testing.T,
	termHint string,
	fixture todo11Fixture,
	index int,
) (todo11Normalized, []byte) {
	t.Helper()
	harness := todo11StartHost(t, fixture, index)
	controller := harness.attach(t, termHint, roleController)
	negotiatedTermHint := harness.negotiatedTermHint(t, controller.clientID)
	replayModes := bytes.Contains(controller.replay, []byte("\x1b[?2004h")) &&
		bytes.Contains(controller.replay, []byte("\x1b[?1004h"))

	inputNames := make([]string, 0, len(fixture.Input)+3)
	for _, item := range fixture.Input {
		input := todo11Decode(t, item.Hex)
		filtered, detached := (&stdinFilter{}).filter(input)
		if detached {
			t.Fatalf("%s detached unexpectedly", item.Name)
		}
		todo11WriteInput(t, controller, filtered)
		inputNames = append(inputNames, item.Name)
		harness.waitInputCount(t, len(inputNames))
	}
	malformedEscape := todo11Decode(t, fixture.MalformedEscapeHex)
	todo11WriteInput(t, controller, malformedEscape)
	inputNames = append(inputNames, "malformed_escape")
	harness.waitInputCount(t, len(inputNames))

	largePaste := append(append([]byte{}, pasteBegin...), bytes.Repeat([]byte("p"), 128<<10)...)
	largePaste = append(largePaste, pasteEnd...)
	filteredPaste, detached := (&stdinFilter{}).filter(largePaste)
	if detached || !bytes.Equal(filteredPaste, largePaste) {
		t.Fatal("large bracketed paste changed before protocol framing")
	}
	todo11WriteInput(t, controller, filteredPaste)
	inputNames = append(inputNames, "large_paste")
	harness.waitInputCount(t, len(inputNames))

	observer := harness.attach(t, termHint, roleObserver)
	todo11WriteInput(t, observer, []byte("observer-must-not-arrive"))
	todo11WriteInput(t, controller, []byte("controller-ack"))
	inputNames = append(inputNames, "controller_ack")
	harness.waitInputCount(t, len(inputNames))

	resizes := [][2]int{{80, 24}, {132, 43}, {1, 1}, {110, 32}}
	for _, size := range resizes {
		if err := controller.write(frameResize, resizePayload(size[0], size[1])); err != nil {
			t.Fatal(err)
		}
	}
	var finalWINCH bool
	waitFor(t, "provider final resize signal", func() bool {
		for _, event := range harness.events(t) {
			if event.Type == "signal" && event.Signal == "SIGWINCH" &&
				event.Cols == 110 && event.Rows == 32 {
				finalWINCH = true
			}
		}
		return finalWINCH
	})

	controller.close()
	reconnected := harness.attach(t, termHint, roleController)
	reconnected.awaitController(t, &harness.transcript)
	disconnectReattached := reconnected.role == roleController
	todo11DropMalformed(t, reconnected, false)
	afterMalformed := harness.attach(t, termHint, roleController)
	afterMalformed.awaitController(t, &harness.transcript)
	malformedDropped := afterMalformed.role == roleController
	todo11DropMalformed(t, afterMalformed, true)
	afterTruncated := harness.attach(t, termHint, roleController)
	afterTruncated.awaitController(t, &harness.transcript)
	truncatedDropped := afterTruncated.role == roleController
	todo11WriteInput(t, afterTruncated, todo11ProviderExit)
	harness.waitInputCount(t, len(inputNames)+1)

	events := harness.events(t)
	startup := events[0]
	var modes []string
	for _, event := range events {
		if event.Type == "modes" {
			modes = append([]string{}, event.Modes...)
		}
	}
	socketRemoved, runtimeEntries := harness.cleanup(t)
	report := todo11ReadAllReport(t, harness)
	harness.transcript.Write(report)
	events = harness.events(t)
	var inputEvents []todo11ProviderEvent
	observerSuppressed := true
	for _, event := range events {
		if event.Type != "input" {
			continue
		}
		if bytes.Equal(todo11EventInput(t, event), []byte("observer-must-not-arrive")) {
			observerSuppressed = false
			continue
		}
		inputEvents = append(inputEvents, event)
	}
	observedInputs := todo11ObservedInputs(t, inputEvents, inputNames)

	normalized := todo11Normalized{
		TermHint: termHint, NegotiatedTermHint: negotiatedTermHint,
		ProviderTERM: startup.TERM, Protocol: int(protocolV2),
		ControllerRole: string(roleController), ReconnectRole: string(reconnected.role),
		InputHex: observedInputs, ProviderModes: modes,
		InitialSize: [2]int{startup.Cols, startup.Rows}, FinalSize: [2]int{110, 32},
		WINCHObserved: finalWINCH, ReplayObserved: len(controller.replay) > 0 && len(reconnected.replay) > 0,
		ReplayModesObserved: replayModes, ObserverSuppressed: observerSuppressed,
		DisconnectReattached: disconnectReattached, MalformedDropped: malformedDropped,
		TruncatedDropped: truncatedDropped, LargePasteBytes: len(filteredPaste),
		CapabilityInferred: false, SocketRemoved: socketRemoved, RuntimeEntries: runtimeEntries,
	}
	if startup.TERM != "xterm-256color" {
		t.Fatalf("provider TERM = %q", startup.TERM)
	}
	return normalized, harness.transcript.Bytes()
}
