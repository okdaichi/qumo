package rtsp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListen(t *testing.T) {
	tests := []struct {
		name    string
		network string
		address string
		wantErr bool
	}{
		{
			name:    "valid tcp",
			network: "tcp",
			address: "127.0.0.1:0",
			wantErr: false,
		},
		{
			name:    "invalid network",
			network: "invalid",
			address: "127.0.0.1:0",
			wantErr: true,
		},
		{
			name:    "invalid address",
			network: "tcp",
			address: "invalid-address:-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := Listen(tt.network, tt.address)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, l)
			} else {
				require.NoError(t, err)
				require.NotNil(t, l)
				err = l.Close()
				require.NoError(t, err)
			}
		})
	}
}

func TestListener_Accept(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		conn, dialErr := net.Dial("tcp", l.Addr().String())
		assert.NoError(t, dialErr)
		defer conn.Close()
	}()

	conn, err := l.Accept()
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	<-done
}

func TestListener_Accept_Closed(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	err = l.Close()
	require.NoError(t, err)

	conn, err := l.Accept()
	require.Error(t, err)
	require.Nil(t, conn)
	require.ErrorIs(t, err, net.ErrClosed)
}

func TestListener_Close(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	err = l.Close()
	require.NoError(t, err)

	// Closing again returns an error (net.ErrClosed or similar depending on the OS, but it should be an error).
	err = l.Close()
	require.Error(t, err)
}

func TestListener_Addr(t *testing.T) {
	l, err := Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	addr := l.Addr()
	require.NotNil(t, addr)
	assert.Equal(t, "tcp", addr.Network())

	tcpAddr, ok := addr.(*net.TCPAddr)
	require.True(t, ok)
	assert.True(t, tcpAddr.IP.IsLoopback())
	assert.NotEqual(t, 0, tcpAddr.Port)
}
