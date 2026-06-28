package rtsp

import (
	"net"
	"net/url"
	"testing"
	"time"
)

// mockConn implements net.Conn to count bytes written and simulate blocking I/O.
type mockConn struct {
	net.Conn
	written int
}

func (m *mockConn) Read(b []byte) (n int, err error) { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error) {
	// Simulate blocking I/O.
	time.Sleep(10 * time.Microsecond)
	m.written += len(b)
	return len(b), nil
}
func (m *mockConn) Close() error { return nil }

func BenchmarkConnWriteRequest(b *testing.B) {
	conn := newConn(&mockConn{})

	u, err := url.Parse("rtsp://example.com/media.mp4")
	if err != nil {
		b.Fatalf("url.Parse: %v", err)
	}
	req := &Request{
		Method: MethodPlay,
		URL:    u,
		Proto:  "RTSP/1.0",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteRequest(req); err != nil {
			b.Fatalf("WriteRequest: %v", err)
		}
	}
}

func BenchmarkConnWriteInterleavedFrame(b *testing.B) {
	conn := newConn(&mockConn{})
	frame := &InterleavedFrame{Channel: 0, Payload: make([]byte, 1024)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteInterleavedFrame(frame); err != nil {
			b.Fatalf("WriteInterleavedFrame: %v", err)
		}
	}
}

func BenchmarkConnWriteInterleavedFrame_Concurrent(b *testing.B) {
	conn := newConn(&mockConn{})
	frame := &InterleavedFrame{Channel: 0, Payload: make([]byte, 1024)}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = conn.WriteInterleavedFrame(frame)
		}
	})
}
