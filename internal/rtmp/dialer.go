package rtmp

import "net"

func Dial(network, address string) (*Conn, error) {
	transport, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	if err := ClientHandshake(transport); err != nil {
		transport.Close()
		return nil, err
	}

	return NewConn(transport), nil
}
