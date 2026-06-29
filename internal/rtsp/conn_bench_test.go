package rtsp

import (
	"net"
	"net/url"
	"testing"
	"time"
)

// benchSinkConn is a net.Conn fake for write benchmarks. It discards written
// bytes (counting only) so b.N iterations do not accumulate memory. It does
// NOT reuse mockConn from conn_test.go: that fake sinks writes into a
// bytes.Buffer, which would OOM under benchmark load.
type benchSinkConn struct {
	written int
}

func (m *benchSinkConn) Read(b []byte) (int, error) { return 0, nil }
func (m *benchSinkConn) Write(b []byte) (int, error) {
	m.written += len(b)
	return len(b), nil
}
func (m *benchSinkConn) Close() error                       { return nil }
func (m *benchSinkConn) LocalAddr() net.Addr                { return nil }
func (m *benchSinkConn) RemoteAddr() net.Addr               { return nil }
func (m *benchSinkConn) SetDeadline(t time.Time) error      { return nil }
func (m *benchSinkConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *benchSinkConn) SetWriteDeadline(t time.Time) error { return nil }

func BenchmarkConnWriteRequest(b *testing.B) {
	conn := newConn(&benchSinkConn{})

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
	conn := newConn(&benchSinkConn{})
	frame := &InterleavedFrame{Channel: 0, Payload: make([]byte, 1024)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteInterleavedFrame(frame); err != nil {
			b.Fatalf("WriteInterleavedFrame: %v", err)
		}
	}
}

func BenchmarkConnWriteInterleavedFrame_Concurrent(b *testing.B) {
	conn := newConn(&benchSinkConn{})
	frame := &InterleavedFrame{Channel: 0, Payload: make([]byte, 1024)}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = conn.WriteInterleavedFrame(frame)
		}
	})
}
