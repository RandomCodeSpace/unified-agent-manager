package ipc

import (
	"bytes"
	"reflect"
	"testing"
)

func TestCodecRoundtrip(t *testing.T) {
	req := Request{Kind: KindSpawn, ID: 7, Payload: []byte(`{"name":"foo"}`)}
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, req); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadFrame(buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", got, req)
	}
}

func TestCodecRejectsOversize(t *testing.T) {
	bad := bytes.Buffer{}
	// 256 MiB length prefix is well above any sane request — should be rejected.
	bad.Write([]byte{0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if _, err := ReadFrame(&bad); err == nil {
		t.Fatalf("expected oversize rejection")
	}
}

func TestCodecEmptyPayload(t *testing.T) {
	req := Request{Kind: KindList, ID: 1, Payload: nil}
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, req); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadFrame(buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Kind != KindList || got.ID != 1 {
		t.Fatalf("metadata mismatch: %+v", got)
	}
	if len(got.Payload) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(got.Payload))
	}
}

func TestWriteFrameRejectsOversizePayload(t *testing.T) {
	huge := make([]byte, MaxFrameSize+1)
	if err := WriteFrame(&bytes.Buffer{}, Request{Kind: KindSpawn, ID: 1, Payload: huge}); err == nil {
		t.Fatalf("expected oversize write rejection")
	}
}

func TestReadFrameRejectsUndersize(t *testing.T) {
	bad := bytes.Buffer{}
	bad.Write([]byte{0x00, 0x00, 0x00, 0x02}) // length=2, below minimum (5)
	bad.Write([]byte{0x00, 0x00})
	if _, err := ReadFrame(&bad); err == nil {
		t.Fatalf("expected undersize rejection")
	}
}

func TestReadFrameTruncated(t *testing.T) {
	// length prefix only, no body
	bad := bytes.Buffer{}
	bad.Write([]byte{0x00, 0x00, 0x00, 0x10})
	if _, err := ReadFrame(&bad); err == nil {
		t.Fatalf("expected truncated read error")
	}
}
