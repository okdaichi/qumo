package rtsp

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

// Conn represents an RTSP connection.
type Conn struct {
	transport net.Conn
	br        *bufio.Reader
	bw        *bufio.Writer

	mu sync.Mutex
}

func newConn(transport net.Conn) *Conn {
	return NewConn(transport)
}

// NewConn wraps an already-established network connection as an RTSP [Conn].
// It is the client-side entry point: the server side constructs Conns via
// [Listener.Accept]. The returned Conn can [Conn.WriteRequest] /
// [Conn.ReadResponse] / [Conn.ReadRequest] (interleaved frames) symmetrically.
func NewConn(transport net.Conn) *Conn {
	return &Conn{
		transport: transport,
		br:        bufio.NewReader(transport),
		bw:        bufio.NewWriter(transport),
	}
}

// ReadRequest reads an RTSP request or an interleaved frame.
func (c *Conn) ReadRequest() (*Request, *InterleavedFrame, error) {
	b, err := c.br.Peek(1)
	if err != nil {
		return nil, nil, err
	}

	if b[0] == '$' {
		frame, err := c.readInterleavedFrame()
		return nil, frame, err
	}

	req, err := ReadRequest(c.br)
	return req, nil, err
}

// ReadResponse reads an RTSP response or an interleaved frame.
func (c *Conn) ReadResponse(req *Request) (*Response, *InterleavedFrame, error) {
	b, err := c.br.Peek(1)
	if err != nil {
		return nil, nil, err
	}

	if b[0] == '$' {
		frame, err := c.readInterleavedFrame()
		return nil, frame, err
	}

	resp, err := ReadResponse(c.br, req)
	return resp, nil, err
}

func (c *Conn) readInterleavedFrame() (*InterleavedFrame, error) {
	var header [4]byte
	if _, err := io.ReadFull(c.br, header[:]); err != nil {
		return nil, err
	}

	if header[0] != '$' {
		return nil, fmt.Errorf("malformed interleaved frame header")
	}

	channel := header[1]
	length := binary.BigEndian.Uint16(header[2:])

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return nil, err
	}

	return &InterleavedFrame{
		Channel: channel,
		Payload: payload,
	}, nil
}

// WriteRequest writes an RTSP request to the connection.
func (c *Conn) WriteRequest(req *Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := req.Write(c.bw); err != nil {
		return err
	}
	return c.bw.Flush()
}

// WriteResponse writes an RTSP response to the connection.
func (c *Conn) WriteResponse(resp *Response) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := resp.Write(c.bw); err != nil {
		return err
	}
	return c.bw.Flush()
}

// WriteInterleavedFrame writes an interleaved frame to the connection.
func (c *Conn) WriteInterleavedFrame(frame *InterleavedFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var header [4]byte
	header[0] = '$'
	header[1] = frame.Channel
	binary.BigEndian.PutUint16(header[2:], uint16(len(frame.Payload)))

	if _, err := c.bw.Write(header[:]); err != nil {
		return err
	}
	if _, err := c.bw.Write(frame.Payload); err != nil {
		return err
	}
	return c.bw.Flush()
}

// Close closes the connection.
func (c *Conn) Close() error {
	return c.transport.Close()
}

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr {
	return c.transport.RemoteAddr()
}
