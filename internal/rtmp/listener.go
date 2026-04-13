package rtmp

import "net"

// Listen creates a new [Listener] that accepts RTMP connections on the given
// network address. The network parameter is typically "tcp".
func Listen(network, address string) (*Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}

	return NewListener(ln), nil
}

// NewListener wraps an existing [net.Listener] to accept RTMP connections.
// Each accepted TCP connection automatically undergoes the server-side
// RTMP handshake before being returned as a [*Conn].
func NewListener(ln net.Listener) *Listener {
	return &Listener{
		rawConnListener: ln,
	}
}

// Listener accepts incoming RTMP connections. It wraps a [net.Listener] and
// performs the RTMP handshake on each accepted TCP connection.
type Listener struct {
	rawConnListener net.Listener
}

// Accept waits for and returns the next RTMP connection. The returned [*Conn]
// has already completed the server-side handshake and is ready for
// [Conn.AcceptStream].
func (l *Listener) Accept() (*Conn, error) {
	transport, err := l.rawConnListener.Accept()
	if err != nil {
		return nil, err
	}

	conn, err := ServerConn(transport)
	if err != nil {
		transport.Close()
		return nil, err
	}

	return conn, nil
}

// Close stops the listener. Any blocked [Listener.Accept] calls will return
// an error.
func (l *Listener) Close() error {
	return l.rawConnListener.Close()
}

// Addr returns the listener's network address.
func (l *Listener) Addr() net.Addr {
	return l.rawConnListener.Addr()
}

// ServerConn performs the server-side RTMP handshake on an existing TCP
// connection and returns a [*Conn] ready for stream negotiation via
// [Conn.AcceptStream].
func ServerConn(transport net.Conn) (*Conn, error) {
	if err := serverHandshake(transport); err != nil {
		return nil, err
	}
	return newConn(transport), nil
}
