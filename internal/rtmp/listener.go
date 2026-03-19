package rtmp

import "net"

func Listen(network, address string) (*Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}

	return NewListener(ln), nil
}

func NewListener(ln net.Listener) *Listener {
	return &Listener{
		rawConnListener: ln,
	}
}

type Listener struct {
	rawConnListener net.Listener
}

func (l *Listener) Accept() (*Conn, error) {
	transport, err := l.rawConnListener.Accept()
	if err != nil {
		return nil, err
	}

	if err := ServerHandshake(transport); err != nil {
		transport.Close()
		return nil, err
	}

	conn := NewConn(transport)
	return conn, nil
}

func (l *Listener) Close() error {
	err := l.rawConnListener.Close()
	if err != nil {
		return err
	}
	return nil
}

func (l *Listener) Addr() net.Addr {
	return l.rawConnListener.Addr()
}
