package rtmp

import (
	"io"
	"net"
	"time"
)

// low-level utility: fakes net.Conn for tests that inspect Conn state without
// performing real I/O. Unexported: only referenced within package rtmp.
var _ net.Conn = (*fakeNetConn)(nil)

// connResult is one entry in a read/write queue. Data is copied into the Read
// buffer (ignored on Write); Err is the returned error.
type connResult struct {
	Data []byte
	Err  error
}

// fakeNetConn models net.Conn as data, not func-fields.
//
// The comprehensive model is a queue per direction: entries are returned in
// order, and once the queue is exhausted the LAST entry repeats for every
// further call. An empty queue means the direction's default — io.EOF for
// Read, success (len(p), nil) for Write.
//
// Consequences:
//   - A persistent failure is a one-entry queue: {{Err: io.ErrUnexpectedEOF}}.
//   - A sequenced read is several entries; the final one then repeats.
//   - The zero value &fakeNetConn{} is usable as a quiet, EOF-on-read pipe.
type fakeNetConn struct {
	Reads    []connResult
	readIdx  int
	Writes   []connResult
	writeIdx int
}

func (f *fakeNetConn) Read(p []byte) (int, error) {
	if len(f.Reads) == 0 {
		return 0, io.EOF
	}
	i := min(f.readIdx, len(f.Reads)-1)
	f.readIdx++
	r := f.Reads[i]
	return copy(p, r.Data), r.Err
}

func (f *fakeNetConn) Write(p []byte) (int, error) {
	if len(f.Writes) == 0 {
		return len(p), nil
	}
	i := min(f.writeIdx, len(f.Writes)-1)
	f.writeIdx++
	return len(p), f.Writes[i].Err
}

func (f *fakeNetConn) Close() error                       { return nil }
func (f *fakeNetConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (f *fakeNetConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (f *fakeNetConn) SetDeadline(_ time.Time) error      { return nil }
func (f *fakeNetConn) SetReadDeadline(_ time.Time) error  { return nil }
func (f *fakeNetConn) SetWriteDeadline(_ time.Time) error { return nil }
