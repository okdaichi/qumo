package rtmp

import (
	"log/slog"
	"net"
	"time"
)

// handshakeTimeout bounds how long the server waits for a client to complete
// the RTMP handshake. Without it, a client that connects and then stalls
// (sends nothing) blocks Accept indefinitely, preventing every other RTMP
// connection from being accepted. A var so tests can shorten it.
var handshakeTimeout = 10 * time.Second

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
//
// A connection that fails the handshake (a health probe, a port scan, a
// half-open or otherwise-misbehaving client) is closed and skipped: Accept
// loops back to the underlying listener rather than returning the error. This
// keeps one bad client from tearing down the listener — server accept loops
// treat any Accept error as fatal.
//
// The handshake is run under a read deadline so a client that connects and
// then stalls cannot hold Accept (and block every other connection). The
// deadline is cleared once the handshake completes, so subsequent streaming
// reads are not bounded.
func (l *Listener) Accept() (*Conn, error) {
	for {
		transport, err := l.rawConnListener.Accept()
		if err != nil {
			return nil, err
		}
		_ = transport.SetReadDeadline(time.Now().Add(handshakeTimeout))
		conn, err := ServerConn(transport)
		if err != nil {
			// Debug, not Warn: a stray probe/scan failing the handshake is
			// routine; a sudden surge is what an operator would investigate.
			slog.Debug("rtmp: handshake failed, closing connection",
				"remote", transport.RemoteAddr(), "error", err)
			_ = transport.Close()
			continue
		}
		_ = transport.SetReadDeadline(time.Time{})
		return conn, nil
	}
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
