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

	// A failed handshake must NOT tear down the listener: Accept loops past it
	// and stays blocked, so a bad client (probe, port scan, half-open conn)
	// can't kill ingest. Give the server time to process the bad handshake —
	// the local TCP close → EOF is handled in well under this window.
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("Accept returned for a failed handshake, killing the listener: %v", err)
	default:
		// Good: the listener is still alive and accepting.
	}

	// Closing the listener is the only legitimate way Accept should return.
	if err := l.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	select {
	case <-errCh:
		// Accept returned on close — expected.
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Accept to return after Close")
	}
}

func TestListen_Error(t *testing.T) {
	// Trying to listen on an invalid address should fail
	_, err := Listen("tcp", "invalid-address:-1")
	if err == nil {
		t.Fatal("expected error listening on invalid address")
	}
}
