package rtmp

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// BenchmarkWriteRawMessage targets Conn.writeRawMessage (the chunked message
// writer), which the async-writer change moves off the caller's mutex.
func BenchmarkWriteRawMessage(b *testing.B) {
	conn := newConn(&mockBenchConn{Buffer: bytes.NewBuffer(nil)})
	conn.writeChunkSize = 4096

	payload := make([]byte, 4000)
	for i := range payload {
		payload[i] = byte(i)
	}
	msg := &rawMessage{typeID: messageTypeVideo, streamID: 1, timestamp: 33, payload: payload}

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = conn.writeRawMessage(csidVideo, msg)
		conn.transport.(*mockBenchConn).Reset()
	}
}

func BenchmarkWriteRawMessageParallel(b *testing.B) {
	conn := newConn(&mockBenchConn{Buffer: bytes.NewBuffer(nil)})
	conn.writeChunkSize = 4096

	payload := make([]byte, 4000)
	for i := range payload {
		payload[i] = byte(i)
	}
	msg := &rawMessage{typeID: messageTypeVideo, streamID: 1, timestamp: 33, payload: payload}

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = conn.writeRawMessage(csidVideo, msg)
		}
	})
}

// mockBenchConn is a sink net.Conn for write benchmarks.
type mockBenchConn struct {
	*bytes.Buffer
}

func (m *mockBenchConn) Close() error                       { return nil }
func (m *mockBenchConn) LocalAddr() net.Addr                { return nil }
func (m *mockBenchConn) RemoteAddr() net.Addr               { return nil }
func (m *mockBenchConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockBenchConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockBenchConn) SetWriteDeadline(t time.Time) error { return nil }
