package rtmp

import (
	"context"
	"net"
)

// Dial connects to an RTMP server at the given address (host:port), performs
// the client-side handshake, and returns a [Conn] ready for [Conn.OpenStream].
// The provided context controls the TCP dial and handshake phases.
func Dial(ctx context.Context, address string) (*Conn, error) {
	var d net.Dialer
	transport, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	conn, err := ClientConn(transport)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}

	return conn, nil
}

// ClientConn performs the client-side RTMP handshake on an existing TCP
// connection and returns a [*Conn] ready for stream negotiation via
// [Conn.OpenStream].
func ClientConn(transport net.Conn) (*Conn, error) {
	if err := clientHandshake(transport); err != nil {
		return nil, err
	}
	return newConn(transport), nil
}
