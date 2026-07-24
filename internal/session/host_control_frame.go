package session

func (h *host) parseControlFrame(client *attachClient, payload []byte) (uint64, []byte, bool) {
	if client.version == protocolV1 {
		h.mu.Lock()
		generation := client.generation
		h.mu.Unlock()
		return generation, payload, true
	}
	generation, input, err := parseOwnedFramePayload(payload)
	return generation, input, err == nil
}

func (h *host) parseResizeFrame(client *attachClient, payload []byte) (uint64, terminalSize, bool) {
	generation, sizePayload, valid := h.parseControlFrame(client, payload)
	if !valid || len(sizePayload) != 4 {
		return 0, terminalSize{}, false
	}
	return generation, terminalSize{
		cols: int(sizePayload[0])<<8 | int(sizePayload[1]),
		rows: int(sizePayload[2])<<8 | int(sizePayload[3]),
	}, true
}

func (h *host) handleStdinFrame(client *attachClient, payload []byte) bool {
	generation, input, valid := h.parseControlFrame(client, payload)
	if !valid {
		return false
	}
	return h.writeControllerInput(client, generation, input) == nil
}

func (h *host) handleResizeFrame(client *attachClient, payload []byte) bool {
	generation, size, valid := h.parseResizeFrame(client, payload)
	if !valid {
		return false
	}
	h.resizeClient(client, generation, size)
	return true
}
