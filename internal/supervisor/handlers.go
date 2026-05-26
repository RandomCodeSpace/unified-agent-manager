package supervisor

import (
	"encoding/json"
	"fmt"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
)

// dispatch routes one control-socket RPC to its handler.
func (s *Supervisor) dispatch(req ipc.Request) []byte {
	switch req.Kind {
	case ipc.KindHello:
		return s.handleHello()
	case ipc.KindSpawn:
		return s.handleSpawn(req.Payload)
	case ipc.KindList:
		return s.handleList(req.Payload)
	case ipc.KindHas:
		return s.handleHas(req.Payload)
	case ipc.KindCapture:
		return s.handleCapture(req.Payload)
	case ipc.KindWrite:
		return s.handleWrite(req.Payload)
	case ipc.KindResize:
		return s.handleResize(req.Payload)
	case ipc.KindKill:
		return s.handleKill(req.Payload)
	case ipc.KindStatus:
		return s.handleStatus(req.Payload)
	case ipc.KindShutdown:
		s.Shutdown()
		return asJSON(struct {
			OK bool `json:"ok"`
		}{OK: true})
	}
	return errPayload(fmt.Sprintf("unknown kind %d", req.Kind))
}

func errPayload(msg string) []byte {
	return asJSON(struct {
		Error string `json:"error"`
	}{Error: msg})
}

func (s *Supervisor) handleHello() []byte {
	return asJSON(struct {
		Pid int `json:"pid"`
	}{Pid: pidSelf()})
}

func (s *Supervisor) handleSpawn(payload []byte) []byte {
	var spec SpawnSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return errPayload(fmt.Sprintf("spawn: bad payload: %v", err))
	}
	rec, err := s.SpawnHost(spec)
	if err != nil {
		return errPayload(err.Error())
	}
	return asJSON(struct {
		Handle string        `json:"handle"`
		Record SessionRecord `json:"record"`
	}{Handle: rec.ID, Record: rec})
}

func (s *Supervisor) handleList(payload []byte) []byte {
	var p struct {
		Prefix string `json:"prefix"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	out := s.listSessions()
	if p.Prefix != "" {
		filtered := out[:0]
		for _, r := range out {
			if hasPrefix(r.ID, p.Prefix) {
				filtered = append(filtered, r)
			}
		}
		out = filtered
	}
	return asJSON(struct {
		Sessions []SessionRecord `json:"sessions"`
	}{Sessions: out})
}

func (s *Supervisor) handleHas(payload []byte) []byte {
	var p struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(payload, &p)
	s.mu.Lock()
	_, ok := s.sessions[p.Handle]
	s.mu.Unlock()
	return asJSON(struct {
		Has bool `json:"has"`
	}{Has: ok})
}

func (s *Supervisor) handleCapture(payload []byte) []byte {
	var p struct {
		Handle string `json:"handle"`
		Bytes  int64  `json:"bytes"`
		Lines  int    `json:"lines"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return errPayload(err.Error())
	}
	// Host expects `{"bytes": N}` — translate Lines hint into a byte budget.
	if p.Bytes <= 0 {
		if p.Lines > 0 {
			p.Bytes = int64(p.Lines) * 200 // ~200 bytes/line is a safe upper bound.
		} else {
			p.Bytes = 64 * 1024
		}
	}
	inner, _ := json.Marshal(struct {
		Bytes int64 `json:"bytes"`
	}{Bytes: p.Bytes})
	resp, err := s.proxyToHost(p.Handle, ipc.KindCapture, inner)
	if err != nil {
		return errPayload(err.Error())
	}
	return resp
}

func (s *Supervisor) handleWrite(payload []byte) []byte {
	var p struct {
		Handle string `json:"handle"`
		Data   []byte `json:"data"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return errPayload(err.Error())
	}
	inner, _ := json.Marshal(struct {
		Data []byte `json:"data"`
	}{Data: p.Data})
	_, err := s.proxyToHost(p.Handle, ipc.KindWrite, inner)
	if err != nil {
		return errPayload(err.Error())
	}
	return asJSON(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (s *Supervisor) handleResize(payload []byte) []byte {
	var p struct {
		Handle string `json:"handle"`
		Cols   uint16 `json:"cols"`
		Rows   uint16 `json:"rows"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return errPayload(err.Error())
	}
	inner, _ := json.Marshal(struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}{Cols: p.Cols, Rows: p.Rows})
	_, err := s.proxyToHost(p.Handle, ipc.KindResize, inner)
	if err != nil {
		return errPayload(err.Error())
	}
	return asJSON(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (s *Supervisor) handleKill(payload []byte) []byte {
	var p struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return errPayload(err.Error())
	}
	_, err := s.proxyToHost(p.Handle, ipc.KindKill, nil)
	// Remove record regardless — the host is dying.
	s.removeSession(p.Handle)
	if err != nil {
		return errPayload(err.Error())
	}
	return asJSON(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (s *Supervisor) handleStatus(payload []byte) []byte {
	var p struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return errPayload(err.Error())
	}
	return mustProxy(s, p.Handle, ipc.KindStatus, nil)
}

func mustProxy(s *Supervisor, handle string, kind ipc.Kind, inner []byte) []byte {
	resp, err := s.proxyToHost(handle, kind, inner)
	if err != nil {
		return errPayload(err.Error())
	}
	return resp
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func pidSelf() int {
	// os.Getpid lifted into a helper for testability.
	return getpid()
}
