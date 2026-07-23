package relay

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/transport"
)

// defaultUDPRcvBuf is the UDP receive buffer size (SO_RCVBUF) used when
// RELAY_UDP_RCVBUF is unset. 256 KB is well above the Windows default (~8 KB)
// and comfortably matches Linux's auto-tuning range (~208 KB by default, kernel
// doubles requests on Linux to ~425KB actual). At this size the relay's single
// QUIC listener socket can absorb burst Initial packets without dropping them
// during the handshake demux.
const defaultUDPRcvBuf = 262144 // 256 KB

// udpRcvBufFromEnv reads RELAY_UDP_RCVBUF from the environment. Returns the
// default (256 KB) when unset or empty, the parsed value when set, and an
// informative msg for logging. A zero-or-negative value disables the override
// (the OS default is used).
func udpRcvBufFromEnv() (int, string) {
	raw := os.Getenv("RELAY_UDP_RCVBUF")
	if raw == "" {
		return defaultUDPRcvBuf, fmt.Sprintf("default %d (256 KB)", defaultUDPRcvBuf)
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("relay: invalid RELAY_UDP_RCVBUF, using default", "raw", raw, "default", defaultUDPRcvBuf)
		return defaultUDPRcvBuf, fmt.Sprintf("default %d (invalid %q)", defaultUDPRcvBuf, raw)
	}
	if v <= 0 {
		return 0, "disabled (0 or negative)"
	}
	return v, fmt.Sprintf("%d (%s)", v, byteSize(v))
}

// byteSize formats a byte count as a human-readable string.
func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// customQUICListener sets up a QUIC listener with a configurable UDP receive
// buffer read from RELAY_UDP_RCVBUF. It wraps the quic-go EarlyListener into
// the transport.QUICListener interface required by gomoqt's moqt.Server.
//
// customQUICListener returns a QUIC-listener factory that applies the UDP
// receive-buffer override (SO_RCVBUF) read from RELAY_UDP_RCVBUF, suitable for
// assignment to moqt.Server.ListenFunc. It returns nil when the override is
// disabled (RELAY_UDP_RCVBUF=0); in that case the caller leaves ListenFunc at
// its zero value and gomoqt uses its default listener.
func customQUICListener() func(string, *tls.Config, *quic.Config) (transport.QUICListener, error) {
	bufSize, desc := udpRcvBufFromEnv()
	if bufSize <= 0 {
		slog.Info("relay: UDP receive buffer override disabled, using OS default")
		return nil
	}
	slog.Info("relay: UDP receive buffer", "size", desc)

	return func(addr string, tlsConfig *tls.Config, quicConfig *quic.Config) (transport.QUICListener, error) {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", addr, err)
		}
		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			return nil, fmt.Errorf("listen udp %q: %w", addr, err)
		}
		if err := conn.SetReadBuffer(bufSize); err != nil {
			slog.Warn("relay: SetReadBuffer failed, continuing with OS default",
				"requested", byteSize(bufSize), "err", err)
		}
		ln, err := quic.ListenEarly(conn, tlsConfig, quicConfig)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("quic.ListenEarly: %w", err)
		}
		return &rcvbufListener{ln: ln}, nil
	}
}

// ---- listener wrapper ----

// rcvbufListener wraps *quic.EarlyListener as transport.QUICListener.
type rcvbufListener struct {
	ln *quic.EarlyListener
}

func (l *rcvbufListener) Accept(ctx context.Context) (transport.StreamConn, error) {
	conn, err := l.ln.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return &rcvbufConn{Conn: conn}, nil
}

func (l *rcvbufListener) Close() error  { return l.ln.Close() }
func (l *rcvbufListener) Addr() net.Addr { return l.ln.Addr() }

// ---- connection wrapper ----

// rcvbufConn wraps *quic.Conn as transport.StreamConn.
//
// Most methods delegate to the embedded *quic.Conn directly because their
// signatures are identical (transport.ConnErrorCode = quic.ApplicationErrorCode
// and transport.StreamErrorCode = quic.StreamErrorCode via gomoqt type aliases).
//
// The six stream-returning methods (AcceptStream, AcceptUniStream, OpenStream,
// OpenStreamSync, OpenUniStream, OpenUniStreamSync) are overridden to convert
// quic-go concrete stream structs to their transport interface counterparts.
// TLS() is added because transport.StreamConn uses TLS() while quic-go uses
// ConnectionState(). QUICConn() exposes the underlying *quic.Conn for code that
// needs it (e.g. the relay's own stats sampling).
type rcvbufConn struct {
	*quic.Conn
}

func (w *rcvbufConn) AcceptStream(ctx context.Context) (transport.Stream, error) {
	s, err := w.Conn.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return streamWrapper{Stream: s}, nil
}

func (w *rcvbufConn) AcceptUniStream(ctx context.Context) (transport.ReceiveStream, error) {
	s, err := w.Conn.AcceptUniStream(ctx)
	if err != nil {
		return nil, err
	}
	return receiveStreamWrapper{ReceiveStream: s}, nil
}

func (w *rcvbufConn) OpenStream() (transport.Stream, error) {
	s, err := w.Conn.OpenStream()
	if err != nil {
		return nil, err
	}
	return streamWrapper{Stream: s}, nil
}

func (w *rcvbufConn) OpenStreamSync(ctx context.Context) (transport.Stream, error) {
	s, err := w.Conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return streamWrapper{Stream: s}, nil
}

func (w *rcvbufConn) OpenUniStream() (transport.SendStream, error) {
	s, err := w.Conn.OpenUniStream()
	if err != nil {
		return nil, err
	}
	return sendStreamWrapper{SendStream: s}, nil
}

func (w *rcvbufConn) OpenUniStreamSync(ctx context.Context) (transport.SendStream, error) {
	s, err := w.Conn.OpenUniStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return sendStreamWrapper{SendStream: s}, nil
}

func (w *rcvbufConn) TLS() *tls.ConnectionState {
	state := w.ConnectionState()
	return &state.TLS
}

// QUICConn exposes the underlying *quic.Conn so that code that needs the
// concrete type (e.g. the relay's stats sampler via connStatsProvider) can
// access it without a separate type assertion. ConnectionStats() is already
// available via the embedded *quic.Conn (transport.ConnectionStats is an alias
// for quic.ConnectionStats), so connStatsProvider is satisfied without this
// method. QUICConn is provided for symmetry with gomoqt's internal connWrapper.
func (w *rcvbufConn) QUICConn() *quic.Conn {
	return w.Conn
}

// ---- stream wrappers ----

// streamWrapper, sendStreamWrapper, and receiveStreamWrapper bridge quic-go
// concrete stream structs (which are pointer-receiver types) to transport
// stream interfaces. We embed the pointer-to-struct (*quic.Stream etc.) so
// that ALL methods (both value and pointer receiver) are promoted and the
// wrapper value satisfies the transport interface.
//
// This works because:
//   - transport.StreamErrorCode = quic.StreamErrorCode (gomoqt type alias)
//   - transport.ApplicationErrorCode = quic.ApplicationErrorCode (alias)
//   - All stream method signatures are structurally identical
type streamWrapper struct{ *quic.Stream }
type sendStreamWrapper struct{ *quic.SendStream }
type receiveStreamWrapper struct{ *quic.ReceiveStream }

// compile-time assertions that the wrappers satisfy the transport interfaces.
// These also guard against silent breakage if gomoqt/quic-go change the
// transport.StreamConn / QUICListener / stream method signatures.
var (
	_ transport.QUICListener  = (*rcvbufListener)(nil)
	_ transport.StreamConn    = (*rcvbufConn)(nil)
	_ transport.Stream        = streamWrapper{}
	_ transport.SendStream    = sendStreamWrapper{}
	_ transport.ReceiveStream = receiveStreamWrapper{}
)
