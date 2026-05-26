package pty

import (
	"errors"
	"os"
	"testing"
)

func TestOpenReturnsPair(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	if p.Master == nil || p.Slave == nil {
		t.Fatalf("expected non-nil master and slave fds")
	}
	if p.SlavePath == "" {
		t.Fatalf("expected non-empty slave path")
	}
	if _, err := os.Stat(p.SlavePath); err != nil {
		t.Fatalf("slave path should exist: %v", err)
	}
}

func TestOpenMasterIsCloexec(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	flags, err := getFdFlags(int(p.Master.Fd()))
	if err != nil {
		t.Fatalf("getFdFlags: %v", err)
	}
	if !flags.CloseOnExec {
		t.Fatalf("master fd must have FD_CLOEXEC")
	}
}

func TestCloseClosesBoth(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	masterFd := int(p.Master.Fd())
	slaveFd := int(p.Slave.Fd())
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := getFdFlags(masterFd); !errors.Is(err, ErrFdClosed) {
		t.Fatalf("expected master fd closed, got %v", err)
	}
	if _, err := getFdFlags(slaveFd); !errors.Is(err, ErrFdClosed) {
		t.Fatalf("expected slave fd closed, got %v", err)
	}
}
