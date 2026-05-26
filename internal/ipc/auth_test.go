package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerUIDOnUDSPair(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	accepted, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer accepted.Close()

	uconn, ok := accepted.(*net.UnixConn)
	if !ok {
		t.Fatalf("accepted is not *net.UnixConn: %T", accepted)
	}
	uid, err := PeerUID(uconn)
	if err != nil {
		t.Fatalf("PeerUID: %v", err)
	}
	if uid != uint32(os.Getuid()) {
		t.Fatalf("expected uid %d, got %d", os.Getuid(), uid)
	}
}

func TestPeerUIDRejectsNil(t *testing.T) {
	if _, err := PeerUID(nil); err == nil {
		t.Fatalf("expected error on nil conn")
	}
}
