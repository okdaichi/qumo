package rtmp

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestListener_Listen(t *testing.T) {
	// Let OS pick a free port.
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer l.Close()

	if l.Addr() == nil {
		t.Fatal("expected non-nil Addr()")
	}
}

func TestListener_Accept(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer l.Close()

	addr := l.Addr().String()

	errCh := make(chan error, 1)
	connCh := make(chan *Conn, 1)

	// Accept in background
	go func() {
		conn, err := l.Accept()
		if err != nil {
			errCh <- err
			return
		}
		connCh <- conn
	}()

	// Dial using RTMP dialer to perform client-side handshake
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	clientConn, err := Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	// Wait for server to accept
	select {
	case err := <-errCh:
		t.Fatalf("Accept failed: %v", err)
	case conn := <-connCh:
		defer conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Accept")
	}
}

func TestListener_Close(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		errCh <- err
	}()

	// Wait briefly to ensure Accept is blocking
	time.Sleep(10 * time.Millisecond)

	if err := l.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from Accept after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Accept to return after Close")
	}
}

func TestServerConn_HandshakeError(t *testing.T) {
	// Let OS pick a free port.
	l, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer l.Close()

	errCh := make(chan error, 1)

	// Accept in background
	go func() {
		_, err := l.Accept()
		errCh <- err
	}()

	// Connect but do not perform RTMP handshake
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	// Write garbage to fail the handshake
	conn.Write([]byte("not an rtmp handshake"))
	conn.Close()

	// Wait for server to accept and fail
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from Accept due to failed handshake")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Accept to return error")
	}
}

func TestListen_Error(t *testing.T) {
	// Trying to listen on an invalid address should fail
	_, err := Listen("tcp", "invalid-address:-1")
	if err == nil {
		t.Fatal("expected error listening on invalid address")
	}
}
