package rtsp

import (
	"net"
	"testing"
	"time"
)

func TestListener_Listen(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer l.Close()

	addr := l.Addr()
	if addr == nil {
		t.Fatal("Addr returned nil")
	}

	if addr.Network() != "tcp" {
		t.Errorf("expected network tcp, got %s", addr.Network())
	}
}

func TestListener_ListenError(t *testing.T) {
	_, err := Listen("invalid_network", "127.0.0.1:0")
	if err == nil {
		t.Fatal("expected error for invalid network, got nil")
	}
}

func TestListener_Accept(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer l.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		errCh <- nil
	}()

	conn, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	defer conn.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Dial failed: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Dial timed out")
	}
}

func TestListener_AcceptError(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	// Close listener immediately so Accept fails
	l.Close()

	_, err = l.Accept()
	if err == nil {
		t.Fatal("expected error on Accept after Close, got nil")
	}
}

func TestListener_Close(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	err = l.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Calling close multiple times usually returns an error in net.Listener
	err = l.Close()
	if err == nil {
		t.Log("Warning: Expected error on second Close, got nil (depends on net.Listener implementation)")
	}
}
