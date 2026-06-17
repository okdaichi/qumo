package rtsp

import "net"

// Listener accepts incoming RTSP connections.
type Listener struct {
	ln net.Listener
}

// Listen creates a new RTSP listener.
func Listen(network, address string) (*Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &Listener{ln: ln}, nil
}

// Accept waits for and returns the next RTSP connection.
func (l *Listener) Accept() (*Conn, error) {
	conn, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	return newConn(conn), nil
}

// Close closes the listener.
func (l *Listener) Close() error {
	return l.ln.Close()
}

// Addr returns the listener's network address.
func (l *Listener) Addr() net.Addr {
	return l.ln.Addr()
}
