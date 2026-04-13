package rtmp

import (
	"io"
	"net"
	"time"
)

// fakeNetConn implements net.Conn for tests that only inspect Conn state
// without performing real I/O.
var _ net.Conn = (*fakeNetConn)(nil)

type fakeNetConn struct {
	ReadFunc  func(a []byte) (int, error)
	WriteFunc func(a []byte) (int, error)
}

func (f *fakeNetConn) Read(a []byte) (int, error) {
	if f.ReadFunc != nil {
		return f.ReadFunc(a)
	}
	return 0, io.EOF
}

func (f *fakeNetConn) Write(a []byte) (int, error) {
	if f.WriteFunc != nil {
		return f.WriteFunc(a)
	}
	return len(a), nil
}

func (f *fakeNetConn) Close() error                       { return nil }
func (f *fakeNetConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (f *fakeNetConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (f *fakeNetConn) SetDeadline(_ time.Time) error      { return nil }
func (f *fakeNetConn) SetReadDeadline(_ time.Time) error  { return nil }
func (f *fakeNetConn) SetWriteDeadline(_ time.Time) error { return nil }
