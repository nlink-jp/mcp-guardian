package transport

import (
	"io"
	"runtime"
	"testing"
)

func TestProcessTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	result, err := NewProcessTransport("cat", nil)
	if err != nil {
		t.Fatalf("NewProcessTransport: %v", err)
	}
	defer result.Transport.Close()

	// Drain stderr in background
	go io.Copy(io.Discard, result.Stderr)

	msg := []byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`)
	if err := result.Transport.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	line, ok := result.Transport.ReadLine()
	if !ok {
		t.Fatal("ReadLine returned false")
	}
	if string(line) != string(msg) {
		t.Errorf("got %q, want %q", line, msg)
	}
}

func TestProcessTransportClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	result, err := NewProcessTransport("cat", nil)
	if err != nil {
		t.Fatalf("NewProcessTransport: %v", err)
	}

	go io.Copy(io.Discard, result.Stderr)

	if err := result.Transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close, ReadLine should return false
	_, ok := result.Transport.ReadLine()
	if ok {
		t.Error("ReadLine should return false after Close")
	}
}

func TestProcessTransportBadCommand(t *testing.T) {
	_, err := NewProcessTransport("nonexistent-command-12345", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}
