package rtmp

import "net"

func NewConn(transport net.Conn) *Conn {
	return &Conn{
		transport: transport,
	}
}

type Conn struct {
	transport net.Conn
}

func (c *Conn) OpenStream() (*Stream, error) {
	return newStream(), nil
}

func (c *Conn) Close() error {
	return c.transport.Close()
}

func newStream() *Stream {
	return &Stream{}
}

type Stream struct {
}
