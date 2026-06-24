package rtmp

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestDial(t *testing.T) {
	// Success path
	t.Run("success", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer ln.Close()

		errCh := make(chan error, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			errCh <- serverHandshake(conn)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := Dial(ctx, ln.Addr().String())
		if err != nil {
			t.Fatalf("Dial failed: %v", err)
		}
		defer conn.Close()

		if err := <-errCh; err != nil {
			t.Fatalf("serverHandshake failed: %v", err)
		}
	})

	// Dial error path
	t.Run("dial error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Dial a port that's very unlikely to be open
		_, err := Dial(ctx, "127.0.0.1:1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	// Handshake error path
	t.Run("handshake error", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer ln.Close()

		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Immediately close connection to cause handshake error
			conn.Close()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = Dial(ctx, ln.Addr().String())
		if err == nil {
			t.Fatal("expected error during handshake, got nil")
		}
	})
}

func TestClientConn(t *testing.T) {
	// Success path
	t.Run("success", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()

		errCh := make(chan error, 1)
		go func() {
			errCh <- serverHandshake(c2)
		}()

		conn, err := ClientConn(c1)
		if err != nil {
			t.Fatalf("ClientConn failed: %v", err)
		}
		defer conn.Close()

		if err := <-errCh; err != nil {
			t.Fatalf("serverHandshake failed: %v", err)
		}
	})

	// Handshake error path
	t.Run("handshake error", func(t *testing.T) {
		mockConn := &fakeNetConn{
			ReadFunc: func(a []byte) (int, error) {
				return 0, io.ErrUnexpectedEOF
			},
			WriteFunc: func(a []byte) (int, error) {
				return 0, io.ErrUnexpectedEOF
			},
		}

		_, err := ClientConn(mockConn)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
